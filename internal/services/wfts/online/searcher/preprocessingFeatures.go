package searcher

import (
	"math"

	"wfts/internal/model"
)

func calcBM25(idf, avgLen, tf, tokens float64) float64 {
	k1 := 1.2
	b := 0.75
	return idf * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * (math.Log(tokens / avgLen) + 0.01)))
}

const a = 50

func getMinQueryDistInDoc(positions [][]model.Position, lenQuery int) int {
	minDencity := math.MaxInt

	bs := func(cur, target int) int {
		arr := positions[cur]
		l, r := 0, len(arr)
		for l < r {
			m := (l + r) / 2
			if arr[m].I <= target {
				l = m + 1
			} else {
				r = m
			}
		}
		return l
	}

	for i := range lenQuery {
		for _, startPos := range positions[i] {
			cur := startPos.I
			last := cur
			valid := true
			for j := i + 1; j < lenQuery; j++ {
				arr := positions[j]
				if len(arr) == 0 {
					penalty := (lenQuery - (j - i)) * a
					minDencity = min(minDencity, penalty + last-cur)
					valid = false
					break
				}
				position := bs(j, last)
				if position >= len(arr) {
					valid = false
					break
				}
				last = arr[position].I
			}
			if !valid {
				continue
			}
			minDencity = min(minDencity, i * a + last-cur)
		}
	}

	return minDencity
}

func boyerMoorAlgorithm(url string, queryWords []string) float64 {
	wordInUrl := 0.0
	urlRunes := []rune(url)
	ul := len(urlRunes)
	for _, word := range queryWords {
		queryWord := []rune(word)
		l := len(queryWord)
		shift := map[rune]int{}
		for i, r := range queryWord {
			shift[r] = max(1, l - i - 1)
		}

		strp := l - 1
		sstrp := l - 1
		for strp < ul {
			if queryWord[sstrp] != urlRunes[strp] {
				if sh, ex := shift[urlRunes[strp]]; ex {
					strp += sh
				} else {
					strp += l
				}
			} else {
				tmp := l // чтобы если отрезки не равны не вернуться в ту же точку
				for sstrp >= 0 && queryWord[sstrp] == urlRunes[strp] {
					tmp++
					sstrp--
					strp--
				}
				if sstrp == -1 {
					wordInUrl += float64(l)
					break
				} else {
					strp += tmp
					sstrp = l - 1
				}
			}
		}
	}
	return math.Log(1 + wordInUrl)
}