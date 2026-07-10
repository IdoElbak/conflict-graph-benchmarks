package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"conflict-graph-benchmarks/internal/algorithms"
	"conflict-graph-benchmarks/internal/digestion"
	"conflict-graph-benchmarks/internal/model"

	"github.com/schollz/progressbar/v3"
)

// BlockMetrics defines the analysis schema for the final JSON output
//type BlockMetrics struct {
//	Blockchain    string            `json:"blockchain"`
//	BlockNumber   int64             `json:"block_number"`
//	NumTxns       int               `json:"num_txns"`
//	UniqueObjects uint32            `json:"unique_objects"`
//	TotalEdges    uint32            `json:"total_edges"`
//	Density       float64           `json:"density"`
//	Times         map[string]string `json:"times"`
//	Graph         []uint64          `json:"graph"`
//}

// BlockMetrics defines the analysis schema for the final JSON output
type BlockMetrics struct {
	Blockchain    string                `json:"blockchain"`
	BlockNumber   int64                 `json:"block_number"`
	NumTxns       int                   `json:"num_txns"`
	UniqueObjects uint32                `json:"unique_objects"`
	TotalEdges    uint32                `json:"total_edges"`
	Density       float64               `json:"density"`
	Times         map[string]string     `json:"times"`
	IslandMetrics map[string]IslandStat `json:"island_metrics"` // <-- This line was missing!
	Graph         []uint64              `json:"graph"`
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

// processFile streams all blocks from a single blockchain, runs the test suite, and collects metrics
func processFile(filePath string, blockchain string, manager *model.Manager, allMetrics *[]BlockMetrics, maxBlocks int) {
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

	graphs := map[string]*model.ConflictGraph{
		"Bitmap":                 model.NewConflictGraph(),
		"IslandDynamicShattered": model.NewConflictGraph(),
		"IslandObjectShattered":  model.NewConflictGraph(),
		"IslandNativeShattered":  model.NewConflictGraph(),
		//"IslandTxnShattered":    model.NewConflictGraph(),
	}

	// Single preallocated topology structure reused by IslandNativeShattered
	topology := model.NewBlockTopology()

	// Preallocate the 7 graph structures ONCE to avoid GC thrashing during the stream
	//graphs := map[string]*model.ConflictGraph{
	//	"NaiveSequential":       model.NewConflictGraph(),
	//	"NaiveParallel":         model.NewConflictGraph(),
	//	"Bitmap":                model.NewConflictGraph(),
	//	"IDBucket":              model.NewConflictGraph(),
	//	"IslandNaive":           model.NewConflictGraph(),
	//	"IslandObjectShattered": model.NewConflictGraph(),
	//	"IslandTxnShattered":    model.NewConflictGraph(),
	//}

	var block model.Block

	// Variables for your trace statistics
	var totalTxns int
	var maxTxns int
	var blocksProcessed int

	// FIX: Use a determinate progress bar based on maxBlocks
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

		// Track statistics
		numTxns := cleanBlock.NumTransactions
		totalTxns += numTxns
		if numTxns > maxTxns {
			maxTxns = numTxns
		}

		manager.Populate(&block, cleanBlock)
		blockSize := uint32(len(block.Transactions))

		for _, g := range graphs {
			g.Reset(blockSize)
		}

		metrics := BlockMetrics{
			Blockchain:    blockchain,
			BlockNumber:   cleanBlock.BlockNumber,
			NumTxns:       cleanBlock.NumTransactions,
			UniqueObjects: block.NumUniqueObjects,
			Times:         make(map[string]string),
		}

		runAlgorithmsAndVerify(&block, graphs, &metrics, topology)

		*allMetrics = append(*allMetrics, metrics)
		blocksProcessed++
		bar.Add(1) // Advance the bar safely
	}

	// FIX: Cleanly terminate the progress bar to prevent visual glitches
	bar.Finish()
	fmt.Println()

	// Print the required statistics
	//if blocksProcessed > 0 {
	//	avgTxns := float64(totalTxns) / float64(blocksProcessed)
	//	fmt.Printf("    [%s Stats] Processed: %d blocks | Avg Size: %.2f txns | Max Size: %d txns\n\n",
	//		blockchain, blocksProcessed, avgTxns, maxTxns)
	//}
	// Print the required statistics
	if blocksProcessed > 0 {
		avgTxns := float64(totalTxns) / float64(blocksProcessed)
		fmt.Printf("    [%s Stats] Processed: %d blocks | Avg Size: %.2f txns | Max Size: %d txns\n",
			blockchain, blocksProcessed, avgTxns, maxTxns)

		fmt.Println("    --- Island Topology Statistics ---")
		for _, algName := range []string{"IslandDynamicShattered", "IslandObjectShattered", "IslandNativeShattered"} {
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

func runAlgorithmsAndVerify(block *model.Block, graphs map[string]*model.ConflictGraph, metrics *BlockMetrics, topology *model.BlockTopology) {
	blockSize := uint32(len(block.Transactions))
	iterations := 500
	metrics.IslandMetrics = make(map[string]IslandStat)

	benchmarkAlg := func(name string, alg func(*model.Block, *model.ConflictGraph) (int, int, float64)) {
		graph := graphs[name]
		var count, maxSz int
		var avgSz float64

		start := time.Now()
		for i := 0; i < iterations; i++ {
			graph.Reset(blockSize)
			count, maxSz, avgSz = alg(block, graph)
		}
		avgTime := time.Since(start) / time.Duration(iterations)
		metrics.Times[name] = avgTime.String()

		if count > 0 {
			metrics.IslandMetrics[name] = IslandStat{Count: count, MaxSize: maxSz, AvgSize: avgSz}
		}
	}

	// Run only the optimized baseline and the target algorithms
	benchmarkAlg("Bitmap", algorithms.BitmapConstruction)
	benchmarkAlg("IslandDynamicShattered", algorithms.IslandConstructionDynamicShattered)
	benchmarkAlg("IslandObjectShattered", algorithms.IslandConstructionObjectShattered)
	//benchmarkAlg("IslandTxnShattered", algorithms.IslandConstructionTxnShattered)
	benchmarkAlg("IslandNativeShattered", func(b *model.Block, g *model.ConflictGraph) (int, int, float64) {
		return algorithms.IslandConstructionNativeShattered(b, g, topology)
	})

	// Use Bitmap as the deterministic source of truth
	truthGraph := graphs["Bitmap"]
	for name, testGraph := range graphs {
		if name == "Bitmap" {
			continue
		}
		if !truthGraph.AreGraphsEqual(testGraph, blockSize) {
			log.Fatalf("\n[FATAL ERROR] %s corrupted the graph on block %d!", name, metrics.BlockNumber)
		}
	}

	edges := truthGraph.CountEdges(blockSize)
	maxEdges := (blockSize * (blockSize - 1)) / 2
	var density float64 = 0
	if maxEdges > 0 {
		density = float64(edges) / float64(maxEdges)
	}

	metrics.TotalEdges = edges
	metrics.Density = density
	metrics.Graph = truthGraph.Export(blockSize)
}

//func runAlgorithmsAndVerify(block *model.Block, graphs map[string]*model.ConflictGraph, metrics *BlockMetrics) {
//	blockSize := uint32(len(block.Transactions))
//	iterations := 500
//
//	benchmarkAlg := func(name string, alg func(*model.Block, *model.ConflictGraph)) {
//		graph := graphs[name]
//		start := time.Now()
//		for i := 0; i < iterations; i++ {
//			graph.Reset(blockSize)
//			alg(block, graph)
//		}
//		avgTime := time.Since(start) / time.Duration(iterations)
//		metrics.Times[name] = avgTime.String()
//	}
//
//	benchmarkAlg("NaiveSequential", algorithms.NaiveSequential)
//	benchmarkAlg("NaiveParallel", algorithms.NaiveParallel)
//	benchmarkAlg("Bitmap", algorithms.BitmapConstruction)
//	benchmarkAlg("IDBucket", algorithms.IDBucketConstruction)
//	benchmarkAlg("IslandNaive", algorithms.IslandConstructionNaive)
//	benchmarkAlg("IslandObjectShattered", algorithms.IslandConstructionObjectShattered)
//	benchmarkAlg("IslandTxnShattered", algorithms.IslandConstructionTxnShattered)
//
//	truthGraph := graphs["NaiveSequential"]
//	for name, testGraph := range graphs {
//		if name == "NaiveSequential" {
//			continue
//		}
//		if !truthGraph.AreGraphsEqual(testGraph, blockSize) {
//			log.Fatalf("\n[FATAL ERROR] %s corrupted the graph on block %d!", name, metrics.BlockNumber)
//		}
//	}
//
//	// Calculate and save topology metrics silently
//	edges := truthGraph.CountEdges(blockSize)
//	maxEdges := (blockSize * (blockSize - 1)) / 2
//	var density float64 = 0
//	if maxEdges > 0 {
//		density = float64(edges) / float64(maxEdges)
//	}
//
//	metrics.TotalEdges = edges
//	metrics.Density = density
//	metrics.Graph = truthGraph.Export(blockSize)
//}

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
