// Package islands holds the Union-Find family. Both algorithms partition the
// block into islands that cannot conflict with each other, keeping hub objects
// (written by many of the block's first transactions) out of the partition,
// build each island's graph separately, and output a model.IslandTopology:
// independent island graphs plus a hub layer, instead of one global matrix.
// IslandHub keeps the hub layer as a bit-matrix over the hub touchers;
// IslandBipartite outputs it as per-hub writer and reader lists. Retired
// variants are in ../archive/islands.
package islands

import (
	"math"
	"runtime"
	"sync/atomic"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// IslandConfig holds the knobs of the island core. The proving ground
// (`go run . -suite=uf`) times several configurations in one run.
type IslandConfig struct {
	PairwiseMax   int    // islands of 3..PairwiseMax (<= 64) members use pairwise HasConflict
	SequentialMax uint32 // blocks of at most this many transactions run inline
	ParallelWords int    // larger blocks run on the pool once their matrices hold this many words
	SplitRows     uint32 // rows per parallel sweep unit of islands wider than 64
	GroupTxns     int    // parallel path: small islands are claimed in groups of about this many members
	// GiantShare > 0 builds every island holding at least this share of the
	// block's transactions (and more than 64) object-major (wideJoin,
	// HybridJoin's kernel in local coordinates) instead of by index build,
	// row sweep and wipe: on an island of most of the block, the Bitmap-style
	// sweep costs what Bitmap costs, where the per-object choice between pair
	// bits and row ORs wins.
	GiantShare float64
	// JoinDense: in the object-major kernel, an object is ORed as rows when
	// its pair bits exceed JoinDense × its accessors × row words.
	JoinDense float64
}

// DefaultIslandConfig holds the knobs the proving ground picked.
var DefaultIslandConfig = IslandConfig{
	PairwiseMax:   4,
	SequentialMax: 300,
	ParallelWords: 8000,
	SplitRows:     16,
	GroupTxns:     256,
	GiantShare:    0.3,
	JoinDense:     1,
}

// Per-object anchor encoding:
//
//	0                     never written (so far)
//	[1, sharedBit)        root+1 of its first writer; no second transaction yet
//	[sharedBit, hubBase)  the same | sharedBit: a second transaction touched it
//	[hubBase, ...)        hub object number hubBase+h, kept out of the partition
//
// Only live objects (shared and not hubs) can produce an island edge: a read
// of a never-written object conflicts with nobody, and neither does a written
// object no second transaction touches.
const (
	sharedBit uint32 = 1 << 30
	hubBase   uint32 = 1 << 31
)

// noRoot marks a transaction root not looked up yet.
const noRoot = ^uint32(0)

// liveBit is 1 for a live anchor (in [sharedBit, hubBase): top bits 01) and
// 0 otherwise, computed without a branch.
func liveBit(anchor uint32) int {
	top := anchor >> 30
	return int(top & 1 &^ (top >> 1))
}

// join unions root with the set of the object anchored at a (not a hub) and
// returns the new root of the current transaction.
func join(f *model.IslandForest, root, a uint32) uint32 {
	if other := f.Find(a&^sharedBit - 1); other != root {
		return f.Link(other, root)
	}
	return root
}

// islandCore holds the output, the per-object anchors, the union-find forest
// and the per-island construction of the local matrices. All scratch is reused;
// steady-state calls allocate nothing.
type islandCore struct {
	cfg    IslandConfig
	out    model.IslandTopology
	forest model.IslandForest
	anchor []uint32
	index  model.IslandIndex
	live   model.LiveAccesses

	localWords int    // words of the carved local matrices
	stride     uint32 // index row stride of the parallel path

	// Parallel path scratch.
	workerIndex []model.IslandIndex  // per-worker one-word index for small islands
	workerLive  []model.LiveAccesses // per-worker compaction scratch for small islands
	wideLive    []model.LiveAccesses // per wide island, kept from build to wipe
	order       []uint32             // wide islands, then small islands
	units       []islandUnit         // claims of the single pool pass
	built       []atomic.Bool        // per wide island: index rows complete
	extraUnits  []islandUnit         // the algorithm's own independent units (matrix hub layer: hub rows)
	extraRun    func(lo, hi uint32)

	// Object-major kernel for giant islands (GiantShare > 0).
	giantMin   uint32 // GiantShare of the current block's transactions, at least 65
	joinSpans  []joinSpan
	join       wideJoin
	workerJoin []wideJoin
}

// Topology returns the output of the last Run.
func (c *islandCore) Topology() *model.IslandTopology {
	return &c.out
}

// begin resets the output and the anchors for a new block.
func (c *islandCore) begin(block *model.Block) {
	c.out.Reset()
	c.anchor = model.Resize(c.anchor, int(block.NumUniqueObjects))
	clear(c.anchor)
	c.extraUnits = c.extraUnits[:0]
}

// collect reads the islands off the partitioned forest and carves their local
// matrices; it returns (islands, largest, mean size).
func (c *islandCore) collect(block *model.Block) (int, int, float64) {
	inIslands, largest := c.out.GroupIslands(&c.forest, uint32(len(block.Transactions)))
	return c.carve(inIslands, largest)
}

// carve carves the local matrices of the grouped islands; it returns
// (islands, largest, mean size).
func (c *islandCore) carve(inIslands, largest int) (int, int, float64) {
	c.localWords = c.out.CarveLocalGraphs()
	c.stride = max(1, model.WordsPerRow(uint32(largest)))
	if len(c.out.Islands) == 0 {
		return 0, 0, 0
	}
	return len(c.out.Islands), largest, float64(inIslands) / float64(len(c.out.Islands))
}

// parallel reports whether the block takes the parallel path: more than
// SequentialMax transactions and at least ParallelWords words to build.
func (c *islandCore) parallel(block *model.Block, words int) bool {
	return uint32(len(block.Transactions)) > c.cfg.SequentialMax && words >= c.cfg.ParallelWords
}

// build builds the collected islands, inline or on the worker pool.
func (c *islandCore) build(block *model.Block, parallel bool) {
	c.index.Prepare(block.NumUniqueObjects, c.stride)
	c.giantMin = ^uint32(0)
	if c.cfg.GiantShare > 0 {
		c.giantMin = max(65, uint32(c.cfg.GiantShare*float64(len(block.Transactions))))
		c.joinSpans = model.Resize(c.joinSpans, int(block.NumUniqueObjects))
	}
	if parallel {
		c.buildParallel(block.Transactions)
	} else {
		c.buildSequential(block.Transactions)
	}
}

// buildSequential builds every island inline. Each island is wiped before the
// next starts, so every island can use its own row stride.
func (c *islandCore) buildSequential(txns []model.Transaction) {
	for i, island := range c.out.Islands {
		local := c.out.LocalGraphs[i]
		switch k := uint32(len(island)); {
		case k == 2:
			pairEdge(local)
		case int(k) <= c.cfg.PairwiseMax:
			pairwiseIsland(txns, island, local)
		case k <= 64:
			compactLive(&c.live, txns, island, c.anchor)
			narrowIsland(&c.live, local, &c.index, 1)
		case k >= c.giantMin:
			compactLive(&c.live, txns, island, c.anchor)
			c.join.build(&c.live, k, local, c.out.LocalWidths[i], c.joinSpans, c.cfg.JoinDense)
		default:
			width := c.out.LocalWidths[i]
			compactLive(&c.live, txns, island, c.anchor)
			buildWide(&c.live, 0, k, &c.index, width)
			sweepWide(&c.live, 0, k, local, width, &c.index, width)
			wipeWide(&c.live, 0, k, &c.index, width)
		}
	}
}

// islandUnit kinds: the units of the parallel path.
const (
	unitExtra = iota // the algorithm's own work, range [lo, hi) of extraRun
	unitBuild        // index rows of wide island a
	unitSmall        // small islands order[lo:hi], end to end
	unitSweep        // rows [lo, hi) of wide island a, once its rows are built
)

// islandUnit is one claim of the parallel path.
type islandUnit struct {
	kind      uint8
	a, lo, hi uint32
}

// buildParallel builds the islands in a single pass of the worker pool: wide
// islands' index builds first, then the algorithm's own units, then the small
// islands in groups of about GroupTxns members on per-worker one-word
// indexes, last the wide islands' rows in SplitRows ranges, each waiting for
// its island's build. The wide rows are wiped afterwards.
func (c *islandCore) buildParallel(txns []model.Transaction) {
	c.prepareWorkers()
	numWide := c.orderIslands()
	c.planUnits(numWide)

	islands, graphs, widths := c.out.Islands, c.out.LocalGraphs, c.out.LocalWidths
	order, units, stride := c.order, c.units, c.stride
	concurrency.ParallelFor(len(units), 1, func(worker, lo, hi int) {
		for _, u := range units[lo:hi] {
			switch u.kind {
			case unitExtra:
				c.extraRun(u.lo, u.hi)
			case unitBuild:
				island := islands[order[u.a]]
				compactLive(&c.wideLive[u.a], txns, island, c.anchor)
				if i := order[u.a]; uint32(len(island)) >= c.giantMin {
					c.workerJoin[worker].build(&c.wideLive[u.a], uint32(len(island)), graphs[i], widths[i], c.joinSpans, c.cfg.JoinDense)
					continue
				}
				buildWide(&c.wideLive[u.a], 0, uint32(len(island)), &c.index, stride)
				c.built[u.a].Store(true)
			case unitSmall:
				for _, i := range order[u.lo:u.hi] {
					c.buildSmall(txns, islands[i], graphs[i], worker)
				}
			case unitSweep:
				for !c.built[u.a].Load() {
					runtime.Gosched()
				}
				i := order[u.a]
				sweepWide(&c.wideLive[u.a], u.lo, u.hi, graphs[i], widths[i], &c.index, stride)
			}
		}
	})

	for j := range numWide {
		if k := uint32(len(islands[order[j]])); k < c.giantMin {
			wipeWide(&c.wideLive[j], 0, k, &c.index, stride)
		}
	}
}

// prepareWorkers sizes every worker's one-word index for the block.
func (c *islandCore) prepareWorkers() {
	if workers := runtime.GOMAXPROCS(0); len(c.workerIndex) < workers {
		c.workerIndex = make([]model.IslandIndex, workers)
		c.workerLive = make([]model.LiveAccesses, workers)
		c.workerJoin = make([]wideJoin, workers)
	}
	for w := range c.workerIndex {
		c.workerIndex[w].Prepare(uint32(len(c.anchor)), 1)
	}
}

// orderIslands lists the islands wider than 64 first, then the rest, readies
// one build flag and access list per wide island, and returns their number.
func (c *islandCore) orderIslands() int {
	c.order = c.order[:0]
	for i, island := range c.out.Islands {
		if len(island) > 64 {
			c.order = append(c.order, uint32(i))
		}
	}
	numWide := len(c.order)
	for i, island := range c.out.Islands {
		if len(island) <= 64 {
			c.order = append(c.order, uint32(i))
		}
	}
	for len(c.wideLive) < numWide {
		c.wideLive = append(c.wideLive, model.LiveAccesses{})
	}
	if len(c.built) < numWide {
		c.built = make([]atomic.Bool, numWide)
	}
	for j := range numWide {
		c.built[j].Store(false)
	}
	return numWide
}

// planUnits lists the claims of the pool pass, in claim order.
func (c *islandCore) planUnits(numWide int) {
	islands := c.out.Islands
	c.units = c.units[:0]
	for j := range numWide {
		c.units = append(c.units, islandUnit{kind: unitBuild, a: uint32(j)})
	}
	c.units = append(c.units, c.extraUnits...)
	for lo := numWide; lo < len(c.order); {
		hi, size := lo, 0
		for hi == lo || hi < len(c.order) && size < c.cfg.GroupTxns {
			size += len(islands[c.order[hi]])
			hi++
		}
		c.units = append(c.units, islandUnit{kind: unitSmall, lo: uint32(lo), hi: uint32(hi)})
		lo = hi
	}
	for j := range numWide {
		k := uint32(len(islands[c.order[j]]))
		if k >= c.giantMin {
			continue // the object-major kernel builds the whole island in its build unit
		}
		for lo := uint32(0); lo < k; lo += c.cfg.SplitRows {
			c.units = append(c.units, islandUnit{kind: unitSweep, a: uint32(j), lo: lo, hi: min(lo+c.cfg.SplitRows, k)})
		}
	}
}

// buildSmall builds an island of at most 64 members end to end on the
// worker's own index.
func (c *islandCore) buildSmall(txns []model.Transaction, island []uint32, local []uint64, worker int) {
	switch k := len(island); {
	case k == 2:
		pairEdge(local)
	case k <= c.cfg.PairwiseMax:
		pairwiseIsland(txns, island, local)
	default:
		compactLive(&c.workerLive[worker], txns, island, c.anchor)
		narrowIsland(&c.workerLive[worker], local, &c.workerIndex[worker], 1)
	}
}

// pairEdge writes the matrix of a two-member island: transactions are only
// ever unioned through a direct W-W or W-R share, so it is one edge.
func pairEdge(local []uint64) {
	local[0], local[1] = 2, 1
}

// pairwiseIsland checks every pair of a small island (at most 64 members)
// directly with HasConflict.
func pairwiseIsland(txns []model.Transaction, island []uint32, local []uint64) {
	clear(local)
	for i := range island {
		for j := i + 1; j < len(island); j++ {
			if txns[island[i]].HasConflict(&txns[island[j]]) {
				local[i] |= 1 << j
				local[j] |= 1 << i
			}
		}
	}
}

// compactLive gathers the live accesses of the island's members into la,
// storing every access and advancing the cursor by its live bit.
func compactLive(la *model.LiveAccesses, txns []model.Transaction, island []uint32, anchor []uint32) {
	nw, nr := 0, 0
	for _, t := range island {
		nw += len(txns[t].WriteSet)
		nr += len(txns[t].ReadSet)
	}
	la.W, la.R = model.Resize(la.W, nw), model.Resize(la.R, nr)
	la.WEnd, la.REnd = model.Resize(la.WEnd, len(island)), model.Resize(la.REnd, len(island))
	nw, nr = 0, 0
	for p, t := range island {
		for _, obj := range txns[t].WriteSet {
			la.W[nw] = obj
			nw += liveBit(anchor[obj])
		}
		la.WEnd[p] = uint32(nw)
		for _, obj := range txns[t].ReadSet {
			la.R[nr] = obj
			nr += liveBit(anchor[obj])
		}
		la.REnd[p] = uint32(nr)
	}
}

// narrowIsland builds, sweeps and wipes the one-word index rows of an island
// of at most 64 members (rows at obj*stride), writing its local matrix.
func narrowIsland(la *model.LiveAccesses, local []uint64, idx *model.IslandIndex, stride uint32) {
	writers, readers := idx.Writers, idx.Readers
	k := uint32(len(local))
	for p := range k {
		bit := uint64(1) << p
		for _, obj := range la.Writes(p) {
			writers[obj*stride] |= bit
		}
		for _, obj := range la.Reads(p) {
			readers[obj*stride] |= bit
		}
	}
	for p := range k {
		var row uint64
		for _, obj := range la.Writes(p) {
			row |= writers[obj*stride] | readers[obj*stride]
		}
		for _, obj := range la.Reads(p) {
			row |= writers[obj*stride]
		}
		local[p] = row &^ (1 << p)
	}
	// Every live read is of an object a member writes: clearing both rows of
	// the live writes clears all the build set.
	for _, obj := range la.W[:la.WEnd[k-1]] {
		writers[obj*stride] = 0
		readers[obj*stride] = 0
	}
}

// buildWide sets the index bits of members [lo, hi) of an island wider than
// 64 (rows of stride words at obj*stride).
func buildWide(la *model.LiveAccesses, lo, hi uint32, idx *model.IslandIndex, stride uint32) {
	for p := lo; p < hi; p++ {
		word, bit := p/64, uint64(1)<<(p%64)
		for _, obj := range la.Writes(p) {
			idx.Writers[obj*stride+word] |= bit
		}
		for _, obj := range la.Reads(p) {
			idx.Readers[obj*stride+word] |= bit
		}
	}
}

// sweepWide writes rows [lo, hi) of a wide island's local matrix (width words
// each) as the OR of the index rows its members conflict through.
func sweepWide(la *model.LiveAccesses, lo, hi uint32, local []uint64, width uint32, idx *model.IslandIndex, stride uint32) {
	for p := lo; p < hi; p++ {
		row := local[p*width : (p+1)*width]
		clear(row)
		for _, obj := range la.Writes(p) {
			writers := idx.Writers[obj*stride:][:len(row)]
			readers := idx.Readers[obj*stride:][:len(row)]
			for i := range row {
				row[i] |= writers[i] | readers[i]
			}
		}
		for _, obj := range la.Reads(p) {
			writers := idx.Writers[obj*stride:][:len(row)]
			for i := range row {
				row[i] |= writers[i]
			}
		}
		row[p/64] &^= 1 << (p % 64)
	}
}

// wipeWide clears exactly the words buildWide set for members [lo, hi).
func wipeWide(la *model.LiveAccesses, lo, hi uint32, idx *model.IslandIndex, stride uint32) {
	for p := lo; p < hi; p++ {
		word := p / 64
		for _, obj := range la.Writes(p) {
			idx.Writers[obj*stride+word] = 0
		}
		for _, obj := range la.Reads(p) {
			idx.Readers[obj*stride+word] = 0
		}
	}
}

// ---------------------------------------------------------------------------
// The object-major kernel of giant islands.
// ---------------------------------------------------------------------------

// joinSpan is one object's place in a wideJoin: first its writer and reader
// counts, then lo = arena start + 1 (0 = not laid out yet), with w and r
// reused as the fill cursors.
type joinSpan struct {
	w, r, lo uint32
}

// wideJoin is the object-major kernel for islands wider than 64 members
// (IslandConfig.GiantShare > 0), HybridJoin's kernel in local coordinates:
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

// ---------------------------------------------------------------------------
// The hub algorithm IslandHub and IslandBipartite share: the hub rule, the
// dense fallback, the tiny path and the partition that keeps the hubs out.
// Each hub layer lives in its algorithm's file.
// ---------------------------------------------------------------------------

// HubConfig holds the knobs of IslandHub and IslandBipartite around the island
// core: the hub rule, and the two block shapes a partition cannot pay for.
type HubConfig struct {
	Island IslandConfig
	// The prefix hub rule: an object is a hub when at least PrefixMinWrites
	// of the first PrefixTxns transactions write it. Blocks under HubMinTxns
	// transactions have no hubs: a hub's touchers are few, and the prefix
	// would be the whole block.
	PrefixTxns, PrefixMinWrites, HubMinTxns int
	// PrefixStrided samples the prefix's PrefixTxns transactions evenly
	// across the block instead of taking the first ones.
	PrefixStrided bool
	// TinyMax: blocks of at most this many transactions (<=
	// competitors.TinyBitmapMax) are built whole by the one-word kernel
	// competitors.TinyBitmap, where every row is one word and a partition can
	// only add work; 0 = off.
	TinyMax int
	// TinyMinRepeats > 0 keeps nearly conflict-free tiny blocks on the
	// islands: the one-word kernel runs only when the block's accesses exceed
	// its distinct objects by at least TinyMinRepeats per transaction. Without
	// repeated objects a block has few conflicts, and the partition finds
	// nothing to build, which beats the kernel's passes.
	TinyMinRepeats float64
	// DenseHubShare > 0 is the dense-block fallback: in blocks of at least
	// DenseMinTxns transactions, when at least DenseHubShare of the prefix's
	// transactions write a hub, one island will hold most of the block, and
	// HybridJoin builds it whole instead. The prefix rule counts those
	// transactions while it re-zeroes its counts, so the decision comes before
	// the partition and costs nothing when it says no.
	DenseHubShare float64
	DenseMinTxns  int
	// HubRowsPerUnit is the number of hub-matrix rows per parallel unit
	// (matrix hub layer).
	HubRowsPerUnit int
}

// hubAccess is one access of a hub object: the toucher index of the
// transaction (the transaction itself in bipartite mode) and the hub number.
type hubAccess struct {
	who, hub uint32
}

// hubIslands is the algorithm IslandHub and IslandBipartite share: the hub
// rule, the partition that keeps the hubs out, one of the two hub layers, and
// the whole-block paths. It is reusable scratch: steady-state calls allocate
// nothing.
type hubIslands struct {
	islandCore
	hcfg      HubConfig
	bipartite bool
	counts    []uint32 // prefix write counts, all-zero between calls
	hubObjs   []uint32
	toucherOf []uint32 // per transaction: toucher index + 1, 0 if none yet
	hubWrites []hubAccess
	hubReads  []hubAccess
	sample    []model.Transaction // PrefixStrided: the sampled prefix

	// Matrix hub layer (IslandHub).
	hubW, hubR   []uint64 // per hub: writer / reader bitset over the touchers
	hubGraph     []uint64
	hubStride    uint32
	complete     bool     // some hub is written by every toucher
	wOff, rOff   []uint32 // parallel path: toucher q's hubs, grouped by toucher
	wHubs, rHubs []uint32

	// Bipartite hub layer (IslandBipartite).
	hubOff, hubArena []uint32

	tinyMinRepeats [competitors.TinyBitmapMax + 1]int // per block size: TinyMinRepeats × n, rounded up
	whole          wholeBlock
}

// init readies h with the given knobs and hub layer.
func (h *hubIslands) init(cfg HubConfig, bipartite bool) {
	cfg.TinyMax = min(cfg.TinyMax, competitors.TinyBitmapMax)
	cfg.HubRowsPerUnit = max(1, cfg.HubRowsPerUnit)
	h.islandCore.cfg, h.hcfg, h.bipartite = cfg.Island, cfg, bipartite
	if cfg.TinyMax > 0 || cfg.DenseHubShare > 0 {
		h.whole = newWholeBlock()
	}
	for n := range h.tinyMinRepeats {
		h.tinyMinRepeats[n] = int(math.Ceil(cfg.TinyMinRepeats * float64(n)))
	}
	h.extraRun = h.sweepHubRows
}

// Run builds the block's topology and returns (islands, largest, mean size).
func (h *hubIslands) Run(block *model.Block) (int, int, float64) {
	if h.takesTiny(block) {
		return h.whole.tinyInto(block, &h.out)
	}
	h.begin(block)
	if h.markHubs(block) {
		return h.whole.joinInto(block, &h.out)
	}
	count, largest, mean := h.partitionAndCollect(block)
	switch {
	case len(h.hubObjs) == 0:
		h.build(block, h.parallel(block, h.localWords))
	case h.bipartite:
		h.buildBipartite()
		h.build(block, h.parallel(block, h.localWords))
	default:
		touchers := len(h.out.HubTouchers)
		parallel := h.parallel(block, h.localWords+touchers*int(model.WordsPerRow(uint32(touchers))))
		if parallel {
			h.planHubRows()
		} else {
			h.buildHubLayer()
		}
		h.build(block, parallel)
	}
	return count, largest, mean
}

// takesTiny reports whether the block is built whole by the one-word kernel.
func (h *hubIslands) takesTiny(block *model.Block) bool {
	txns := block.Transactions
	if h.hcfg.TinyMax == 0 || len(txns) > h.hcfg.TinyMax {
		return false
	}
	if h.hcfg.TinyMinRepeats == 0 {
		return true
	}
	repeats := -int(block.NumUniqueObjects)
	for t := range txns {
		repeats += len(txns[t].WriteSet) + len(txns[t].ReadSet)
	}
	return repeats >= h.tinyMinRepeats[len(txns)]
}

// partitionAndCollect partitions the block, groups its islands and carves
// their local matrices; it returns (islands, largest, mean size).
//
// Each case has its own loop: on the P-cores, folding them into one loop
// slowed the plain case by 13% on Ethereum, a code-layout effect (the loops
// profiled equal in a go test binary, and an unused function added elsewhere
// moved such gaps by ±10%).
func (h *hubIslands) partitionAndCollect(block *model.Block) (int, int, float64) {
	h.forest.Reset(uint32(len(block.Transactions)))
	switch {
	case len(h.hubObjs) == 0:
		partitionPlain(block, &h.forest, h.anchor)
	case h.bipartite:
		h.partitionBipartite(block)
	default:
		h.partitionHubs(block)
	}
	return h.collect(block)
}

// markHubs applies the prefix rule and marks hub h as anchor hubBase+h. It
// reports whether the block takes the dense fallback (DenseHubShare).
func (h *hubIslands) markHubs(block *model.Block) (dense bool) {
	h.hubObjs = h.hubObjs[:0]
	n := len(block.Transactions)
	if n < h.hcfg.HubMinTxns {
		return false
	}
	txns := block.Transactions[:min(n, h.hcfg.PrefixTxns)]
	if h.hcfg.PrefixStrided && n > len(txns) {
		h.sample = h.sample[:0]
		for i := range len(txns) {
			h.sample = append(h.sample, block.Transactions[i*n/len(txns)])
		}
		txns = h.sample
	}
	need := uint32(max(2, h.hcfg.PrefixMinWrites))
	h.counts = model.Resize(h.counts, int(block.NumUniqueObjects))
	counts := h.counts
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			if counts[obj]++; counts[obj] == need {
				h.hubObjs = append(h.hubObjs, obj)
			}
		}
	}
	for hub, obj := range h.hubObjs {
		h.anchor[obj] = hubBase + uint32(hub)
	}
	if h.hcfg.DenseHubShare > 0 && n >= h.hcfg.DenseMinTxns {
		writers := rezeroCountingHubWriters(txns, counts, h.anchor)
		return float64(writers) >= h.hcfg.DenseHubShare*float64(len(txns))
	}
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			counts[obj] = 0
		}
	}
	return false
}

