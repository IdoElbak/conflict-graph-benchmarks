# Archived algorithms

Retired Union-Find variants, kept for reference and possible revival (2026-07-18 cleanup).
Every file carries a `//go:build ignore` header, so nothing here is ever compiled.

| File | Algorithm | Why archived |
|---|---|---|
| `island_naive.go` | `IslandConstructionNaive` | First UF variant: no hubs, pairwise islands only. Superseded by the whole family. |
| `island_txn_shattered.go` | `IslandConstructionTxnShattered` | Hubs on the wrong axis (huge *transactions*, `TxnHubThreshold`). Retired early. |
| `island_object_shattered.go` | `IslandConstructionObjectShattered` | First object-hub variant; fixed-width sweeps, reintegrates against all n txns. Superseded by Native. |
| `island_dynamic_object_shattered.go` | `IslandConstructionDynamicShattered` | ObjectShattered + dynamic sweep width + toucher-only reintegration, but allocates per call. Superseded by Native. |

Live variants remain in `internal/algorithms/`: `IslandConstructionNativeShattered` and `IslandConstructionTopologyShattered`, plus the Bitmap/IDBucket/naive baselines.

## Phase 2 review (2026-09-28): none revived

Each file was judged on its idea, not its old number:

| File | Idea | Verdict |
|---|---|---|
| `island_naive.go` | UF + pairwise islands, no hubs | Subsumed: Native/Topology with hubs off and a large pairwise cut-off is this algorithm on the zero-allocation skeleton. |
| `island_object_shattered.go` | object hubs, sweep islands | Subsumed by Native (same idea, allocation-free implementation). |
| `island_dynamic_object_shattered.go` | + live-width sweeps, toucher-only reintegration | Subsumed by Native. |
| `island_txn_shattered.go` | hubs on the *transaction* axis | A distinct hub heuristic, not a distinct construction algorithm: evaluate it as a Phase 4 heuristic on the Topology skeleton. |

## Reviving a file

Revival is no longer a plain file move. Phase 2 removed what these files were
written against: `concurrency.ParallelRun` (now `concurrency.ParallelFor`),
the fixed 47-word `model.BucketSize` rows of `ConflictGraph` and
`ObjectInvertedIndex` (both are sized to the live block now), and the
archive-only helpers `populateIDBucket`, `countObjectAccesses`,
`buildMasterIndex`, `partitionViaStarTopology`, `partitionViaInvertedIndex`
and `groupValidIslands` (they are in git history, commit `c9fdd2b`). A revived
variant must be ported to the Phase 2 standard anyway: reusable scratch, no
allocation per call, live-sized rows, `ParallelFor` with a sequential threshold.

1. Move the file up one directory (back into `internal/algorithms/`) and delete
   the `//go:build ignore` line.
2. Port it as above.
3. Register it in `newAlgorithms` in `main.go`.
