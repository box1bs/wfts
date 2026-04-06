package repository

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dgraph-io/badger/v3"
)

const (
	ngKey = "ng:%s:%04d"
	shingleKey = "shingle:%s:%04d"
	ngFileName = ".local/ngs/ng%d.bin"
	BloomPath = ".local/bloom.bin"
	wordKey = "word:%s"
	seqKey = "num:%d"
	inck = "inc:"
	a, b = 2654435761, 2246822519 // константы для рандомизации битов
)

type BitSet struct {
	bits [8]uint64 // 512 bit
}

func (bs *BitSet) Add(data uint32) {
	h1 := data * a
	h2 := data * b
	for i := range 5 {
		bit := (h1 + h2 * uint32(i)) % 512 // 0-511
		bs.bits[bit/64] |= 1 << (bit % 64) // меняем 1 << bit % 64(0-63) бит на 1 в bit / 64(0-7) массиве
	}
}

func (bs *BitSet) Contain(data uint32) bool {
	h1 := data * a
	h2 := data * b
	for i := range 5 {
		bit := (h1 + h2 * uint32(i)) % 512 // 0-511
		if bs.bits[bit/64] & (1 << (bit % 64)) == 0 { // если конкретный бит не был помечен
			return false
		}
	}
	return true
}

type ngChunkIndex struct {
	chunks 	[676]*os.File // a * 26 + b
	locks 	[676]sync.RWMutex
	bloom 	*RotatingBloom
	lIdx	uint32
}

func NewWordIndex(cap uint32) *ngChunkIndex {
	return &ngChunkIndex{
		bloom: &RotatingBloom{cap: cap},
	}
}

type RotatingBloom struct {
	active, standby [17576]BitSet // a * 676 + b * 26 + c
	counts [17576]uint32
	cap uint32
}

func (rb *RotatingBloom) Add(trigram uint16, seq uint32) bool {
	if rb.active[trigram].Contain(seq) || rb.standby[trigram].Contain(seq) {
		return false
	}

	rb.active[trigram].Add(seq)
	rb.standby[trigram].Add(seq)
	rb.counts[trigram]++
	if rb.counts[trigram] == rb.cap {
		rb.rotate(trigram)
	}
	return true
}

func (rb *RotatingBloom) rotate(idx uint16) {
	rb.standby[idx] = rb.active[idx]
	rb.active[idx] = BitSet{}
	rb.counts[idx] = 0
}

func (ir *IndexRepository) SaveBloom() error {
	file, err := os.OpenFile(BloomPath, os.O_CREATE | os.O_WRONLY | os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, *ir.ni.bloom); err != nil {
		return err
	}
	return nil
}

func (ni *ngChunkIndex) loadBloom() error {
	file, err := os.OpenFile(BloomPath, os.O_RDONLY, 0600)
	if err != nil && os.IsExist(err) {
		return err
	} else if err != nil && os.IsNotExist(err) {
		return nil
	}
	if err := binary.Read(file, binary.LittleEndian, ni.bloom); err != nil {
		return err
	}
	return nil
}

func makeBiGramKey(ng string) uint16 {
	return uint16(ng[0] - 'a') * 26 + uint16(ng[1] - 'a')
}

func (ir *IndexRepository) IndexTriGrams(words []string) error {
	sequence, err := ir.saveOrLoadWord(words...)
	if err != nil {
		return err
	}
	for i, word := range words {
		for _, ng := range extractTGrams(word) {
			bkey := makeBiGramKey(ng)
			tkey := uint16(ng[0] - 'a') * 676 + uint16(ng[1] - 'a') * 26 + uint16(ng[2] - 'a')
			ir.ni.locks[bkey].Lock()
			if file := ir.ni.chunks[bkey]; file == nil {
				ngfile, err := os.OpenFile(fmt.Sprintf(ngFileName, bkey), os.O_APPEND | os.O_WRONLY | os.O_CREATE, 0600)
				if err != nil {
					ir.ni.locks[bkey].Unlock()
					return err
				}
				ir.ni.chunks[bkey] = ngfile
			}
			if !ir.ni.bloom.Add(tkey, sequence[i]) {
				ir.ni.locks[bkey].Unlock()
				continue
			}
			data := fmt.Sprintf("%d:%d", ng[2] - 'a', sequence[i])
			if err := binary.Write(ir.ni.chunks[bkey], binary.LittleEndian, uint16(len(data))); err != nil {
				ir.ni.locks[bkey].Unlock()
				return err
			}
			ir.ni.chunks[bkey].Write([]byte(data))
			ir.ni.locks[bkey].Unlock()
		}
	}
	return nil
}

