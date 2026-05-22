package repository

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"wfts/internal/model"

	"github.com/cockroachdb/pebble"
)

type IndexRepository struct {
	DB 				*pebble.DB
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

func NewIndexRepository(ctx context.Context, path string, workersCount int, log *model.Logger, cacher func(int) Cacher) (*IndexRepository, error) {
	opts := &pebble.Options{
		MemTableSize: 64 << 20,
		MemTableStopWritesThreshold: 2,

		L0CompactionThreshold: 2,
		L0StopWritesThreshold: 8,

    	MaxConcurrentCompactions: func() int { return 2 },

		Levels: []pebble.LevelOptions{
			{TargetFileSize: 4 << 20},  // L0
			{TargetFileSize: 8 << 20},  // L1
			{TargetFileSize: 16 << 20}, // L2+
		},
    	Cache: pebble.NewCache(32 << 20),
	}
	db, err := pebble.Open(path + "/index", opts)
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
		ni: NewWordIndex(uint32(workersCount)),
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
	c, err := ir.LoadIndexC()
	if err != nil {
		return nil, err
	}
	t := time.NewTicker(24 * time.Hour)
	go func() {
		<-ctx.Done()
		t.Stop()
		ir.si.stop()
		ir.UpdateIndexC(atomic.LoadUint32(&c))
	}()
	for i := range shardSize {
		go ir.alloc(&c, ir.ni.shards[i], cacher(workersCount))
	}

	for i := range 512 {
		go ir.shingleWorker(i)
	}

	return ir, nil
}

func (ir *IndexRepository) LoadVisitedUrls(visitedURLs *sync.Map) error {
	prefix := []byte("visited:")
	it, err := ir.DB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
    	UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return err
	}
	defer it.Close()
	for it.First(); it.Valid(); it.Next() {
		key := string(it.Key())
		url := strings.TrimPrefix(key, "visited:")
		visitedURLs.Store(url, decCount(it.Value()))
	}
	return nil
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
		if err := ir.DB.Set([]byte("visited:"+u.url), encCount(u.depth), pebble.NoSync); err != nil {
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
			val := marshalWCP(wcp)
			if len(val) > 1024 * 1024 { // нужен ли нам текстовый токен более 1 мб? я думаю нет, я правда не сильно верю что это условие вообще хоть раз отработает
				continue
			}
			if err := ir.DB.Set(key, val, pebble.NoSync); err != nil {
				return err
			}
		}
	}

	return nil
}

func (ir *IndexRepository) GetDocumentsByWord(word string) (map[[32]byte]model.WordCountAndPositions, error) {
	revertWordIndex := make(map[[32]byte]model.WordCountAndPositions)
	wprefix := fmt.Appendf(nil, "ri:%s_", word)
	it, err := ir.DB.NewIter(&pebble.IterOptions{
		LowerBound: wprefix,
		UpperBound: prefixUpperBound(wprefix),
	})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	for it.First(); it.Valid(); it.Next() {
		keyPart := it.Key()[len(wprefix):]

		decoded, err := hex.DecodeString(string(keyPart))
		if err != nil {
			return nil, err
		}
		id := [32]byte{}
		copy(id[:], decoded)

		positions := model.WordCountAndPositions{}
		if positions, err = unmarshalWCP(it.Value()); err != nil {
			return nil, err
		}

		revertWordIndex[id] = positions
	}
	return revertWordIndex, nil
}

func prefixUpperBound(prefix []byte) []byte {
    upper := make([]byte, len(prefix))
    copy(upper, prefix)
    for i := len(upper) - 1; i >= 0; i-- {
        upper[i]++
        if upper[i] != 0 {
            return upper[:i+1]
        }
    }
    return nil
}

func marshalWCP(wcp model.WordCountAndPositions) []byte {
    size := 2 + len(wcp.Positions)*8
    buf := make([]byte, size)
    binary.LittleEndian.PutUint16(buf[0:], uint16(wcp.Count))
    for i, p := range wcp.Positions {
        binary.LittleEndian.PutUint32(buf[2+i*4:], encPosEntry(p.Type, p.I))
    }
    return buf
}

func unmarshalWCP(data []byte) (model.WordCountAndPositions, error) {
    if len(data) < 2 {
        return model.WordCountAndPositions{}, fmt.Errorf("data is unexpectable short")
    }
    count := binary.LittleEndian.Uint16(data[0:])
    positions := make([]model.Position, min(count, 500))
    for i := range positions {
		t, p := decPosEntry(binary.LittleEndian.Uint32(data[2+i*4:]))
        positions[i] = model.Position{
            Type: t,
			I: p,
        }
    }
    return model.WordCountAndPositions{Count: int(count), Positions: positions}, nil
}

func encPosEntry(a byte, p int) uint32 {
	return uint32((p << 1) | int(a))
}

func decPosEntry(d uint32) (byte, int) {
	return byte(d & 1), int(d >> 1)
}

const biK = "big:%d:%d"

func (ir *IndexRepository) UpdateBiFreq(biS map[[2]uint64]int) error {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	b := ir.DB.NewBatch()
	c := 0
	for lr, freq := range biS {
		key := fmt.Appendf(nil, biK, lr[0], lr[1])
		val, closer, err := ir.DB.Get(key)
		if err != nil && err != pebble.ErrNotFound {
			b.Close()
			return err
		}
		if err == nil {
			freq += decCount(val)
			closer.Close()
		}
		if err := b.Set(key, encCount(freq), nil); err != nil {
			b.Close()
			return err
		}
		c++
		if c == 20 {
			if err := b.Commit(pebble.NoSync); err != nil {
				return err
			}
			b = ir.DB.NewBatch()
			c = 0
		}
	}
	return b.Commit(pebble.NoSync)
}

func (ir *IndexRepository) GetFreq(l, r uint64) (int, error) {
	freq := 0
	val, closer, err := ir.DB.Get(fmt.Appendf(nil, biK, l, r))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	freq = decCount(val)
	closer.Close()
	return freq, nil
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
		seq := &sequence{}
		if val := cacheSys.Get(entry.word); val == nil {
			key := fmt.Appendf(nil, wordKey, entry.word)
			val, closer, err := ir.DB.Get(key)
			if err == nil {
				seq.id = binary.LittleEndian.Uint32(val)
				seq.loaded = true
				closer.Close()
			} else {
				data := [4]byte{}
				seq.id = atomic.AddUint32(startPos, 1)
				binary.LittleEndian.PutUint32(data[:], seq.id)
				if err := ir.DB.Set(key, data[:], pebble.NoSync); err != nil {
					ir.log.Errorf("allocation failed: %v", err)
					return
				}
				if err := ir.DB.Set(fmt.Appendf(nil, seqKey, seq.id), []byte(entry.word), pebble.NoSync); err != nil {
					ir.log.Errorf("allocation failed: %v", err)
					return
				}
				cacheSys.Put(entry.word, seq)
			}
		} else {
			*seq = *val.(*sequence)
			seq.loaded = true
		}
		entry.seqC <- seq
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