package main

import (
	"fmt"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
)

// newCompetitorArena is the registry of the cmp suite, the proving ground of
// the competitors: BitmapTiny as the reference, then HybridJoinTiny with
// HybridJoin's knobs moved one at a time. Every configuration is timed on
// every block of the same run, so `go run . -suite=cmp` re-tunes them in one
// command; compare with `show_results.py ... --baseline BitmapTiny --league`.
func newCompetitorArena() []algorithm {
	algs := []algorithm{
		matrixAlgorithm("BitmapTiny", competitors.NewBitmapTiny().Run),
	}
	hybridJoin := func(name string, change func(*competitors.HybridJoinConfig)) {
		cfg := competitors.DefaultHybridJoinConfig
		change(&cfg)
		algs = append(algs, matrixAlgorithm(name, competitors.NewHybridJoinTiny(cfg).Run))
	}
	hybridJoin("HybridJoinTiny", func(*competitors.HybridJoinConfig) {})
	for _, f := range []float64{0.25, 0.5, 2, 4} {
		hybridJoin(fmt.Sprintf("HybridJoinTiny-dense%g", f), func(c *competitors.HybridJoinConfig) { c.DenseFactor = f })
	}
	for _, w := range []uint32{1, 2, 8} {
		hybridJoin(fmt.Sprintf("HybridJoinTiny-minWords%d", w), func(c *competitors.HybridJoinConfig) { c.MinWords = w })
	}
	return algs
}
