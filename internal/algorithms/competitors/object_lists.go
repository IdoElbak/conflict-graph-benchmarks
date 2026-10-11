// Package competitors holds the algorithms the island family is measured
// against, all writing the full conflict matrix: Bitmap, the baseline, and the
// Phase 3 constructions from the literature (SortJoin, HybridJoin). All are
// built to the same standard as the island algorithms: no allocation per
// call, rows sized to the live block, the shared worker pool with a
// sequential threshold, and the access filters.
package competitors

import "conflict-graph-benchmarks/internal/model"

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
