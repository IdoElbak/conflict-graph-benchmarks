package model

import "math/bits"

// IslandTopology is the output of the island algorithms (IslandHub and
// IslandBipartite). Together, the island graphs and the hub layer hold every
// conflict edge of the block:
//   - Islands are disjoint groups of transactions (global indices); island i's
//     conflicts among its members are LocalGraphs[i], a symmetric matrix of
//     len(island) rows × LocalWidths[i] words in local coordinates (bit p of a
//     row = Islands[i][p]).
//   - The hub layer holds the conflicts through hub objects, which the
//     partition ignores. IslandHub writes it as a symmetric matrix of
//     len(HubTouchers) rows × HubWidth words over the transactions touching a
//     hub (bit q of a row = HubTouchers[q]), so every edge is an explicit bit;
//     IslandBipartite as per-hub writer and reader lists (HubWriters,
//     HubReaders). An edge may appear in both parts.
//
// Transactions in no island and no hub row have no conflicts. The islands can
// be graph-colored independently; the hub layer resolves the collisions.
type IslandTopology struct {
	Islands     [][]uint32
	LocalGraphs [][]uint64
	LocalWidths []uint32

	HubTouchers []uint32
	HubGraph    []uint64
	HubWidth    uint32

	// Bipartite hub layer (IslandBipartite), in place of the hub matrix: hub
	// object h's writers HubWriters[h] and readers HubReaders[h], as global
	// transaction ids. Each writer conflicts with every other writer and
	// every reader of the hub.
	HubWriters, HubReaders [][]uint32

	members []uint32 // backing array of Islands
	local   []uint64 // backing array of LocalGraphs
}

// Reset empties the topology for a new block, keeping its memory.
func (t *IslandTopology) Reset() {
	t.Islands = t.Islands[:0]
	t.LocalGraphs = t.LocalGraphs[:0]
	t.LocalWidths = t.LocalWidths[:0]
	t.HubTouchers = t.HubTouchers[:0]
	t.HubGraph = t.HubGraph[:0]
	t.HubWidth = 0
	t.HubWriters = t.HubWriters[:0]
	t.HubReaders = t.HubReaders[:0]
}

// GroupIslands records every set of two or more of the forest's n
// transactions as an island, its members in list order, and returns the
// number of transactions in islands and the size of the largest island.
func (t *IslandTopology) GroupIslands(f *IslandForest, n uint32) (inIslands, largest int) {
	t.members = Resize(t.members, int(n))
	pos := 0
	for root := uint32(0); root < n; root++ {
		if f.size[root] < 2 || f.parent[root] != root {
			continue
		}
		start := pos
		for x := root; ; {
			t.members[pos] = x
			pos++
			if x = f.next[x]; x == root {
				break
			}
		}
		t.Islands = append(t.Islands, t.members[start:pos:pos])
		largest = max(largest, pos-start)
	}
	return pos, largest
}

// SetWholeBlock makes the topology a single island of the block's n
// transactions in index order (members must be 0..n-1) whose local graph is
// matrix, n rows of width words: the output of an island algorithm that built
// the block as one matrix. It keeps no hub layer.
func (t *IslandTopology) SetWholeBlock(members []uint32, matrix []uint64, width uint32) {
	t.Reset()
	t.Islands = append(t.Islands, members)
	t.LocalGraphs = append(t.LocalGraphs, matrix)
	t.LocalWidths = append(t.LocalWidths, width)
}

// CarveLocalGraphs cuts one local matrix per island out of the topology's
// arena and returns the words they take. The matrices are not cleared: the
// island algorithms write every word of them.
func (t *IslandTopology) CarveLocalGraphs() int {
	total := 0
	for _, island := range t.Islands {
		total += len(island) * int(WordsPerRow(uint32(len(island))))
	}
	t.local = Resize(t.local, total)
	off := 0
	for _, island := range t.Islands {
		width := WordsPerRow(uint32(len(island)))
		end := off + len(island)*int(width)
		t.LocalGraphs = append(t.LocalGraphs, t.local[off:end:end])
		t.LocalWidths = append(t.LocalWidths, width)
		off = end
	}
	return total
}

// Reconstruct ORs every edge of the topology into graph, which must be sized
// and cleared for the block. It exists for the parity check and is never timed.
func (t *IslandTopology) Reconstruct(graph *ConflictGraph) {
	for i, island := range t.Islands {
		addMatrixEdges(graph, island, t.LocalGraphs[i], t.LocalWidths[i])
	}
	if len(t.HubTouchers) > 0 {
		addMatrixEdges(graph, t.HubTouchers, t.HubGraph, t.HubWidth)
	}
	for h, writers := range t.HubWriters {
		addHubEdges(graph, writers, t.HubReaders[h])
	}
}

