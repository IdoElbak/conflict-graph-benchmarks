# Archived algorithms

Retired algorithms, kept for reference and possible revival, in one subfolder per family:
`islands/` (the Union-Find lineage) and `competitors/` (matrix-output algorithms). Every file
carries a `//go:build ignore` header, so nothing here is ever compiled. Each file's `package`
line names the live package it would return to.

Live algorithms (2026-10-11): `islands/` — IslandLean and IslandLeanHub; `competitors/` — Bitmap,
SortJoin and HybridJoin; `NaiveSequential` (in `internal/algorithms/`) is the untimed parity
reference.

## `islands/` — the Union-Find lineage

| File | Algorithm | Retired | Why archived |
|---|---|---|---|
| `island_naive.go` | `IslandConstructionNaive` | 2026-07-18 | First UF variant: no hubs, pairwise islands only. Superseded by the whole family. |
| `island_txn_shattered.go` | `IslandConstructionTxnShattered` | 2026-07-18 | Hubs on the wrong axis (huge *transactions*, `TxnHubThreshold`). Retired early. |
| `island_object_shattered.go` | `IslandConstructionObjectShattered` | 2026-07-18 | First object-hub variant; fixed-width sweeps, reintegrates against all n txns. Superseded by Native. |
| `island_dynamic_object_shattered.go` | `IslandConstructionDynamicShattered` | 2026-07-18 | ObjectShattered + dynamic sweep width + toucher-only reintegration, but allocates per call. Superseded by Native. |
| `island_native_shattered.go` | `IslandConstructionNativeShattered` | 2026-10-02 | Superseded by `IslandLean` (Phase 4): same idea, global-matrix output, ×0.3–0.7 of IslandLean's speed on every trace. |
| `island_topology_shattered.go` | `IslandConstructionTopologyShattered` | 2026-10-02 | Superseded by `IslandLean` / `IslandLeanHub` (Phase 4), which keep its output contract (now `model.IslandTopology`) and are ×1.4–2.6 faster. |
| `island_shared.go` | the two files' shared helpers and hub knobs | 2026-10-02 | Only Native and Topology used them; the live `islands/island_shared.go` is the Phase 4 rewrite. |

### Phase 2 review (2026-09-28): none of the 2026-07-18 files revived

Each file was judged on its idea, not its old number:

| File | Idea | Verdict |
|---|---|---|
| `island_naive.go` | UF + pairwise islands, no hubs | Subsumed: Native/Topology with hubs off and a large pairwise cut-off is this algorithm on the zero-allocation skeleton. |
| `island_object_shattered.go` | object hubs, sweep islands | Subsumed by Native (same idea, allocation-free implementation). |
| `island_dynamic_object_shattered.go` | + live-width sweeps, toucher-only reintegration | Subsumed by Native. |
| `island_txn_shattered.go` | hubs on the *transaction* axis | A distinct hub heuristic, not a distinct construction algorithm: evaluate it as a Phase 4 heuristic on the Topology skeleton. |

### Reviving an island file

Revival is not a plain file move. Phase 2 removed what the 2026-07-18 files were written against:
`concurrency.ParallelRun` (now `concurrency.ParallelFor`), the fixed 47-word `model.BucketSize`
rows of `ConflictGraph` and `ObjectInvertedIndex` (both are sized to the live block now), and the
archive-only helpers `populateIDBucket`, `countObjectAccesses`, `buildMasterIndex`,
`partitionViaStarTopology`, `partitionViaInvertedIndex` and `groupValidIslands` (they are in git
history, commit `c9fdd2b`). A revived variant must be ported to the Phase 2 standard anyway:
reusable scratch, no allocation per call, live-sized rows, `ParallelFor` with a sequential
threshold.

Native and Topology need `model.BlockTopology` and `model.UnionFind`, archived in
`internal/model/archive/`; move those back as well (`BlockTopology` also needs
`growU32`/`growU64`, now the generic `model.Resize`). Phase 4's numbers for both are in
`PHASE4_RESULTS.md`.

1. Move the file to `internal/algorithms/islands/` and delete the `//go:build ignore` line.
2. Port it as above.
3. Register it in `newAlgorithms` in `main.go` (or in `ufarena.go`) and in `parity_test.go`.

## `competitors/` — matrix-output algorithms

| File | Algorithm | Retired | Why archived |
|---|---|---|---|
| `naive_parallel.go` | `NaiveParallel` | 2026-10-02 | Removed from the timed suite (O(n²), 10–100× slower than the rest). `NaiveSequential` stays, untimed, as the parity reference. |
| `id_bucket_construction.go` | `IDBucketConstruction` | 2026-10-11 | The spec's "optimized hash-map" (row-wise Gustavson): ×0.46 geo vs Bitmap, beaten by SortJoin and HybridJoin on every trace (`PHASE3_RESULTS.md` §5). |
| `hybrid_bitmap.go` | `HybridBitmap` | 2026-10-11 | Roaring-style per-object list-or-bitset containers: ×1.00–1.02 geo vs Bitmap, beaten by HybridJoin on every column outside placement noise. |
| `object_lists.go` | `objectLists` with dense marking | 2026-10-11 | The version HybridBitmap was written against: `build(block, denseOver)` also marks the dense objects (`objectSpan.dense`, `denseObjs`) and has `accessors()`. The live copy dropped those. |

To revive one, move it to `internal/algorithms/competitors/`, delete the `//go:build ignore`
line, and register it in `main.go` (or `cmparena.go`) and `parity_test.go`. Also:
- **IDBucket** needs `model.ObjectIdBucket`, archived in `internal/model/archive/id_bucket.go`
  (it revives as is); add its knobs back to `tune_hyperparams.py` (`IDBucketSequentialMax` 217,
  `IDBucketChunkRows` 64).
- **HybridBitmap** needs this `object_lists.go` in place of the live one (then pass `noDense` in
  SortJoin's and HybridJoin's `build` calls). It was written when Bitmap lived in package
  `algorithms`: drop that import and call `BitmapConstruction` directly. Its test configs:
  default; `MinWords 1, SequentialMax 0` for the parallel path; that with `DenseFactor` 1e9 for
  all lists and 0 for all dense.
- **NaiveParallel** revives as is (it already uses `ParallelFor`).
