package main

import (
	"fmt"

	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/algorithms/islands/uflab"
	"conflict-graph-benchmarks/internal/model"
)

// newUFArena is the registry of the uf suite, the proving ground of the island
// family: IslandLean with its knobs moved one at a time, then IslandLeanHub
// with every hub rule and its own mini sweep. Every configuration is timed on
// every block of the same run, so `go run . -suite=uf` re-runs the whole sweep
// in one command; compare with `show_results.py ... --baseline Lean --league`.
func newUFArena() []algorithm {
	var algs []algorithm
	add := func(name string, v islandVariant) {
		algs = append(algs, islandAlgorithm(name, v))
	}

	// IslandLean and its knobs, one at a time.
	base := islands.DefaultIslandConfig
	add("Lean", islands.NewIslandLean(base))
	knob := func(name string, change func(*islands.IslandConfig)) {
		cfg := base
		change(&cfg)
		add(name, islands.NewIslandLean(cfg))
	}
	for _, pairwise := range []int{2, 8, 16} {
		knob(fmt.Sprintf("Lean-pw%d", pairwise), func(c *islands.IslandConfig) { c.PairwiseMax = pairwise })
	}
	knob("Lean-sequential", func(c *islands.IslandConfig) { c.SequentialMax = model.MaximumGraphSize })
	knob("Lean-alwaysParallel", func(c *islands.IslandConfig) { c.SequentialMax, c.ParallelWords = 0, 0 })
	for _, words := range []int{4000, 16000} {
		knob(fmt.Sprintf("Lean-words%dk", words/1000), func(c *islands.IslandConfig) { c.ParallelWords = words })
	}
	// HybridJoin's object-major kernel for wide islands (Phase 3 fairness
	// transfer): neutral on the laptop, kept for the many-core re-tune.
	knob("Lean-join1", func(c *islands.IslandConfig) { c.JoinDense = 1 })
	knob("Lean-join1-w8", func(c *islands.IslandConfig) { c.JoinDense, c.JoinMinWidth = 1, 8 })

	// IslandLeanHub with every hub rule, each with its own mini sweep.
	hub := func(name string, rule islands.HubRule) {
		add(name, islands.NewIslandLeanHub(base, rule))
	}
	for _, p := range [][2]int{{16, 8}, {32, 4}, {64, 4}, {64, 8}, {128, 8}} {
		hub(fmt.Sprintf("Hub-prefix%d-%d", p[0], p[1]), &islands.PrefixHubs{Txns: p[0], MinWrites: p[1]})
	}
	joinCfg := base
	joinCfg.JoinDense = 1
	add("Hub-join1", islands.NewIslandLeanHub(joinCfg, islands.DefaultHubRule()))
	for _, t := range []uint32{16, 32, 64} {
		hub(fmt.Sprintf("Hub-accesses%d", t), &uflab.CountedHubs{Count: uflab.Accesses, Threshold: t})
	}
	for _, t := range []uint32{4, 8, 16} {
		hub(fmt.Sprintf("Hub-writers%d", t), &uflab.CountedHubs{Count: uflab.Writers, Threshold: t})
	}
	for _, t := range []uint32{32, 64, 128} {
		hub(fmt.Sprintf("Hub-weighted%d", t), &uflab.CountedHubs{Count: uflab.Weighted, Threshold: t, WriteWeight: 4})
	}
	for _, t := range []uint32{8, 16} {
		hub(fmt.Sprintf("Hub-split%d-%d", t, 8*t), &uflab.CountedHubs{Count: uflab.Split, Threshold: t, ReadThreshold: 8 * t})
	}
	for _, f := range []float64{0.10, 0.25} {
		hub(fmt.Sprintf("Hub-relative%.0f", 100*f), &uflab.CountedHubs{Count: uflab.Relative, Threshold: 8, Fraction: f})
	}
	for _, t := range []uint32{16, 32, 64} {
		hub(fmt.Sprintf("Hub-sampled%d", t), &uflab.CountedHubs{Count: uflab.Sampled, Threshold: t, SampleEvery: 4})
	}
	return algs
}
