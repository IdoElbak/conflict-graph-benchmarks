# Archived algorithms

Retired Union-Find variants, kept for reference and possible revival (2026-07-18 cleanup).
Every file carries a `//go:build ignore` header, so nothing here is ever compiled.

| File | Algorithm | Why archived |
|---|---|---|
| `island_naive.go` | `IslandConstructionNaive` | First UF variant: no hubs, pairwise islands only. Superseded by the whole family. |
| `island_txn_shattered.go` | `IslandConstructionTxnShattered` | Hubs on the wrong axis (huge *transactions*, `TxnHubThreshold`). Retired early. |
| `island_object_shattered.go` | `IslandConstructionObjectShattered` | First object-hub variant; fixed-width sweeps, reintegrates against all n txns. Superseded by Native. |
| `island_dynamic_object_shattered.go` | `IslandConstructionDynamicShattered` | ObjectShattered + dynamic sweep width + toucher-only reintegration, but allocates per call. Superseded by Native. |

Live variants remain in `internal/algorithms/`: `IslandConstructionNativeShattered` (frozen reference) and `IslandConstructionTopologyShattered` (champion), plus the Bitmap/naive/IDBucket baselines.

## Reviving a file

1. Move it up one directory (back into `internal/algorithms/`).
2. Delete the `//go:build ignore` line and the blank line after it.
3. Note: `ObjectHubThreshold` now lives in `island_shared.go` (shared with the live family) —
   `island_object_shattered.go` and `island_dynamic_object_shattered.go` compile against it as-is.
   `TxnHubThreshold` and `LargeIslandThreshold` travel inside their archived files.
4. The shared helpers these files call (`populateIDBucket`, `countObjectAccesses`,
   `partitionViaStarTopology`, `partitionViaInvertedIndex`, `groupValidIslands`,
   `processSingleIsland`, `CalculateIslandStats`) still exist in the live package.
5. Re-register in `pipeline_utils.go`: add the name to the `graphs` map, add a
   `benchmarkAlg(...)` call in `runAlgorithmsAndVerify`, and (optionally) add the name to
   the island-stats print list in `processFile`.
