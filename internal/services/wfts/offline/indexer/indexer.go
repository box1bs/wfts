package indexer

import (
	"fmt"
	"sync"

	"wfts/configs"
	"wfts/internal/model"
	"wfts/internal/services/wfts/offline/indexer/spellChecker"
	"wfts/internal/services/wfts/offline/indexer/textHandling"

	lr "github.com/box1bs/logistic_regression_go"
)

type repository interface {
	IndexUrlsByHash([32]byte, []byte) error
	GetPageUrlsByHash([32]byte) ([]byte, error)

	LoadVisitedUrls(*sync.Map) error
	SaveVisitedUrls(*sync.Map) error

	IndexTriGrams([]string) error
	GetWordsByTGrams(string) ([]string, error)
	IndexDocShingles([128]uint64) error
	GetSimilarSigns([128]uint64) ([][128]uint64, error)

	UpdateBiFreq(map[[2]uint64]int) error
	GetFreq(uint64, uint64) (int, error)

	SaveSaltArrays([128]uint64, [128]uint64) error
	UploadSaltArrays() (*[128]uint64, *[128]uint64, error)

	IndexDocumentWords([32]byte, map[string]int, map[string][]model.Position) error
	GetDocumentsByWord(string) (map[[32]byte]model.WordCountAndPositions, error)

	SaveDocument(*model.Document) error
	GetDocumentByID([32]byte) (*model.Document, error)
	GetAllDocuments() ([]*model.Document, error)
	GetDocumentsCount() (int, error)

	UpdateIndexC() error
	SaveBloom() error
}

type indexer struct {
	repository
	model 		lr.RegressionModel
	scaler 		lr.Scaler
	stemmer 	*textHandling.EnglishStemmer
	sc 			*spellChecker.SpellChecker
	minHash 	*minHash
	mu 			*sync.RWMutex
}

func NewIndexer(repo repository, config *configs.ConfigData) (*indexer, error) {
	model := lr.LogisticRegressor(0.4)
	if err := model.LoadFromFile("./model"); err != nil {
		return nil, err
	}
	scaler := lr.RobustScaler()
	if err := scaler.LoadFromFile("./scaler"); err != nil {
		return nil, err
	}
	idx := &indexer{
		model: 		model,
		scaler: 	scaler,
		stemmer:   	textHandling.NewEnglishStemmer(),
		sc: 		spellChecker.NewSpellChecker(config.MaxTypo),
		mu: 		new(sync.RWMutex),
		repository: repo,
	}
	if a, b, err := idx.UploadSaltArrays(); err != nil {
		return nil, err
	} else if a == nil {
		if c, err := idx.GetDocumentsCount(); err != nil {
			return nil, err
		} else if c != 0 {
			return nil, fmt.Errorf("index isn't empty, but salt arrays is")
		}
		idx.minHash = NewHasher([128]uint64{}, [128]uint64{}, true) // пересоздаем
	} else {
		idx.minHash = NewHasher(*a, *b, false) // просто получаем структуру
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