package competitors

import "conflict-graph-benchmarks/internal/model"

// HybridJoinTiny is HybridJoin with TinyBitmap for blocks of at most
// TinyBitmapMax transactions (HybridJoin itself runs Bitmap there).
type HybridJoinTiny struct {
	tiny TinyBitmap
	join *HybridJoin
}

// NewHybridJoinTiny returns a HybridJoinTiny with HybridJoin's given knobs.
func NewHybridJoinTiny(cfg HybridJoinConfig) *HybridJoinTiny {
	return &HybridJoinTiny{join: NewHybridJoin(cfg)}
}

// Run builds the block's full matrix into graph.
func (h *HybridJoinTiny) Run(block *model.Block, graph *model.ConflictGraph) {
	if len(block.Transactions) <= TinyBitmapMax {
		h.tiny.Run(block, graph)
		return
	}
	h.join.Run(block, graph)
}

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

// HybridJoin is SortJoin (the classical sort-based conflict join, archived)
// with a dense path for hot objects: the accesses are
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
// needs atomic ORs, which SortJoin's parallel path measured as a loss on 6 cores. Blocks with
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

// emitPairs sets both bits of every writer × writer and writer × reader edge
// of one object, each pair once (the sets are disjoint and ascending).
func emitPairs(graph *model.ConflictGraph, writers, readers []uint32) {
	for a, w := range writers {
		for _, p := range writers[a+1:] {
			graph.AddEdge(w, p)
			graph.AddEdge(p, w)
		}
		for _, p := range readers {
			graph.AddEdge(w, p)
			graph.AddEdge(p, w)
		}
	}
}

// objectSpan locates one object's accessors in objectLists.arena: its writers
// in [lo, mid), then its readers in [mid, hi). Readers are listed only for
// written objects: a read of a never-written object conflicts with nobody.
type objectSpan struct {
	lo, mid, hi uint32
}

// objectLists groups a block's accesses by object, the "sort" of a sort-based
// join: object IDs are dense, so it is a counting sort, and each object's
// writers and readers come out in ascending transaction order. It is reusable
// scratch: steady-state calls allocate nothing.
type objectLists struct {
	spans []objectSpan
	arena []uint32
}

// build fills the lists for the block.
func (l *objectLists) build(block *model.Block) {
	txns := block.Transactions
	l.spans = model.Resize(l.spans, int(block.NumUniqueObjects))
	spans := l.spans
	clear(spans)
	// Count writers into mid and readers of written objects into hi.
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			spans[obj].mid++
		}
	}
	for t := range txns {
		for _, obj := range txns[t].ReadSet {
			if spans[obj].mid != 0 {
				spans[obj].hi++
			}
		}
	}
	l.layout()
	arena := l.arena
	// mid and hi are the fill cursors of the writers and the readers; the
	// writers' cursor ends where the readers start, the readers' at the end.
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			s := &spans[obj]
			arena[s.mid] = uint32(t)
			s.mid++
		}
	}
	for t := range txns {
		for _, obj := range txns[t].ReadSet {
			if s := &spans[obj]; s.mid != s.lo {
				arena[s.hi] = uint32(t)
				s.hi++
			}
		}
	}
}

// layout turns the counts into arena positions (a prefix sum) and leaves mid
// and hi as the fill cursors.
func (l *objectLists) layout() {
	pos := uint32(0)
	for obj := range l.spans {
		s := &l.spans[obj]
		writers, readers := s.mid, s.hi
		s.lo, s.mid, s.hi = pos, pos, pos+writers
		pos += writers + readers
	}
	l.arena = model.Resize(l.arena, int(pos))
}

// writers returns the writers of the object of span s.
func (l *objectLists) writers(s *objectSpan) []uint32 {
	return l.arena[s.lo:s.mid]
}

// readers returns the readers of the (written) object of span s.
func (l *objectLists) readers(s *objectSpan) []uint32 {
	return l.arena[s.mid:s.hi]
}

// setBits sets the bit of every transaction of peers in row.
func setBits(row []uint64, peers []uint32) {
	for _, p := range peers {
		row[p/64] |= 1 << (p % 64)
	}
}
