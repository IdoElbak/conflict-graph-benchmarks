//go:build ignore

package ufnext

import (
	"runtime"
	"sync/atomic"

	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// This file is the island half of IslandLeanHub (islands/island_shared.go),
// forked so the ufnext variants can change the pipeline's inside while the
// originals stay untouched for comparison. Unchanged from the original except
// that the object-major wide kernel (IslandConfig.JoinDense, neutral in Phase
// 3) is left out: islands wider than 64 always take the Bitmap-style kernel.

// Per-object anchor encoding (as in package islands, plus countTag):
//
//	0                     never written (so far)
//	[1, sharedBit)        root+1 of its first writer; no second transaction yet
//	[sharedBit, hubBase)  the same | sharedBit: a second transaction touched it
//	[hubBase, countTag)   hub object number hubBase+h, kept out of the partition
//	[countTag, ...)       FusedPrefix only: written countTag+c times by the hub
//	                      prefix, not (yet) a hub; reads as "never written"
const (
	sharedBit uint32 = 1 << 30
	hubBase   uint32 = 1 << 31
	countTag  uint32 = 3 << 30
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

// core holds the output, the per-object anchors, the union-find forest and
// the per-island construction of the local matrices. All scratch is reused;
// steady-state calls allocate nothing.
type core struct {
	cfg    islands.IslandConfig
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
	extraUnits  []islandUnit         // the variant's own independent units (hub rows)
	extraRun    func(lo, hi uint32)

	// Object-major kernel for giant islands (giantShare > 0).
	giantShare float64 // islands of at least this share of the block (and > 64)
	giantMin   uint32  // the members that makes, for the current block
	joinSpans  []joinSpan
	join       wideJoin
	workerJoin []wideJoin
}

// Topology returns the output of the last Run.
func (c *core) Topology() *model.IslandTopology {
	return &c.out
}

// begin resets the output and the anchors for a new block (the variant
// resets the forest it partitions with).
func (c *core) begin(block *model.Block) {
	c.out.Reset()
	c.anchor = model.Resize(c.anchor, int(block.NumUniqueObjects))
	clear(c.anchor)
	c.extraUnits = c.extraUnits[:0]
}

// collect reads the islands off the partitioned forest and carves their local
// matrices; it returns (islands, largest, mean size).
func (c *core) collect(block *model.Block) (int, int, float64) {
	inIslands, largest := c.out.GroupIslands(&c.forest, uint32(len(block.Transactions)))
	return c.carve(inIslands, largest)
}

// carve carves the local matrices of the grouped islands; it returns
// (islands, largest, mean size).
func (c *core) carve(inIslands, largest int) (int, int, float64) {
	c.localWords = c.out.CarveLocalGraphs()
	c.stride = max(1, model.WordsPerRow(uint32(largest)))
	if len(c.out.Islands) == 0 {
		return 0, 0, 0
	}
	return len(c.out.Islands), largest, float64(inIslands) / float64(len(c.out.Islands))
}

// parallel reports whether the block takes the parallel path: more than
// SequentialMax transactions and at least ParallelWords words to build.
func (c *core) parallel(block *model.Block, words int) bool {
	return uint32(len(block.Transactions)) > c.cfg.SequentialMax && words >= c.cfg.ParallelWords
}

// build builds the collected islands, inline or on the worker pool.
func (c *core) build(block *model.Block, parallel bool) {
	c.index.Prepare(block.NumUniqueObjects, c.stride)
	c.giantMin = ^uint32(0)
	if c.giantShare > 0 {
		c.giantMin = max(65, uint32(c.giantShare*float64(len(block.Transactions))))
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
func (c *core) buildSequential(txns []model.Transaction) {
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
	unitExtra = iota // the variant's own work, range [lo, hi) of extraRun
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
// islands' index builds first, then the variant's own units, then the small
// islands in groups of about GroupTxns members on per-worker one-word
// indexes, last the wide islands' rows in SplitRows ranges, each waiting for
// its island's build. The wide rows are wiped afterwards.
func (c *core) buildParallel(txns []model.Transaction) {
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
func (c *core) prepareWorkers() {
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
func (c *core) orderIslands() int {
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
func (c *core) planUnits(numWide int) {
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
func (c *core) buildSmall(txns []model.Transaction, island []uint32, local []uint64, worker int) {
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
