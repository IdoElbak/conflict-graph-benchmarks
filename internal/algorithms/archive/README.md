# Archived algorithms

Retired algorithms, kept for reference and possible revival. Every file has a `//go:build ignore`
header, so nothing here compiles; its `package` line names the package it would return to.
Verdicts with numbers: `optimization_log/7_final_build/WHAT_WE_TRIED.md`. Pseudo-code:
`optimization_log/ALGORITHM_LINEAGE.md`.

## `islands/` — the Union-Find lineage

| File | Algorithm | Retired | Why |
|---|---|---|---|
| `island_naive.go` | IslandNaive | 2026-07-18 | no hubs, pairwise islands: giant islands explode |
| `island_txn_shattered.go` | IslandTxnShattered | 2026-07-18 | hubs on the wrong axis (huge transactions) |
| `island_object_shattered.go` | IslandObjectShattered | 2026-07-18 | superseded by Native |
| `island_dynamic_object_shattered.go` | IslandDynamicShattered | 2026-07-18 | superseded by Native (allocates per call) |
| `island_native_shattered.go` | IslandNativeShattered | 2026-10-02 | global-matrix output; ×0.3–0.7 of IslandLean |
| `island_topology_shattered.go` | IslandTopologyShattered | 2026-10-02 | IslandLean keeps its output contract and is ×1.4–2.6 faster |
| `island_shared.go` | Native's and Topology's helpers | 2026-10-02 | only they used it |
| `island_lean.go`, `island_lean_hub.go`, `island_lean_shared.go` | IslandLean, IslandLeanHub and their core | 2026-10-13 | succeeded by IslandHub (same partition and hub matrix, plus the tiny path, dense fallback and giant kernel) |
| `uflab/` | counted hub rules, Space-Saving | 2026-10-13 | the prefix rule won |
| `ufnext/` | `Next`, `Adaptive`, the CAS partition, probes | 2026-10-13 | the experiment package of the last two UF rounds; finalists promoted |

## `competitors/` — matrix output

| File | Algorithm | Retired | Why |
|---|---|---|---|
| `naive_parallel.go` | NaiveParallel | 2026-10-02 | O(n²); NaiveSequential stays as the parity reference |
| `id_bucket_construction.go` | IDBucket | 2026-10-11 | the spec's hash-map: ×0.46 vs Bitmap |
| `hybrid_bitmap.go` | HybridBitmap | 2026-10-11 | ×1.00–1.02 vs Bitmap, beaten by HybridJoin |
| `object_lists.go` | `objectLists` with dense marking | 2026-10-11 | the version HybridBitmap needs |
| `sort_join.go` | SortJoin | 2026-10-13 | dominated by HybridJoin on every column |

## Reviving a file

1. Move it to its live folder and delete the `//go:build ignore` line.
2. Register it in `main.go` (or `ufarena.go` / `cmparena.go`) and in `parity_test.go`.
3. Fix what changed since. Pre-2026-09-28 files need the Phase 2 standard:
   - `ParallelFor` instead of `ParallelRun`;
   - live-sized rows, no 47-word `BucketSize`;
   - no allocation per call.
   The helpers they used are in git history (`c9fdd2b`).

Per file:
- **Native, Topology:** need `model.BlockTopology` and `model.UnionFind` from
  `internal/model/archive/`.
- **IDBucket:** needs `model.ObjectIdBucket` (`internal/model/archive/id_bucket.go`) and its knobs
  back in `tune_hyperparams.py` (`IDBucketSequentialMax` 217, `IDBucketChunkRows` 64).
- **HybridBitmap:** needs the archived `object_lists.go` in place of the `objectLists` in
  `hybrid_join_tiny.go`. It calls `BitmapConstruction` directly.
- **SortJoin, NaiveParallel:** revive as is (`emitPairs` is in `hybrid_join_tiny.go`).
- **Lean files:** `island_lean_shared.go` collides with the live `island_shared.go` (same names),
  so revive them as their own package. `uflab/` and `ufnext/` revive together with them.
