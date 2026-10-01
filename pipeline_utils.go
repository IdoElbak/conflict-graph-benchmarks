package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"slices"
	"time"

	"conflict-graph-benchmarks/internal/algorithms"
	"conflict-graph-benchmarks/internal/digestion"
	"conflict-graph-benchmarks/internal/model"

	"github.com/schollz/progressbar/v3"
)

// BlockMetrics defines the analysis schema for the final JSON output
type BlockMetrics struct {
	Blockchain     string                `json:"blockchain"`
	BlockNumber    int64                 `json:"block_number"`
	NumTxns        int                   `json:"num_txns"`
	UniqueObjects  uint32                `json:"unique_objects"`
	TotalEdges     uint32                `json:"total_edges"`
	Density        float64               `json:"density"`
	ConflictedTxns float64               `json:"conflicted_txns_ratio"`
	Times          map[string]string     `json:"times"`
	IslandMetrics  map[string]IslandStat `json:"island_metrics"`
}

type IslandStat struct {
	Count   int     `json:"count"`
	MaxSize int     `json:"max_size"`
	AvgSize float64 `json:"avg_size"`
}

func runParsers(parsers []func() error) {
	for i, parseFunc := range parsers {
		fmt.Printf(" -> Running parser %d/%d...\n", i+1, len(parsers))

		err := parseFunc()
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				fmt.Println("    [Skipped] Cleaned file already exists.")
				continue
			}
			log.Fatalf("    [Fatal] Error during parsing: %v", err)
		}
		fmt.Println("    [Success] Parsed and cleaned data.")
	}
}

// processFile streams all blocks from a single blockchain, times every selected
// algorithm on each block, verifies parity, and collects metrics.
func processFile(filePath string, blockchain string, manager *model.Manager, allMetrics *[]BlockMetrics, maxBlocks int, algs []algorithm) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf(" -> [Warning] Could not open %s. Make sure digestion ran successfully.", filePath)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if _, err := decoder.Token(); err != nil {
		log.Fatalf("JSON structural error in %s: %v", filePath, err)
	}

	oracle := model.NewConflictGraph()
	var block model.Block

	// Variables for your trace statistics
	var totalTxns int
	var maxTxns int
	var blocksProcessed int
	var skippedBlocks int

	// Use a determinate progress bar based on maxBlocks
	bar := progressbar.Default(int64(maxBlocks), fmt.Sprintf("Processing %s Blocks", blockchain))

	for decoder.More() {
		// Enforce the block limit
		if blocksProcessed >= maxBlocks {
			break
		}

		var cleanBlock digestion.CleanedBlock
		if err := decoder.Decode(&cleanBlock); err != nil {
			log.Printf("Failed to decode a block in %s: %v", blockchain, err)
			continue
		}

		// Every structure is preallocated for model.MaximumGraphSize txns; a
		// larger block would silently spill into neighbouring rows, so skip it.
		numTxns := len(cleanBlock.Transactions)
		if numTxns > int(model.MaximumGraphSize) {
			skippedBlocks++
			continue
		}

		// Track statistics
		totalTxns += numTxns
		if numTxns > maxTxns {
			maxTxns = numTxns
		}

		manager.Populate(&block, cleanBlock)

		metrics := BlockMetrics{
			Blockchain:    blockchain,
			BlockNumber:   cleanBlock.BlockNumber,
			NumTxns:       numTxns,
			UniqueObjects: block.NumUniqueObjects,
			Times:         make(map[string]string),
		}

		runAlgorithmsAndVerify(&block, algs, oracle, &metrics)

		*allMetrics = append(*allMetrics, metrics)
		blocksProcessed++
		bar.Add(1) // Advance the bar safely
	}

	// Cleanly terminate the progress bar to prevent visual glitches
	bar.Finish()
	fmt.Println()
	if skippedBlocks > 0 {
		log.Printf("    [%s] skipped %d blocks larger than MaximumGraphSize (%d txns)", blockchain, skippedBlocks, model.MaximumGraphSize)
	}

	// Print the required statistics
	if blocksProcessed > 0 {
		avgTxns := float64(totalTxns) / float64(blocksProcessed)
		fmt.Printf("    [%s Stats] Processed: %d blocks | Avg Size: %.2f txns | Max Size: %d txns\n",
			blockchain, blocksProcessed, avgTxns, maxTxns)

		fmt.Println("    --- Island Topology Statistics ---")
		for _, algName := range []string{"IslandNativeShattered", "IslandTopologyShattered"} {
			var totalIslands, maxIslands, maxIslandSize int
			var sumAvgSizes float64
			var validBlocks int

			// Analyze only the blocks from this specific blockchain pass
			startIndex := len(*allMetrics) - blocksProcessed
			for _, m := range (*allMetrics)[startIndex:] {
				if stat, ok := m.IslandMetrics[algName]; ok && stat.Count > 0 {
					validBlocks++
					totalIslands += stat.Count
					if stat.Count > maxIslands {
						maxIslands = stat.Count
					}
					if stat.MaxSize > maxIslandSize {
						maxIslandSize = stat.MaxSize
					}
					sumAvgSizes += stat.AvgSize
				}
			}

			if validBlocks > 0 {
				fmt.Printf("    [%-21s] Avg Islands: %6.1f | Max Islands: %4d | Avg Size: %6.1f | Max Size: %4d\n",
					algName, float64(totalIslands)/float64(validBlocks), maxIslands, sumAvgSizes/float64(validBlocks), maxIslandSize)
			}
		}
		fmt.Println()
	}
}

