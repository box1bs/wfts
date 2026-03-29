package repository

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dgraph-io/badger/v3"
)

const (
	ngKey = "ng:%s:%04d"
	shingleKey = "shingle:%s:%04d"
	maxRecords = 10000
	maxIncompleteRecs = 1000
)

type wordChunkData struct {
	buffer	map[string][]string
	incomplete map[string]struct{}
	counts	map[string]int
	curBufSize int32
}

type shingleChunkData struct {
	buffer	map[[4]uint64][][128]uint64
	incomplete map[[4]uint64]struct{}
	counts 	map[[4]uint64]int
	curBufSize int32
}

func (ir *IndexRepository) makeShingleKey(sh [4]uint64) string {
	strs := [4]string{}
	for i := range 4 {
		strs[i] = strconv.FormatUint(sh[i], 10)
	}
	return strings.Join(strs[:], ".")
}

func (ir *IndexRepository) IndexNGrams(words []string, n int) error {
	for _, word := range words {
		for _, ng := range ir.extractNGrams(word, n) {
			ir.mu.Lock()
			buf := ir.nGramIndexer.buffer[ng]
			buf = append(buf, word)
			atomic.AddInt32(&ir.nGramIndexer.curBufSize, 1)
			if len(buf) >= ir.chunkSize {
				chId := ir.nGramIndexer.counts[ng]
				ir.nGramIndexer.counts[ng]++
				toFlush := make([]string, len(buf)) // чтоб не обнулялось
				copy(toFlush, buf)
				delete(ir.nGramIndexer.buffer, ng)
				atomic.AddInt32(&ir.nGramIndexer.curBufSize, -int32(ir.chunkSize))

				if err := ir.flushChunk(nil, chId, ngKey, ng, toFlush); err != nil {
					ir.mu.Unlock()
					return err
				}
				ir.mu.Unlock()
				continue
			}
			ir.nGramIndexer.buffer[ng] = buf
			if atomic.LoadInt32(&ir.nGramIndexer.curBufSize) >= maxRecords {
				ir.mu.Unlock()
				atomic.StoreInt32(&ir.nGramIndexer.curBufSize, 0)
				if err := ir.FlushAll(); err != nil {return err}
				continue
			}
			ir.mu.Unlock()
		}
	}
	return nil
}

func (ir *IndexRepository) IndexDocShingles(signature [128]uint64) error {
	for i := 0; i <= 128 - 4; i += 4 {
		var lshKey [4]uint64
        copy(lshKey[:], signature[i: i + 4])
		ir.mu.Lock()
		buf := append(ir.shingleIndexer.buffer[lshKey], signature)
		atomic.AddInt32(&ir.shingleIndexer.curBufSize, 1)
		if len(buf) >= ir.chunkSize {
			chId := ir.shingleIndexer.counts[lshKey]
			ir.shingleIndexer.counts[lshKey]++
			toFlush := make([][128]uint64, len(buf)) // чтоб не обнулялся
			copy(toFlush, buf)
			delete(ir.shingleIndexer.buffer, lshKey)
			atomic.AddInt32(&ir.shingleIndexer.curBufSize, -int32(ir.chunkSize))
			if err := ir.flushChunk(nil, chId, shingleKey, ir.makeShingleKey(lshKey), toFlush); err != nil {
				ir.mu.Unlock()
				return err
			}
			ir.mu.Unlock()
			continue
		}
		ir.shingleIndexer.buffer[lshKey] = buf
		if atomic.LoadInt32(&ir.shingleIndexer.curBufSize) >= maxRecords {
			ir.mu.Unlock()
			atomic.StoreInt32(&ir.shingleIndexer.curBufSize, 0)
			if err := ir.FlushAll(); err != nil {return err}
			continue
		}
		ir.mu.Unlock()
	}
	return nil
}

