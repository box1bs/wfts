package scheduler

import (
	"testing"
	"wfts/internal/model"
)

func TestMinMaxHeap(t *testing.T) {
	tests := []struct {
		name 		string
		inserts 	[]*model.LinkToken
		ops 		[]bool
		expected 	[]int
	} {
		{
			name: "base max test",
			inserts: []*model.LinkToken{{Priority: 6}, {Priority: 8}, {Priority: 16}, {Priority: 1}},
			ops: []bool{true, true, true, true},
			expected: []int{0, 1, 2, 2},
		},
		{
			name: "base min test",
			inserts: []*model.LinkToken{{Priority: 6}, {Priority: 2}, {Priority: 16}, {Priority: 1}},
			ops: []bool{false, false, false, false},
			expected: []int{0, 1, 1, 3},
		},
		{
			name: "min max test",
			inserts: []*model.LinkToken{{Priority: 1e-15}, {Priority: 1e-16}, {Priority: 1e-18}, {Priority: 1}},
			ops: []bool{true, false, true, false},
			expected: []int{0, 1, 0, 2},
		},
	}

	for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
			h := NewMinMaxHeap()
            for i := range tt.ops {
				h.Insert(tt.inserts[i])
				exp := tt.inserts[tt.expected[i]]
				if tt.ops[i] {
					if n, f := h.GetMax(); n.Value != exp || !f {
						t.Errorf("heap.GetMax() = %f; want %f\n", n.Value.Priority, exp.Priority)
					}
				} else {
					if n, f := h.GetMin(); n.Value != exp || !f {
						t.Errorf("heap.GetMin() = %f; want %f\n", n.Value.Priority, exp.Priority)
					}
				}
			}
        })
    }
}