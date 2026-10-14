//go:build ignore

package ufnext

import (
	"math"
	"math/bits"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/model"
)

// NextConfig holds Next's knobs. With every experiment off, Next is
// IslandLeanHub with the default prefix hub rule.
type NextConfig struct {
	Island islands.IslandConfig
	// The prefix hub rule: an object is a hub when at least PrefixMinWrites
	// of the first PrefixTxns transactions write it (islands.PrefixHubs).
	PrefixTxns, PrefixMinWrites int
	// PrefixCountReads counts the prefix's reads too: an object accessed by
	// at least PrefixMinWrites prefix transactions becomes a hub, so a
	// read-hot object written once stops gluing its readers into one island.
	// Meant for the bipartite hub layer, where a hub costs one list entry
	// per access instead of its touchers' clique.
	PrefixCountReads bool
	// FusedPrefix counts the prefix writes in the anchors themselves, so the
	// hub rule needs no count array and no pass to re-zero it: the partition's
	// writer pass overwrites the counts as it reaches each object.
	FusedPrefix bool
	// Bipartite outputs the hub layer as per-hub writer and reader lists
	// (report B's bipartite projection) instead of a bit-matrix over the hub
	// touchers. It changes the output contract: experimental.
	Bipartite bool
	// Batched partitions in branch-free classification passes and a union
	// pass over the shares (next_batched.go).
	Batched bool
	// GiantShare > 0 builds every island of at least GiantShare of the
	// block's transactions (and more than 64) with the object-major kernel
	// (wide_join.go, dense factor Island.JoinDense, default 1).
	GiantShare float64
	// DenseGiant > 0 is the prefix-giant dense detector (next_prefixed.go):
	// in blocks of at least DenseMinTxns transactions, the partition runs the
	// first PrefixTxns transactions first, and when their largest set holds
	// at least DenseGiant of them, HybridJoin builds the block whole. Use it
	// with DenseHubShare = 0.
	DenseGiant float64
	// EarlyDense applies the prefix rule and the dense check on the counts
	// alone, before the anchors are cleared, so a block taking the dense
	// fallback never pays for them (countHubs; not with FusedPrefix or
	// PrefixCountReads).
	EarlyDense bool
	// ConcurrentMin: blocks of at least this many transactions are
	// partitioned on the worker pool with a CAS union-find (next_cas.go);
	// 0 = off.
	ConcurrentMin int
	// TinyMax: blocks of at most this many transactions (<=
	// competitors.TinyBitmapMax) are built whole by the one-word kernel;
	// 0 = off.
	TinyMax int
	// TinyMinRepeats > 0 keeps nearly conflict-free tiny blocks on the
	// islands: the one-word kernel runs only when the block's accesses
	// exceed its distinct objects by at least TinyMinRepeats per
	// transaction. Without repeated objects a block has few conflicts, and
	// the partition finds nothing to build, which beats the kernel's passes.
	TinyMinRepeats float64
	// DenseHubShare > 0 is the dense-block fallback (report B's
	// largest-component skipping, with a free detector): in blocks of at least
	// DenseMinTxns transactions, when at least DenseHubShare of the prefix's
	// transactions write a hub, the block is contended enough that one island
	// will hold most of it, and HybridJoin builds it whole instead. The prefix
	// rule counts those transactions while it re-zeroes its counts, so the
	// decision comes before the partition and costs nothing when it says no.
	DenseHubShare float64
	DenseMinTxns  int
	// DenseDensity > 0 makes DenseHubShare a gate: a gated block takes the
	// fallback only if the prefix's own conflict density (the one-word
	// kernel on the prefix) reaches DenseDensity. Sparse blocks never pass
	// the gate, so they never pay for the kernel.
	DenseDensity float64
}

// DefaultNextConfig is IslandLeanHub's configuration with every experiment off.
var DefaultNextConfig = NextConfig{
	Island:          islands.DefaultIslandConfig,
	PrefixTxns:      64,
	PrefixMinWrites: 4,
}

// hubRowsPerUnit is the number of hub-matrix rows per parallel unit.
const hubRowsPerUnit = 64