// rezeroCountingHubWriters re-zeroes the counts of the prefix's writes and
// returns how many of its transactions write a hub (anchor marked).
func rezeroCountingHubWriters(txns []model.Transaction, counts, anchor []uint32) int {
	writers := 0
	for t := range txns {
		hub := 0
		for _, obj := range txns[t].WriteSet {
			if anchor[obj] >= hubBase {
				hub = 1
			}
			counts[obj] = 0
		}
		writers += hub
	}
	return writers
}

// partitionPlain is the partition of a block without hubs.
func partitionPlain(block *model.Block, f *model.IslandForest, anchor []uint32) {
	txns := block.Transactions
	for t := range txns {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			a := anchor[obj]
			if a == 0 {
				anchor[obj] = root + 1
				continue
			}
			anchor[obj] = a | sharedBit
			root = join(f, root, a)
		}
	}
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			a := anchor[obj]
			if a == 0 {
				continue
			}
			anchor[obj] = a | sharedBit
			if root == noRoot {
				root = f.Find(uint32(t))
			}
			root = join(f, root, a)
		}
	}
}

// wholeBlock builds a block as a single island holding every transaction, for
// the blocks where a partition cannot pay: tiny blocks with competitors'
// one-word TinyBitmap, dense ones with HybridJoin. The island is exact but
// gives the downstream colouring no parallelism inside it; on such blocks the
// islands would be ~10 transactions or one giant anyway.
type wholeBlock struct {
	tiny     competitors.TinyBitmap
	tinyRows [competitors.TinyBitmapMax]uint64
	join     *competitors.HybridJoin
	graph    *model.ConflictGraph
	identity []uint32
}

