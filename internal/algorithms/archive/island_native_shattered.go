//go:build ignore

package algorithms

import (
	"math/bits"

	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// NativePairwiseMax is the island size at or below which Native checks pairs
// directly with HasConflict instead of sweeping index rows.
const NativePairwiseMax = 8

// NativeSequentialMax is the block size at or below which Native runs both
// phases inline, because starting workers costs more than it saves.
const NativeSequentialMax uint32 = 1146

// NativeSplitRows is the row-range size large sweep islands are cut into for
// the parallel per-island phase.
const NativeSplitRows uint32 = 16

// IslandConstructionNativeShattered implements the "Masked Island" paradigm on a
// preallocated BlockTopology: physical bits go into the global ConflictGraph,
// while the logical map (islands + sparse hub edges) is extracted lock-free for
// the downstream scheduler. The partition is driven by per-object anchors in
// O(accesses), and the bitset index is built lazily — only for transactions in
// sweep-sized islands plus the hub accesses of hub touchers — so singletons and
// tiny islands never touch it. Steady-state calls perform zero heap allocations.
func IslandConstructionNativeShattered(block *model.Block, graph *model.ConflictGraph, topo *model.BlockTopology) (int, int, float64) {
	n := uint32(len(block.Transactions))
	numObjects := block.NumUniqueObjects
	rowWidth := (n + model.SizeOfUint64 - 1) / model.SizeOfUint64

	topo.PrepareForBlock(n, numObjects, rowWidth)
	graph.Reset(n)

	// 1. Fused pass: per-object access counts + hub detection at the exact
	// increment where an object crosses the threshold (no post-scan needed).
	countAccessesAndDetectHubs(block, topo)

	// 2. Track the transactions that touch any hub object.
	if len(topo.HubObjs) > 0 {
		collectHubTouchers(block, topo, n)
	}

	// 3. Union-Find partition via per-object anchors, bypassing hub objects.
	topo.InitUnionFind(n)
	partitionViaAnchors(block, topo)

	// 4. Group multi-member islands; singletons are skipped entirely.
	topo.GroupIslands(n)

	// 5. Lazy index build: only sweep-sized islands need full object rows, and
	// only hub rows need the touchers. Everything else skips the index.
	buildLazyIndex(block, topo, rowWidth)

	// 6. Per-island construction (non-hub conflicts only; hubs are step 7's job).
	sequential := n <= NativeSequentialMax
	if sequential {
		for islandIdx := range topo.Islands {
			dispatchIsland(islandIdx, block, graph, topo, rowWidth)
		}
	} else {
		planIslandWork(topo, NativePairwiseMax, NativeSplitRows)
		concurrency.ParallelFor(len(topo.WorkUnits), 1, func(_, lo, hi int) {
			for _, unit := range topo.WorkUnits[lo:hi] {
				island := topo.Islands[unit.Island]
				if len(island) > NativePairwiseMax {
					processIslandCompact(island[unit.Lo:unit.Hi], block, graph, topo, rowWidth)
				} else {
					dispatchIsland(int(unit.Island), block, graph, topo, rowWidth)
				}
			}
		})
	}

	// 7. Hub reintegration: flush hub bits to the global rows of the touchers
	// and extract the sparse cross edges lock-free into per-worker lists.
	if len(topo.HubTouchers) > 0 {
		if sequential {
			for _, txnIdx := range topo.HubTouchers {
				reintegrateAndExtract(txnIdx, block, graph, topo, rowWidth, 0)
			}
		} else {
			concurrency.ParallelFor(len(topo.HubTouchers), HubToucherChunk, func(worker, lo, hi int) {
				for _, txnIdx := range topo.HubTouchers[lo:hi] {
					reintegrateAndExtract(txnIdx, block, graph, topo, rowWidth, worker)
				}
			})
		}
	}

	// 8. Restore the all-zero invariants for the next call, mirroring exactly
	// what the lazy build touched.
	wipeLazyIndex(block, topo, rowWidth)

	return CalculateIslandStats(topo.Islands)
}

// buildLazyIndex populates object rows only where they will actually be read:
// full rows for members of sweep-sized islands (island closure guarantees those
// rows never need bits from outside the island), and hub rows for hub touchers.
// Double insertion of a bit is harmless — the index is idempotent.
func buildLazyIndex(block *model.Block, topo *model.BlockTopology, rowWidth uint32) {
	for _, island := range topo.Islands {
		if len(island) <= NativePairwiseMax {
			continue
		}
		for _, txnIdx := range island {
			word := txnIdx / model.SizeOfUint64
			mask := model.MaskActiveBit << (txnIdx % model.SizeOfUint64)
			txn := &block.Transactions[txnIdx]
			for _, objID := range txn.ReadSet {
				topo.ReadBits[objID*rowWidth+word] |= mask
			}
			for _, objID := range txn.WriteSet {
				topo.WriteBits[objID*rowWidth+word] |= mask
			}
		}
	}
	for _, txnIdx := range topo.HubTouchers {
		word := txnIdx / model.SizeOfUint64
		mask := model.MaskActiveBit << (txnIdx % model.SizeOfUint64)
		txn := &block.Transactions[txnIdx]
		for _, objID := range txn.ReadSet {
			if topo.IsHub[objID] {
				topo.ReadBits[objID*rowWidth+word] |= mask
			}
		}
		for _, objID := range txn.WriteSet {
			if topo.IsHub[objID] {
				topo.WriteBits[objID*rowWidth+word] |= mask
			}
		}
	}
}

// wipeLazyIndex re-zeroes exactly the words buildLazyIndex touched, restoring
// the all-zero invariant (zeroing an already-zero word is harmless).
func wipeLazyIndex(block *model.Block, topo *model.BlockTopology, rowWidth uint32) {
	for _, island := range topo.Islands {
		if len(island) <= NativePairwiseMax {
			continue
		}
		for _, txnIdx := range island {
			word := txnIdx / model.SizeOfUint64
			txn := &block.Transactions[txnIdx]
			for _, objID := range txn.ReadSet {
				topo.ReadBits[objID*rowWidth+word] = 0
			}
			for _, objID := range txn.WriteSet {
				topo.WriteBits[objID*rowWidth+word] = 0
			}
		}
	}
	for _, txnIdx := range topo.HubTouchers {
		word := txnIdx / model.SizeOfUint64
		txn := &block.Transactions[txnIdx]
		for _, objID := range txn.ReadSet {
			topo.ReadBits[objID*rowWidth+word] = 0
		}
		for _, objID := range txn.WriteSet {
			topo.WriteBits[objID*rowWidth+word] = 0
		}
	}
	for _, objID := range topo.HubObjs {
		topo.IsHub[objID] = false
	}
}

// dispatchIsland routes one island to its cheapest construction strategy.
func dispatchIsland(islandIdx int, block *model.Block, graph *model.ConflictGraph, topo *model.BlockTopology, rowWidth uint32) {
	island := topo.Islands[islandIdx]
	if len(island) == 2 {
		// Two txns are only ever unioned through a direct W-W/W-R share,
		// so the island is exactly one known edge: no checks, no sweeps.
		graph.AddEdge(island[0], island[1])
		graph.AddEdge(island[1], island[0])
		return
	}
	if len(island) <= NativePairwiseMax {
		processSingleIsland(island, block, graph)
		return
	}
	processIslandCompact(island, block, graph, topo, rowWidth)
}

// processIslandCompact sweeps each island member's non-hub object rows straight
// into the member's global matrix row (still all-zero: islands are disjoint and
// run before reintegration). Hub object bits are deliberately skipped —
// reintegration owns them — so every index row is swept exactly once.
func processIslandCompact(island []uint32, block *model.Block, graph *model.ConflictGraph, topo *model.BlockTopology, rowWidth uint32) {
	for _, txnIdx := range island {
		row := graph.Row(txnIdx)
		txn := &block.Transactions[txnIdx]

		for _, objID := range txn.WriteSet {
			if topo.IsHub[objID] {
				continue
			}
			base := objID * rowWidth
			writers := topo.WriteBits[base:][:len(row)]
			readers := topo.ReadBits[base:][:len(row)]
			for bIdx := range row {
				row[bIdx] |= writers[bIdx] | readers[bIdx]
			}
		}
		for _, objID := range txn.ReadSet {
			if topo.IsHub[objID] {
				continue
			}
			writers := topo.WriteBits[objID*rowWidth:][:len(row)]
			for bIdx := range row {
				row[bIdx] |= writers[bIdx]
			}
		}

		row[txnIdx/model.SizeOfUint64] &^= model.MaskActiveBit << (txnIdx % model.SizeOfUint64)
	}
}

// reintegrateAndExtract sweeps only the hub object rows of one hub toucher,
// flushes the bits to its global matrix row, and extracts the upper-triangle
// sparse edges into the private hub-edge list of the worker.
func reintegrateAndExtract(txnIdx uint32, block *model.Block, graph *model.ConflictGraph, topo *model.BlockTopology, rowWidth uint32, worker int) {
	txn := &block.Transactions[txnIdx]
	vec := topo.WorkerVecs[worker]

	for _, objID := range txn.WriteSet {
		if !topo.IsHub[objID] {
			continue
		}
		base := objID * rowWidth
		writers := topo.WriteBits[base : base+rowWidth]
		readers := topo.ReadBits[base : base+rowWidth]
		for bIdx := uint32(0); bIdx < rowWidth; bIdx++ {
			vec[bIdx] |= writers[bIdx] | readers[bIdx]
		}
	}
	for _, objID := range txn.ReadSet {
		if !topo.IsHub[objID] {
			continue
		}
		base := objID * rowWidth
		writers := topo.WriteBits[base : base+rowWidth]
		for bIdx := uint32(0); bIdx < rowWidth; bIdx++ {
			vec[bIdx] |= writers[bIdx]
		}
	}

	vec[txnIdx/model.SizeOfUint64] &= ^(model.MaskActiveBit << (txnIdx % model.SizeOfUint64))
	graph.AddRow(txnIdx, vec)

	// Extract sparse edges (upper triangle only: each pair is recorded once,
	// by its lower-indexed toucher) and fast-clear the vector as we go.
	for bIdx := uint32(0); bIdx < rowWidth; bIdx++ {
		activeBits := vec[bIdx]
		basePeerIdx := bIdx * model.SizeOfUint64
		for activeBits != 0 {
			peerIdx := basePeerIdx + uint32(bits.TrailingZeros64(activeBits))
			if txnIdx < peerIdx {
				topo.HubEdges[worker] = append(topo.HubEdges[worker], model.Edge{TxnA: txnIdx, TxnB: peerIdx})
			}
			activeBits &= activeBits - 1
		}
		vec[bIdx] = 0
	}
}