// hubAccess is one access of a hub object: the toucher index of the
// transaction (the transaction itself in bipartite mode) and the hub number.
type hubAccess struct {
	who, hub uint32
}

// Next is IslandLeanHub (forked, see next_core.go) with the experiments of
// NextConfig. It is reusable scratch: steady-state calls allocate nothing.
type Next struct {
	core
	ncfg      NextConfig
	counts    []uint32 // prefix counts of the unfused rule, all-zero between calls
	hubObjs   []uint32
	toucherOf []uint32 // per transaction: toucher index + 1, 0 if none yet
	hubWrites []hubAccess
	hubReads  []hubAccess
	shares    []anchorShare // Batched: the accesses of earlier-written objects

	// Matrix hub layer.
	hubW, hubR   []uint64 // per hub: writer / reader bitset over the touchers
	hubGraph     []uint64
	hubStride    uint32
	complete     bool     // some hub is written by every toucher
	wOff, rOff   []uint32 // parallel path: toucher q's hubs, grouped by toucher
	wHubs, rHubs []uint32

	// Bipartite hub layer.
	hubOff, hubArena []uint32

	tinyMinRepeats [competitors.TinyBitmapMax + 1]int // per block size: TinyMinRepeats × n, rounded up
	cas            casPartition
	whole          wholeBlock
	sampleRows     [competitors.TinyBitmapMax]uint64
}

// NewNext returns a Next with the given knobs.
func NewNext(cfg NextConfig) *Next {
	cfg.TinyMax = min(cfg.TinyMax, competitors.TinyBitmapMax)
	if cfg.GiantShare > 0 && cfg.Island.JoinDense == 0 {
		cfg.Island.JoinDense = 1
	}
	h := &Next{core: core{cfg: cfg.Island, giantShare: cfg.GiantShare}, ncfg: cfg}
	if cfg.TinyMax > 0 || cfg.DenseHubShare > 0 || cfg.DenseGiant > 0 {
		h.whole = newWholeBlock()
	}
	for n := range h.tinyMinRepeats {
		h.tinyMinRepeats[n] = int(math.Ceil(cfg.TinyMinRepeats * float64(n)))
	}
	h.extraRun = h.sweepHubRows
	return h
}