// newWholeBlock returns the scratch of the whole-block paths.
func newWholeBlock() wholeBlock {
	w := wholeBlock{
		join:     competitors.NewHybridJoin(competitors.DefaultHybridJoinConfig),
		graph:    model.NewConflictGraph(),
		identity: make([]uint32, model.MaximumGraphSize),
	}
	for t := range w.identity {
		w.identity[t] = uint32(t)
	}
	return w
}

// tinyInto builds a block of at most competitors.TinyBitmapMax transactions
// into out; it returns (islands, largest, mean size).
func (w *wholeBlock) tinyInto(block *model.Block, out *model.IslandTopology) (int, int, float64) {
	n := len(block.Transactions)
	rows := w.tinyRows[:n]
	w.tiny.Build(block.Transactions, block.NumUniqueObjects, rows)
	out.SetWholeBlock(w.identity[:n], rows, 1)
	return 1, n, float64(n)
}

// joinInto builds the block with HybridJoin into out; it returns (islands,
// largest, mean size).
func (w *wholeBlock) joinInto(block *model.Block, out *model.IslandTopology) (int, int, float64) {
	w.join.Run(block, w.graph)
	return w.publish(block, out)
}

// publish makes the block's matrix out's only island.
func (w *wholeBlock) publish(block *model.Block, out *model.IslandTopology) (int, int, float64) {
	n := len(block.Transactions)
	matrix, width := w.graph.Matrix(uint32(n))
	out.SetWholeBlock(w.identity[:n], matrix, width)
	return 1, n, float64(n)
}
