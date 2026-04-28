package repository

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"wfts/internal/model"

	"github.com/dgraph-io/badger/v3"
)

type IndexRepository struct {
	DB 				*badger.DB
	log 			*model.Logger
	mu 				*sync.Mutex
	ni				*ngChunkIndex
	si				*shingleIndex
	ctx 			context.Context
	path 			string
}

type Cacher interface {
	Put(any, any)
	Get(any) any
}

func NewIndexRepository(ctx context.Context, backupWg *sync.WaitGroup, path string, workersCount int, log *model.Logger, cacher func(int) Cacher) (*IndexRepository, error) {
	opts := badger.DefaultOptions(path + "/index")
	opts.BlockCacheSize = 32 << 20
	opts.IndexCacheSize = 16 << 20
	db, err := badger.Open(opts.WithLoggingLevel(badger.INFO))
	if err != nil {
		return nil, err
	}
	done := [512]chan struct{}{}
	writes := [512]chan [1088]byte{}
	for i := range 512 {
		writes[i] = make(chan [1088]byte, workersCount)
		done[i] = make(chan struct{})
	}
	ir := &IndexRepository{
		DB: db,
		log: log,
		mu: new(sync.Mutex),
		ni: NewWordIndex(58, uint32(workersCount)),
		si: &shingleIndex{
			writes: writes,
			done: done,
		},
		path: path,
		ctx: ctx,
	}
	if err := os.MkdirAll(filepath.Join(path, "/ngs"), 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(path, "/shingles"), 0755); err != nil {
		return nil, err
	}
	if err := ir.ni.loadBloom(path); err != nil {
		return nil, err
	}
	c, err := ir.LoadIndexC()
	if err != nil {
		return nil, err
	}

	backupWg.Go(func() {
		<-ctx.Done()
		ir.ni.saveBloom(ir.path)
		ir.si.stop()
		ir.UpdateIndexC(atomic.LoadUint32(&c))
	})

	for i := range shardSize {
		go ir.alloc(&c, ir.ni.shards[i], cacher(workersCount))
	}

	for i := range 512 {
		go ir.shingleWorker(i)
	}

	return ir, nil
}

func (ir *IndexRepository) LoadVisitedUrls(visitedURLs *sync.Map) error {
    return ir.DB.View(func(txn *badger.Txn) error {
        it := txn.NewIterator(badger.DefaultIteratorOptions)
        defer it.Close()
        for it.Seek([]byte("visited:")); it.ValidForPrefix([]byte("visited:")); it.Next() {
            item := it.Item()
            key := string(item.Key())
            url := strings.TrimPrefix(key, "visited:")
			depth, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
            visitedURLs.Store(url, decCount(depth))
        }
        return nil
    })
}

func (ir *IndexRepository) SaveVisitedUrls(visitedURLs *sync.Map) error {
	urls := make([]struct{url string; depth int}, 0, 512)
	visitedURLs.Range(func(key, value any) bool {
		if url, ok := key.(string); ok {
			urls = append(urls, struct{url string; depth int}{url, value.(int)})
		}
		return true
	})
	for _, u := range urls {
		if err := ir.DB.Update(func(txn *badger.Txn) error {
			return txn.Set([]byte("visited:"+u.url), encCount(u.depth))
		}); err != nil {
			return err
		}
	}
	return nil
}

func (ir *IndexRepository) IndexDocumentWords(docID [32]byte, sequence map[string]int, pos map[string][]model.Position) error {
	type wordEntry struct {
		word string
		freq int
	}
	
	entries := make([]wordEntry, 0, len(sequence))
	for w, f := range sequence {
		entries = append(entries, wordEntry{word: w, freq: f})
	}

	const iterSize = 500
	for i := 0; i < len(entries); i += iterSize {
		chunk := entries[i: min(len(entries), i + iterSize)]

		if err := ir.DB.Update(func(txn *badger.Txn) error {
			for _, entry := range chunk {
				key := fmt.Appendf(nil, WordDocumentKeyFormat, entry.word, docID)
				positions := pos[entry.word]
				if len(positions) > 500 {
					positions = positions[:500] // более 500 вхождений одного слова в один документ....
				}

				wcp := model.WordCountAndPositions{
					Count:     entry.freq,
					Positions: positions,
				}
				val, err := json.Marshal(wcp)
				if err != nil {
					return err
				}
				if len(val) > 1024 * 1024 { // нужен ли нам текстовый токен более 1 мб? я думаю нет, я правда не сильно верю что это условие вообще хоть раз отработает
					continue
				}
				if err := txn.Set(key, val); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to update chunk %d: %w", i, err)
		}
	}

	return nil
}

func (ir *IndexRepository) GetDocumentsByWord(word string) (map[[32]byte]model.WordCountAndPositions, error) {
	revertWordIndex := make(map[[32]byte]model.WordCountAndPositions)
	wprefix := fmt.Appendf(nil, "ri:%s_", word)
	return revertWordIndex, ir.DB.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Seek(wprefix); it.ValidForPrefix(wprefix); it.Next() {
			item := it.Item()
			keyPart := item.Key()[len(wprefix):]

			decoded, err := hex.DecodeString(string(keyPart))
			if err != nil {
				return err
			}
			id := [32]byte{}
			copy(id[:], decoded)

			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			positions := model.WordCountAndPositions{}
			if err := json.Unmarshal(val, &positions); err != nil {
				return err
			}

			revertWordIndex[id] = positions
		}
		
		return nil
	})
}