// addHubEdges adds the edges of one bipartite hub: every writer with every
// other writer and every reader (a transaction never conflicts with itself).
func addHubEdges(graph *ConflictGraph, writers, readers []uint32) {
	for i, w := range writers {
		for _, peers := range [][]uint32{writers[i+1:], readers} {
			for _, p := range peers {
				if p != w {
					graph.AddEdge(w, p)
					graph.AddEdge(p, w)
				}
			}
		}
	}
}

// addMatrixEdges adds the edges of a local symmetric matrix whose bit p stands
// for global transaction ids[p].
func addMatrixEdges(graph *ConflictGraph, ids []uint32, matrix []uint64, width uint32) {
	for p, txn := range ids {
		row := matrix[uint32(p)*width : uint32(p+1)*width]
		for w, word := range row {
			for word != 0 {
				graph.AddEdge(txn, ids[w*64+bits.TrailingZeros64(word)])
				word &= word - 1
			}
		}
	}
}

// IslandStats summarizes the island structure of a topology: island count
// and sizes, transactions in islands, hub touchers, and an island-size
// histogram.
type IslandStats struct {
	Count, MaxSize        int
	AvgSize               float64
	InIslands, HubTouched int
	Size2, Size3to8       int
	Size9to64, SizeOver64 int
}

// Stats computes the island statistics of the topology.
func (t *IslandTopology) Stats() IslandStats {
	s := IslandStats{Count: len(t.Islands), HubTouched: len(t.HubTouchers)}
	for _, island := range t.Islands {
		k := len(island)
		s.InIslands += k
		s.MaxSize = max(s.MaxSize, k)
		switch {
		case k == 2:
			s.Size2++
		case k <= 8:
			s.Size3to8++
		case k <= 64:
			s.Size9to64++
		default:
			s.SizeOver64++
		}
	}
	if s.Count > 0 {
		s.AvgSize = float64(s.InIslands) / float64(s.Count)
	}
	return s
}

// IslandForest is a union-find over a block's transactions (union by size,
// path halving) that also threads every set's members on a circular list:
// linking two roots swaps their next pointers, which splices the two lists.
// Islands are then read off the lists, without a Find per transaction or a
// counting sort.
type IslandForest struct {
	parent, size, next []uint32
}

// Reset makes every transaction of an n-transaction block its own set.
func (f *IslandForest) Reset(n uint32) {
	f.parent = Resize(f.parent, int(n))
	f.size = Resize(f.size, int(n))
	f.next = Resize(f.next, int(n))
	for i := range n {
		f.parent[i] = i
		f.size[i] = 1
		f.next[i] = i
	}
}

// Find returns the root of x's set, halving the path on the way.
func (f *IslandForest) Find(x uint32) uint32 {
	parent := f.parent
	for parent[x] != x {
		parent[x] = parent[parent[x]]
		x = parent[x]
	}
	return x
}

// Size returns the number of transactions in the set of root.
func (f *IslandForest) Size(root uint32) uint32 {
	return f.size[root]
}

// Link merges the sets of the distinct roots a and b and returns the new root.
func (f *IslandForest) Link(a, b uint32) uint32 {
	if f.size[a] < f.size[b] {
		a, b = b, a
	}
	f.parent[b] = a
	f.size[a] += f.size[b]
	f.next[a], f.next[b] = f.next[b], f.next[a]
	return a
}

// IslandIndex holds per-object writer and reader bitsets over one island's
// members (local coordinates), each row stride words starting at obj*stride.
// It is all-zero between uses: every use wipes the words it set.
type IslandIndex struct {
	Writers, Readers []uint64
}

// Prepare sizes the index for numObjects rows of stride words.
func (x *IslandIndex) Prepare(numObjects, stride uint32) {
	x.Writers = Resize(x.Writers, int(numObjects*stride))
	x.Readers = Resize(x.Readers, int(numObjects*stride))
}

// LiveAccesses holds the accesses of an island's members that can produce an
// edge, compacted per member: member p's live writes are W[WEnd[p-1]:WEnd[p]]
// (from 0 for p = 0) and its live reads R[REnd[p-1]:REnd[p]].
type LiveAccesses struct {
	W, R       []uint32
	WEnd, REnd []uint32
}

// Writes returns member p's live writes.
func (la *LiveAccesses) Writes(p uint32) []uint32 {
	if p == 0 {
		return la.W[:la.WEnd[0]]
	}
	return la.W[la.WEnd[p-1]:la.WEnd[p]]
}

// Reads returns member p's live reads.
func (la *LiveAccesses) Reads(p uint32) []uint32 {
	if p == 0 {
		return la.R[:la.REnd[0]]
	}
	return la.R[la.REnd[p-1]:la.REnd[p]]
}

// Resize returns s resliced to length need, reallocating only when its
// capacity is too small. Reused elements keep their values: the scratch types
// of this package keep the words they reuse all-zero themselves.
func Resize[T any](s []T, need int) []T {
	if cap(s) < need {
		return make([]T, need)
	}
	return s[:need]
}
