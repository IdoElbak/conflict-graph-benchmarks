package main

import (
	"fmt"
	"os"

	"conflict-graph-benchmarks/internal/digestion"
	"conflict-graph-benchmarks/internal/model"
)

func main() {
	fmt.Println("=== Conflict Graph Benchmark Engine ===")

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

	// Define specific file paths
	ethFile := "internal/digestion/data/Eth_cleaned_blocks_21297000_21297999.json"
	solFile := "internal/digestion/data/Solana_cleaned_blocks_391000000_391000998.json"

	processFile(ethFile, "Ethereum", manager, &allMetrics, 1000)
	processFile(solFile, "Solana", manager, &allMetrics, 100)

	// Phase 4: Analysis Export
	fmt.Println("\n[Phase 4] Exporting Analysis Metrics...")
	saveMetrics(allMetrics, "benchmark_results.json")
}

func runDigestionPhase() {
	pipeline := []func() error{
		digestion.ParseEthereumStream,
		digestion.ParseSolanaStream,
	}
	runParsers(pipeline)
}
