package roundTripper

import (
	"hash/fnv"
)

const m, k = 1 << 20, 5

type Bloom struct {
	bits [m / 64]uint64
}

func (b *Bloom) Add(s string) {
	h1, h2 := b.hash(s)
	for i := range k {
		bit := (h1 + h2*uint64(i)) % m
		b.bits[bit/64] |= 1 << (bit % 64)
	}
}

func (b *Bloom) Contain(s string) bool {
	h1, h2 := b.hash(s)
	for i := range k {
		bit := (h1 + h2*uint64(i)) % m
		if b.bits[bit/64] & (1 << (bit % 64)) == 0 {
			return false
		}
	}
	return true
}

func (b *Bloom) hash(s string) (uint64, uint64) {
	hasher := fnv.New64a()
	hasher.Write([]byte(s))
	h1 := hasher.Sum64()
	h2 := h1 ^ (h1 >> 33) * 0xff51afd7ed558ccd
	return h1, h2
}