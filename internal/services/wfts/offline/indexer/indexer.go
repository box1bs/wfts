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
	stemmer 	*textHandling.EnglishStemmer
	sc 			*spellChecker.SpellChecker
	minHash 	*minHash
	mu 			*sync.RWMutex
	repository 	repository
}

func NewIndexer(repo repository, config *configs.ConfigData) *indexer {
	return &indexer{
		stemmer:   	textHandling.NewEnglishStemmer(),
		sc: 		spellChecker.NewSpellChecker(config.MaxTypo, config.NGramCount),
		mu: 		new(sync.RWMutex),
		repository: repo,
	}
}

func (idx *indexer) Index(vis *sync.Map, scrapeFunc func()) error {
	if err := idx.repository.LoadVisitedUrls(vis); err != nil {
		return err
	}
	defer idx.repository.SaveVisitedUrls(vis)
	defer idx.repository.FlushAll()
	if a, b, err := idx.repository.UploadSaltArrays(); err != nil && err.Error() != "Key not found" {
		return err
	} else if err != nil && err.Error() == "Key not found" {
		if c, err := idx.repository.GetDocumentsCount(); err != nil {
			return err
		} else if c != 0 {
			return fmt.Errorf("index isn't empty, but salt arrays is")
		}
		idx.minHash = NewHasher(a, b, true) // пересоздаем
	} else {
		idx.minHash = NewHasher(a, b, false) // просто получаем структуру
	}
	defer idx.repository.SaveSaltArrays(idx.minHash.a, idx.minHash.b)
	scrapeFunc()
	return nil
}

func (idx *indexer) GetAVGLen() (float64, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var tokens int
	docs, err := idx.repository.GetAllDocuments()
	if err != nil {
		return 0, err
	}

	for _, doc := range docs {
		tokens += doc.TokenCount
	}

	return float64(tokens) / (float64(len(docs)) + 1), nil
}

func (idx *indexer) SaveUrlsToBank(key [32]byte, data []byte) error {
	return idx.repository.IndexUrlsByHash(key, data)
}

func (idx *indexer) GetUrlsByHash(key [32]byte) ([]byte, error) {
	return idx.repository.GetPageUrlsByHash(key)
}