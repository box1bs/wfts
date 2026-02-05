package scheduler

import (
	"math"
	"wfts/internal/model"
)

type Item struct {
	Priority float64
	Value    *model.CrawlNode
}

type minMaxHeap struct {
	data []Item
}

func NewMinMaxHeap() *minMaxHeap {
	return &minMaxHeap{}
}

func (h *minMaxHeap) Insert(v *model.CrawlNode) {
	h.data = append(h.data, Item{v.Priority, v})
	h.bubbleUp(len(h.data) - 1)
}

func (h *minMaxHeap) GetMin() (Item, bool) {
	if len(h.data) == 0 {
		return Item{}, false
	}
	return h.data[0], true
}

func (h *minMaxHeap) GetMax() (Item, bool) {
	n := len(h.data)
	if n == 0 {
		return Item{}, false
	}
	if n == 1 {
		return h.data[0], true
	}
	if n == 2 || h.data[1].Priority > h.data[2].Priority {
		return h.data[1], true
	}
	return h.data[2], true
}

func (h *minMaxHeap) DeleteMin() (Item, bool) {
	if len(h.data) == 0 {
		return Item{}, false
	}
	min := h.data[0]
	last := len(h.data) - 1
	h.data[0] = h.data[last]
	h.data = h.data[:last]
	if len(h.data) > 0 {
		h.trickleDown(0)
	}
	return min, true
}

func (h *minMaxHeap) DeleteMax() (Item, bool) {
	n := len(h.data)
	if n == 0 {
		return Item{}, false
	}

	var idx int
	if n == 1 {
		idx = 0
	} else if n == 2 || h.data[1].Priority > h.data[2].Priority {
		idx = 1
	} else {
		idx = 2
	}

	max := h.data[idx]
	last := n - 1
	h.data[idx] = h.data[last]
	h.data = h.data[:last]
	if idx < len(h.data) {
		h.trickleDown(idx)
	}
	return max, true
}

func level(i int) int {
	return int(math.Floor(math.Log2(float64(i + 1))))
}

func isMinLevel(i int) bool {
	return level(i) % 2 == 0
}

func (h *minMaxHeap) bubbleUp(i int) {
	if i == 0 {
		return
	}
	p := (i - 1) / 2

	if isMinLevel(i) {
		if h.data[i].Priority > h.data[p].Priority {
			h.swap(i, p)
			h.bubbleUpMax(p)
		} else {
			h.bubbleUpMin(i)
		}
	} else {
		if h.data[i].Priority < h.data[p].Priority {
			h.swap(i, p)
			h.bubbleUpMin(p)
		} else {
			h.bubbleUpMax(i)
		}
	}
}

func (h *minMaxHeap) bubbleUpMin(i int) {
	for i >= 3 {
		gp := (i - 3) / 4
		if h.data[i].Priority < h.data[gp].Priority {
			h.swap(i, gp)
			i = gp
		} else {
			break
		}
	}
}

func (h *minMaxHeap) bubbleUpMax(i int) {
	for i >= 3 {
		gp := (i - 3) / 4
		if h.data[i].Priority > h.data[gp].Priority {
			h.swap(i, gp)
			i = gp
		} else {
			break
		}
	}
}

func (h *minMaxHeap) trickleDown(i int) {
	if isMinLevel(i) {
		h.trickleDownMin(i)
	} else {
		h.trickleDownMax(i)
	}
}

func (h *minMaxHeap) trickleDownMin(i int) {
	for {
		m := h.smallestDescendant(i)
		if m == -1 {
			return
		}

		if isGrandchild(i, m) {
			if h.data[m].Priority < h.data[i].Priority {
				h.swap(i, m)
				p := (m - 1) / 2
				if h.data[m].Priority > h.data[p].Priority {
					h.swap(m, p)
				}
				i = m
			} else {
				return
			}
		} else {
			if h.data[m].Priority < h.data[i].Priority {
				h.swap(i, m)
			}
			return
		}
	}
}

func (h *minMaxHeap) trickleDownMax(i int) {
	for {
		m := h.largestDescendant(i)
		if m == -1 {
			return
		}

		if isGrandchild(i, m) {
			if h.data[m].Priority > h.data[i].Priority {
				h.swap(i, m)
				p := (m - 1) / 2
				if h.data[m].Priority < h.data[p].Priority {
					h.swap(m, p)
				}
				i = m
			} else {
				return
			}
		} else {
			if h.data[m].Priority > h.data[i].Priority {
				h.swap(i, m)
			}
			return
		}
	}
}

func (h *minMaxHeap) smallestDescendant(i int) int {
	return h.extremeDescendant(i, true)
}

func (h *minMaxHeap) largestDescendant(i int) int {
	return h.extremeDescendant(i, false)
}

func (h *minMaxHeap) extremeDescendant(i int, min bool) int {
	n := len(h.data)
	best := -1

	children := []int{2 * i + 1, 2 * i + 2}
	grand := []int{
		4 * i + 3, 4 * i + 4,
		4 * i + 5, 4 * i + 6,
	}

	for _, idx := range append(children, grand...) {
		if idx >= n {
			continue
		}
		if best == -1 ||
			(min && h.data[idx].Priority < h.data[best].Priority) ||
			(!min && h.data[idx].Priority > h.data[best].Priority) {
			best = idx
		}
	}
	return best
}

func isGrandchild(i, j int) bool {
	return j >= 4 * i + 3
}

func (h *minMaxHeap) swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
}