const (
	// timingRounds x iterationsPerRound calls are timed per block and algorithm.
	timingRounds       = 5
	iterationsPerRound = 100

	// roundBudgetNanos caps one round's length: a call slower than
	// roundBudgetNanos/iterationsPerRound (2 ms) gets fewer iterations, so the
	// O(n²) reference algorithms stay measurable on 2000-txn blocks.
	roundBudgetNanos = 200_000_000
)

// timeAlgorithm returns the median per-call time over timingRounds rounds.
// One untimed warm-up call and a GC come first, so every algorithm starts
// warm and from the same heap state; the median of short rounds discards
// rounds disturbed by an interrupt or core migration.
func timeAlgorithm(run func()) time.Duration {
	start := nowNanos()
	run()
	iterations := int(min(iterationsPerRound, max(1, roundBudgetNanos/max(1, nowNanos()-start))))
	runtime.GC()
	var rounds [timingRounds]int64
	for r := range rounds {
		start := nowNanos()
		for range iterations {
			run()
		}
		rounds[r] = (nowNanos() - start) / int64(iterations)
	}
	slices.Sort(rounds[:])
	return time.Duration(rounds[timingRounds/2])
}

// runAlgorithmsAndVerify times every algorithm on the block and checks its
// output bit-for-bit against NaiveSequential, the definitional pairwise
// reference, which runs untimed once per block.
func runAlgorithmsAndVerify(block *model.Block, algs []algorithm, oracle *model.ConflictGraph, metrics *BlockMetrics) {
	blockSize := uint32(len(block.Transactions))
	metrics.IslandMetrics = make(map[string]IslandStat)

	algorithms.NaiveSequential(block, oracle)

	for _, alg := range algs {
		var count, maxSz int
		var avgSz float64
		metrics.Times[alg.name] = timeAlgorithm(func() {
			count, maxSz, avgSz = alg.run(block)
		}).String()
		if count > 0 {
			metrics.IslandMetrics[alg.name] = IslandStat{Count: count, MaxSize: maxSz, AvgSize: avgSz}
		}
		if alg.materialize != nil {
			alg.materialize(blockSize)
		}
		if !oracle.AreGraphsEqual(alg.graph, blockSize) {
			log.Fatalf("\n[FATAL ERROR] %s corrupted the graph on block %d!", alg.name, metrics.BlockNumber)
		}
	}

	edges := oracle.CountEdges(blockSize)
	maxEdges := (blockSize * (blockSize - 1)) / 2
	var density float64 = 0
	if maxEdges > 0 {
		density = float64(edges) / float64(maxEdges)
	}

	metrics.TotalEdges = edges
	metrics.Density = density
	metrics.ConflictedTxns = conflictedTxnsRatio(oracle, blockSize)
}

// conflictedTxnsRatio returns the fraction of transactions with at least one
// conflict edge, the other common "conflict ratio" definition next to density.
func conflictedTxnsRatio(graph *model.ConflictGraph, blockSize uint32) float64 {
	if blockSize == 0 {
		return 0
	}
	conflicted := 0
	for i := uint32(0); i < blockSize; i++ {
		if slices.ContainsFunc(graph.Row(i), func(word uint64) bool { return word != 0 }) {
			conflicted++
		}
	}
	return float64(conflicted) / float64(blockSize)
}

func saveMetrics(metrics []BlockMetrics, filename string) {
	file, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Failed to create metrics file: %v", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(metrics); err != nil {
		log.Fatalf("Failed to encode metrics: %v", err)
	}

	fmt.Printf(" -> Successfully wrote telemetry for %d blocks to %s\n", len(metrics), filename)
}

func printGraphVisual(graph *model.ConflictGraph, blockSize uint32, limit uint32) {
	if blockSize < limit {
		limit = blockSize
	}

	fmt.Printf("    --- Adjacency Matrix Snippet (Top %dx%d) ---\n", limit, limit)
	fmt.Print("        ")
	for i := uint32(0); i < limit; i++ {
		fmt.Printf("%2d ", i)
	}
	fmt.Println("\n       " + string(make([]byte, limit*3+2)))

	for i := uint32(0); i < limit; i++ {
		fmt.Printf("    %2d |", i)
		for j := uint32(0); j < limit; j++ {
			if graph.IsEdge(i, j) {
				fmt.Print(" X ")
			} else {
				fmt.Print(" . ")
			}
		}
		fmt.Println()
	}
	fmt.Println()
}
