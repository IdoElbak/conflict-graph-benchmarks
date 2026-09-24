package algorithms

import (
	"math/bits"

	"conflict-graph-benchmarks/internal/concurrency"
	"conflict-graph-benchmarks/internal/model"
)

// IslandConstructionTopologyShattered is the topology-output variant of the
// Native pipeline: instead of writing conflict bits into the global
// ConflictGraph matrix, it produces the BlockTopology itself as the final
// structure — disjoint per-island local bit-matrices (topo.LocalGraphs) plus a
// sparse cross-island hub-edge list (topo.HubEdges). The union of both is
// mathematically identical to the global matrix, but the global scatter-write
// and the hub reintegration flush are gone, and island sweeps shrink from
// ceil(n/64) to ceil(k/64) words. Steady-state calls perform zero heap
// allocations. Downstream, each local graph can be colored independently.
func IslandConstructionTopologyShattered(block *model.Block, topo *model.BlockTopology) (int, int, float64) {
	n := uint32(len(block.Transactions))
	numObjects := block.NumUniqueObjects
	rowWidth := (n + model.SizeOfUint64 - 1) / model.SizeOfUint64

	topo.PrepareForBlock(n, numObjects, rowWidth)

	// 1-4. Identical front half to NativeShattered: fused counting + hub
	// detection, hub-toucher collection, anchor-driven partition, grouping.
	countAccessesAndDetectHubs(block, topo)
	if len(topo.HubObjs) > 0 {
		collectHubTouchers(block, topo, n)
	}
	topo.InitUnionFind(n)
	partitionViaAnchors(block, topo)
	topo.GroupIslands(n)

	// 5. Carve the per-island local matrices (the actual output surface).
	topo.PrepareLocalGraphs()

	// 6. Lazy index: non-hub rows of sweep-sized islands are built in LOCAL
	// coordinates; hub rows keep GLOBAL coordinates for the cross-island pass.
	buildLazyIndexLocal(block, topo, rowWidth)

	// 7. Per-island construction straight into each island's local matrix.
	sequential := n <= SequentialBlockMax
	if sequential {
		vec := topo.WorkerVecs[0]
		for islandIdx := range topo.Islands {
			dispatchIslandLocal(islandIdx, block, topo, vec, rowWidth)
		}
	} else {
		topo.ResetSlots()
		concurrency.ParallelRun(len(topo.Islands), func(jobQueue <-chan int) {
			vec := topo.WorkerVecs[topo.NextSlot()]
			for islandIdx := range jobQueue {
				dispatchIslandLocal(islandIdx, block, topo, vec, rowWidth)
			}
		})
	}

	// 8. Hub conflicts become sparse edges only — no matrix flush anywhere.
	if len(topo.HubTouchers) > 0 {
		if sequential {
			vec := topo.WorkerVecs[0]
			for _, txnIdx := range topo.HubTouchers {
				extractHubEdges(txnIdx, block, topo, vec, rowWidth, 0)
			}
		} else {
			topo.ResetSlots()
			concurrency.ParallelRun(len(topo.HubTouchers), func(jobQueue <-chan int) {
				slot := topo.NextSlot()
				vec := topo.WorkerVecs[slot]
				for jobIdx := range jobQueue {
					extractHubEdges(topo.HubTouchers[jobIdx], block, topo, vec, rowWidth, slot)
				}
			})
		}
	}

	// 9. Restore the all-zero index invariant, mirroring the build exactly.
	wipeLazyIndexLocal(block, topo, rowWidth)

	return CalculateIslandStats(topo.Islands)
}

