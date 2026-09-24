package model

import (
	"runtime"
	"sync/atomic"
)

// Edge is a conflict between two transaction indices
type Edge struct {
	TxnA, TxnB uint32
}

// BlockTopology is the "Masked Island" output plus all reusable memory
// for IslandConstructionNativeShattered. Physical conflict bits live in the
// global ConflictGraph matrix; this structure carries the logical isolation map
// (disjoint islands + sparse hub edges) that the downstream scheduler consumes.
// It is allocated once and passed into every call, so the hot path performs
// zero heap allocations in steady state.
//
// Invariant: ReadBits, WriteBits and IsHub are all-zero between calls.
// The algorithm restores this at the end of each call by wiping exactly the
// entries it touched (far cheaper than re-zeroing the whole arenas).
type BlockTopology struct {
	Islands  [][]uint32 // logical txn groupings, views into txnArena
	HubEdges [][]Edge   // per-worker sparse hub conflict lists

	// LocalGraphs holds one packed symmetric bit-matrix per island (same order
	// as Islands) in local coordinates: row p covers island[p] and spans
	// LocalWidths[i] (= ceil(len(island)/64)) words. Views into localArena.
	// Used only by the topology-output pipeline; untouched by NativeShattered.
	LocalGraphs [][]uint64
	LocalWidths []uint32
	localArena  []uint64

	// Compact inverted index: one row of RowWidth (= ceil(n/64)) words per object.
	ReadBits  []uint64
	WriteBits []uint64
	RowWidth  uint32

	// Counts holds per-object total accesses (reads+writes) for hub detection.
	Counts []uint32

	// AnchorOf holds, per object, its first writer encoded as txnIdx+1
	// (0 = no writer yet). Drives the O(accesses) union-find partition.
	AnchorOf []uint32

	// Hub bookkeeping.
	IsHub       []bool
	HubObjs     []uint32
	HubTouchers []uint32

	// Union-Find forest, backed by reusable scratch arrays.
	uf *UnionFind

	// Island grouping scratch.
	islandOffsets []uint32 // per-root segment offsets, len n+1
	rootCache     []uint32 // txn -> root, valid only inside GroupIslands
	txnArena      []uint32 // txn indices grouped by island

	// Per-worker scratch vectors (BucketSize words, kept zeroed between uses).
	WorkerVecs [][]uint64
	slotCtr    atomic.Uint32
}

// NewBlockTopology allocates the per-worker buffers once. The block-sized
// arenas grow lazily in PrepareForBlock and are reused afterwards.
func NewBlockTopology() *BlockTopology {
	numWorkers := runtime.GOMAXPROCS(0)
	topo := &BlockTopology{
		WorkerVecs: make([][]uint64, numWorkers),
		HubEdges:   make([][]Edge, numWorkers),
	}
	for i := 0; i < numWorkers; i++ {
		topo.WorkerVecs[i] = make([]uint64, BucketSize)
		topo.HubEdges[i] = make([]Edge, 0, 256)
	}
	topo.uf = WrapUnionFind(nil, nil)
	return topo
}

// PrepareForBlock grows the scratch arenas to fit the block and resets all
// per-call state. Growth allocations are amortized away because capacity only
// increases; repeated calls on same-sized blocks allocate nothing.
func (topo *BlockTopology) PrepareForBlock(n, numObjects, rowWidth uint32) {
	topo.RowWidth = rowWidth

	indexLen := int(numObjects * rowWidth)
	topo.ReadBits = growU64(topo.ReadBits, indexLen)
	topo.WriteBits = growU64(topo.WriteBits, indexLen)

	topo.Counts = growU32(topo.Counts, int(numObjects))
	clear(topo.Counts)
	topo.AnchorOf = growU32(topo.AnchorOf, int(numObjects))
	clear(topo.AnchorOf)

	if cap(topo.IsHub) < int(numObjects) {
		topo.IsHub = make([]bool, numObjects)
	}
	topo.IsHub = topo.IsHub[:numObjects]

	topo.uf.Grow(n)
	topo.islandOffsets = growU32(topo.islandOffsets, int(n)+1)
	topo.rootCache = growU32(topo.rootCache, int(n))
	topo.txnArena = growU32(topo.txnArena, int(n))

	topo.Islands = topo.Islands[:0]
	topo.HubObjs = topo.HubObjs[:0]
	topo.HubTouchers = topo.HubTouchers[:0]
	for i := range topo.HubEdges {
		topo.HubEdges[i] = topo.HubEdges[i][:0]
	}
}

