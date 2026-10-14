//go:build ignore

package ufnext

import "conflict-graph-benchmarks/internal/model"

// The object-major wide kernel, forked from package islands (wideJoin, the
// Phase 3 fairness transfer of HybridJoin's kernel). Next uses it for giant
// islands only (NextConfig.GiantShare): a Bitmap-style index sweep over an
// island of most of the block costs what Bitmap costs, where HybridJoin's
// per-object choice between pair bits and row ORs wins.

// joinSpan is one object's place in a wideJoin: first its writer and reader
// counts, then lo = arena start + 1 (0 = not laid out yet), with w and r
// reused as the fill cursors.
type joinSpan struct {
	w, r, lo uint32
}

// wideJoin is the object-major kernel for islands wider than 64 members
// (IslandConfig.JoinDense > 0), HybridJoin's kernel in local coordinates:
// the island's live accesses are grouped by object (a counting sort), then
// each object emits its writer × writer and writer × reader pairs as bits, or,
// when it has many pairs, ORs its writer and accessor rows into its accessors'
// rows. It replaces the index build, row sweep and wipe of the Bitmap-style
// wide kernel. Every accessor of a live object is in the object's island, so
// the per-object spans of concurrently built islands never overlap.
type wideJoin struct {
	objs       []uint32 // the island's live objects, in first-write order
	arena      []uint32 // per object: writers, then readers (local positions)
	wRow, aRow []uint64 // scratch rows of a dense object, all-zero between uses
}

// build writes the local matrix of an island of k members (width words per
// row) from its compacted live accesses. spans is indexed by object and is
// all-zero on entry and on return.
func (j *wideJoin) build(la *model.LiveAccesses, k uint32, local []uint64, width uint32, spans []joinSpan, denseFactor float64) {
	j.group(la, k, spans)
	clear(local)
	j.wRow, j.aRow = model.Resize(j.wRow, int(width)), model.Resize(j.aRow, int(width))
	rowCost := denseFactor * float64(width)
	for _, obj := range j.objs {
		s := &spans[obj]
		lo := s.lo - 1
		writers := j.arena[lo:s.w]
		readers := j.arena[s.w:s.r]
		*s = joinSpan{}
		nw, nr := len(writers), len(readers)
		if pairBits := nw*(nw-1) + 2*nw*nr; float64(pairBits) <= rowCost*float64(nw+nr) {
			emitLocalPairs(local, width, writers, readers)
			continue
		}
		j.orDense(local, width, writers, readers)
	}
}

// group counting-sorts the island's live accesses by object into the arena:
// count, lay out in first-write order, fill. Every live read is of an object
// a member writes, so the writes list every object.
func (j *wideJoin) group(la *model.LiveAccesses, k uint32, spans []joinSpan) {
	for p := range k {
		for _, obj := range la.Writes(p) {
			spans[obj].w++
		}
		for _, obj := range la.Reads(p) {
			spans[obj].r++
		}
	}
	j.objs = j.objs[:0]
	pos := uint32(0)
	for _, obj := range la.W[:la.WEnd[k-1]] {
		s := &spans[obj]
		if s.lo != 0 {
			continue
		}
		j.objs = append(j.objs, obj)
		writers, readers := s.w, s.r
		// The counts become the fill cursors: the writers' ends at the
		// readers' start, the readers' at the object's end.
		s.lo, s.w, s.r = pos+1, pos, pos+writers
		pos += writers + readers
	}
	j.arena = model.Resize(j.arena, int(pos))
	for p := range k {
		for _, obj := range la.Writes(p) {
			s := &spans[obj]
			j.arena[s.w] = p
			s.w++
		}
		for _, obj := range la.Reads(p) {
			s := &spans[obj]
			j.arena[s.r] = p
			s.r++
		}
	}
}

// emitLocalPairs sets both bits of every writer × writer and writer × reader
// pair of one object in a local matrix.
func emitLocalPairs(local []uint64, width uint32, writers, readers []uint32) {
	for a, w := range writers {
		row := local[w*width : (w+1)*width]
		for _, p := range writers[a+1:] {
			row[p/64] |= 1 << (p % 64)
			local[p*width+w/64] |= 1 << (w % 64)
		}
		for _, p := range readers {
			row[p/64] |= 1 << (p % 64)
			local[p*width+w/64] |= 1 << (w % 64)
		}
	}
}

// orDense ORs one hot object's rows into its accessors' rows: a writer gets
// every accessor but itself, a reader every writer.
func (j *wideJoin) orDense(local []uint64, width uint32, writers, readers []uint32) {
	wRow, aRow := j.wRow, j.aRow
	for _, p := range writers {
		wRow[p/64] |= 1 << (p % 64)
	}
	copy(aRow, wRow)
	for _, p := range readers {
		aRow[p/64] |= 1 << (p % 64)
	}
	for _, w := range writers {
		row := local[w*width : (w+1)*width]
		for i := range row {
			row[i] |= aRow[i]
		}
		row[w/64] &^= 1 << (w % 64)
	}
	for _, r := range readers {
		row := local[r*width : (r+1)*width]
		for i := range row {
			row[i] |= wRow[i]
		}
	}
	clear(wRow)
	clear(aRow)
}