func (ir *IndexRepository) GetWordsByTGrams(word string) ([]string, error) {
	result := make([]string, 0, 64)
	alreadyInc := map[string]struct{}{}
	ngs := extractTGramsByBGram(word)

	for bkey, ng := range ngs {
		ir.ni.locks[bkey].Lock()
		file, err := os.OpenFile(fmt.Sprintf(ngFileName, bkey), os.O_RDONLY, 0600)
		if err != nil && os.IsExist(err) {
			ir.ni.locks[bkey].Unlock()
			return nil, err
		} else if err != nil && os.IsNotExist(err) {
			ir.ni.locks[bkey].Unlock()
			continue
		}
		defer file.Close()
		for {
			var len uint16
			if err := binary.Read(file, binary.LittleEndian, &len); err != nil {
				if err == io.EOF {
					break
				} else {
					ir.ni.locks[bkey].Unlock()
					return nil, err
				}
			}
			buf := make([]byte, len)
			if _, err := io.ReadFull(file, buf); err != nil {
				ir.ni.locks[bkey].Unlock()
				return nil, err
			}
			if splited := strings.Split(string(buf), ":"); slices.Contains(ng, splited[0]) {
				if _, ex := alreadyInc[splited[1]]; ex {
					continue
				}
				alreadyInc[splited[1]] = struct{}{}
				result = append(result, splited[1])
			}
		}
		ir.ni.locks[bkey].Unlock()
	}

	return result, nil
}

func (ir *IndexRepository) saveOrLoadWord(words ...string) ([]uint32, error) {
	var idxs []uint32
	return idxs, ir.DB.Update(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for _, word := range words {
			if it, err := txn.Get(fmt.Appendf(nil, wordKey, word)); err != nil && err != badger.ErrKeyNotFound {
				return err
			} else if err == badger.ErrKeyNotFound {
				data := [4]byte{}
				binary.LittleEndian.PutUint32(data[:], atomic.AddUint32(&ir.ni.lIdx, 1)) // очевидно 0 будет проигнорирован
				if err := txn.Set(fmt.Appendf(nil, wordKey, word), data[:]); err != nil {
					return err
				}
				idxs = append(idxs, ir.ni.lIdx)
			} else {
				data, err := it.ValueCopy(nil)
				if err != nil {
					return err
				}
				decoded := binary.LittleEndian.Uint32(data)
				idxs = append(idxs, decoded)
			}
		}
		return nil
	})
}

func (ir *IndexRepository) getWordsFromSeq(nums ...uint32) ([]string, error) {
	var words []string
	return words, ir.DB.Update(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for _, id := range nums {
			if it, err := txn.Get(fmt.Appendf(nil, seqKey, id)); err != nil && err != badger.ErrKeyNotFound {
				return err
			} else if err == badger.ErrKeyNotFound {
				return fmt.Errorf("Storage internal error, invalid id: %d", id)
			} else {
				data, err := it.ValueCopy(nil)
				if err != nil {
					return err
				}
				words = append(words, string(data))
			}
		}
		return nil
	})
}

func (ir *IndexRepository) LoadIndexC() error {
	return ir.DB.View(func(txn *badger.Txn) error {
		if it, err := txn.Get([]byte(inck)); err == nil {
			val, err := it.ValueCopy(nil)
			if err != nil {
				return err
			}
			ir.ni.lIdx = binary.LittleEndian.Uint32(val)
		} else if err != badger.ErrKeyNotFound {
			return err
		}
		return nil
	})
}

func (ir *IndexRepository) UpdateIndexC() error {
	return ir.DB.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(inck), binary.LittleEndian.AppendUint32(nil, ir.ni.lIdx))
	})
}

type shingleChunkData struct {
	buffer	map[[4]uint64][][128]uint64
	counts 	map[[4]uint64]int
}

