# Archived model files

Build-ignored (`//go:build ignore`); kept for the archived algorithms in
`internal/algorithms/archive/`.

| File | Type | Why archived |
|---|---|---|
| `island_graph.go` | `BlockTopology` (+ `Edge`, `IslandSpan`) | Output and scratch of IslandNativeShattered / IslandTopologyShattered, retired 2026-10-02. The island algorithms now use `model.IslandTopology`, `IslandForest`, `IslandIndex` and `LiveAccesses` (`island_topology.go`). |
| `id_bucket.go` | `ObjectIdBucket` | IDBucket's per-object writer/reader CSR lists; archived with IDBucket 2026-10-11. Revives as is. |
| `union_find.go` | `UnionFind` | Used only by `BlockTopology`; replaced by `IslandForest`, a union-find that also threads each set's members on a circular list. |

To revive them, move them back, delete the `//go:build ignore` lines and
replace `growU32` / `growU64` with the generic `Resize`.
