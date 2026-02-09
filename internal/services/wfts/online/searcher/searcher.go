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

func (s *Searcher) Search(wr io.Writer, query string, maxLen int) ([]*model.Document, []*model.DocRanking, *model.SearchMetrics) {
	log := model.NewLogger(slog.New(slog.NewJSONHandler(wr, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
		Level: slog.LevelInfo,
	})).With(slog.String("query", query)))
	metrics := &model.SearchMetrics{}
	t1p := time.Now()
	searchContext := context.WithValue(context.Background(), model.DefLogKey, log)
	words, index, err := s.idx.HandleTextQuery(searchContext, query)
	if err != nil {
		log.Errorf("handling words error: %v",  err)
		return nil, nil, nil
	}
	metrics.HandleQuery = time.Since(t1p)
	
	queryLen := len(words)
	
	avgLen, err := s.idx.GetAVGLen()
	if err != nil {
		log.Errorf("%v", err)
		return nil, nil, nil
	}
	
	length, err := s.repo.GetDocumentsCount()
	if err != nil {
		log.Errorf("%v", err)
		return nil, nil, nil
	}
	
	t2p := time.Now()
	rank := make(map[[32]byte]*model.DocRanking)
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
				return nil, nil, nil
			}
			resChan <- docs[id]
		}
	}
	close(resChan)

	paddingMask := []model.Position{}
	for id, doc := range docs {
		r := &model.DocRanking{}
		positions := make([][]model.Position, queryLen)
		for i := range words {
			if item, ex := index[i][id]; ex {
				tf := float64(item.Count) / float64(doc.TokenCount)
				r.Tf_Idf += tf * idf[i]
				r.BM25 += calcBM25(idf[i], tf, doc, avgLen)
				positions[i] = item.Positions
			} else {
				positions[i] = paddingMask
			}
		}
		r.TermProximity = getMinQueryDistInDoc(positions, queryLen)
		r.LogLenWordInURL = boyerMoorAlgorithm(strings.ToLower(doc.URL), words)
		for i := range words {
			if item, ex := index[i][id]; ex {
				for i := 0; i < min(item.Count, 500) && !r.HasWordInHeader; i++ {
					r.HasWordInHeader = item.Positions[i].Type == model.HeaderType
				}
			}
			if r.HasWordInHeader {
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
		return nil, nil, nil
	}
	metrics.TotalResults = length

	t3p := time.Now()

	sort.Slice(result, func(i, j int) bool {
		ir, jr := rank[result[i].Id], rank[result[j].Id]
		if ir.BM25 != jr.BM25 {
			return ir.BM25 > jr.BM25
		}
		if ir.Tf_Idf != jr.Tf_Idf {
			return ir.Tf_Idf > jr.Tf_Idf
		}
		return ir.TermProximity > jr.TermProximity
	})

	topN := result[:min(length, maxLen)]
	sort.Slice(topN, func(i, j int) bool {
		ir, jr := rank[topN[i].Id], rank[topN[j].Id]
		if ir.LogLenWordInURL != jr.LogLenWordInURL {
			return ir.LogLenWordInURL > jr.LogLenWordInURL
		}
		return ir.HasWordInHeader && !jr.HasWordInHeader
	})
	metrics.Sort = time.Since(t3p)

	length = len(topN)
	topNMetrics := make([]*model.DocRanking, length)
	for i := 0; i < length; i++ {
		topNMetrics[i] = rank[topN[i].Id]
	}

	metrics.Total = time.Since(t1p)
	return topN, topNMetrics, metrics
}