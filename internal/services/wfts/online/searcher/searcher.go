package searcher

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"wfts/internal/model"
)

type index interface {
	HandleTextQuery(context.Context, string) ([]string, []map[[32]byte]model.WordCountAndPositions, error)
	GetAVGLen() (float64, error)
}

type resitory interface {
	GetDocumentsCount() (int, error)
	GetDocumentByID([32]byte) (*model.Document, error)
}

type Searcher struct {
	idx 		index
	repo 	 	resitory
}

func NewSearcher(idx index, repo resitory) *Searcher {
	return &Searcher{
		idx:       	idx,
		repo: 	 	repo,
	}
}

type requestRanking struct {
	tf_idf 				float64
	bm25 				float64
	logLenWordInURL 	float64
	termProximity 		int
	hasWordInHeader 	bool
	//any ranking scores
}

func (s *Searcher) Search(wr io.Writer, query string, maxLen int) ([]*model.Document, *model.SearchMetrics) {
	log := model.NewLogger(slog.New(slog.NewJSONHandler(wr, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
	})).With(slog.String("query", query)))
	metrics := &model.SearchMetrics{}
	t1p := time.Now()
	searchContext := context.WithValue(context.Background(), 0, log)
	words, index, err := s.idx.HandleTextQuery(searchContext, query)
	if err != nil {
		log.Errorf("handling words error: %v",  err)
		return nil, nil
	}
	metrics.HandleQuery = time.Since(t1p)
	
	queryLen := len(words)
	
	avgLen, err := s.idx.GetAVGLen()
	if err != nil {
		log.Errorf("%v", err)
		return nil, nil
	}
	
	length, err := s.repo.GetDocumentsCount()
	if err != nil {
		log.Errorf("%v", err)
		return nil, nil
	}
	
	t2p := time.Now()
	rank := make(map[[32]byte]*requestRanking)
	result := make([]*model.Document, 0, min(1000, length))
	resChan := make(chan *model.Document, 100)

	idf := make([]float64, queryLen)
	docs := make(map[[32]byte]*model.Document)
	go func() {
		for doc := range resChan {
			result = append(result, doc)
		}
	}()
	for i := range words {
		idf[i] = math.Log(float64(length) / float64(len(index[i]) + 1)) + 1
		for id := range index[i] {
			if _, ex := docs[id]; ex {
				continue
			}
			docs[id], err = s.repo.GetDocumentByID(id)
			if err != nil {
				return nil, nil
			}
			resChan <- docs[id]
		}
	}
	close(resChan)

	for id, doc := range docs {
		r := &requestRanking{}
		for i := range words {
			if item, ex := index[i][id]; ex {
				tf := float64(item.Count) / float64(docs[id].TokenCount)
				r.tf_idf += tf * idf[i]
				r.bm25 += calcBM25(idf[i], tf, doc, avgLen)
			}
		}
		positions := [][]model.Position{}
		for i := range words {
			if item, ex := index[i][id]; ex {
				positions = append(positions, item.Positions)
			}
		}
		r.termProximity = getMinQueryDistInDoc(positions, queryLen)
		r.logLenWordInURL = boyerMoorAlgorithm(strings.ToLower(doc.URL), words)
		for i := range words {
			if item, ex := index[i][id]; ex {
				for i := 0; i < item.Count && !r.hasWordInHeader; i++ {
					r.hasWordInHeader = item.Positions[i].Type == model.HeaderType
				}
			}
			if r.hasWordInHeader {
				break
			}
		}
		rank[id] = r
	}
	
	log.Infof("result len: %d", len(result))
	metrics.FetchAndProcess = time.Since(t2p)

	length = len(result)
	if length == 0 {
		log.Infof("empty result")
		return nil, nil
	}

	t3p := time.Now()

	sort.Slice(result, func(i, j int) bool {
		if rank[result[i].Id].bm25 != rank[result[j].Id].bm25 {
			return rank[result[i].Id].bm25 > rank[result[j].Id].bm25
		}
		if rank[result[i].Id].tf_idf != rank[result[j].Id].tf_idf {
			return rank[result[i].Id].tf_idf > rank[result[j].Id].tf_idf
		}
		return rank[result[i].Id].termProximity > rank[result[j].Id].termProximity
	})

	topN := result[:min(length, maxLen)]
	sort.Slice(topN, func(i, j int) bool {
		if rank[topN[i].Id].logLenWordInURL != rank[topN[j].Id].logLenWordInURL {
			return rank[topN[i].Id].logLenWordInURL > rank[topN[i].Id].logLenWordInURL
		}
		return rank[topN[i].Id].hasWordInHeader && !rank[topN[j].Id].hasWordInHeader
	})

	metrics.Sort = time.Since(t3p)
	metrics.Total = time.Since(t1p)
	return topN, metrics
}