func (ir *IndexRepository) GetWordsByNGram(word string, n int) ([]string, error) {
	result := make([]string, 0, 64)
	alreadyInc := map[string]struct{}{}

	for _, ngram := range ir.extractNGrams(word, n) {
		for _, word := range ir.nGramIndexer.buffer[ngram] { // берем из буффера
			if _, ex := alreadyInc[word]; ex {
				continue
			}
			alreadyInc[word] = struct{}{}
			result = append(result, word)
		}
		prefix := []byte("ng:" + ngram + ":")
		if err := ir.DB.View(func(txn *badger.Txn) error { // берем из памяти, технически можно это делать не через итератор, а напрямую меняя ключ в цикле от 0 до count для этой нграммы, это позволило бы лучше обрабатывать ошибочные ситуации
			it := txn.NewIterator(badger.DefaultIteratorOptions)
			defer it.Close()
			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				item := it.Item()
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				var words []string
				if err := json.Unmarshal(val, &words); err != nil {
					return err
				}
				for _, w := range words {
					if _, ex := alreadyInc[w]; ex {
						continue
					}
					alreadyInc[w] = struct{}{}
					result = append(result, w)
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (ir *IndexRepository) extractNGrams(word string, n int) []string {
	runes := []rune(strings.ToLower(word))
	out := make([]string, 0, 8)
	alIn := map[string]struct{}{}
	if len(runes) < n {
		return nil
	}
	for i := range len(runes) - n + 1 {
		ng := string(runes[i:i + n])
		if _, ex := alIn[ng]; ex {
			continue
		}
		alIn[ng] = struct{}{}
		out = append(out, ng)
	}
	return out
}

func (ir *IndexRepository) GetSimilarSignatures(signature [128]uint64) ([][128]uint64, error) {
	result := make([][128]uint64, 0, 16)
	alreadyInc := map[[128]uint64]struct{}{}

	for i := 0; i <= 128 - 4; i += 4 {
		var lshKey [4]uint64
        copy(lshKey[:], signature[i: i + 4])
		ir.mu.Lock()
		buf := make([][128]uint64, len(ir.shingleIndexer.buffer[lshKey]))
        copy(buf, ir.shingleIndexer.buffer[lshKey])
		ir.mu.Unlock()
		for _, sign := range buf {
			if _, ex := alreadyInc[sign]; ex {
				continue
			}
			alreadyInc[sign] = struct{}{}
			result = append(result, sign)
		}
		prefix := []byte("shingle:" + ir.makeShingleKey(lshKey) + ":")
		if err := ir.DB.View(func(txn *badger.Txn) error {
			it := txn.NewIterator(badger.DefaultIteratorOptions)
			defer it.Close()
			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				item := it.Item()
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				var signatures [][128]uint64
				if err := json.Unmarshal(val, &signatures); err != nil {
					return err
				}
				for _, sign := range signatures {
					if _, ex := alreadyInc[sign]; ex {
						continue
					}
					alreadyInc[sign] = struct{}{}
					result = append(result, sign)
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (ir *IndexRepository) flushChunk(txn *badger.Txn, id int, k, kPart string, data any) error {
	key := fmt.Appendf(nil, k, kPart, id)
	val, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if txn != nil {
		if err := txn.Set(key, val); err != nil {
			ir.log.Errorf("error flushing chunk %v", err)
			return err
		}
	} else {
		if err := ir.DB.Update(func(txn *badger.Txn) error {
			return txn.Set(key, val)
		}); err != nil {
			ir.log.Errorf("error flushing chunk %v", err)
			return err
		}
	}
	return nil
}

func (ir *IndexRepository) FlushAll() error {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	for ng, buf := range ir.nGramIndexer.buffer {
		if len(buf) == 0 {
			continue
		}
		if ir.nGramIndexer.counts[ng] >= 5 && len(ir.nGramIndexer.buffer[ng]) < ir.chunkSize / 2 {
			ir.nGramIndexer.incomplete[ng] = struct{}{}
		}
		if len(ir.nGramIndexer.incomplete) >= maxIncompleteRecs {
			if err := ir.optimizeNgramIndex(); err != nil {
				return err
			}
		}
		if err := ir.flushChunk(nil, ir.nGramIndexer.counts[ng], ngKey, ng, buf); err != nil {return err}
		ir.nGramIndexer.counts[ng]++
		delete(ir.nGramIndexer.buffer, ng)
	}
	for sh, buf := range ir.shingleIndexer.buffer {
		if len(buf) == 0 {
			continue
		}
		if ir.shingleIndexer.counts[sh] >= 5 && len(ir.shingleIndexer.buffer[sh]) < ir.chunkSize / 2 {
			ir.shingleIndexer.incomplete[sh] = struct{}{}
		}
		if len(ir.shingleIndexer.incomplete) >= maxIncompleteRecs {
			if err := ir.optimizeShingleIndex(); err != nil {
				return err
			}
		}
		if err := ir.flushChunk(nil, ir.shingleIndexer.counts[sh], shingleKey, ir.makeShingleKey(sh), buf); err != nil {return err}
		ir.shingleIndexer.counts[sh]++
		delete(ir.shingleIndexer.buffer, sh)
	}
	return nil
}

func (ir *IndexRepository) optimizeNgramIndex() error {
	type mergeData struct {
        words []string
        keys  [][]byte
    }

	toMerge := make(map[string]*mergeData)
	if err := ir.DB.View(func(txn *badger.Txn) error {
		for ng := range ir.nGramIndexer.incomplete {
			alreadyInc := map[string]struct{}{}
			buf := make([]string, 0, 128)
			prefixes := [][]byte{}

			count := ir.nGramIndexer.counts[ng]
			for i := min(count, 5); i > 0; i-- {
				prefixes = append(prefixes, fmt.Appendf(nil, "ng:%s:%d", ng, count-i))
			}
			for _, prefix := range prefixes {
				item, err := txn.Get(prefix)
				if err != nil && err != badger.ErrKeyNotFound {
					return err
				}
				if err == badger.ErrKeyNotFound {
					continue
				}
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				var words []string
				if err := json.Unmarshal(val, &words); err != nil {
					return err
				}
				for _, w := range words {
					if _, ex := alreadyInc[w]; ex {
						continue
					}
					alreadyInc[w] = struct{}{}
					buf = append(buf, w)
				}
			}
			toMerge[ng] = &mergeData{words: buf, keys: prefixes}
		}
		return nil
	}); err != nil {return err}

	return ir.DB.Update(func(txn *badger.Txn) error {
		for ng, it := range toMerge {
			chId := ir.nGramIndexer.counts[ng]
			for _, key := range it.keys {
				if err := txn.Delete(key); err != nil {
					return err
				}
				chId--
			}
			bufLen := len(it.words)
			for i := 0; i < bufLen; i += ir.chunkSize {
				chunk := it.words[i:min(ir.chunkSize + i, bufLen)]
				if err := ir.flushChunk(txn, chId, ngKey, ng, chunk); err != nil {
					return err
				}
				chId++
			}
			ir.nGramIndexer.counts[ng] = chId
			delete(ir.nGramIndexer.incomplete, ng)
			newChunksNum := bufLen / ir.chunkSize
			if newChunksNum > len(it.keys) {
				panic("invariant broken: chunk overflow")
			}
			ir.log.Debugf("ngram index optimized %d -> %d, for '%s'", chId, ir.nGramIndexer.counts[ng], ng)
		}
		return nil
	})
}

func (ir *IndexRepository) optimizeShingleIndex() error {
	type mergeData struct {
        shingles [][128]uint64
        keys  [][]byte
    }

	toMerge := make(map[[4]uint64]*mergeData)
	if err := ir.DB.View(func(txn *badger.Txn) error {
		for sh := range ir.shingleIndexer.incomplete {
			alreadyInc := map[[128]uint64]struct{}{}
			buf := make([][128]uint64, 0, 128)
			prefixes := [][]byte{}

			count := ir.shingleIndexer.counts[sh]
			for i := min(count, 5); i > 0; i-- {
				prefixes = append(prefixes, fmt.Appendf(nil, shingleKey, ir.makeShingleKey(sh), count-i))
			}
			for _, prefix := range prefixes {
				item, err := txn.Get(prefix)
				if err != nil && err != badger.ErrKeyNotFound {
					return err
				}
				if err == badger.ErrKeyNotFound {
					continue
				}
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				var signs [][128]uint64
				if err := json.Unmarshal(val, &signs); err != nil {
					return err
				}
				for _, sign := range signs {
					if _, ex := alreadyInc[sign]; ex {
						continue
					}
					alreadyInc[sign] = struct{}{}
					buf = append(buf, sign)
				}
			}
			toMerge[sh] = &mergeData{shingles: buf, keys: prefixes}
		}
		return nil
	}); err != nil {return err}

	return ir.DB.Update(func(txn *badger.Txn) error {
		for sh, it := range toMerge {
			chId := ir.shingleIndexer.counts[sh]
			for _, key := range it.keys {
				if err := txn.Delete(key); err != nil {
					return err
				}
				chId--
			}
			bufLen := len(it.shingles)
			for i := 0; i < bufLen; i += ir.chunkSize {
				chunk := it.shingles[i:min(ir.chunkSize + i, bufLen)]
				if err := ir.flushChunk(txn, chId, shingleKey, ir.makeShingleKey(sh), chunk); err != nil {
					return err
				}
				chId++
			}
			delete(ir.shingleIndexer.incomplete, sh)
			ir.shingleIndexer.counts[sh] = chId
			newChunksNum := bufLen / ir.chunkSize
			if newChunksNum > len(it.keys) {
				panic("invariant broken: chunk overflow")
			}
			ir.log.Debugf("ngram index optimized %d -> %d, for '%s'", chId, ir.shingleIndexer.counts[sh], ir.makeShingleKey(sh))
		}
		return nil
	})
}

func (ir * IndexRepository) UpdateChunkingCounts() error {
	prefixN := []byte("ng:")
	if err := ir.DB.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		ir.mu.Lock()
		defer ir.mu.Unlock()
		for it.Seek(prefixN); it.ValidForPrefix(prefixN); it.Next() {
			item := it.Item()
			ngramButch := strings.Split(strings.TrimPrefix(string(item.Key()), string(prefixN)), ":")
			if len(ngramButch) < 2 {
				return fmt.Errorf("invalid data chunk")
			}
			lnum, err := strconv.Atoi(ngramButch[1])
			if err != nil {
				return err
			}
			ir.nGramIndexer.counts[ngramButch[0]] = max(ir.nGramIndexer.counts[ngramButch[0]], lnum)
		}
		return nil
	}); err != nil {
		return err
	}
	prefixS := []byte("shingle:")
	return ir.DB.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		ir.mu.Lock()
		defer ir.mu.Unlock()
		for it.Seek(prefixS); it.ValidForPrefix(prefixS); it.Next() {
			item := it.Item()
			shingleButch := strings.Split(strings.TrimPrefix(string(item.Key()), string(prefixS)), ":")
			if len(shingleButch) < 2 {
				return fmt.Errorf("invalid data chunk")
			}
			num, err := strconv.Atoi(shingleButch[1])
			if err != nil {
				return err
			}
			lshKey := [4]uint64{}
			rawKeys := strings.Split(shingleButch[0], ".")
			if len(rawKeys) != 4 {
				return fmt.Errorf("invalid key size")
			}
			for i := range 4 {
				mHash, err := strconv.Atoi(rawKeys[i])
				if err != nil {
					return err
				}
				lshKey[i] = uint64(mHash)
			}
			ir.shingleIndexer.counts[lshKey] = max(ir.shingleIndexer.counts[lshKey], num)
		}
		return nil
	})
}

const saltKey = "salt:%s%s"

func (ir *IndexRepository) SaveSaltArrays(a, b [128]uint64) error {
	abuf := bytes.NewBuffer(nil)
	if err := binary.Write(abuf, binary.LittleEndian, a); err != nil {
		return err
	}
	bbuf := bytes.NewBuffer(nil)
	if err := binary.Write(bbuf, binary.LittleEndian, b); err != nil {
		return err
	}
	return ir.DB.Update(func(txn *badger.Txn) error {
		return txn.Set(fmt.Appendf(nil, saltKey, abuf.Bytes(), bbuf.Bytes()), nil)
	})
}

func (ir *IndexRepository) UploadSaltArrays() ([128]uint64, [128]uint64, error) {
	a, b := [128]uint64{}, [128]uint64{}
	return a, b, ir.DB.View(func(txn *badger.Txn) error {
		iter := txn.NewIterator(badger.DefaultIteratorOptions)
		defer iter.Close()
		if iter.Seek([]byte("salt:")); iter.ValidForPrefix([]byte("salt:")) {
			key := iter.Item().Key()
			i := len("salt:")
			if l := 128 * 2 * 8 + i; len(key) != l {
				return fmt.Errorf("invalid key len: %d, needed L: %d", len(key), l)
			}
			partA := key[i:i + 128 * 8]
			partB := key[i + 128 * 8:]
			for i := range 128 {
				start := i * 8
				a[i] = binary.LittleEndian.Uint64(partA[start:start + 8])
				b[i] = binary.LittleEndian.Uint64(partB[start:start + 8])
			}
			return nil
		}
		return badger.ErrKeyNotFound
	})
}