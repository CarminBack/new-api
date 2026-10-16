package codec

import "math"

// mergeCandidate orders equal-rank merges from left to right, just as the
// original slice scan does. version invalidates pairs changed by a merge.
// 32-bit fields keep per-byte memory close to the original implementation.
type mergeCandidate struct {
	rank, index, version uint32
}
type mergeQueue []mergeCandidate

func mergeBefore(a, b mergeCandidate) bool {
	return a.rank < b.rank || a.rank == b.rank && a.index < b.index
}
func (q *mergeQueue) push(v mergeCandidate) {
	*q = append(*q, v)
	i := len(*q) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !mergeBefore(v, (*q)[p]) {
			break
		}
		(*q)[i] = (*q)[p]
		i = p
	}
	(*q)[i] = v
}
func (q *mergeQueue) pop() mergeCandidate {
	result := (*q)[0]
	last := (*q)[len(*q)-1]
	*q = (*q)[:len(*q)-1]
	if len(*q) == 0 {
		return result
	}
	i := 0
	for {
		child := 2*i + 1
		if child >= len(*q) {
			break
		}
		if child+1 < len(*q) && mergeBefore((*q)[child+1], (*q)[child]) {
			child++
		}
		if !mergeBefore((*q)[child], last) {
			break
		}
		(*q)[i] = (*q)[child]
		i = child
	}
	(*q)[i] = last
	return result
}

// mergeNode i starts at byte offset i of the piece. prev/next link the live
// parts; next == noNode marks the last boundary or a merged-away part.
type mergeNode struct{ prev, next, version uint32 }

const noNode = math.MaxUint32

// Derived from github.com/tiktoken-go/tokenizer v0.6.2 (MIT; see ../LICENSE).
// mergePairs keeps the same BPE rank and tie-breaking rules without rescanning
// and shifting all remaining bytes for every merge. Nodes keep their original
// indices so a priority queue can select the next pair in O(log n).
func (c *Codec) mergePairs(piece string) []part {
	if len(piece) <= 256 || len(piece) >= noNode {
		return c.mergePairsSmall(piece)
	}
	n := uint32(len(piece))
	remaining := len(piece) + 1
	nodes := make([]mergeNode, n+1)
	q := make(mergeQueue, 0, n)
	for i := uint32(0); i <= n; i++ {
		nodes[i] = mergeNode{prev: i - 1, next: i + 1} // prev of node 0 wraps to noNode
	}
	nodes[n].next = noNode
	for i := uint32(0); i+1 < n; i++ {
		if rank, ok := c.vocabulary[piece[i:i+2]]; ok {
			q.push(mergeCandidate{rank: uint32(rank), index: i})
		}
	}
	for len(q) > 0 {
		candidate := q.pop()
		left := candidate.index
		if nodes[left].next == noNode || nodes[left].version != candidate.version {
			continue
		}
		right := nodes[left].next
		after := nodes[right].next
		if after == noNode {
			continue
		}
		remaining--
		nodes[left].next = after
		nodes[after].prev = left
		nodes[right].next = noNode
		nodes[right].version++
		for _, index := range [2]uint32{nodes[left].prev, left} {
			if index == noNode {
				continue
			}
			nodes[index].version++
			next := nodes[index].next
			if next == noNode || nodes[next].next == noNode {
				continue
			}
			if rank, ok := c.vocabulary[piece[index:nodes[next].next]]; ok {
				q.push(mergeCandidate{rank: uint32(rank), index: index, version: nodes[index].version})
			}
		}
	}
	result := make([]part, 0, remaining)
	for index := uint32(0); index != noNode; index = nodes[index].next {
		result = append(result, part{offset: int(index), rank: math.MaxUint})
	}
	return result
}

func (c *Codec) mergePairsSmall(piece string) []part {
	parts := make([]part, len(piece)+1)
	for i := range len(parts) {
		parts[i] = part{i, math.MaxUint}
	}

	getRank := func(index, skip int) uint {
		if index+skip+2 < len(parts) {
			start := parts[index].offset
			end := parts[index+skip+2].offset
			if rank, ok := c.vocabulary[piece[start:end]]; ok {
				return rank
			}
		}
		return math.MaxUint
	}

	for i := 0; i < len(parts)-2; i++ {
		parts[i].rank = getRank(i, 0)
	}

	for {
		if len(parts) == 1 {
			break
		}

		minRank := uint(math.MaxUint)
		minIndex := 0
		for i, p := range parts[:len(parts)-1] {
			if p.rank < minRank {
				minRank = p.rank
				minIndex = i
			}
		}

		if minRank == math.MaxUint {
			break
		}

		parts[minIndex].rank = getRank(minIndex, 1)

		if minIndex > 0 {
			parts[minIndex-1].rank = getRank(minIndex-1, 1)
		}

		parts = append(parts[:minIndex+1], parts[minIndex+2:]...)
	}

	return parts
}
