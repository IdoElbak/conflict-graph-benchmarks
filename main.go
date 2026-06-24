package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"conflict-graph-benchmarks/internal/digestion"
)

// runParsers iterates through a list of digestion functions and handles their execution state
func runParsers(parsers []func() error) {
	for i, parseFunc := range parsers {
		fmt.Printf("Running parser %d/%d...\n", i+1, len(parsers))

		err := parseFunc()
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				fmt.Println(" -> Skipped: Cleaned file already exists.")
				continue
			}
			log.Fatalf(" -> Fatal error during parsing: %v", err)
		}

		fmt.Println(" -> Successfully parsed and cleaned data.")
	}
}

func main() {
	fmt.Println("=== Conflict Graph Benchmark Engine ===")
	fmt.Println("Initializing Data Digestion Pipeline...")

	// Register all blockchain-specific parsers here
	pipeline := []func() error{
		digestion.ParseEthereumStream,
		digestion.ParseSolanaStream,
	}

	runParsers(pipeline)

	fmt.Println("Digestion complete. Data is ready for the BlockManager.")
}
