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
	// stats, when set, replaces the island statistics returned by run with
	// richer ones read off the algorithm's output, untimed.
	stats func() IslandStat
}

// newAlgorithms registers the timed algorithms of the main suite, each with
// its own reusable scratch, in presentation order. NaiveSequential is not
// timed: it is the parity reference every algorithm is checked against.
func newAlgorithms() []algorithm {
	bitmap, bitmapIndex := model.NewConflictGraph(), model.NewInvertedIndex()
	idBucket, bucket := model.NewConflictGraph(), model.NewIdBucket()

	return []algorithm{
		{"Bitmap", func(b *model.Block) (int, int, float64) {
			algorithms.BitmapConstruction(b, bitmap, bitmapIndex)
			return 0, 0, 0
		}, bitmap, nil, nil},
		{"IDBucket", func(b *model.Block) (int, int, float64) {
			algorithms.IDBucketConstruction(b, idBucket, bucket)
			return 0, 0, 0
		}, idBucket, nil, nil},
		islandAlgorithm("IslandLean", algorithms.NewIslandLean(algorithms.DefaultIslandConfig)),
		islandAlgorithm("IslandLeanHub", algorithms.NewIslandLeanHub(algorithms.DefaultIslandConfig, algorithms.DefaultHubRule())),
	}
}

// islandVariant is an island algorithm: its output is a model.IslandTopology.
type islandVariant interface {
	Run(*model.Block) (int, int, float64)
	Topology() *model.IslandTopology
}

// islandAlgorithm wraps an island algorithm for the harness: its topology is
// reconstructed into a matrix for the parity check, and its island statistics
// are read off the output, both untimed.
func islandAlgorithm(name string, v islandVariant) algorithm {
	graph := model.NewConflictGraph()
	return algorithm{
		name:  name,
		run:   v.Run,
		graph: graph,
		materialize: func(n uint32) {
			graph.Reset(n)
			v.Topology().Reconstruct(graph)
		},
		stats: func() IslandStat {
			s := v.Topology().Stats()
			return IslandStat{Count: s.Count, MaxSize: s.MaxSize, AvgSize: s.AvgSize,
				InIslands: s.InIslands, HubTouched: s.HubTouched, Size2: s.Size2,
				Size3to8: s.Size3to8, Size9to64: s.Size9to64, SizeOver64: s.SizeOver64}
		},
	}
}

// suites maps a -suite name to its registry and default results file. "uf" is
// the proving ground: island variants against each other.
var suites = map[string]struct {
	registry func() []algorithm
	results  string
}{
	"main": {newAlgorithms, "benchmark_results.json"},
	"uf":   {newUFArena, "benchmark_results_uf.json"},
}

// selectAlgorithms returns the algorithms of the registry named in the
// comma-separated list, in registry order, or all of them when it is empty.
func selectAlgorithms(all []algorithm, list string) ([]algorithm, error) {
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
	algList := flag.String("algs", "", "comma-separated algorithms to time (default: all of the suite)")
	suiteName := flag.String("suite", "main", "algorithm registry: main (all algorithms) or uf (island variants only)")
	outPath := flag.String("out", "", "results file (default: the suite's)")
	traceList := flag.String("traces", "", "comma-separated traces to run (default: all)")
	blockCap := flag.Int("blocks", 0, "at most this many blocks per trace (default: the trace's own limit)")
	flag.Parse()

	fmt.Println("=== Conflict Graph Benchmark Engine ===")
	fmt.Println("[Platform]", prepareProcess())
	suite, ok := suites[*suiteName]
	if !ok {
		log.Fatalf("unknown suite %q", *suiteName)
	}
	if *outPath == "" {
		*outPath = suite.results
	}
	algs, err := selectAlgorithms(suite.registry(), *algList)
	if err != nil {
		log.Fatalf("selecting algorithms: %v", err)
	}
	names := make([]string, len(algs))
	for i, alg := range algs {
		names[i] = alg.name
	}
	fmt.Println("[Algorithms]", strings.Join(names, ", "))

	// Skip if results already exist
	if _, err := os.Stat(*outPath); err == nil {
		fmt.Printf("\n[Skipped] %s already exists. Remove it to run a new benchmark.\n", *outPath)
		return
	}
	wantedTraces := map[string]bool{}
	for _, name := range strings.Split(*traceList, ",") {
		if name = strings.TrimSpace(name); name != "" {
			wantedTraces[name] = true
		}
	}

	// Phase 1: Data Digestion
	fmt.Println("\n[Phase 1] Initializing Data Digestion Pipeline...")
	runDigestionPhase()

	// Phase 2 & 3: Stream, Translate, and Execute
	fmt.Println("\n[Phase 2 & 3] Streaming Blocks and Executing Algorithms...")
	manager := model.NewManager()
	var allMetrics []BlockMetrics
	for _, trace := range traces {
		if len(wantedTraces) > 0 && !wantedTraces[trace.name] {
			continue
		}
		maxBlocks := trace.maxBlocks
		if *blockCap > 0 {
			maxBlocks = min(maxBlocks, *blockCap)
		}
		processFile(trace.path, trace.name, manager, &allMetrics, maxBlocks, algs)
	}

	// Phase 4: Analysis Export
	fmt.Println("\n[Phase 4] Exporting Analysis Metrics...")
	saveMetrics(allMetrics, *outPath)
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