// PrepareLocalGraphs carves one all-zero local bit-matrix per island out of the
// shared arena, in the same order as topo.Islands. Called after GroupIslands by
// the topology-output pipeline only, so it also owns resetting its own slices
// (keeping PrepareForBlock — and therefore NativeShattered — untouched). The
// arena reallocates only when a block needs more words than any block before.
func (topo *BlockTopology) PrepareLocalGraphs() {
	topo.LocalGraphs = topo.LocalGraphs[:0]
	topo.LocalWidths = topo.LocalWidths[:0]

	totalWords := 0
	for _, island := range topo.Islands {
		k := len(island)
		totalWords += k * int((uint32(k)+SizeOfUint64-1)/SizeOfUint64)
	}
	topo.localArena = growU64(topo.localArena, totalWords)
	clear(topo.localArena[:totalWords])

	offset := 0
	for _, island := range topo.Islands {
		k := len(island)
		width := (uint32(k) + SizeOfUint64 - 1) / SizeOfUint64
		words := k * int(width)
		topo.LocalGraphs = append(topo.LocalGraphs, topo.localArena[offset:offset+words])
		topo.LocalWidths = append(topo.LocalWidths, width)
		offset += words
	}
}

// growU64 resizes a scratch slice to length need, reallocating only on growth.
func growU64(s []uint64, need int) []uint64 {
	if cap(s) < need {
		return make([]uint64, need)
	}
	return s[:need]
}

// growU32 resizes a scratch slice to length need, reallocating only on growth.
func growU32(s []uint32, need int) []uint32 {
	if cap(s) < need {
		return make([]uint32, need)
	}
	return s[:need]
}

// ResetSlots must be called before each parallel phase so workers can claim
// their private scratch slot via NextSlot.
func (topo *BlockTopology) ResetSlots() {
	topo.slotCtr.Store(0)
}

// NextSlot hands a worker goroutine its private scratch index.
func (topo *BlockTopology) NextSlot() uint32 {
	return topo.slotCtr.Add(1) - 1
}

func (topo *BlockTopology) InitUnionFind(n uint32) {
	topo.uf.Reset(n)
}

func (topo *BlockTopology) Find(txnIdx uint32) uint32 {
	return topo.uf.Find(txnIdx)
}

func (topo *BlockTopology) Union(txnIdx1, txnIdx2 uint32) {
	topo.uf.Union(txnIdx1, txnIdx2)
}

// GroupIslands buckets all transactions by union-find root into the shared
// arena (counting sort, O(n), zero allocations) and records every island with
// more than one member in topo.Islands. Members are in ascending txn order.
func (topo *BlockTopology) GroupIslands(n uint32) {
	offsets := topo.islandOffsets[: n+1 : n+1]
	clear(offsets)

	// Count members per root (shifted by one for the prefix sum).
	for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
		root := topo.Find(txnIdx)
		topo.rootCache[txnIdx] = root
		offsets[root+1]++
	}
	for i := uint32(1); i <= n; i++ {
		offsets[i] += offsets[i-1]
	}

	// Record multi-member segments as island views before cursors move.
	for root := uint32(0); root < n; root++ {
		if offsets[root+1]-offsets[root] > 1 {
			topo.Islands = append(topo.Islands, topo.txnArena[offsets[root]:offsets[root+1]])
		}
	}

	// Place transactions; offsets[root] doubles as the segment cursor.
	for txnIdx := uint32(0); txnIdx < n; txnIdx++ {
		root := topo.rootCache[txnIdx]
		topo.txnArena[offsets[root]] = txnIdx
		offsets[root]++
	}
}
