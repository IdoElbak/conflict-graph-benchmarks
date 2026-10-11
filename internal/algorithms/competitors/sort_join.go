package competitors

import (
	"sync/atomic"

	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// SortJoinConfig holds SortJoin's knobs.
type SortJoinConfig struct {
	SequentialMax uint32 // blocks of at most this many transactions run inline
	ChunkObjects  int    // objects a worker claims at once
	ChunkRows     int    // rows a worker clears at once
}

// DefaultSortJoinConfig holds the knobs the competitor proving ground picked
// (`go run . -suite=cmp`): always sequential. On 6 cores the atomic ORs of the
// parallel path cost more than the workers save (×0.7 and worse on every
// trace); revisit on a many-core machine.
var DefaultSortJoinConfig = SortJoinConfig{
	SequentialMax: model.MaximumGraphSize,
	ChunkObjects:  64,
	ChunkRows:     64,
}

// SortJoin is the classical sort-based conflict join of databases (a
// sort-merge self-join on the object key, restricted to pairs with a writer):
// group the accesses by object, then for every object emit each writer ×
// writer and writer × reader pair once, as the two bits of the edge. Object
// IDs are dense, so the sort is a counting sort. Emission is object-major,
// where Bitmap is row-major; in SpGEMM terms it is the outer product
// formulation of W·Wᵀ ∨ W·Rᵀ ∨ R·Wᵀ. Objects that cannot produce an edge
// (never written, or touched by one transaction) are skipped.
//
// The parallel path splits the objects among the workers, which set bits with
// atomic ORs: OR is commutative and idempotent, so the matrix is the same for
// every interleaving. It first clears the matrix in a separate pool pass.
type SortJoin struct {
	cfg   SortJoinConfig
	lists objectLists
}

// NewSortJoin returns a SortJoin with the given knobs (normally
// DefaultSortJoinConfig). It is reusable scratch: steady-state calls allocate
// nothing.
func NewSortJoin(cfg SortJoinConfig) *SortJoin {
	return &SortJoin{cfg: cfg}
}

// Run builds the block's full symmetric conflict matrix into graph, clearing
// it first.
func (j *SortJoin) Run(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))
	j.lists.build(block)
	numObjects := len(j.lists.spans)
	if n <= j.cfg.SequentialMax {
		graph.Reset(n)
		j.emit(graph, 0, numObjects)
		return
	}
	graph.SetSize(n)
	concurrency.ParallelFor(int(n), j.cfg.ChunkRows, func(_, lo, hi int) {
		for t := lo; t < hi; t++ {
			clear(graph.Row(uint32(t)))
		}
	})
	concurrency.ParallelFor(numObjects, j.cfg.ChunkObjects, func(_, lo, hi int) {
		j.emitAtomic(graph, lo, hi)
	})
}

// emit sets the edges of objects [lo, hi) with plain ORs.
func (j *SortJoin) emit(graph *model.ConflictGraph, lo, hi int) {
	l := &j.lists
	for obj := lo; obj < hi; obj++ {
		s := &l.spans[obj]
		writers := l.writers(s)
		if len(writers) == 0 || s.hi-s.lo < 2 {
			continue
		}
		emitPairs(graph, writers, l.readers(s))
	}
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

// emitAtomic sets the edges of objects [lo, hi) with atomic ORs (parallel
// path: other workers may write the same words).
func (j *SortJoin) emitAtomic(graph *model.ConflictGraph, lo, hi int) {
	l := &j.lists
	for obj := lo; obj < hi; obj++ {
		s := &l.spans[obj]
		writers := l.writers(s)
		if len(writers) == 0 || s.hi-s.lo < 2 {
			continue
		}
		readers := l.readers(s)
		for a, w := range writers {
			row := graph.Row(w)
			for _, p := range writers[a+1:] {
				atomic.OrUint64(&row[p/64], 1<<(p%64))
				atomic.OrUint64(&graph.Row(p)[w/64], 1<<(w%64))
			}
			for _, p := range readers {
				atomic.OrUint64(&row[p/64], 1<<(p%64))
				atomic.OrUint64(&graph.Row(p)[w/64], 1<<(w%64))
			}
		}
	}
}
