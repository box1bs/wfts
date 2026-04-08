package spellChecker

import "testing"

func TestBestReplacement(t *testing.T) {
    tests := []struct {
        name           string
		query          []string
        replacements   []string
        scores         [][2]float64
		expectedIndex  []int
        expectedWords  []int
        expectedLen    int
        checkLen       bool
    }{
        {
            name:          "simple replacement", 
            query:         []string{"test", "qury", "text"},
            replacements:  []string{"query", "qures", "quit"},
            scores:        [][2]float64{{0.99, 0.4}, {0.25, 0.25}, {0.9, 0}},
			expectedIndex: []int{0},
            checkLen:      false,
            expectedLen:   3,
            expectedWords: []int{1},
        },
        {
            name:          "splitting replacement",
            query:         []string{"test", "querytext", "replacement"},
            replacements:  []string{"query", "qures", "quit", "text"},
            scores:        [][2]float64{{0.99, 0.4}, {0.1, 0.2}, {0.9, 0}, {0.55, 0.6}},
			expectedIndex: []int{0, 3},
            checkLen:      true,
            expectedLen:   4,
            expectedWords: []int{1, 2},
        },
        {
            name:          "splitting replacement with stop word",
            query:         []string{"test", "somerequest", "replacement"},
            replacements:  []string{"some", "something", "request"},
            scores:        [][2]float64{{0.99, 0.4}, {0.9, 0}, {0.55, 0.6}},
			expectedIndex: []int{0, 2},
            checkLen:      true,
            expectedLen:   4,
            expectedWords: []int{1, 2},
        },
    }
    
	sc := NewSpellChecker(3)
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            testQuery := make([]string, len(tt.query))
            copy(testQuery, tt.query)
            baseLen := len(testQuery)
            sc.BestReplacement(&testQuery, tt.expectedWords[0], tt.replacements, tt.scores)
            
            if tt.checkLen {
                if l := len(testQuery); baseLen == l || l != tt.expectedLen {
                    t.Errorf("word: %s, wasn't replaced", testQuery[tt.expectedWords[0]])
                    return
                }
                if testQuery[tt.expectedWords[0]] != tt.replacements[tt.expectedIndex[0]] && 
                   testQuery[tt.expectedWords[1]] != tt.replacements[tt.expectedIndex[1]] {
                    t.Errorf("invalid replacement value %s : %s, instead of %s : %s", 
                        testQuery[tt.expectedWords[0]], testQuery[tt.expectedWords[1]], 
                        tt.replacements[tt.expectedIndex[0]], tt.replacements[tt.expectedIndex[1]])
                }
            } else {
                if testQuery[tt.expectedWords[0]] != tt.replacements[tt.expectedIndex[0]] {
                    t.Errorf("invalid replacement value %s, instead of %s", testQuery[tt.expectedWords[0]], tt.replacements[tt.expectedIndex[0]])
                }
            }
        })
    }
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
        name     string
        word1    string
        word2    string
        maxDist  int
        expected int
    }{
        {
            name:     "ascii",
            word1:    "testWord",
            word2:    "WordWithBigDistance", 
            maxDist:  3,
            expected: 4,
        },
        {
            name:     "non-ascii",
            word1:    "первое",
            word2:    "первый",
            maxDist:  3,
            expected: 2,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            dist := levenshteinDistance([]rune(tt.word1), []rune(tt.word2), tt.maxDist)
            if dist != tt.expected {
                t.Errorf("invalid levenshtein distance %d, instead of %d, with words: %q : %q", 
                    dist, tt.expected, tt.word1, tt.word2)
            }
        })
    }
}