func (ir *IndexRepository) makeShingleKey(sh [4]uint64) string {
	strs := [4]string{}
	for i := range 4 {
		strs[i] = strconv.FormatUint(sh[i], 10)
	}
	return strings.Join(strs[:], ".")
}

func (ir *IndexRepository) IndexDocShingles(signature [128]uint64) error {
	for i := 0; i <= 128 - 4; i += 4 {
		var lshKey [4]uint64
        copy(lshKey[:], signature[i: i + 4])
		ir.mu.Lock()
		buf := append(ir.shingleIndexer.buffer[lshKey], signature)
		if len(buf) >= ir.chunkSize {
			chId := ir.shingleIndexer.counts[lshKey]
			ir.shingleIndexer.counts[lshKey]++
			toFlush := make([][128]uint64, len(buf)) // чтоб не обнулялся
			copy(toFlush, buf)
			delete(ir.shingleIndexer.buffer, lshKey)
			if err := ir.flushChunk(nil, chId, shingleKey, ir.makeShingleKey(lshKey), toFlush); err != nil {
				ir.mu.Unlock()
				return err
			}
			ir.mu.Unlock()
			continue
		}
		ir.shingleIndexer.buffer[lshKey] = buf
		ir.mu.Unlock()
	}
	return nil
}

// assuming that word contains only lower case letters
func extractTGrams(word string) []string {
	runes := []rune(word)
	out := make([]string, 0, 8)
	alIn := map[string]struct{}{}
	if len(runes) < 3 {
		return nil
	}
	for i := range len(runes) - 2 {
		ng := string(runes[i:i + 3])
		if _, ex := alIn[ng]; ex {
			continue
		}
		alIn[ng] = struct{}{}
		out = append(out, ng)
	}
	return out
}

func extractTGramsByBGram(word string) map[uint16][]string {
	runes := []rune(word)
	out := make(map[uint16][]string)
	alIn := map[string]struct{}{}
	for i := range len(runes) - 2 {
		ng := string(runes[i:i + 3])
		if _, ex := alIn[ng]; ex {
			continue
		}
		alIn[ng] = struct{}{}
		bikey := makeBiGramKey(ng)
		out[bikey] = append(out[bikey], ng)
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
	// for ng, buf := range ir.nGramIndexer.buffer {
	// 	if len(buf) == 0 {
	// 		continue
	// 	}
	// 	if err := ir.flushChunk(nil, ir.nGramIndexer.counts[ng], ngKey, ng, buf); err != nil {return err}
	// 	ir.nGramIndexer.counts[ng]++
	// 	delete(ir.nGramIndexer.buffer, ng)
	// }
	for sh, buf := range ir.shingleIndexer.buffer {
		if len(buf) == 0 {
			continue
		}
		if err := ir.flushChunk(nil, ir.shingleIndexer.counts[sh], shingleKey, ir.makeShingleKey(sh), buf); err != nil {return err}
		ir.shingleIndexer.counts[sh]++
		delete(ir.shingleIndexer.buffer, sh)
	}
	return nil
}

func (ir * IndexRepository) UpdateChunkingCounts() error {
	// prefixN := []byte("ng:")
	// if err := ir.DB.View(func(txn *badger.Txn) error {
	// 	it := txn.NewIterator(badger.DefaultIteratorOptions)
	// 	defer it.Close()
	// 	ir.mu.Lock()
	// 	defer ir.mu.Unlock()
	// 	for it.Seek(prefixN); it.ValidForPrefix(prefixN); it.Next() {
	// 		item := it.Item()
	// 		ngramButch := strings.Split(strings.TrimPrefix(string(item.Key()), string(prefixN)), ":")
	// 		if len(ngramButch) < 2 {
	// 			return fmt.Errorf("invalid data chunk")
	// 		}
	// 		lnum, err := strconv.Atoi(ngramButch[1])
	// 		if err != nil {
	// 			return err
	// 		}
	// 		ir.nGramIndexer.counts[ngramButch[0]] = max(ir.nGramIndexer.counts[ngramButch[0]], lnum)
	// 	}
	// 	return nil
	// }); err != nil {
	// 	return err
	// }
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