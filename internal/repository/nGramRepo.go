package repository

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/dgraph-io/badger/v3"
)

type BitSet struct {
	Bits [8]uint64 // 512 bit
}

func (bs *BitSet) Add(data uint32) {
	h1 := data * a
	h2 := data * b
	for i := range 5 {
		bit := (h1 + h2 * uint32(i)) % 512 // 0-511
		bs.Bits[bit/64] |= 1 << (bit % 64) // меняем 1 << bit % 64(0-63) бит на 1 в bit / 64(0-7) массиве
	}
}

func (bs *BitSet) Contain(data uint32) bool {
	h1 := data * a
	h2 := data * b
	for i := range 5 {
		bit := (h1 + h2 * uint32(i)) % 512 // 0-511
		if bs.Bits[bit/64] & (1 << (bit % 64)) == 0 { // если конкретный бит не был помечен
			return false
		}
	}
	return true
}

type RotatingBloom struct {
	Active, Standby [17576]BitSet // a * 676 + b * 26 + c
	Counts [17576]uint32
	Cap uint32
}

func (rb *RotatingBloom) Add(trigram uint16, seq uint32) bool {
	if rb.Active[trigram].Contain(seq) || rb.Standby[trigram].Contain(seq) {
		return false
	}

	rb.Active[trigram].Add(seq)
	rb.Standby[trigram].Add(seq)
	rb.Counts[trigram]++
	if rb.Counts[trigram] == rb.Cap {
		rb.rotate(trigram)
	}
	return true
}

func (rb *RotatingBloom) rotate(idx uint16) {
	rb.Standby[idx] = rb.Active[idx]
	rb.Active[idx] = BitSet{}
	rb.Counts[idx] = 0
}

type srequest struct {
	word string
	seqC chan uint32
}

type ngChunkIndex struct {
	chunks 	[676]*os.File // a * 26 + b
	locks 	[676]sync.RWMutex
	shards 	[shardSize]chan srequest
	Bloom 	*RotatingBloom
}

func NewWordIndex(Cap, bufSize uint32) *ngChunkIndex {
	chans := [shardSize]chan srequest{}
	for i := range shardSize {
		chans[i] = make(chan srequest, bufSize)
	}
	return &ngChunkIndex{
		Bloom: &RotatingBloom{Cap: Cap},
		shards: chans,
	}
}

func (ni *ngChunkIndex) saveBloom(gpath string) error {
	file, err := os.OpenFile(filepath.Join(gpath, BloomPath), os.O_CREATE | os.O_WRONLY | os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, *ni.Bloom); err != nil {
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
	if err := binary.Read(file, binary.LittleEndian, ni.Bloom); err != nil {
		return err
	}
	return nil
}

func makeBiGramKey(ng string) uint16 {
	a := ng[0]
	b := ng[1]
	if a > 'Z' {a -= 'a'} else {a -= 'A'}
	if b > 'Z' {b -= 'a'} else {b -= 'A'}
	return uint16(a) * 26 + uint16(b)
}

func makeTriGramKey(ng string) uint16 {
	a := ng[0]
	b := ng[1]
	c := ng[2]
	if a > 'Z' {a -= 'a'} else {a -= 'A'}
	if b > 'Z' {b -= 'a'} else {b -= 'A'}
	if c > 'Z' {c -= 'a'} else {c -= 'A'}
	return uint16(a) * 676 + uint16(b) * 26 + uint16(c)
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
	for _, word := range words {
		if len(word) < 2 {
			continue
		}
		req := srequest{word: word, seqC: make(chan uint32, 1)}
		var seq uint32
		shardKey := word[0]
		if shardKey > 'Z' {shardKey -= 'a'} else {shardKey -= 'A'}
		select {
		case ir.ni.shards[shardKey] <- req:
			select {
			case seq = <- req.seqC:
			case <-ir.ctx.Done():
				return nil
			}
		case <-ir.ctx.Done():
			return nil
		}

		for _, ng := range extractTGrams(word) {
			bkey := makeBiGramKey(ng)
			tkey := makeTriGramKey(ng)
			ir.ni.locks[bkey].Lock()
			if ir.ni.chunks[bkey] == nil {
				ngfile, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(ngPath, bkey)), os.O_APPEND | os.O_WRONLY | os.O_CREATE, 0600)
				if err != nil {
					ir.ni.locks[bkey].Unlock()
					return err
				}
				ir.ni.chunks[bkey] = ngfile
			}
			if !ir.ni.Bloom.Add(tkey, seq) {
				ir.ni.locks[bkey].Unlock()
				continue
			}
			if err := binary.Write(ir.ni.chunks[bkey], binary.LittleEndian, encNGData(seq, ng[2])); err != nil {
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
		file, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(ngPath, bkey)), os.O_RDONLY, 0600)
		if err != nil && os.IsExist(err) {
			return nil, err
		} else if err != nil {
			continue
		}
		defer file.Close()
		for {
			buf := make([]byte, 4 * 16) // 16 по uint32
			n, err := file.Read(buf)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
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
	}

	return ir.getWordsFromSeq(result...)
}

func (ir *IndexRepository) getWordsFromSeq(nums ...uint32) ([]string, error) {
	var words []string
	return words, ir.DB.View(func(txn *badger.Txn) error {
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

func (ir *IndexRepository) LoadIndexC() (uint32, error) {
	var id uint32
	return id, ir.DB.View(func(txn *badger.Txn) error {
		if it, err := txn.Get([]byte(inck)); err == nil {
			val, err := it.ValueCopy(nil)
			if err != nil {
				return err
			}
			id =  binary.LittleEndian.Uint32(val)
		} else if err != badger.ErrKeyNotFound {
			return err
		}
		return nil
	})
}

func (ir *IndexRepository) UpdateIndexC(id uint32) error {
	return ir.DB.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(inck), binary.LittleEndian.AppendUint32(nil, id))
	})
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