// buildLazyIndexLocal populates object rows only where a sweep will read them.
// Non-hub rows of sweep-sized islands are written in local coordinates (bit p
// = island[p]); this is safe because island closure guarantees every accessor
// of a written non-hub object lives in the same island, and read-only object
// rows — the one case where local bits from different islands may collide —
// are never swept (a sweep reads readers only for objects the txn writes).
// Hub rows are written in global coordinates because touchers span islands.
func buildLazyIndexLocal(block *model.Block, topo *model.BlockTopology, rowWidth uint32) {
	for _, island := range topo.Islands {
		if len(island) <= PairwiseIslandMax {
			continue
		}
		for localIdx, txnIdx := range island {
			word := uint32(localIdx) / model.SizeOfUint64
			mask := model.MaskActiveBit << (uint32(localIdx) % model.SizeOfUint64)
			txn := &block.Transactions[txnIdx]
			for _, objID := range txn.ReadSet {
				if !topo.IsHub[objID] {
					topo.ReadBits[objID*rowWidth+word] |= mask
				}
			}
			for _, objID := range txn.WriteSet {
				if !topo.IsHub[objID] {
					topo.WriteBits[objID*rowWidth+word] |= mask
				}
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

// wipeLazyIndexLocal re-zeroes exactly the words buildLazyIndexLocal touched:
// local-coordinate words for island rows, global-coordinate words for hub rows.
func wipeLazyIndexLocal(block *model.Block, topo *model.BlockTopology, rowWidth uint32) {
	for _, island := range topo.Islands {
		if len(island) <= PairwiseIslandMax {
			continue
		}
		for localIdx, txnIdx := range island {
			word := uint32(localIdx) / model.SizeOfUint64
			txn := &block.Transactions[txnIdx]
			for _, objID := range txn.ReadSet {
				if !topo.IsHub[objID] {
					topo.ReadBits[objID*rowWidth+word] = 0
				}
			}
			for _, objID := range txn.WriteSet {
				if !topo.IsHub[objID] {
					topo.WriteBits[objID*rowWidth+word] = 0
				}
			}
		}
	}
	for _, txnIdx := range topo.HubTouchers {
		word := txnIdx / model.SizeOfUint64
		txn := &block.Transactions[txnIdx]
		for _, objID := range txn.ReadSet {
			if topo.IsHub[objID] {
				topo.ReadBits[objID*rowWidth+word] = 0
			}
		}
		for _, objID := range txn.WriteSet {
			if topo.IsHub[objID] {
				topo.WriteBits[objID*rowWidth+word] = 0
			}
		}
	}
	for _, objID := range topo.HubObjs {
		topo.IsHub[objID] = false
	}
}

// dispatchIslandLocal routes one island to its cheapest construction strategy,
// writing results into the island's own local matrix.
func dispatchIslandLocal(islandIdx int, block *model.Block, topo *model.BlockTopology, vec []uint64, rowWidth uint32) {
	island := topo.Islands[islandIdx]
	local := topo.LocalGraphs[islandIdx]
	if len(island) == 2 {
		// A two-txn union implies one direct W-W/W-R edge: rows are 1 word wide.
		local[0] = model.MaskActiveBit << 1
		local[1] = model.MaskActiveBit
		return
	}
	width := topo.LocalWidths[islandIdx]
	if len(island) <= PairwiseIslandMax {
		processIslandPairwiseLocal(island, block, local, width)
		return
	}
	processIslandSweepLocal(island, block, topo, local, width, vec, rowWidth)
}

// processIslandPairwiseLocal runs the direct pairwise scan of a tiny island,
// recording each conflict symmetrically in the local matrix.
func processIslandPairwiseLocal(island []uint32, block *model.Block, local []uint64, width uint32) {
	for i := 0; i < len(island); i++ {
		for j := i + 1; j < len(island); j++ {
			if block.Transactions[island[i]].HasConflict(&block.Transactions[island[j]]) {
				local[uint32(i)*width+uint32(j)/model.SizeOfUint64] |= model.MaskActiveBit << (uint32(j) % model.SizeOfUint64)
				local[uint32(j)*width+uint32(i)/model.SizeOfUint64] |= model.MaskActiveBit << (uint32(i) % model.SizeOfUint64)
			}
		}
	}
}

// processIslandSweepLocal sweeps each member's non-hub object rows — already in
// local coordinates — into a conflict vector of only `width` words and stores
// it as the member's local matrix row. Hub bits are extractHubEdges' job.
func processIslandSweepLocal(island []uint32, block *model.Block, topo *model.BlockTopology, local []uint64, width uint32, vec []uint64, rowWidth uint32) {
	for localIdx, txnIdx := range island {
		txn := &block.Transactions[txnIdx]

		for _, objID := range txn.WriteSet {
			if topo.IsHub[objID] {
				continue
			}
			base := objID * rowWidth
			writers := topo.WriteBits[base : base+width]
			readers := topo.ReadBits[base : base+width]
			for bIdx := uint32(0); bIdx < width; bIdx++ {
				vec[bIdx] |= writers[bIdx] | readers[bIdx]
			}
		}
		for _, objID := range txn.ReadSet {
			if topo.IsHub[objID] {
				continue
			}
			base := objID * rowWidth
			writers := topo.WriteBits[base : base+width]
			for bIdx := uint32(0); bIdx < width; bIdx++ {
				vec[bIdx] |= writers[bIdx]
			}
		}

		// Clear the self bit at the LOCAL position, store the row, fast-clear.
		lIdx := uint32(localIdx)
		vec[lIdx/model.SizeOfUint64] &= ^(model.MaskActiveBit << (lIdx % model.SizeOfUint64))
		rowBase := lIdx * width
		for bIdx := uint32(0); bIdx < width; bIdx++ {
			local[rowBase+bIdx] = vec[bIdx]
			vec[bIdx] = 0
		}
	}
}

// extractHubEdges sweeps only the hub object rows of one hub toucher (global
// coordinates) and emits the upper-triangle sparse edges into the worker's
// private hub-edge list. Unlike native's reintegration there is no matrix
// flush — the edge list itself is the output.
func extractHubEdges(txnIdx uint32, block *model.Block, topo *model.BlockTopology, vec []uint64, rowWidth uint32, slot uint32) {
	txn := &block.Transactions[txnIdx]

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

	// Each pair is recorded once, by its lower-indexed toucher; fast-clear as we go.
	for bIdx := uint32(0); bIdx < rowWidth; bIdx++ {
		activeBits := vec[bIdx]
		basePeerIdx := bIdx * model.SizeOfUint64
		for activeBits != 0 {
			peerIdx := basePeerIdx + uint32(bits.TrailingZeros64(activeBits))
			if txnIdx < peerIdx {
				topo.HubEdges[slot] = append(topo.HubEdges[slot], model.Edge{TxnA: txnIdx, TxnB: peerIdx})
			}
			activeBits &= activeBits - 1
		}
		vec[bIdx] = 0
	}
}

// ReconstructTopologyGraph materializes the topology output (local island
// matrices + sparse hub edges) into a global ConflictGraph. It exists purely so
// the benchmark harness can verify bit-for-bit parity against Bitmap; it is
// never part of the timed path.
func ReconstructTopologyGraph(topo *model.BlockTopology, graph *model.ConflictGraph) {
	for islandIdx, island := range topo.Islands {
		local := topo.LocalGraphs[islandIdx]
		width := topo.LocalWidths[islandIdx]
		for localIdx, txnIdx := range island {
			rowBase := uint32(localIdx) * width
			for bIdx := uint32(0); bIdx < width; bIdx++ {
				activeBits := local[rowBase+bIdx]
				basePeer := bIdx * model.SizeOfUint64
				for activeBits != 0 {
					peer := basePeer + uint32(bits.TrailingZeros64(activeBits))
					// Local matrices are symmetric, so every direction is visited.
					graph.AddEdge(txnIdx, island[peer])
					activeBits &= activeBits - 1
				}
			}
		}
	}
	for _, workerEdges := range topo.HubEdges {
		for _, edge := range workerEdges {
			graph.AddEdge(edge.TxnA, edge.TxnB)
			graph.AddEdge(edge.TxnB, edge.TxnA)
		}
	}
}
