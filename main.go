package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"conflict-graph-benchmarks/internal/algorithms"
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

// algorithm is one benchmarked construction algorithm. run is the timed call
// and owns all of its work, including any clearing of its output. Its result
// must end up in graph for the parity check, either directly or through
// materialize, which runs untimed.
type algorithm struct {
	name        string
	run         func(*model.Block) (int, int, float64)
	graph       *model.ConflictGraph
	materialize func(n uint32)
}

// newAlgorithms registers every algorithm with its own reusable scratch, in
// presentation order.
func newAlgorithms() []algorithm {
	bitmap, bitmapIndex := model.NewConflictGraph(), model.NewInvertedIndex()
	native, nativeTopo := model.NewConflictGraph(), model.NewBlockTopology()
	topo, topoGraph := model.NewBlockTopology(), model.NewConflictGraph()
	idBucket, bucket := model.NewConflictGraph(), model.NewIdBucket()
	naivePar := model.NewConflictGraph()
	naiveSeq := model.NewConflictGraph()

	return []algorithm{
		{"Bitmap", func(b *model.Block) (int, int, float64) {
			algorithms.BitmapConstruction(b, bitmap, bitmapIndex)
			return 0, 0, 0
		}, bitmap, nil},
		{"IslandNativeShattered", func(b *model.Block) (int, int, float64) {
			return algorithms.IslandConstructionNativeShattered(b, native, nativeTopo)
		}, native, nil},
		// Topology's output is the BlockTopology itself; it is materialized
		// into a matrix only for the parity check.
		{"IslandTopologyShattered", func(b *model.Block) (int, int, float64) {
			return algorithms.IslandConstructionTopologyShattered(b, topo)
		}, topoGraph, func(n uint32) {
			topoGraph.Reset(n)
			algorithms.ReconstructTopologyGraph(topo, topoGraph)
		}},
		{"IDBucket", func(b *model.Block) (int, int, float64) {
			algorithms.IDBucketConstruction(b, idBucket, bucket)
			return 0, 0, 0
		}, idBucket, nil},
		{"NaiveParallel", func(b *model.Block) (int, int, float64) {
			algorithms.NaiveParallel(b, naivePar)
			return 0, 0, 0
		}, naivePar, nil},
		{"NaiveSequential", func(b *model.Block) (int, int, float64) {
			algorithms.NaiveSequential(b, naiveSeq)
			return 0, 0, 0
		}, naiveSeq, nil},
	}
}

// selectAlgorithms returns the algorithms named in the comma-separated list,
// in registry order, or all of them when the list is empty.
func selectAlgorithms(list string) ([]algorithm, error) {
	all := newAlgorithms()
	if list == "" {
		return all, nil
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	var selected []algorithm
	for _, alg := range all {
		if wanted[alg.name] {
			selected = append(selected, alg)
			delete(wanted, alg.name)
		}
	}
	for name := range wanted {
		return nil, fmt.Errorf("unknown algorithm %q", name)
	}
	return selected, nil
}

func main() {
	algList := flag.String("algs", "", "comma-separated algorithms to time (default: all)")
	flag.Parse()

	fmt.Println("=== Conflict Graph Benchmark Engine ===")
	fmt.Println("[Platform]", prepareProcess())
	algs, err := selectAlgorithms(*algList)
	if err != nil {
		log.Fatalf("selecting algorithms: %v", err)
	}
	names := make([]string, len(algs))
	for i, alg := range algs {
		names[i] = alg.name
	}
	fmt.Println("[Algorithms]", strings.Join(names, ", "))

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
		processFile(trace.path, trace.name, manager, &allMetrics, trace.maxBlocks, algs)
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
