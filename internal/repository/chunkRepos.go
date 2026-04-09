package repository

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/dgraph-io/badger/v3"
)

const (
	shPath = "/shingles/chunk%d.bin"
	ngPath = "/ngs/ng%d.bin"
	BloomPath = "/bloom.bin"
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
	file, err := os.OpenFile(filepath.Join(ir.path, BloomPath), os.O_CREATE | os.O_WRONLY | os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, *ir.ni.bloom); err != nil {
		return err
	}
	return nil
}

func (ni *ngChunkIndex) loadBloom(path string) error {
	file, err := os.OpenFile(filepath.Join(path, BloomPath), os.O_RDONLY, 0600)
	if err != nil && os.IsExist(err) {
		return err
	} else if err != nil {
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

func encNGData(wordId uint32, lastLet byte) uint32 {
	return (wordId << 8) | uint32(lastLet)
}

func decNGData(data uint32) (uint32, uint8) {
	a := data >> 8
	b := uint8(data & 0xFF)
	return a, b
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
			if ir.ni.chunks[bkey] == nil {
				ngfile, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(ngPath, bkey)), os.O_APPEND | os.O_WRONLY | os.O_CREATE, 0600)
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
			if err := binary.Write(ir.ni.chunks[bkey], binary.LittleEndian, encNGData(sequence[i], ng[2])); err != nil {
				ir.ni.locks[bkey].Unlock()
				return err
			}
			ir.ni.locks[bkey].Unlock()
		}
	}
	return nil
}

func (ir *IndexRepository) GetWordsByTGrams(word string) ([]string, error) {
	result := make([]uint32, 0, 32)
	alreadyInc := map[uint32]struct{}{}
	ngs := extractTGramsByBGram(word)

	for bkey, ng := range ngs {
		ir.ni.locks[bkey].Lock()
		file, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(ngPath, bkey)), os.O_RDONLY, 0600)
		if err != nil && os.IsExist(err) {
			ir.ni.locks[bkey].Unlock()
			return nil, err
		} else if err != nil {
			ir.ni.locks[bkey].Unlock()
			continue
		}
		defer file.Close()
		for {
			buf := make([]byte, 4 * 16) // 16 по uint32
			n, err := file.Read(buf)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				ir.ni.locks[bkey].Unlock()
				return nil, err
			} else if n == 0 && err != nil {
				break
			}
			for i := range n / 4 {
				p := i * 4
				data := binary.LittleEndian.Uint32(buf[p:p + 4])
				seq, lastLatter := decNGData(data)
				if !slices.Contains(ng, lastLatter) {
					continue
				}
				if _, ex := alreadyInc[seq]; ex {
					continue
				}
				alreadyInc[seq] = struct{}{}
				result = append(result, seq)
			}
		}
		ir.ni.locks[bkey].Unlock()
	}

	return ir.getWordsFromSeq(result...)
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

type shingleIndex struct {
	chunks 	[512]*os.File
	mutexes	[512]sync.Mutex
}

func (ir *IndexRepository) IndexDocShingles(sign [128]uint64) error {
	for i := 0; i <= 128 - 8; i += 8 {
		var signChunk [8]uint64
		copy(signChunk[:], sign[i:i + 8])
		chunkKey := makeShingleKey(signChunk)
		ir.si.mutexes[chunkKey].Lock()
		if ir.si.chunks[chunkKey] == nil {
			file, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(shPath, chunkKey)), os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0600)
			if err != nil {
				ir.si.mutexes[chunkKey].Unlock()
				return err
			}
			ir.si.chunks[chunkKey] = file
		}
		var data [136]uint64
		copy(data[:8], signChunk[:])
		copy(data[8:], sign[:])
		if err := binary.Write(ir.si.chunks[chunkKey], binary.LittleEndian, data); err != nil {
			ir.si.mutexes[chunkKey].Unlock()
			return err
		}
		ir.si.mutexes[chunkKey].Unlock()
	}
	return nil
}

func (ir *IndexRepository) GetSimilarSigns(sign [128]uint64) ([][128]uint64, error) {
	signs := make([][128]uint64, 0)
	for i := 0; i <= 128 - 8; i += 8 {
		var signChunk [8]uint64
		copy(signChunk[:], sign[i:i + 8])
		chunkKey := makeShingleKey(signChunk)
		ir.si.mutexes[chunkKey].Lock()
		file, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(shPath, chunkKey)), os.O_RDONLY, 0600)
		if err != nil && os.IsExist(err) {
			ir.si.mutexes[chunkKey].Unlock()
			return nil, err
		} else if err != nil {
			ir.si.mutexes[chunkKey].Unlock()
			continue
		}
		defer file.Close()
		var data [136]uint64
		if err := binary.Read(file, binary.LittleEndian, &data); err != nil {
			ir.si.mutexes[chunkKey].Unlock()
			return nil, err
		}
		if !slices.Equal(data[:8], signChunk[:]) {
			ir.si.mutexes[chunkKey].Unlock()
			continue
		}
		ir.si.mutexes[chunkKey].Unlock()
		var sign [128]uint64
		copy(sign[:], data[8:])
		signs = append(signs, sign)
	}
	return signs, nil
}

func makeShingleKey(sign [8]uint64) uint16 {
	var key uint16
	for i := range 8 {
		key += uint16(sign[i] % 512)
	}
	return key % 512
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

func extractTGramsByBGram(word string) map[uint16][]uint8 {
	runes := []rune(word)
	out := make(map[uint16][]uint8)
	alIn := map[string]struct{}{}
	for i := range len(runes) - 2 {
		ng := string(runes[i:i + 3])
		if _, ex := alIn[ng]; ex {
			continue
		}
		alIn[ng] = struct{}{}
		bikey := makeBiGramKey(ng)
		out[bikey] = append(out[bikey], ng[2])
	}
	return out
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