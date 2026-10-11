// Package uflab holds the experimental parts of the Union-Find proving ground
// (`go run . -suite=uf`, registry in ufarena.go): hub rules that compete with
// islands.PrefixHubs inside islands.IslandLeanHub. New island heuristics are
// tried here first; a winner moves to package islands.
package uflab

import "conflict-graph-benchmarks/internal/model"

// Count is what a CountedHubs rule compares with its threshold.
type Count int

const (
	// Accesses: reads + writes > Threshold.
	Accesses Count = iota
	// Weighted: WriteWeight*writes + reads > Threshold.
	Weighted
	// Split: writes > Threshold or reads > ReadThreshold.
	Split
	// Relative: accesses > max(Threshold, Fraction*n).
	Relative
	// Writers: writes > Threshold (the pre-pass reads only the writes).
	Writers
	// Sampled: accesses of every SampleEvery-th transaction > Threshold/SampleEvery.
	Sampled
)

// CountedHubs is a hub rule over per-object counts from a pre-pass over the
// block (every transaction, or every SampleEvery-th). Only written objects
// can be hubs. Phase 4 found every such rule slower than the prefix rule
// except on raw Ethereum, because the pre-pass costs a pass over the
// accesses; the aggressive ones split the islands most (PHASE4_RESULTS.md).
type CountedHubs struct {
	Count         Count
	Threshold     uint32
	ReadThreshold uint32  // Split
	WriteWeight   uint32  // Weighted
	Fraction      float64 // Relative
	SampleEvery   int     // Sampled
	counts        []uint32
}

// Hubs counts the accesses and appends every written object the rule selects.
// Counts pack writes in the high 16 bits and accesses in the low 16.
func (r *CountedHubs) Hubs(block *model.Block, hubs []uint32) []uint32 {
	r.countAccesses(block)
	threshold := r.Threshold
	switch r.Count {
	case Relative:
		threshold = max(threshold, uint32(r.Fraction*float64(len(block.Transactions))))
	case Sampled:
		threshold /= uint32(r.step())
	}
	for obj, c := range r.counts {
		writes, accesses := c>>16, c&0xFFFF
		if writes > 0 && r.selects(writes, accesses, threshold) {
			hubs = append(hubs, uint32(obj))
		}
	}
	return hubs
}

// countAccesses fills r.counts for the block.
func (r *CountedHubs) countAccesses(block *model.Block) {
	txns := block.Transactions
	r.counts = model.Resize(r.counts, int(block.NumUniqueObjects))
	counts, step, writesOnly := r.counts, r.step(), r.Count == Writers
	clear(counts)
	for t := 0; t < len(txns); t += step {
		for _, obj := range txns[t].WriteSet {
			counts[obj] += 1 + 1<<16
		}
		if writesOnly {
			continue
		}
		for _, obj := range txns[t].ReadSet {
			counts[obj]++
		}
	}
}

// step is the transaction stride of the pre-pass.
func (r *CountedHubs) step() int {
	if r.Count == Sampled {
		return max(1, r.SampleEvery)
	}
	return 1
}

// selects applies the rule to one written object's counts.
func (r *CountedHubs) selects(writes, accesses, threshold uint32) bool {
	switch r.Count {
	case Weighted:
		return r.WriteWeight*writes+accesses-writes > threshold
	case Split:
		return writes > threshold || accesses-writes > r.ReadThreshold
	case Writers:
		return writes > threshold
	default:
		return accesses > threshold
	}
}