// Run builds the block's topology and returns (islands, largest, mean size).
func (h *Next) Run(block *model.Block) (int, int, float64) {
	if h.takesTiny(block) {
		return h.whole.tinyInto(block, &h.out)
	}
	if h.ncfg.EarlyDense {
		if h.countHubs(block) {
			return h.whole.joinInto(block, &h.out)
		}
		h.begin(block)
		for hub, obj := range h.hubObjs {
			h.anchor[obj] = hubBase + uint32(hub)
		}
	} else {
		h.begin(block)
		if h.markHubs(block) {
			return h.whole.joinInto(block, &h.out)
		}
	}
	var count, largest int
	var mean float64
	if n := len(block.Transactions); h.ncfg.DenseGiant > 0 && n >= h.ncfg.DenseMinTxns {
		h.forest.Reset(uint32(n))
		prefix := min(n, h.ncfg.PrefixTxns)
		if h.partitionPrefixed(block, prefix, uint32(math.Ceil(h.ncfg.DenseGiant*float64(prefix)))) {
			return h.whole.joinInto(block, &h.out)
		}
		count, largest, mean = h.collect(block)
	} else {
		count, largest, mean = h.partitionAndCollect(block)
	}
	switch {
	case len(h.hubObjs) == 0:
		h.build(block, h.parallel(block, h.localWords))
	case h.ncfg.Bipartite:
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
func (h *Next) takesTiny(block *model.Block) bool {
	txns := block.Transactions
	if h.ncfg.TinyMax == 0 || len(txns) > h.ncfg.TinyMax {
		return false
	}
	if h.ncfg.TinyMinRepeats == 0 {
		return true
	}
	repeats := -int(block.NumUniqueObjects)
	for t := range txns {
		repeats += len(txns[t].WriteSet) + len(txns[t].ReadSet)
	}
	return repeats >= h.tinyMinRepeats[len(txns)]
}

// partitionAndCollect partitions the block (concurrently from ConcurrentMin
// transactions), groups its islands and carves their local matrices; it
// returns (islands, largest, mean size).
//
// Each sequential case has its own loop: on the P-cores, folding the
// experiments into one loop slowed the default case by 13% on Ethereum, a
// code-layout effect (the loops profiled equal in a go test binary, and an
// unused function added elsewhere moved such gaps by ±10%).
func (h *Next) partitionAndCollect(block *model.Block) (int, int, float64) {
	n := len(block.Transactions)
	if h.ncfg.ConcurrentMin > 0 && n >= h.ncfg.ConcurrentMin {
		h.partitionConcurrent(block)
		inIslands, largest := 0, 0
		for _, island := range h.out.Islands {
			inIslands += len(island)
			largest = max(largest, len(island))
		}
		return h.carve(inIslands, largest)
	}
	h.forest.Reset(uint32(n))
	counted := h.ncfg.FusedPrefix && n >= islands.HubMinTxns
	switch {
	case h.ncfg.Batched:
		h.partitionBatched(block)
		if len(h.hubObjs) > 0 && !h.ncfg.Bipartite {
			h.numberTouchers(n)
		}
	case len(h.hubObjs) == 0 && !counted:
		partitionPlain(block, &h.forest, h.anchor)
	case h.ncfg.Bipartite || len(h.hubObjs) == 0:
		h.partitionBipartite(block, counted)
	default:
		h.partitionHubs(block, counted)
	}
	return h.collect(block)
}

// markHubs applies the prefix rule and marks hub h as anchor hubBase+h (with
// FusedPrefix the counts stay in the anchors as countTag+c). It reports
// whether the block takes the dense fallback (DenseHubShare).
func (h *Next) markHubs(block *model.Block) (dense bool) {
	h.hubObjs = h.hubObjs[:0]
	n := len(block.Transactions)
	if n < islands.HubMinTxns {
		return false
	}
	txns := block.Transactions[:min(n, h.ncfg.PrefixTxns)]
	need := uint32(max(2, h.ncfg.PrefixMinWrites))
	if h.ncfg.FusedPrefix {
		h.countInAnchors(txns, need)
		return false
	}
	h.counts = model.Resize(h.counts, int(block.NumUniqueObjects))
	counts := h.counts
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			if counts[obj]++; counts[obj] == need {
				h.hubObjs = append(h.hubObjs, obj)
			}
		}
	}
	if h.ncfg.PrefixCountReads {
		h.hubObjs = countPrefixReads(txns, counts, need, h.hubObjs)
	}
	for hub, obj := range h.hubObjs {
		h.anchor[obj] = hubBase + uint32(hub)
	}
	if h.ncfg.PrefixCountReads {
		for t := range txns {
			for _, obj := range txns[t].ReadSet {
				counts[obj] = 0
			}
		}
	}
	if h.ncfg.DenseHubShare > 0 && n >= h.ncfg.DenseMinTxns {
		writers := rezeroCountingHubWriters(txns, counts, h.anchor)
		if float64(writers) < h.ncfg.DenseHubShare*float64(len(txns)) {
			return false
		}
		return h.ncfg.DenseDensity == 0 || h.prefixDensity(txns, block.NumUniqueObjects) >= h.ncfg.DenseDensity
	}
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			counts[obj] = 0
		}
	}
	return false
}