const biK = "big:%d:%d"

func (ir *IndexRepository) UpdateBiFreq(biS map[[2]uint64]int) error {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	for lr, freq := range biS {
		if err := ir.DB.Update(func(txn *badger.Txn) error {
			key := fmt.Appendf(nil, biK, lr[0], lr[1])
			item, err := txn.Get(key)
			if err != nil && err != badger.ErrKeyNotFound {
				return err
			}
			if err != badger.ErrKeyNotFound {
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				freq += decCount(val)
			}
			return txn.Set(key, encCount(freq))
		}); err != nil {
			return err
		}
	}
	return nil
}

func (ir *IndexRepository) GetFreq(l, r uint64) (int, error) {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	freq := 0
	return freq, ir.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get(fmt.Appendf(nil, biK, l, r))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return nil
			}
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		freq = decCount(val)
		return nil
	})
}

func (ir *IndexRepository) alloc(startPos *uint32, await chan srequest, cacheSys Cacher) {
	defer func() {
		close(await)
	}()

	for entry := range await {
		select {
		case <-ir.ctx.Done():
			return
		default:
		}
		var idx uint32
		if val := cacheSys.Get(entry.word); val == nil {
			key := fmt.Appendf(nil, wordKey, entry.word)
			if err := ir.DB.Update(func(txn *badger.Txn) error {
				it, err := txn.Get(key)
				if err == nil {
					it.Value(func(val []byte) error {
						idx = binary.LittleEndian.Uint32(val)
						return nil
					})
					return nil
				}

				data := [4]byte{}
				idx = atomic.AddUint32(startPos, 1)
				binary.LittleEndian.PutUint32(data[:], idx)
				if err := txn.Set(key, data[:]); err != nil {
					return err
				}
				if err := txn.Set(fmt.Appendf(nil, seqKey, idx), []byte(entry.word)); err != nil {
					return err
				}
				return nil
			}); err != nil {
				ir.log.Errorf("allocation failed: %v", err)
				return
			}
			cacheSys.Put(entry.word, idx)
		} else {
			idx = val.(uint32)
		}
		entry.seqC <- idx
	}
}

func (ir *IndexRepository) SaveSaltArrays(a, b [128]uint64) error {
	var data [256]uint64
	copy(data[:128], a[:])
	copy(data[128:], b[:])
	file, err := os.OpenFile(filepath.Join(ir.path, "/salt.bin"), os.O_CREATE | os.O_TRUNC | os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(
		unsafe.Slice((*byte)(unsafe.Pointer(&data[0])), 256 * 8),
	)
	return err
}

func (ir *IndexRepository) UploadSaltArrays() (*[128]uint64, *[128]uint64, error) {
	file, err := os.OpenFile(filepath.Join(ir.path, "/salt.bin"), os.O_RDONLY, 0600)
	if err != nil && os.IsExist(err) {
		return nil, nil, err
	} else if err != nil {
		return nil, nil, nil
	}
	defer file.Close()
	var a, b [128]uint64
	if _, err = io.ReadFull(file, unsafe.Slice((*byte)(unsafe.Pointer(&a[0])), 128 * 8)); err != nil {
		return nil, nil, err
	}
	if _, err = io.ReadFull(file, unsafe.Slice((*byte)(unsafe.Pointer(&b[0])), 128 * 8)); err != nil {
		return nil, nil, err
	}
	return &a, &b, nil
}

func encCount(n int) []byte {
	return binary.BigEndian.AppendUint32(nil, uint32(n))
}

func decCount(c []byte) int {
	return int(binary.BigEndian.Uint32(c))
}