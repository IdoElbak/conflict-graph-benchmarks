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
		"Bitmap":                  model.NewConflictGraph(),
		"IslandNativeShattered":   model.NewConflictGraph(),
		"IslandTopologyShattered": model.NewConflictGraph(), // reconstruction target for parity only
	}

	// Single preallocated topology structure reused by IslandNativeShattered
	topology := model.NewBlockTopology()

	// Separate instance for IslandTopologyShattered: its Islands/LocalGraphs/
	// HubEdges ARE the output and must survive until the parity check, so it
	// cannot share native's scratch (PrepareForBlock would wipe them).
	topologyOutput := model.NewBlockTopology()

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
		blockSize := uint32(len(block.Transactions))

		for _, g := range graphs {
			g.Reset(blockSize)
		}

		metrics := BlockMetrics{
			Blockchain:    blockchain,
			BlockNumber:   cleanBlock.BlockNumber,
			NumTxns:       numTxns,
			UniqueObjects: block.NumUniqueObjects,
			Times:         make(map[string]string),
		}

		runAlgorithmsAndVerify(&block, graphs, &metrics, topology, topologyOutput)

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
)

// timeAlgorithm returns the median per-call time over timingRounds rounds of
// iterationsPerRound calls. One untimed warm-up call and a GC come first, so
// every algorithm starts warm and from the same heap state; the median of
// short rounds discards rounds disturbed by an interrupt or core migration.
func timeAlgorithm(run func()) time.Duration {
	run()
	runtime.GC()
	var rounds [timingRounds]int64
	for r := range rounds {
		start := nowNanos()
		for range iterationsPerRound {
			run()
		}
		rounds[r] = (nowNanos() - start) / iterationsPerRound
	}
	slices.Sort(rounds[:])
	return time.Duration(rounds[timingRounds/2])
}

func runAlgorithmsAndVerify(block *model.Block, graphs map[string]*model.ConflictGraph, metrics *BlockMetrics, topology *model.BlockTopology, topologyOutput *model.BlockTopology) {
	blockSize := uint32(len(block.Transactions))
	metrics.IslandMetrics = make(map[string]IslandStat)

	benchmarkAlg := func(name string, alg func(*model.Block, *model.ConflictGraph) (int, int, float64)) {
		graph := graphs[name]
		var count, maxSz int
		var avgSz float64

		metrics.Times[name] = timeAlgorithm(func() {
			graph.Reset(blockSize)
			count, maxSz, avgSz = alg(block, graph)
		}).String()

		if count > 0 {
			metrics.IslandMetrics[name] = IslandStat{Count: count, MaxSize: maxSz, AvgSize: avgSz}
		}
	}

	// Run only the optimized baseline and the target algorithms
	benchmarkAlg("Bitmap", algorithms.BitmapConstruction)
	benchmarkAlg("IslandNativeShattered", func(b *model.Block, g *model.ConflictGraph) (int, int, float64) {
		return algorithms.IslandConstructionNativeShattered(b, g, topology)
	})

	// IslandTopologyShattered never touches the global matrix, so its timed
	// loop has no per-iteration graph.Reset; the reconstruction below runs
	// untimed and only materializes the output for the parity check.
	{
		name := "IslandTopologyShattered"
		var count, maxSz int
		var avgSz float64
		metrics.Times[name] = timeAlgorithm(func() {
			count, maxSz, avgSz = algorithms.IslandConstructionTopologyShattered(block, topologyOutput)
		}).String()
		if count > 0 {
			metrics.IslandMetrics[name] = IslandStat{Count: count, MaxSize: maxSz, AvgSize: avgSz}
		}
		algorithms.ReconstructTopologyGraph(topologyOutput, graphs[name])
	}

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
	metrics.ConflictedTxns = conflictedTxnsRatio(truthGraph, blockSize)
}

// conflictedTxnsRatio returns the fraction of transactions with at least one
// conflict edge, the other common "conflict ratio" definition next to density.
func conflictedTxnsRatio(graph *model.ConflictGraph, blockSize uint32) float64 {
	if blockSize == 0 {
		return 0
	}
	rows := graph.Export(blockSize)
	conflicted := 0
	for i := uint32(0); i < blockSize; i++ {
		row := rows[i*model.BucketSize : (i+1)*model.BucketSize]
		if slices.ContainsFunc(row, func(word uint64) bool { return word != 0 }) {
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
