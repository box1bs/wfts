package repository

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"wfts/internal/model"

	"github.com/dgraph-io/badger/v3"
)

type IndexRepository struct {
	DB 				*badger.DB
	log 			*model.Logger
	wg 				*sync.WaitGroup
	mu 				*sync.Mutex
	ni				*ngChunkIndex
	shingleIndexer	*shingleChunkData
	chunkSize 		int
}

func NewIndexRepository(path string, log *model.Logger, chunkSize int) (*IndexRepository, error) {
	db, err := badger.Open(badger.DefaultOptions(path).WithLoggingLevel(badger.WARNING))
	if err != nil {
		return nil, err
	}
	db.CacheMaxCost(badger.BlockCache, 128 << 20)
	ir := &IndexRepository{
		DB: db,
		log: log,
		wg: new(sync.WaitGroup),
		mu: new(sync.Mutex),
		ni: NewWordIndex(58),
		shingleIndexer: &shingleChunkData{buffer: make(map[[4]uint64][][128]uint64), counts: make(map[[4]uint64]int)},
		chunkSize: chunkSize,
	}
	if err := ir.LoadIndexC(); err != nil {
		return nil, err
	}
	return ir, ir.UpdateChunkingCounts() // сомнительно потому что нам не нужно это прокидывать если мы не будем индексировать
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

func encCount(n int) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(n))
	return buf
}

func decCount(c []byte) int {
	return int(binary.BigEndian.Uint32(c))
}