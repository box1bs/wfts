package repository

import (
	"encoding/json"
	"fmt"

	"wfts/internal/model"

	"github.com/cockroachdb/pebble"
)

const (
	DocumentKeyPrefix     = "doc:%s"
	WordDocumentKeyFormat = "ri:%s_%x"
)

type docDBSt struct {
	Id        []byte      `json:"id"`
	URL       string      `json:"url"`
	TokenCount int        `json:"words_count"`
}

func (ir *IndexRepository) documentToBytes(doc *model.Document) ([]byte, error) {
	p := docDBSt{
		Id:        doc.Id[:],
		URL:       doc.URL,
		TokenCount:doc.TokenCount,
	}
	return json.Marshal(p)
}

func (ir *IndexRepository) bytesToDocument(body []byte) (*model.Document, error) {
	p := docDBSt{}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	
	if len(p.Id) != 32 {
		return nil, fmt.Errorf("invalid id length: %d", len(p.Id))
	}
	
	var idArr [32]byte
	copy(idArr[:], p.Id)
	
	return &model.Document{
		Id:        idArr,
		URL:       p.URL,
		TokenCount:p.TokenCount,
	}, nil
}

func (ir *IndexRepository) SaveDocument(doc *model.Document) error {
	docBytes, err := ir.documentToBytes(doc)
	if err != nil {
		return err
	}
	ir.mu.Lock()
	defer ir.mu.Unlock()
	return ir.DB.Set(fmt.Appendf(nil, DocumentKeyPrefix, doc.Id[:]), docBytes, pebble.NoSync)
}

func (ir *IndexRepository) GetDocumentByID(docID [32]byte) (*model.Document, error) {
	val, closer, err := ir.DB.Get(fmt.Appendf(nil, DocumentKeyPrefix, docID[:]))
	if err != nil {
		return nil, err
	}
	docBytes := make([]byte, len(val))
	copy(docBytes, val)
	closer.Close()
	return ir.bytesToDocument(docBytes)
}

func (ir *IndexRepository) GetAllDocuments() ([]*model.Document, error) {
	prefix := []byte("doc:")
	documents := make([]*model.Document, 0, 512)
	it, err := ir.DB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
    	UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer it.Close()

	for it.First(); it.Valid(); it.Next() {
		docBytes := make([]byte, len(it.Value()))
		copy(docBytes, it.Value())

		doc, err := ir.bytesToDocument(docBytes)
		if err != nil {
			return nil, err
		}
		documents = append(documents, doc)
	}

	return documents, nil
}

func (ir *IndexRepository) GetDocumentsCount() (int, error) {
	var count int
	docPrefix := []byte("doc:")
	it, err := ir.DB.NewIter(&pebble.IterOptions{
		LowerBound: docPrefix,
    	UpperBound: prefixUpperBound(docPrefix),
		KeyTypes:   pebble.IterKeyTypePointsOnly,
	})
	if err != nil {
		return 0, err
	}
	defer it.Close()
	for it.First(); it.Valid(); it.Next() {
		count++
	}
	return count, nil
}