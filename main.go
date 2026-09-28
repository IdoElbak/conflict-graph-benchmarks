package main

import (
	"fmt"
	"os"

	"conflict-graph-benchmarks/internal/digestion"
	"conflict-graph-benchmarks/internal/model"
)

// traces lists every benchmarked trace in presentation order; the name is the
// "blockchain" label in benchmark_results.json.
var traces = []struct {
	name      string
	path      string
	maxBlocks int
}{
	{"Ethereum", digestion.EthCleanFilePath, 1000},
	{"EthereumNoHub", digestion.EthNoHubsCleanFilePath, 1000},
	{"Solana", digestion.SolanaCleanFilePath, 100},
	{"Sui", digestion.SuiCleanFilePath, 2000},
	{"YCSB", digestion.YCSBFilePath, digestion.SyntheticBlockCount},
	{"TPC", digestion.TPCFilePath, digestion.SyntheticBlockCount},
}

func main() {
	fmt.Println("=== Conflict Graph Benchmark Engine ===")
	fmt.Println("[Platform]", prepareProcess())

	// Skip if results already exist
	if _, err := os.Stat("benchmark_results.json"); err == nil {
		fmt.Println("\n[Skipped] benchmark_results.json already exists. Remove it to run a new benchmark.")
		return
	}

	// Phase 1: Data Digestion
	fmt.Println("\n[Phase 1] Initializing Data Digestion Pipeline...")
	runDigestionPhase()

	// Phase 2 & 3: Stream, Translate, and Execute
	fmt.Println("\n[Phase 2 & 3] Streaming Blocks and Executing Algorithms...")
	manager := model.NewManager()
	var allMetrics []BlockMetrics
	for _, trace := range traces {
		processFile(trace.path, trace.name, manager, &allMetrics, trace.maxBlocks)
	}

	// Phase 4: Analysis Export
	fmt.Println("\n[Phase 4] Exporting Analysis Metrics...")
	saveMetrics(allMetrics, "benchmark_results.json")
}

// runDigestionPhase produces every derived trace that does not exist yet. The
// no-hub Ethereum filter must run after the Ethereum parser it reads from.
func runDigestionPhase() {
	pipeline := []func() error{
		digestion.ParseEthereumStream,
		digestion.GenerateEthereumNoHubTrace,
		digestion.ParseSolanaStream,
		digestion.ParseSuiStream,
		digestion.GenerateYCSBTrace,
		digestion.GenerateTPCTrace,
	}
	runParsers(pipeline)
}
