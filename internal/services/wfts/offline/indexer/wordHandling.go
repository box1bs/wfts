package indexer

import (
	"context"
	"fmt"
	"math"

	"wfts/internal/model"
	"wfts/internal/services/wfts/offline/indexer/textHandling"

	lr "github.com/box1bs/logistic_regression_go"
)

func (idx *Indexer) HandleDocumentWords(ctx context.Context, doc *model.Document, externalFeatures *model.CrawlFeatures, priority *float64, passages []model.Passage) error {
	stem := make(map[string]int, 512)
	pos := make(map[string][]model.Position, 512)
	
	logger := ctx.Value(model.DefLogKey).(*model.Logger)
	if logger == nil {
		return fmt.Errorf("context canceled")
	}
	i := 0
	allWordTokens := make([]string, 0, 256)
	for _, passage := range passages {
		orig, stemmed, err := idx.stemmer.TokenizeAndStem(passage.Text)
		if err != nil {
			return err
		}
		if len(stemmed) == 0 {
			continue
		}

		allWordTokens = append(allWordTokens, orig...)
		for _, w := range stemmed {
			if len(w.Value) > 64 {
				continue
			}
			stem[w.Value]++
			pos[w.Value] = append(pos[w.Value], model.NewTypeTextObj[model.Position](passage.Type, "", i))
			i++
		}
	}
	doc.TokenCount = i
	utokens := len(pos)
	skipIndexAdding := false
	X := lr.Vec(float64(externalFeatures.DomDepth), float64(externalFeatures.TagCount), float64(externalFeatures.UrlCount),
	float64(doc.TokenCount), float64(utokens), float64(doc.TokenCount) / float64(externalFeatures.UrlCount), float64(utokens) / float64(doc.TokenCount),
	float64(externalFeatures.UrlLen), float64(externalFeatures.PathLen), float64(externalFeatures.HostLen))
	idx.scaler.Scale1D(X)
	score := idx.model.Predict(X)[0]
	if score == 0 && doc.TokenCount < 150 {
		skipIndexAdding = true
	}

	sim := 0.0
	if l := len(allWordTokens); !skipIndexAdding && l > 4 {
		sign := idx.minHash.CreateSignature(allWordTokens[:min(5000, l)])
		conds, err := idx.repository.GetSimilarSigns(sign)
		if err != nil {
			return err
		}
		if sim = calcSim(sign, conds); sim > 0.8 {
			logger.Debugf("finded %f similar page, with word tokens len: %d", sim, len(allWordTokens))
			return fmt.Errorf("page already indexed")
		}
		if err := idx.repository.IndexDocShingles(sign); err != nil && err != context.Canceled {
			return err
		}
	}

	bigrams := make(map[[2]uint64]int)
	for j := 1; j < len(allWordTokens); j++ {
		bigrams[[2]uint64{idx.minHash.Hash64(allWordTokens[j - 1]), idx.minHash.Hash64(allWordTokens[j])}]++
	}
	if err := idx.repository.UpdateBiFreq(bigrams); err != nil {
		logger.Errorf("error updating bigrams frequency: %v", err)
		return err
	}
	if skipIndexAdding {
		logger.Debugf("skipped by model decision")
		return fmt.Errorf("skipped by model decision")
	}
	if err := idx.repository.SaveDocument(doc); err != nil {
		logger.Errorf("error saving document: %v", err)
		return err
	}
	if err := idx.repository.IndexDocumentWords(doc.Id, stem, pos); err != nil {
		logger.Errorf("error indexing document words: %v", err)
		return err
	}
	if err := idx.repository.IndexTriGrams(allWordTokens); err != nil && err != context.Canceled {
		logger.Errorf("error indexing ngrams: %v", err)
		return err
	}

	*priority += math.Log(float64(utokens) + 1) + 1 // (1 + sameDomain) * (log(linksNumber + 1) + log(UniqTokenCount + 1) + 1) / ((parentDepth + 1) * (1 - simRate) * (log(tokenCount + 1) + 1)) * e**(-a * (maxDepth - (scrapedDepth - depth))) // наивная метрика приоритизации
	*priority /= ((math.Log(float64(doc.TokenCount) + 1) + 1) * (1 - sim))
	return nil
}

