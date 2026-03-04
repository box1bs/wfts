package indexer

import (
	"hash/fnv"
	"math/rand"
	"strings"
)

const (
	nGramSize = 4
	prime = (1 << 61) - 1 // 61 единица в бинарном представлении
)

type minHash struct {
	a, b [128]uint64
}

func NewHasher(a, b [128]uint64, reCreateF bool) *minHash {
	if reCreateF {
		rsid := rand.New(rand.NewSource(1))

		for i := range 128 {
			a[i] = uint64(rsid.Int63n(int64(prime - 1))) + 1
			b[i] = uint64(rsid.Int63n(int64(prime)))
		}
	}

	return &minHash{a: a, b: b}
}

func (mh *minHash) CreateSignature(rawTokens []string) [128]uint64 {
	shingles := getWordNGrams(rawTokens)
	sign := [128]uint64{}
	for i := range 128 {
		sign[i] = ^uint64(0)
	}

	for _, shingle := range shingles {
		hash := mh.Hash64(shingle)
		for i := range 128 {
			x := mh.a[i] * hash + mh.b[i]
			x %= prime
			if x < sign[i] {
				sign[i] = x
			}
		}
	}
	return sign
}

func (mh *minHash) Hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func getWordNGrams(rawTokens []string) []string {
	result := make([]string, 0, 8)
	for i := 0; i <= len(rawTokens) - nGramSize; i++ {
		gram := make([]string, nGramSize)
		copy(gram, rawTokens[i: i + nGramSize])
		result = append(result, strings.Join(gram, " "))
	}
	return result
}