# How to run

Everything runs from this folder. Needs Go (see `go.mod`) and Python 3 with matplotlib and
numpy. The traces live in `internal/digestion/data/` and `syn_data/` (not in git).

## Check correctness (seconds)

```
go test ./...
```

Checks every algorithm, and every path of the island algorithms, against the all-pairs reference
on seeded random blocks.

## Benchmark the main suite (~2.5 min)

```
go run .                                   # 4 algorithms x 6 traces -> benchmark_results.json
go run . -algs=IslandHub,HybridJoinTiny    # a subset
go run . -traces=Ethereum,Solana -blocks=100
go run . -out=my_run.json
```

- It stops at once if the output file exists: delete it or pass `-out`.
- Every block is parity-checked; a mismatch stops the run.
- Each algorithm is timed on 9 scratch copies (median of 9 rounds of 100 calls).
- Before a long run, plug the laptop in and close anything CPU-heavy. Compare algorithms within
  one run only: whole runs drift by up to ×1.1.

Traces: `Ethereum`, `EthereumNoHub` (fee recipient removed), `Solana`, `Sui`, `YCSB`, `TPC`. A
missing derived trace (parsed JSON, no-hub Ethereum, synthetic blocks) is produced on the first
run. The raw H5 dumps are turned into JSON by `internal/digestion/script-parser.py`.

## Read the results

```
python show_results.py [FILE]                            # tables vs HybridJoinTiny
python show_results.py FILE --league --baseline X        # one row per algorithm / variant
python plot_metrics.py [FILE] --save PREFIX [--algs A,B] # PREFIX_by_size.png, PREFIX_contention.png
python trace_statistics.py                               # block sizes of the traces
```

## Tune

- **Island knobs** (the proving ground; every variant timed in one run):
  `go run . -suite=uf -blocks=300 [-algs=...]` writes `benchmark_results_uf.json`. The
  registry is `ufarena.go`: both island algorithms with each knob moved one at a time. Pick a
  handful with `-algs`; all of them at once need a lot of memory.
- **HybridJoin's knobs**: `go run . -suite=cmp` writes `benchmark_results_cmp.json` (registry
  `cmparena.go`).
- **Bitmap's knobs**: `python tune_hyperparams.py` (resumable; `--plan`, `--report`, `--check`).
  It rewrites the constants in `bitmap_tiny.go`.

## Trustworthy numbers: average over code layouts

Moving the same code to other addresses shifts results by up to ±10% on this laptop. For a final
number, run the same flags on three builds with different layouts and average:

```
bash layout_builds.sh . /tmp/builds 1 2 3            # builds bench_L1.exe .. bench_L3.exe
/tmp/builds/bench_L1.exe -out=run_L1.json            # run from THIS folder (trace paths are relative)
...
python layout_league.py --baseline HybridJoinTiny run_L1.json run_L2.json run_L3.json
```

## The documents

- `ALGORITHMS.tex` / `ALGORITHMS.pdf`: pseudo-code of the four algorithms (`pdflatex` twice).
- `optimization_log/7_final_build/`: the current results and everything tried.
- `optimization_log/<era>/`: each era's figures and full report.