func (idx *Indexer) HandleTextQuery(ctx context.Context, text string) ([]string, []map[[32]byte]model.WordCountAndPositions, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	logger := ctx.Value(model.DefLogKey).(*model.Logger)
	if logger == nil {
		return nil, nil, fmt.Errorf("context canceled")
	}
	reverthIndex := []map[[32]byte]model.WordCountAndPositions{}
	words, stemmed, err := idx.stemmer.TokenizeAndStem(text)
	lenStem := len(stemmed)
	if lenStem == 0 {
		return nil, nil, fmt.Errorf("empty tokens")
	}
	lenWords := len(words)
	stemmedTokens := make([]string, 0, lenStem)
	wordPos := 0
	isTwoWordCorrection := false
	lastDoubleCorrPointer := lenStem

	for i := range lenStem {
		documents, err := idx.repository.GetDocumentsByWord(stemmed[i].Value)
		if err != nil {
			return nil, nil, err
		}
		if len(documents) == 0 && stemmed[i].Type == textHandling.WORD { // исправляем только слова
			conds, err := idx.repository.GetWordsByTGrams(words[wordPos])
			if err != nil {
				return nil, nil, err
			}
			lenCandidates := len(conds)
			scores := make([][2]float64, lenCandidates)
			left := uint64(0)
			if wordPos > 0 {
				left = idx.minHash.Hash64(words[wordPos - 1])
			}
			right := uint64(0)
			if wordPos + 1 < lenWords {
				right = idx.minHash.Hash64(words[wordPos + 1])
			}
			if right != 0 || left != 0 {
				for j := range lenCandidates {
					cond := idx.minHash.Hash64(conds[j])
					lscore := 0
					if left != 0 {
						lscore, err = idx.repository.GetFreq(left, cond)
						if err != nil {
							return nil, nil, err
						}
					}
					rscore := 0
					if right != 0 {
						rscore, err = idx.repository.GetFreq(cond, right)
						if err != nil {
							return nil, nil, err
						}
					}
					scores[j][0], scores[j][1] = math.Log(float64(1 + lscore)), math.Log(float64(1 + rscore)) // снижаем зависимость результата от контекстуального совпадения
				}
			}
			tmp := lenWords
			tmpArr := make([]string, lenWords)
			copy(tmpArr, words)
			idx.sc.BestReplacement(&words, wordPos, conds, scores)
			logger.Infof("word '%s' replaced with '%s'", tmpArr[wordPos], words[wordPos])
			_, stem, err := idx.stemmer.TokenizeAndStem(words[wordPos])
			if err != nil {
				return nil, nil, err
			}
			if len(stem) == 0 { // если заменяется на стоп слово
				wordPos++
				continue
			}
			stemmed[i] = stem[0]
			documents, err = idx.repository.GetDocumentsByWord(stem[0].Value)
			if err != nil {
				return nil, nil, err
			}
			if tmp < len(words) {
				wordPos++
				lenWords++
				_, stem, err := idx.stemmer.TokenizeAndStem(words[wordPos])
				if err != nil {
					return nil, nil, err
				}
				stemmed = append(stemmed, stem[0])
				docs, err := idx.repository.GetDocumentsByWord(stem[0].Value)
				if err != nil {
					return nil, nil, err
				}
				for k, v := range docs {
					if _, ex := documents[k]; !ex {
						documents[k] = v
						continue
					}
					tmp := model.WordCountAndPositions{}
					tmp.Positions = append(documents[k].Positions, v.Positions...)
					tmp.Count = documents[k].Count + v.Count
					documents[k] = tmp
				}
			}
		}
		stemmedTokens = append(stemmedTokens, stemmed[i].Value)
		reverthIndex = append(reverthIndex, documents)
		if stemmed[i].Type == textHandling.WORD {
			wordPos++
		}
		if isTwoWordCorrection {
			stemmedTokens = append(stemmedTokens, stemmed[lastDoubleCorrPointer].Value)
			lastDoubleCorrPointer++
		}
	}

	return stemmedTokens, reverthIndex, err
}

func calcSim(curSign [128]uint64, condidates [][128]uint64) float64 {
	result := 0.0
	l := len(condidates)
	for i := range l {
		sum := 0
		for j := range 128 {
			if curSign[j] == condidates[i][j] {
				sum++
			}
		}
		sim := float64(sum) / 128.0
		result = max(result, sim)
	}
	return result
}