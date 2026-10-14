# conflict-graph-benchmarks

Fast, deterministic construction of a blockchain block's **conflict graph**: an edge between two
transactions iff one writes an object the other reads or writes. The project compares Union-Find
"island" algorithms against the strongest matrix algorithms, on real (Ethereum, Solana, Sui) and
synthetic (YCSB, TPC-C style) traces.

## The four algorithms (the main suite)

| Algorithm | Family | Output | File |
|---|---|---|---|
| BitmapTiny | competitor (the baseline) | full matrix | `internal/algorithms/competitors/bitmap_tiny.go` |
| HybridJoinTiny | competitor (the strongest) | full matrix | `internal/algorithms/competitors/hybrid_join_tiny.go` |
| IslandHub | Union-Find (ours) | island graphs + hub bit-matrix | `internal/algorithms/islands/island_hub.go` |
| IslandBipartite | Union-Find (ours) | island graphs + per-hub lists | `internal/algorithms/islands/island_bipartite.go` |

Pseudo-code: `ALGORITHMS.tex` (`ALGORITHMS.pdf`). Every output is checked bit for bit against
the all-pairs reference `internal/algorithms/naive_sequential.go` on every block of every run.

**Result** (2026-10-13, median per-block speed-up vs HybridJoinTiny, geometric mean over the
trace columns): IslandBipartite ×1.21, IslandHub ×1.17, BitmapTiny ×0.85. The islands win
on sparse and medium blocks, Ethereum and Solana, tie on dense blocks, and lose ×0.94 on Sui.
Details: `optimization_log/7_final_build/`.

## Layout

| Path | What |
|---|---|
| `main.go`, `pipeline_utils.go`, `bench_platform_*.go` | the benchmark harness and the main suite's registry |
| `ufarena.go`, `cmparena.go` | the proving grounds: island knobs (`-suite=uf`), HybridJoin's knobs (`-suite=cmp`) |
| `internal/algorithms/` | the algorithms: `islands/`, `competitors/`, the parity reference and test; retired ones in `archive/` |
| `internal/model/` | blocks, the conflict graph, the island output (`island_topology.go`) and the scratch structures |
| `internal/digestion/` | trace parsers and synthetic generators (data in `data/` and `syn_data/`, not in git) |
| `internal/concurrency/` | the shared worker pool |
| `*.py` | result tables, plots, the Bitmap tuner (see `how_to_run.md`) |
| `optimization_log/` | the history: one folder per era with its figures and report, and the final build |

How to run everything: `how_to_run.md`.
