package repository

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

const (
	shPath = "/shingles/chunk%d.bin"
	ngPath = "/ngs/ng%d.bin"
	BloomPath = "/bloom.bin"
	wordKey = "word:%s"
	seqKey = "num:%d"
	inck = "inc:"
	shardSize = 26
	a, b = 2654435761, 2246822519 // константы для рандомизации битов
)

type shingleIndex struct {
	chunks 	[512]*os.File
	mutexes	[512]sync.Mutex
}

func (ir *IndexRepository) IndexDocShingles(sign [128]uint64) error {
	b := unsafe.Slice((*byte)(unsafe.Pointer(&sign[0])), 128 * 8)
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
		if _, err := ir.si.chunks[chunkKey].Write(unsafe.Slice((*byte)(unsafe.Pointer(&signChunk[0])), 8 * 8)); err != nil {
			ir.si.mutexes[chunkKey].Unlock()
			return err
		}
		if _, err := ir.si.chunks[chunkKey].Write(b); err != nil {
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
		for {
			var key [8]uint64
			if _, err := io.ReadFull(ir.si.chunks[chunkKey], unsafe.Slice((*byte)(unsafe.Pointer(&key[0])), 8 * 8)); err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				ir.si.mutexes[chunkKey].Unlock()
				return nil, err
			} else if err != nil {
				break
			}
			if key != signChunk {
				if _, err := ir.si.chunks[chunkKey].Seek(128 * 8, io.SeekCurrent); err != nil { // пропускаем sign, чтобы проверить ключ следующего
					ir.si.mutexes[chunkKey].Unlock()
					return nil, err
				}
				continue
			}
			var sign [128]uint64
			if _, err := io.ReadFull(ir.si.chunks[chunkKey], unsafe.Slice((*byte)(unsafe.Pointer(&sign[0])), 128 * 8)); err != nil {
				ir.si.mutexes[chunkKey].Unlock()
				return nil, err
			}
			signs = append(signs, sign)
		}
		ir.si.mutexes[chunkKey].Unlock()
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