package repository

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"
)

const (
	shPath = "/shingles/chunk%d.bin"
	ngPath = "/ngs/ng%d.bin"
	wordKey = "word:%s"
	seqKey = "num:%d"
	inck = "inc:"
	shardSize = 26
	a, b = 2654435761, 2246822519 // константы для рандомизации битов
)

type shingleIndex struct {
	writes 	[512]chan [8 * 8 + 128 * 8]byte
	done 	[512]chan struct{}
}

func (ir *IndexRepository) IndexDocShingles(sign [128]uint64) error {
	b := unsafe.Slice((*byte)(unsafe.Pointer(&sign[0])), 128 * 8)
	for i := 0; i <= 128 - 8; i += 8 {
		var signChunk [8]uint64
		copy(signChunk[:], sign[i:i + 8])
		chunkKey := makeShingleKey(signChunk)
		data := [8 * 8 + 128 * 8]byte{}
		copy(data[:], unsafe.Slice((*byte)(unsafe.Pointer(&signChunk[0])), 8 * 8))
		copy(data[8 * 8:], b)
		select {
		case ir.si.writes[chunkKey] <- data:
		case <-ir.ctx.Done():
			return ir.ctx.Err()
		}
	}
	return nil
}

func (ir *IndexRepository) GetSimilarSigns(sign [128]uint64) ([][128]uint64, error) {
	signs := make([][128]uint64, 0)
	for i := 0; i <= 128 - 8; i += 8 {
		var signChunk [8]uint64
		copy(signChunk[:], sign[i:i + 8])
		chunkKey := makeShingleKey(signChunk)
		file, err := os.OpenFile(filepath.Join(ir.path, fmt.Sprintf(shPath, chunkKey)), os.O_RDONLY, 0600)
		if err != nil && os.IsExist(err) {
			return nil, err
		} else if err != nil {
			continue
		}
		defer file.Close()

		for {
			var key [8]uint64
			if _, err := io.ReadFull(file, unsafe.Slice((*byte)(unsafe.Pointer(&key[0])), 8 * 8)); err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				return nil, err
			} else if err != nil {
				break
			}
			if key != signChunk {
				if _, err := file.Seek(128 * 8, io.SeekCurrent); err != nil { // пропускаем sign, чтобы проверить ключ следующего
					return nil, err
				}
				continue
			}
			var sign [128]uint64
			if _, err := io.ReadFull(file, unsafe.Slice((*byte)(unsafe.Pointer(&sign[0])), 128 * 8)); err != nil {
				return nil, err
			}
			signs = append(signs, sign)
		}
	}
	return signs, nil
}

func (ir *IndexRepository) shingleWorker(i int) {
	defer close(ir.si.done[i])

	fname := fmt.Sprintf(shPath, i)
	file, err := os.OpenFile(filepath.Join(ir.path, fname), os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0600)
	if err != nil {
		ir.log.Errorf("open file '%s' failed: %v", fname, err)
		return
	}

	for toWrite := range ir.si.writes[i] {
		if _, err := file.Write(toWrite[:]); err != nil {
			ir.log.Errorf("shingle write failed: %v", err)
			return
		}
	}
}

func (si *shingleIndex) stop() {
	for i := range 512 {
		close(si.writes[i])
	}

	for i := range 512 {
		<-si.done[i]
	}
}

func makeShingleKey(sign [8]uint64) uint16 {
	var key uint16
	for i := range 8 {
		key += uint16(sign[i] % 512)
	}
	return key % 512
}