//go:build ignore

package ufnext_test

import (
	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/algorithms/islands/ufnext"
	"conflict-graph-benchmarks/internal/model"
)

// variantRunners lists the ufnext variants (and their competitor fairness
// counterparts) profiled by BenchmarkTrace.
func variantRunners() []runner {
	adaptive := func(name string, cfg ufnext.AdaptiveConfig) runner {
		return runner{name, func() func(*model.Block) {
			a := ufnext.NewAdaptive(cfg, islands.NewIslandLeanHub(islands.DefaultIslandConfig, islands.DefaultHubRule()))
			return func(b *model.Block) { a.Run(b) }
		}}
	}
	matrix := func(name string, run func(*model.Block, *model.ConflictGraph)) runner {
		return runner{name, func() func(*model.Block) {
			g := model.NewConflictGraph()
			return func(b *model.Block) { run(b, g) }
		}}
	}
	next := func(name string, change func(*ufnext.NextConfig)) runner {
		return runner{name, func() func(*model.Block) {
			cfg := ufnext.DefaultNextConfig
			change(&cfg)
			n := ufnext.NewNext(cfg)
			return func(b *model.Block) { n.Run(b) }
		}}
	}
	return []runner{
		next("Next", func(*ufnext.NextConfig) {}),
		next("NextBip", func(c *ufnext.NextConfig) { c.Bipartite = true }),
		next("NextM", NextMConfig),
		next("NextB", NextBConfig),
		next("NextMbat", func(c *ufnext.NextConfig) { NextMConfig(c); c.Batched = true }),
		next("NextBbat", func(c *ufnext.NextConfig) { NextBConfig(c); c.Batched = true }),
		adaptive("Adapt", ufnext.DefaultAdaptiveConfig),
		adaptive("AdaptTinyOnly", ufnext.AdaptiveConfig{TinyMax: 64}),
		matrix("BitmapTiny", competitors.NewBitmapTiny().Run),
		matrix("HybridJoinTiny", competitors.NewHybridJoinTiny(competitors.DefaultHybridJoinConfig).Run),
	}
}

// NextMConfig and NextBConfig are the finalists of the UF next round
// (ufarena.go's NextM-s0.5 and NextB-s0.5).
func NextMConfig(c *ufnext.NextConfig) {
	c.TinyMax, c.TinyMinRepeats, c.DenseHubShare, c.DenseMinTxns = 64, 0.5, 0.5, 256
}

// NextBConfig: see NextMConfig.
func NextBConfig(c *ufnext.NextConfig) {
	NextMConfig(c)
	c.Bipartite, c.PrefixMinWrites = true, 3
}