// countHubs applies the prefix rule (appending the hubs to hubObjs) and the
// dense check without touching the anchors, and leaves the counts all-zero.
// It reports whether the block takes the dense fallback.
func (h *Next) countHubs(block *model.Block) (dense bool) {
	h.hubObjs = h.hubObjs[:0]
	n := len(block.Transactions)
	if n < islands.HubMinTxns {
		return false
	}
	txns := block.Transactions[:min(n, h.ncfg.PrefixTxns)]
	need := uint32(max(2, h.ncfg.PrefixMinWrites))
	h.counts = model.Resize(h.counts, int(block.NumUniqueObjects))
	counts := h.counts
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			if counts[obj]++; counts[obj] == need {
				h.hubObjs = append(h.hubObjs, obj)
			}
		}
	}
	if h.ncfg.DenseHubShare == 0 || n < h.ncfg.DenseMinTxns {
		for t := range txns {
			for _, obj := range txns[t].WriteSet {
				counts[obj] = 0
			}
		}
		return false
	}
	// A hub keeps its count (>= need) until every prefix write is checked.
	writers := 0
	for t := range txns {
		hub := uint32(0)
		for _, obj := range txns[t].WriteSet {
			c := counts[obj]
			isHub := (need - 1 - c) >> 31 // c >= need
			hub |= isHub
			counts[obj] = c & -isHub
		}
		writers += int(hub)
	}
	for _, obj := range h.hubObjs {
		counts[obj] = 0
	}
	if float64(writers) < h.ncfg.DenseHubShare*float64(len(txns)) {
		return false
	}
	return h.ncfg.DenseDensity == 0 || h.prefixDensity(txns, block.NumUniqueObjects) >= h.ncfg.DenseDensity
}

// prefixDensity returns the conflict density among the prefix's
// transactions (at most 64), built by the one-word kernel.
func (h *Next) prefixDensity(txns []model.Transaction, numObjects uint32) float64 {
	k := min(len(txns), competitors.TinyBitmapMax)
	h.whole.tiny.Build(txns[:k], numObjects, h.sampleRows[:k])
	return float64(competitors.Edges(h.sampleRows[:k])) / float64(k*(k-1)/2)
}

// countPrefixReads adds the prefix's reads to the counts and appends every
// object whose count reaches need to hubs.
func countPrefixReads(txns []model.Transaction, counts []uint32, need uint32, hubs []uint32) []uint32 {
	for t := range txns {
		for _, obj := range txns[t].ReadSet {
			if counts[obj]++; counts[obj] == need {
				hubs = append(hubs, obj)
			}
		}
	}
	return hubs
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

// countInAnchors counts the prefix's writes in the anchors (countTag+c) and
// turns an object into hub hubBase+h the moment its count reaches need.
func (h *Next) countInAnchors(txns []model.Transaction, need uint32) {
	anchor := h.anchor
	for t := range txns {
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = countTag + 1
			case a >= countTag:
				if c := a - countTag + 1; c < need {
					anchor[obj] = countTag + c
					continue
				}
				anchor[obj] = hubBase + uint32(len(h.hubObjs))
				h.hubObjs = append(h.hubObjs, obj)
			}
		}
	}
}

// partitionHubs unions every transaction with the first writer of each
// object it writes, then of each object it reads (islands' partitionWithHubs
// verbatim, plus counted): hub accesses union nothing and are recorded for
// the matrix hub layer under the toucher's index. With counted (FusedPrefix),
// an anchor holding a prefix count reads as never written: every such object
// is written by a prefix transaction, so the writer pass replaces all counts
// before it leaves the prefix.
func (h *Next) partitionHubs(block *model.Block, counted bool) {
	txns, f, anchor := block.Transactions, &h.forest, h.anchor
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	h.toucherOf = model.Resize(h.toucherOf, len(txns))
	clear(h.toucherOf)
	for t := range txns {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = root + 1
			case a >= hubBase:
				if counted && a >= countTag {
					anchor[obj] = root + 1
					continue
				}
				h.hubWrites = append(h.hubWrites, hubAccess{h.toucher(uint32(t)), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				root = join(f, root, a)
			}
		}
	}
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			switch a := anchor[obj]; {
			case a == 0:
			case a >= hubBase:
				h.hubReads = append(h.hubReads, hubAccess{h.toucher(uint32(t)), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				if root == noRoot {
					root = f.Find(uint32(t))
				}
				root = join(f, root, a)
			}
		}
	}
}

