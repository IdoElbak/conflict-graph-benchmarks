//go:build ignore

package competitors

import (
	"conflict-graph-benchmarks/internal/algorithms"
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// HybridConfig holds HybridBitmap's knobs.
type HybridConfig struct {
	MinWords      uint32  // blocks whose rows are narrower than this run Bitmap's code
	DenseFactor   float64 // an object is dense when it has more than DenseFactor × row words accessors
	SequentialMax uint32  // blocks of at most this many transactions sweep inline
	ChunkRows     int     // rows a worker claims at once
}

// DefaultHybridConfig holds the knobs the competitor proving ground picked
// (`go run . -suite=cmp`).
var DefaultHybridConfig = HybridConfig{
	MinWords:      8,
	DenseFactor:   1,
	SequentialMax: 518,
	ChunkRows:     16,
}

// HybridBitmap is Bitmap with a container per object, as in Roaring bitmaps
// (Chambi, Lemire et al., 2016) and in SpGEMM codes that pick a dense or a
// sparse accumulator by row (Gustavson's algorithm): an object with many
// accessors keeps word-wide writer and reader bitsets, and a transaction's row
// ORs them in, as Bitmap does; an object with few accessors keeps its sorted
// writer and reader lists, and the row sets one bit per peer, as IDBucket
// does. A sweep then costs min(row words, accessors) per access instead of
// always the row words (Bitmap) or always the accessors (IDBucket). The lists
// come from a counting sort of the accesses by object (objectLists), and the
// dense objects' bitsets from their lists. Objects that cannot produce an
// edge are skipped. Blocks with rows narrower than MinWords words run Bitmap's
// own code: there a list never pays.
type HybridBitmap struct {
	cfg          HybridConfig
	lists        objectLists
	wBits, rBits []uint64 // dense objects' writer and reader bitsets, row words each
	width        uint32
	bitmap       *model.ObjectInvertedIndex // Bitmap's scratch for narrow blocks
}

// NewHybridBitmap returns a HybridBitmap with the given knobs (normally
// DefaultHybridConfig). It is reusable scratch: steady-state calls allocate
// nothing.
func NewHybridBitmap(cfg HybridConfig) *HybridBitmap {
	return &HybridBitmap{cfg: cfg, bitmap: model.NewInvertedIndex()}
}

// Run builds the block's full symmetric conflict matrix into graph. Every row
// is overwritten, so graph needs no clearing beforehand.
func (h *HybridBitmap) Run(block *model.Block, graph *model.ConflictGraph) {
	n := uint32(len(block.Transactions))
	h.width = model.WordsPerRow(n)
	if h.width < h.cfg.MinWords {
		algorithms.BitmapConstruction(block, graph, h.bitmap)
		return
	}
	h.lists.build(block, uint32(h.cfg.DenseFactor*float64(h.width)))
	h.fillDense()
	graph.SetSize(n)

	if n <= h.cfg.SequentialMax {
		h.sweepRows(block, graph, 0, n)
		return
	}
	concurrency.ParallelFor(int(n), h.cfg.ChunkRows, func(_, lo, hi int) {
		h.sweepRows(block, graph, uint32(lo), uint32(hi))
	})
}

// fillDense builds the writer and reader bitsets of the dense objects from
// their lists.
func (h *HybridBitmap) fillDense() {
	width, l := h.width, &h.lists
	words := len(l.denseObjs) * int(width)
	h.wBits, h.rBits = model.Resize(h.wBits, words), model.Resize(h.rBits, words)
	clear(h.wBits)
	clear(h.rBits)
	for d, obj := range l.denseObjs {
		s := &l.spans[obj]
		setBits(h.wBits[uint32(d)*width:][:width], l.writers(s))
		setBits(h.rBits[uint32(d)*width:][:width], l.readers(s))
	}
}

// sweepRows writes rows [lo, hi) of graph: a written object contributes its
// writers and readers, a read one its writers, as word ORs for a dense object
// and as single bits for a listed one; the self bit is cleared last. A written
// object with one accessor is the transaction itself and is skipped; a
// never-written object has no writers to list.
func (h *HybridBitmap) sweepRows(block *model.Block, graph *model.ConflictGraph, lo, hi uint32) {
	l, width := &h.lists, h.width
	for txnIdx := lo; txnIdx < hi; txnIdx++ {
		row := graph.Row(txnIdx)
		clear(row)
		txn := &block.Transactions[txnIdx]
		for _, obj := range txn.WriteSet {
			s := &l.spans[obj]
			if s.dense == 0 {
				if s.hi-s.lo > 1 {
					setBits(row, l.accessors(s))
				}
				continue
			}
			off := (s.dense - 1) * width
			writers, readers := h.wBits[off:][:len(row)], h.rBits[off:][:len(row)]
			for k := range row {
				row[k] |= writers[k] | readers[k]
			}
		}
		for _, obj := range txn.ReadSet {
			s := &l.spans[obj]
			if s.dense == 0 {
				setBits(row, l.writers(s))
				continue
			}
			writers := h.wBits[(s.dense-1)*width:][:len(row)]
			for k := range row {
				row[k] |= writers[k]
			}
		}
		row[txnIdx/64] &^= 1 << (txnIdx % 64)
	}
}
