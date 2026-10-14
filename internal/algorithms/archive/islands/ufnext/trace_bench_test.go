//go:build ignore

package ufnext_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"conflict-graph-benchmarks/internal/algorithms/competitors"
	"conflict-graph-benchmarks/internal/algorithms/islands"
	"conflict-graph-benchmarks/internal/digestion"
	"conflict-graph-benchmarks/internal/model"
)

// The trace benchmarks profile the island variants on real blocks, e.g.
//
//	go test ./internal/algorithms/islands/ufnext -run=^$ -bench=Trace/Solana/Hub -cpuprofile=cpu.out
//	go tool pprof -top cpu.out
//
// They are skipped when the trace files are missing. The paths in digestion
// are relative to the module root, so the loader resolves them from there.

// traceFiles lists the benchmarked traces and the blocks loaded from each.
// The synthetic traces are split at 10% density, where the island family
// loses to HybridJoin.
var traceFiles = []struct {
	name    string
	path    string
	blocks  int
	density func(float64) bool // keeps blocks of this density; nil keeps all
}{
	{"Ethereum", digestion.EthCleanFilePath, 300, nil},
	{"EthereumNoHub", digestion.EthNoHubsCleanFilePath, 300, nil},
	{"Solana", digestion.SolanaCleanFilePath, 100, nil},
	{"Sui", digestion.SuiCleanFilePath, 300, nil},
	{"YCSBsparse", digestion.YCSBFilePath, 300, func(d float64) bool { return d < 0.1 }},
	{"YCSBdense", digestion.YCSBFilePath, 300, func(d float64) bool { return d >= 0.1 }},
	{"TPCsparse", digestion.TPCFilePath, 300, func(d float64) bool { return d < 0.1 }},
	{"TPCdense", digestion.TPCFilePath, 300, func(d float64) bool { return d >= 0.1 }},
}

// moduleRoot is the module directory, three levels above this package.
func moduleRoot() string {
	return filepath.Join("..", "..", "..", "..")
}

// loadTrace returns up to limit blocks of a cleaned trace file whose density
// keep accepts (nil: all), each with its own transaction and access memory.
func loadTrace(path string, limit int, keep func(float64) bool) ([]*model.Block, error) {
	file, err := os.Open(filepath.Join(moduleRoot(), path))
	if err != nil {
		return nil, fmt.Errorf("opening trace: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("reading trace header: %w", err)
	}
	var blocks []*model.Block
	for decoder.More() && len(blocks) < limit {
		var clean digestion.CleanedBlock
		if err := decoder.Decode(&clean); err != nil {
			return nil, fmt.Errorf("decoding block: %w", err)
		}
		if len(clean.Transactions) > int(model.MaximumGraphSize) {
			continue
		}
		block := &model.Block{}
		model.NewManager().Populate(block, clean)
		if keep == nil || keep(density(block)) {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

// density returns the block's edge density.
func density(block *model.Block) float64 {
	n := uint32(len(block.Transactions))
	if n < 2 {
		return 0
	}
	g := model.NewConflictGraph()
	competitors.BitmapConstruction(block, g, model.NewInvertedIndex())
	return float64(g.CountEdges(n)) / float64(n*(n-1)/2)
}

// runner is one benchmarked algorithm, built fresh per trace.
type runner struct {
	name string
	make func() func(*model.Block)
}

// runners are the profiled algorithms: the two island champions, the
// competitors to beat, and every ufnext variant.
func runners() []runner {
	rs := []runner{
		{"Hub", func() func(*model.Block) {
			h := islands.NewIslandLeanHub(islands.DefaultIslandConfig, islands.DefaultHubRule())
			return func(b *model.Block) { h.Run(b) }
		}},
		{"Lean", func() func(*model.Block) {
			l := islands.NewIslandLean(islands.DefaultIslandConfig)
			return func(b *model.Block) { l.Run(b) }
		}},
		{"HybridJoin", func() func(*model.Block) {
			j, g := competitors.NewHybridJoin(competitors.DefaultHybridJoinConfig), model.NewConflictGraph()
			return func(b *model.Block) { j.Run(b, g) }
		}},
		{"Bitmap", func() func(*model.Block) {
			g, idx := model.NewConflictGraph(), model.NewInvertedIndex()
			return func(b *model.Block) { competitors.BitmapConstruction(b, g, idx) }
		}},
	}
	return append(rs, variantRunners()...)
}

// BenchmarkTrace times every runner over the loaded blocks of every trace;
// one iteration runs the whole block list once.
func BenchmarkTrace(b *testing.B) {
	for _, tf := range traceFiles {
		blocks, err := loadTrace(tf.path, tf.blocks, tf.density)
		if err != nil {
			b.Logf("skipping %s: %v", tf.name, err)
			continue
		}
		runtime.GC()
		for _, r := range runners() {
			b.Run(tf.name+"/"+r.name, func(b *testing.B) {
				run := r.make()
				for _, block := range blocks {
					run(block)
				}
				b.ResetTimer()
				for b.Loop() {
					for _, block := range blocks {
						run(block)
					}
				}
			})
		}
	}
}