// partitionBipartite is partitionHubs for the bipartite hub layer (and for
// counted blocks without hubs): a hub access records the transaction itself,
// so no toucher is numbered.
func (h *Next) partitionBipartite(block *model.Block, counted bool) {
	txns, f, anchor := block.Transactions, &h.forest, h.anchor
	h.hubWrites, h.hubReads = h.hubWrites[:0], h.hubReads[:0]
	for t := range txns {
		root := uint32(t)
		for _, obj := range txns[t].WriteSet {
			switch a := anchor[obj]; {
			case a == 0:
				anchor[obj] = root + 1
			case a >= hubBase:
				if counted && a >= countTag {
					anchor[obj] = root + 1
					continue
				}
				h.hubWrites = append(h.hubWrites, hubAccess{uint32(t), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				root = join(f, root, a)
			}
		}
	}
	for t := range txns {
		root := noRoot
		for _, obj := range txns[t].ReadSet {
			switch a := anchor[obj]; {
			case a == 0:
			case a >= hubBase:
				h.hubReads = append(h.hubReads, hubAccess{uint32(t), a - hubBase})
			default:
				anchor[obj] = a | sharedBit
				if root == noRoot {
					root = f.Find(uint32(t))
				}
				root = join(f, root, a)
			}
		}
	}
}

// partitionPlain is the partition of a block without hubs or counts
// (islands.partitionByAnchors).
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

// toucher returns t's toucher index, numbering t on its first hub access.
func (h *Next) toucher(t uint32) uint32 {
	if q := h.toucherOf[t]; q != 0 {
		return q - 1
	}
	q := uint32(len(h.out.HubTouchers))
	h.out.HubTouchers = append(h.out.HubTouchers, t)
	h.toucherOf[t] = q + 1
	return q
}

// buildBipartite groups the hub accesses by hub (a counting sort over the
// keys 2h = writers of hub h, 2h+1 = its readers): hub h's writers become
// HubWriters[h] and its readers HubReaders[h], in ascending transaction order.
func (h *Next) buildBipartite() {
	keys := 2 * len(h.hubObjs)
	off := model.Resize(h.hubOff, keys+2)
	clear(off)
	for _, acc := range h.hubWrites {
		off[2*acc.hub+2]++
	}
	for _, acc := range h.hubReads {
		off[2*acc.hub+3]++
	}
	for k := 2; k < keys+2; k++ {
		off[k] += off[k-1]
	}
	arena := model.Resize(h.hubArena, len(h.hubWrites)+len(h.hubReads))
	for _, acc := range h.hubWrites {
		arena[off[2*acc.hub+1]] = acc.who
		off[2*acc.hub+1]++
	}
	for _, acc := range h.hubReads {
		arena[off[2*acc.hub+2]] = acc.who
		off[2*acc.hub+2]++
	}
	for hub := range len(h.hubObjs) {
		h.out.HubWriters = append(h.out.HubWriters, arena[off[2*hub]:off[2*hub+1]])
		h.out.HubReaders = append(h.out.HubReaders, arena[off[2*hub+1]:off[2*hub+2]])
	}
	h.hubOff, h.hubArena = off, arena
}

// prepareHubLayer sizes the hub matrix (one row per toucher), builds the hub
// objects' writer and reader bitsets over the touchers and checks whether the
// matrix is complete.
func (h *Next) prepareHubLayer() {
	touchers := uint32(len(h.out.HubTouchers))
	hubs := uint32(len(h.hubObjs))
	stride := model.WordsPerRow(touchers)
	h.out.HubWidth, h.hubStride = stride, stride
	h.hubW = model.Resize(h.hubW, int(hubs*stride))
	h.hubR = model.Resize(h.hubR, int(hubs*stride))
	clear(h.hubW)
	clear(h.hubR)
	for _, acc := range h.hubWrites {
		h.hubW[acc.hub*stride+acc.who/64] |= 1 << (acc.who % 64)
	}
	for _, acc := range h.hubReads {
		h.hubR[acc.hub*stride+acc.who/64] |= 1 << (acc.who % 64)
	}
	h.hubGraph = model.Resize(h.hubGraph, int(touchers*stride))
	h.out.HubGraph = h.hubGraph
	h.complete = h.hasUniversalHub(touchers)
}

// hasUniversalHub reports whether some hub is written by every toucher.
func (h *Next) hasUniversalHub(touchers uint32) bool {
	stride := h.hubStride
	for hub := range uint32(len(h.hubObjs)) {
		writers := 0
		for _, word := range h.hubW[hub*stride : (hub+1)*stride] {
			writers += bits.OnesCount64(word)
		}
		if uint32(writers) == touchers {
			return true
		}
	}
	return false
}

// fillCompleteRows writes rows [lo, hi) of a complete hub matrix.
func (h *Next) fillCompleteRows(lo, hi uint32) {
	graph, stride := h.hubGraph, h.hubStride
	touchers := uint32(len(h.out.HubTouchers))
	last := ^uint64(0) >> ((64 - touchers%64) % 64)
	for q := lo; q < hi; q++ {
		row := graph[q*stride : (q+1)*stride]
		for k := range row {
			row[k] = ^uint64(0)
		}
		row[stride-1] = last
		row[q/64] &^= 1 << (q % 64)
	}
}

// buildHubLayer builds the hub matrix inline.
func (h *Next) buildHubLayer() {
	h.prepareHubLayer()
	touchers := uint32(len(h.out.HubTouchers))
	if h.complete {
		h.fillCompleteRows(0, touchers)
		return
	}
	graph, stride := h.hubGraph, h.hubStride
	clear(graph)
	for _, acc := range h.hubWrites {
		row := graph[acc.who*stride : (acc.who+1)*stride]
		writers := h.hubW[acc.hub*stride:][:len(row)]
		readers := h.hubR[acc.hub*stride:][:len(row)]
		for k := range row {
			row[k] |= writers[k] | readers[k]
		}
	}
	for _, acc := range h.hubReads {
		row := graph[acc.who*stride : (acc.who+1)*stride]
		writers := h.hubW[acc.hub*stride:][:len(row)]
		for k := range row {
			row[k] |= writers[k]
		}
	}
	for q := range touchers {
		graph[q*stride+q/64] &^= 1 << (q % 64)
	}
}

// planHubRows prepares the hub layer for the parallel path.
func (h *Next) planHubRows() {
	h.prepareHubLayer()
	touchers := len(h.out.HubTouchers)
	h.wOff, h.wHubs = groupByToucher(h.hubWrites, touchers, h.wOff, h.wHubs)
	h.rOff, h.rHubs = groupByToucher(h.hubReads, touchers, h.rOff, h.rHubs)
	for lo := 0; lo < touchers; lo += hubRowsPerUnit {
		hi := min(lo+hubRowsPerUnit, touchers)
		h.extraUnits = append(h.extraUnits, islandUnit{kind: unitExtra, lo: uint32(lo), hi: uint32(hi)})
	}
}

// groupByToucher counting-sorts accesses by toucher: the hubs of toucher q end
// up in hubIDs[off[q]:off[q+1]].
func groupByToucher(accesses []hubAccess, touchers int, off, hubIDs []uint32) ([]uint32, []uint32) {
	off = model.Resize(off, touchers+2)
	clear(off)
	for _, acc := range accesses {
		off[acc.who+2]++
	}
	for q := 2; q < touchers+2; q++ {
		off[q] += off[q-1]
	}
	hubIDs = model.Resize(hubIDs, len(accesses))
	for _, acc := range accesses {
		hubIDs[off[acc.who+1]] = acc.hub
		off[acc.who+1]++
	}
	return off, hubIDs
}

// sweepHubRows writes rows [lo, hi) of the hub matrix (parallel path).
func (h *Next) sweepHubRows(lo, hi uint32) {
	if h.complete {
		h.fillCompleteRows(lo, hi)
		return
	}
	graph, stride := h.hubGraph, h.hubStride
	for q := lo; q < hi; q++ {
		row := graph[q*stride : (q+1)*stride]
		clear(row)
		for _, hub := range h.wHubs[h.wOff[q]:h.wOff[q+1]] {
			writers := h.hubW[hub*stride:][:len(row)]
			readers := h.hubR[hub*stride:][:len(row)]
			for k := range row {
				row[k] |= writers[k] | readers[k]
			}
		}
		for _, hub := range h.rHubs[h.rOff[q]:h.rOff[q+1]] {
			writers := h.hubW[hub*stride:][:len(row)]
			for k := range row {
				row[k] |= writers[k]
			}
		}
		row[q/64] &^= 1 << (q % 64)
	}
}
