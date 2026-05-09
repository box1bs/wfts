package scraper

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
)

type Stack struct {
	rw 		*os.File
	mOffset int64
}

const headerOffset = 2 // uint16

func InitStack(path string, maxOffset int64) (*Stack, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	return &Stack{
		rw: file,
		mOffset: maxOffset,
	}, nil
}

func (st *Stack) Close() error { return st.rw.Close() }

func (st *Stack) Push(data []byte) error {
	if offset, err := st.rw.Seek(0, io.SeekEnd); err != nil {
		return err
	} else if offset > int64(st.mOffset) {
		return nil
	}
	length := uint16(len(data))
	toWrite := make([]byte, headerOffset + length)
	binary.LittleEndian.PutUint16(toWrite[length:], length)
	copy(toWrite[:length], data)
	_, err := st.rw.Write(toWrite)
	return err
}

func (st *Stack) Pop() ([]byte, error) {
	size, err := st.rw.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if size < headerOffset {
		return nil, io.EOF
	}
	if _, err := st.rw.Seek(-headerOffset, io.SeekEnd); err != nil {
		return nil, err
	}
	var length uint16
	if err := binary.Read(st.rw, binary.LittleEndian, &length); err != nil {
		return nil, err
	}
	offset := size - headerOffset - int64(length)
	if offset < 0 {
		return nil, errors.New("corrupted file")
	}
	st.rw.Seek(offset, io.SeekStart)
	buf := make([]byte, length)
	if _, err := st.rw.Read(buf); err != nil {
		return nil, err
	}
	return buf, st.rw.Truncate(offset)
}