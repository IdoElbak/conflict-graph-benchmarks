package main

import (
	"fmt"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/model"
)

// newCompetitorArena is the registry of the cmp suite, the proving ground of
// the Phase 3 competitors: Bitmap as the reference, then each competitor with
// its knobs moved one at a time. Every configuration is timed on every block
// of the same run, so `go run . -suite=cmp` re-tunes them in one command;
// compare with `show_results.py ... --baseline Bitmap --league`.
func newCompetitorArena() []algorithm {
	index := model.NewInvertedIndex()
	algs := []algorithm{
		matrixAlgorithm("Bitmap", func(b *model.Block, g *model.ConflictGraph) {
			competitors.BitmapConstruction(b, g, index)
		}),
	}

	hybridJoin := func(name string, change func(*competitors.HybridJoinConfig)) {
		cfg := competitors.DefaultHybridJoinConfig
		change(&cfg)
		algs = append(algs, matrixAlgorithm(name, competitors.NewHybridJoin(cfg).Run))
	}
	hybridJoin("HybridJoin", func(*competitors.HybridJoinConfig) {})
	for _, f := range []float64{0.25, 0.5, 2, 4} {
		hybridJoin(fmt.Sprintf("HybridJoin-dense%g", f), func(c *competitors.HybridJoinConfig) { c.DenseFactor = f })
	}
	for _, w := range []uint32{1, 2, 8} {
		hybridJoin(fmt.Sprintf("HybridJoin-minWords%d", w), func(c *competitors.HybridJoinConfig) { c.MinWords = w })
	}

	join := func(name string, change func(*competitors.SortJoinConfig)) {
		cfg := competitors.DefaultSortJoinConfig
		change(&cfg)
		algs = append(algs, matrixAlgorithm(name, competitors.NewSortJoin(cfg).Run))
	}
	join("SortJoin", func(*competitors.SortJoinConfig) {})
	return algs
}
