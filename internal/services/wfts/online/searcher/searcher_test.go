package searcher

import (
	"math"
	"strings"
	"testing"

	"wfts/internal/model"
)

func TestGetMinQueryDistInDoc(t *testing.T) {
    tests := []struct {
        name        string
        positions   [][]model.Position
        lenQuery    int
        expected    int
    }{
        {
            name:     "happy path minimal distance",
            positions: [][]model.Position{
                {{I: 10}, {I: 20}, {I: 30}},
                {{I: 15}, {I: 25}},
                {{I: 35}},
            },
            lenQuery: 3,
            expected: 15,
        },
        {
            name:     "missing first word",
            positions: [][]model.Position{
                {},
                {{I: 15}, {I: 25}},
                {{I: 35}},
            },
            lenQuery: 3,
            expected: 10 + 1 * a,
        },
        {
            name:     "empty positions list",
            positions: [][]model.Position{
                {},
            },
            lenQuery: 1,
            expected: math.MaxInt,
        },
        {
            name:     "empty second term positions",
            positions: [][]model.Position{
                {{I: 10}},
                {},
            },
            lenQuery: 2,
            expected: a * (2 - 1),
        },
        {
            name:     "no valid sequence",
            positions: [][]model.Position{
                {{I: 10}},
                {{I: 5}},
            },
            lenQuery: 2,
            expected: a * 1,
        },
        {
            name:     "multiple valid sequences",
            positions: [][]model.Position{
                {{I: 5}, {I: 15}},
                {{I: 10}, {I: 20}},
                {{I: 25}, {I: 35}},
            },
            lenQuery: 3,
            expected: 10,
        },
        {
            name:     "binary search boundary",
            positions: [][]model.Position{
                {{I: 100}},
                {{I: 30}, {I: 140}, {I: 200}},
            },
            lenQuery: 2,
            expected: 40,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := getMinQueryDistInDoc(tt.positions, tt.lenQuery)
            if result != tt.expected {
                t.Errorf("getMinQueryDistInDoc() = %d; want %d", result, tt.expected)
            }
        })
    }
}

func TestBoyerMoorAlgorithm(t *testing.T) {
	tests := []struct{
		name 		string
		base 		string
		query 		[]string
		expected	float64
	}{
		{
			name: "perfect match",
			base: "google.com",
			query: []string{"google.com"},
			expected: math.Log(1 + 10),
		},
		{
			name: "no match",
			base: "google.com",
			query: []string{"/////////"},
			expected: math.Log(1 + 0),
		},
		{
			name: "multiple matches",
			base: "searchqueryexample",
			query: []string{"search", "example"},
			expected: math.Log(1 + 6 + 7),
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if log := boyerMoorAlgorithm(tt.base, tt.query); log != tt.expected {
				t.Errorf("boyerMoorAlgorithm(%s, %s) = %f, want %f", tt.base, strings.Join(tt.query, ","), log, tt.expected)
			}
		})
	}
}