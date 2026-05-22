package repository

import (
	"fmt"

	"github.com/cockroachdb/pebble"
)

const urlsKey = "hashKey:%s"

func (ir *IndexRepository) IndexUrlsByHash(hash [32]byte, urlsStruct []byte) error {
	return ir.DB.Set(fmt.Appendf(nil, urlsKey, hash), urlsStruct, pebble.NoSync)
}

func (ir *IndexRepository) GetPageUrlsByHash(hash [32]byte) ([]byte, error) {
	val, closer, err := ir.DB.Get(fmt.Appendf(nil, urlsKey, hash))
	if err != nil {
		return nil, err
	}
	urlsStruct := make([]byte, len(val))
	copy(urlsStruct, val)
	closer.Close()
	return urlsStruct, nil
}