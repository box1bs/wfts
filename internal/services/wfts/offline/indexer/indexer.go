package indexer

import (
	"fmt"
	"sync"

	"wfts/configs"
	"wfts/internal/model"
	"wfts/internal/services/wfts/offline/indexer/spellChecker"
	"wfts/internal/services/wfts/offline/indexer/textHandling"
)

type repository interface {
	IndexUrlsByHash([32]byte, []byte) error
	GetPageUrlsByHash([32]byte) ([]byte, error)

	LoadVisitedUrls(*sync.Map) error
	SaveVisitedUrls(*sync.Map) error

	IndexNGrams([]string, int) error
	GetWordsByNGram(string, int) ([]string, error)
	IndexDocShingles([128]uint64) error
	GetSimilarSignatures([128]uint64) ([][128]uint64, error)
	FlushAll()

	UpdateBiFreq(map[[2]uint64]int) error
	GetFreq(uint64, uint64) (int, error)

	SaveSaltArrays([128]uint64, [128]uint64) error
	UploadSaltArrays() ([128]uint64, [128]uint64, error)

	IndexDocumentWords([32]byte, map[string]int, map[string][]model.Position) error
	GetDocumentsByWord(string) (map[[32]byte]model.WordCountAndPositions, error)

	SaveDocument(*model.Document) error
	GetDocumentByID([32]byte) (*model.Document, error)
	GetAllDocuments() ([]*model.Document, error)
	GetDocumentsCount() (int, error)
}

type indexer struct {
	repository
	stemmer 	*textHandling.EnglishStemmer
	sc 			*spellChecker.SpellChecker
	minHash 	*minHash
	mu 			*sync.RWMutex
}

func NewIndexer(repo repository, config *configs.ConfigData) (*indexer, error) {
	idx := &indexer{
		stemmer:   	textHandling.NewEnglishStemmer(),
		sc: 		spellChecker.NewSpellChecker(config.MaxTypo, config.NGramCount),
		mu: 		new(sync.RWMutex),
		repository: repo,
	}
	if a, b, err := idx.UploadSaltArrays(); err != nil && err.Error() != "Key not found" {
		return nil, err
	} else if err != nil && err.Error() == "Key not found" {
		if c, err := idx.GetDocumentsCount(); err != nil {
			return nil, err
		} else if c != 0 {
			return nil, fmt.Errorf("index isn't empty, but salt arrays is")
		}
		idx.minHash = NewHasher(a, b, true) // пересоздаем
	} else {
		idx.minHash = NewHasher(a, b, false) // просто получаем структуру
	}
	return idx, nil
}

func (idx *indexer) SaveHashArrays() error {
	return idx.SaveSaltArrays(idx.minHash.a, idx.minHash.b)
}

func (idx *indexer) GetAVGLen() (float64, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var tokens int
	docs, err := idx.GetAllDocuments()
	if err != nil {
		return 0, err
	}

	for _, doc := range docs {
		tokens += doc.TokenCount
	}

	return float64(tokens) / (float64(len(docs)) + 1), nil
}