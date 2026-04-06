package spellChecker

import "math"

const ( // константы зашумленного канала
    a = 2
    b = 3
    c = 2
)

type SpellChecker struct {
	maxTypo     int
}

func NewSpellChecker(maxTypoLen int) *SpellChecker {
	return &SpellChecker{
		maxTypo: maxTypoLen,
	}
}

func (s *SpellChecker) BestReplacement(in *[]string, index int, candidates []string, scores [][2]float64) {
    if len(candidates) == 0 {
        return
    }
    best := candidates[0]
    bscore := -math.MaxFloat32
    orig := []rune((*in)[index])
	for i, candidate := range candidates {
		if score, distance := s.noisyChannelScore(orig, []rune(candidate), scores[i][0], scores[i][1]); score > bscore && distance < s.maxTypo {
            best = candidate
            bscore = score
        }
	}
	if bscore != -math.MaxFloat32 {
		(*in)[index] = best
		return
	}

	set := map[string]int{}
	for i, candidate := range candidates {
		set[candidate] = i
	}
	repl := make([]string, 2)
	wordLen := len(orig)
	for i := 3; i < wordLen - 2; i++ { // считаем что длина токена >= 3
		part1 := string(orig[:i])
		if _, ex := set[part1]; !ex {
			continue
		}
		part2 := string(orig[i:])
		if _, ex := set[part2]; !ex {
			continue
		}
		if score := math.Log(max((a * scores[set[part1]][0] + b * scores[set[part2]][1]), 0.00001)); score > bscore {
			bscore = score
			repl[0], repl[1] = part1, part2
		}
	}
	*in = append((*in)[:index], append(repl, (*in)[index + 1:]...)...)
}

func (s *SpellChecker) noisyChannelScore(word1, word2 []rune, probabilityLog1, probabilityLog2 float64) (float64, int) {
	ld := levenshteinDistance(word1, word2, s.maxTypo)
	return math.Pow(c, -float64(ld)) * max((a * probabilityLog1 + b * probabilityLog2), 0.00001), ld
}

func levenshteinDistance(word1, word2 []rune, maxTypo int) int {
    l1, l2 := len(word1), len(word2)
    if l1 == 0 {
        return l2
    }
    if l2 == 0 {
        return l1
    }
	dp := make([]int, l2 + 1)
	for i := 0; i < l2 + 1; i++ {
		dp[i] = i
	}

	for i := range l1 {
		dp[0] = i
		upLeft := dp[0]
		minRow := i
		for j := 1; j <= l2; j++ {
			nextUpLeft := dp[j]
			if word1[i] == word2[j-1] {
				dp[j] = upLeft
			} else {
				dp[j] = min(dp[j - 1], upLeft, dp[j]) + 1
			}
			upLeft = nextUpLeft
			minRow = min(minRow, dp[j])
		}
        if minRow > maxTypo {
            return maxTypo + 1
        }
	}
	return dp[l2]
}