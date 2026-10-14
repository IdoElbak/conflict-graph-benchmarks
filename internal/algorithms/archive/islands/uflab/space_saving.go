//go:build ignore

package uflab

import "conflict-graph-benchmarks/internal/model"

// SpaceSavingHubs is report B's streaming hub detector: the Space-Saving
// heavy-hitter sketch (Metwally et al.) over the writes of every Every-th
// transaction. K counters track K objects; a write of an untracked object
// evicts the minimum counter (the lowest index on ties, so the result is
// deterministic) and takes its count plus one. Objects whose (over)estimated
// count reaches Threshold become hubs. Unlike a count array it is bounded in
// space, but each write scans the K counters, where a count array (CountedHubs
// with Writers) costs one increment: object IDs here are dense already.
type SpaceSavingHubs struct {
	K         int
	Threshold uint32
	Every     int
	objs      []uint32
	counts    []uint32
}

// untracked marks an empty sketch slot.
const untracked = ^uint32(0)

// Hubs runs the sketch over the block and appends every tracked object whose
// count reaches the threshold.
func (r *SpaceSavingHubs) Hubs(block *model.Block, hubs []uint32) []uint32 {
	r.objs = model.Resize(r.objs, r.K)
	r.counts = model.Resize(r.counts, r.K)
	objs, counts := r.objs, r.counts
	for i := range objs {
		objs[i], counts[i] = untracked, 0
	}
	txns := block.Transactions
	for t := 0; t < len(txns); t += max(1, r.Every) {
		for _, obj := range txns[t].WriteSet {
			observe(objs, counts, obj)
		}
	}
	for i, obj := range objs {
		if obj != untracked && counts[i] >= r.Threshold {
			hubs = append(hubs, obj)
		}
	}
	return hubs
}

// observe counts one occurrence of obj in the sketch.
func observe(objs, counts []uint32, obj uint32) {
	low := 0
	for i, o := range objs {
		if o == obj {
			counts[i]++
			return
		}
		if counts[i] < counts[low] {
			low = i
		}
	}
	objs[low] = obj
	counts[low]++
}
