package main

import (
	"fmt"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/algorithms/islands"
)

// newUFArena is the registry of the uf suite, the proving ground of the island
// family: the two competitors as the bars to clear, then IslandHub and
// IslandBipartite with their knobs moved one at a time over a wide range. Every
// configuration is timed on every block of the same run; select a sweep with
// -algs and compare with `show_results.py ... --baseline HybridJoinTiny
// --league`, or, over builds with different code layouts, `layout_league.py`.
func newUFArena() []algorithm {
	algs := []algorithm{
		matrixAlgorithm("BitmapTiny", competitors.NewBitmapTiny().Run),
		matrixAlgorithm("HybridJoinTiny", competitors.NewHybridJoinTiny(competitors.DefaultHybridJoinConfig).Run),
	}
	families := []struct {
		name string
		cfg  islands.HubConfig
		make func(islands.HubConfig) islandVariant
	}{
		{"Hub", islands.DefaultIslandHubConfig, func(c islands.HubConfig) islandVariant { return islands.NewIslandHub(c) }},
		{"Bip", islands.DefaultIslandBipartiteConfig, func(c islands.HubConfig) islandVariant { return islands.NewIslandBipartite(c) }},
	}
	for _, f := range families {
		knob := func(name string, change func(*islands.HubConfig)) {
			cfg := f.cfg
			change(&cfg)
			algs = append(algs, islandAlgorithm(f.name+name, f.make(cfg)))
		}
		ints := func(name string, values []int, set func(*islands.HubConfig, int)) {
			for _, v := range values {
				knob(fmt.Sprintf("-%s%d", name, v), func(c *islands.HubConfig) { set(c, v) })
			}
		}
		floats := func(name string, values []float64, set func(*islands.HubConfig, float64)) {
			for _, v := range values {
				knob(fmt.Sprintf("-%s%g", name, v), func(c *islands.HubConfig) { set(c, v) })
			}
		}
		knob("", func(*islands.HubConfig) {})

		// The hub rule.
		ints("prefix", []int{32, 48, 96, 128, 192, 256}, func(c *islands.HubConfig, v int) { c.PrefixTxns = v })
		ints("minWrites", []int{2, 4, 5, 6, 8}, func(c *islands.HubConfig, v int) { c.PrefixMinWrites = v })
		for _, p := range [][2]int{{32, 2}, {128, 6}, {256, 12}} {
			knob(fmt.Sprintf("-rule%d-%d", p[0], p[1]), func(c *islands.HubConfig) { c.PrefixTxns, c.PrefixMinWrites = p[0], p[1] })
		}
		knob("-strided", func(c *islands.HubConfig) { c.PrefixStrided = true })
		ints("hubMin", []int{32, 96, 128, 192, 256}, func(c *islands.HubConfig, v int) { c.HubMinTxns = v })
		// The dense fallback.
		floats("share", []float64{0, 0.3, 0.4, 0.6, 0.7, 0.8, 0.9}, func(c *islands.HubConfig, v float64) { c.DenseHubShare = v })
		ints("denseMin", []int{256, 384, 768, 1024, 1536}, func(c *islands.HubConfig, v int) { c.DenseMinTxns = v })
		// The tiny path.
		ints("tinyMax", []int{0, 32, 48}, func(c *islands.HubConfig, v int) { c.TinyMax = v })
		floats("repeats", []float64{0, 0.25, 0.75, 1, 1.5}, func(c *islands.HubConfig, v float64) { c.TinyMinRepeats = v })
		// The island core.
		floats("giant", []float64{0, 0.15, 0.2, 0.4, 0.5, 0.7}, func(c *islands.HubConfig, v float64) { c.Island.GiantShare = v })
		floats("joinDense", []float64{0.5, 2}, func(c *islands.HubConfig, v float64) { c.Island.JoinDense = v })
		ints("pw", []int{2, 3, 6, 8}, func(c *islands.HubConfig, v int) { c.Island.PairwiseMax = v })
		ints("seqMax", []int{150, 200, 450, 600, 1000}, func(c *islands.HubConfig, v int) { c.Island.SequentialMax = uint32(v) })
		ints("words", []int{4000, 16000, 32000}, func(c *islands.HubConfig, v int) { c.Island.ParallelWords = v })
		ints("split", []int{8, 32, 64}, func(c *islands.HubConfig, v int) { c.Island.SplitRows = uint32(v) })
		ints("group", []int{128, 512, 1024}, func(c *islands.HubConfig, v int) { c.Island.GroupTxns = v })
		ints("hubRows", []int{32, 128}, func(c *islands.HubConfig, v int) { c.HubRowsPerUnit = v })
	}
	return algs
}
