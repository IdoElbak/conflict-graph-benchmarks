//go:build ignore

package ufnext_test

import (
	"os"
	"slices"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/model"
)

// TestWideSpanProbe measures, for islands wider than 64 members, the words a
// span-limited sweep would OR per access (the non-empty word range of the
// object's accessor positions) against the full row width, under three member
// orders: the forest's list order, ascending transaction order and BFS order.
func TestWideSpanProbe(t *testing.T) {
	if os.Getenv("UFNEXT_STATS") == "" {
		t.Skip("set UFNEXT_STATS=1")
	}
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, 300, tf.density)
		if err != nil {
			continue
		}
		lean := islands.NewIslandLean(islands.DefaultIslandConfig)
		var full, list, sorted, bfs float64
		for _, b := range blocks {
			lean.Run(b)
			for _, island := range lean.Topology().Islands {
				if len(island) <= 64 {
					continue
				}
				width := float64(model.WordsPerRow(uint32(len(island))))
				f, l := spanCost(b, island)
				full += f * width
				list += l
				s := slices.Clone(island)
				slices.Sort(s)
				_, c := spanCost(b, s)
				sorted += c
				_, c = spanCost(b, bfsOrder(b, island))
				bfs += c
			}
		}
		if full > 0 {
			t.Logf("%-14s word ORs per wide sweep vs full width: list %.2f sorted %.2f bfs %.2f", tf.name, list/full, sorted/full, bfs/full)
		}
	}
}

// spanCost returns (accesses, Σ span words) over an island's accesses to
// objects shared within it, with positions in the given member order.
func spanCost(b *model.Block, members []uint32) (float64, float64) {
	lo, hi, cnt, written := map[uint32]int{}, map[uint32]int{}, map[uint32]int{}, map[uint32]bool{}
	visit := func(obj uint32, p int) {
		if _, ok := lo[obj]; !ok {
			lo[obj], hi[obj] = p, p
		}
		lo[obj], hi[obj] = min(lo[obj], p), max(hi[obj], p)
		cnt[obj]++
	}
	for p, tx := range members {
		for _, o := range b.Transactions[tx].WriteSet {
			visit(o, p)
			written[o] = true
		}
		for _, o := range b.Transactions[tx].ReadSet {
			visit(o, p)
		}
	}
	var acc, words float64
	for obj, c := range cnt {
		if !written[obj] || c < 2 {
			continue
		}
		acc += float64(c)
		words += float64(c) * float64(hi[obj]/64-lo[obj]/64+1)
	}
	return acc, words
}

// bfsOrder numbers an island's members breadth-first through shared objects,
// starting from the member with the fewest accesses (Cuthill-McKee's start).
func bfsOrder(b *model.Block, island []uint32) []uint32 {
	byObj := map[uint32][]uint32{}
	for _, tx := range island {
		for _, o := range b.Transactions[tx].WriteSet {
			byObj[o] = append(byObj[o], tx)
		}
		for _, o := range b.Transactions[tx].ReadSet {
			byObj[o] = append(byObj[o], tx)
		}
	}
	start := island[0]
	deg := func(tx uint32) int { return len(b.Transactions[tx].WriteSet) + len(b.Transactions[tx].ReadSet) }
	for _, tx := range island {
		if deg(tx) < deg(start) {
			start = tx
		}
	}
	seen := map[uint32]bool{start: true}
	order := []uint32{start}
	for i := 0; i < len(order); i++ {
		tx := order[i]
		for _, set := range [][]uint32{b.Transactions[tx].WriteSet, b.Transactions[tx].ReadSet} {
			for _, o := range set {
				for _, peer := range byObj[o] {
					if !seen[peer] {
						seen[peer] = true
						order = append(order, peer)
					}
				}
				byObj[o] = nil
			}
		}
	}
	for _, tx := range island { // members joined only through dead objects
		if !seen[tx] {
			order = append(order, tx)
		}
	}
	return order
}
