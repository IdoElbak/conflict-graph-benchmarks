package competitors

import (
	"conflict-graph-benchmarks/internal/model"
)

// HybridJoinConfig holds HybridJoin's knobs.
type HybridJoinConfig struct {
	MinWords    uint32  // blocks whose rows are narrower than this run Bitmap's code
	DenseFactor float64 // an object is ORed as bitsets when its pair bits exceed DenseFactor × its accessors × row words
}

// DefaultHybridJoinConfig holds the knobs the competitor proving ground picked
// (`go run . -suite=cmp`).
var DefaultHybridJoinConfig = HybridJoinConfig{
	MinWords:    4,
	DenseFactor: 1,
}

// HybridJoin is SortJoin with a dense path for hot objects: the accesses are
// grouped by object (a counting sort), and each object that can produce an
// edge is emitted object-major, by whichever of two kernels is cheaper for it:
//   - few pairs: each writer × writer and writer × reader pair is set as its
//     two bits (SortJoin);
//   - many pairs: the object's writer bitset and accessor bitset are built in
//     two scratch rows, then ORed word-wide into each accessor's row (writers
//     take the accessors, readers the writers), as Bitmap does, but with the
//     two rows hot in L1 and no per-object index.
//
// It is the hybrid accumulator idea of SpGEMM (a sparse or a dense kernel per
// unit of work) applied to the outer-product formulation of
// W·Wᵀ ∨ W·Rᵀ ∨ R·Wᵀ. It runs sequentially: the parallel object-major path
// needs atomic ORs, which SortJoin measured as a loss on 6 cores. Blocks with
// rows narrower than MinWords words run Bitmap's code.
type HybridJoin struct {
	cfg              HybridJoinConfig
	lists            objectLists
	writers, touched []uint64 // scratch rows: the current dense object's writers and accessors
	bitmap           *model.ObjectInvertedIndex
}

// NewHybridJoin returns a HybridJoin with the given knobs (normally
// DefaultHybridJoinConfig). It is reusable scratch: steady-state calls
// allocate nothing.
func NewHybridJoin(cfg HybridJoinConfig) *HybridJoin {
	return &HybridJoin{cfg: cfg, bitmap: model.NewInvertedIndex()}
}

// Run builds the block's full symmetric conflict matrix into graph, clearing
// it first.
func (j *HybridJoin) Run(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))
	width := model.WordsPerRow(n)
	if width < j.cfg.MinWords {
		BitmapConstruction(block, graph, j.bitmap)
		return
	}
	j.lists.build(block)
	graph.Reset(n)
	j.writers = model.Resize(j.writers, int(width))
	j.touched = model.Resize(j.touched, int(width))
	rowCost := j.cfg.DenseFactor * float64(width)

	l := &j.lists
	for obj := range l.spans {
		s := &l.spans[obj]
		writers := l.writers(s)
		if len(writers) == 0 || s.hi-s.lo < 2 {
			continue
		}
		readers := l.readers(s)
		nw, nr := len(writers), len(readers)
		if pairBits := nw*(nw-1) + 2*nw*nr; float64(pairBits) <= rowCost*float64(nw+nr) {
			emitPairs(graph, writers, readers)
			continue
		}
		j.orDense(graph, writers, readers)
	}
}

// orDense ORs one hot object's bitsets into its accessors' rows: a writer
// gets every accessor but itself, a reader every writer. The scratch rows are
// all-zero on entry and on return.
func (j *HybridJoin) orDense(graph *model.ConflictGraph, writers, readers []uint32) {
	wRow, aRow := j.writers, j.touched
	setBits(wRow, writers)
	copy(aRow, wRow)
	setBits(aRow, readers)
	for _, w := range writers {
		row := graph.Row(w)
		for k := range row {
			row[k] |= aRow[k]
		}
		row[w/64] &^= 1 << (w % 64)
	}
	for _, r := range readers {
		row := graph.Row(r)
		for k := range row {
			row[k] |= wRow[k]
		}
	}
	clear(wRow)
	clear(aRow)
}
