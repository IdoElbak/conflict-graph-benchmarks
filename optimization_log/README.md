# Optimization log

The project's history: one folder per era, oldest first, each with its figures (rendered by
`plot_metrics.py`) and its full report. **Start with `7_final_build/`**: the current results
(`RESULTS.md`) and every idea tried with its verdict (`WHAT_WE_TRIED.md`).

| Era | Folder | Dates | What happened | Report | Figures |
|---|---|---|---|---|---|
| 1 | `1_lineage_unequal_baseline/` | 2026-06 → 07 | the UF lineage up to Native and Topology, against an **unequal** Bitmap | — | background only: never quote a ratio |
| 2 | `2_equalized_baselines/` | 2026-09 | every algorithm to one standard; the islands' lead was the baseline's handicap | `ANOMALIES.md` (measurement fixes) | paper |
| 3 | `3_uf_proving_ground/` | 2026-10-01 | the UF rewrite: IslandLean, IslandLeanHub; access filters for everyone | `PHASE4_RESULTS.md`, `PHASE4_DIRECTIONS.md` | paper |
| 4 | `4_competitors/` | 2026-10-07 | competitors from the literature; HybridJoin; 9 scratch copies | `PHASE3_RESULTS.md` | paper |
| 5 | `5_uf_next/` | 2026-10-11 | tiny path, dense fallback, bipartite hubs; layout-averaged numbers | `UF_NEXT_RESULTS.md` | background |
| 6 | `6_uf_round3/` | 2026-10-13 | giant-island kernel; IslandHub, IslandBipartite; the four-algorithm suite | `UF_ROUND3_RESULTS.md` | paper (the final figures) |
| 7 | `7_final_build/` | current | the final numbers and the summary of everything tried | `RESULTS.md`, `WHAT_WE_TRIED.md` | uses era 6's |

Also here: `PROJECT_HISTORY.md` (the full changelog) and `ALGORITHM_LINEAGE.md` (pseudo-code of
every algorithm, live and archived). The reports are historical records: file names in them are
as they were then.

## Figures

| Figure | Shows | Use |
|---|---|---|
| `1_.../Phase0_Baseline_*` | IslandNaive and ObjectShattered lose to Bitmap (Solana ×2–10) | background: "a naive partition loses" |
| `1_.../Phase2_bitmap_for_large_islands_*` | Bitmap-style sweeps for big islands remove the O(k²) cliff | background: the family's key design step |
| `1_.../Phase4_improved_heap_allocation_*` | Native, the first to "beat" Bitmap | background, only next to era 2 |
| `1_.../Phase5_independent_graphs_*` | Topology: island graphs instead of one matrix | background: the output-contract pivot |
| `1_.../minor_steps/*` | intermediate steps | skip |
| `2_.../Phase7_equalized_baselines_*` | Topology ×0.47–0.96 vs the fair Bitmap; it still wins at ≤ 0.3% density | paper: the fairness section |
| `3_.../Phase8_uf_lean_*` | IslandLeanHub ×1.30 vs Bitmap; crossover moved from ~1% to ~10% density | paper |
| `4_.../Phase9_competitors_*` | HybridJoin ×1.20 vs Bitmap; IslandLeanHub leads it only ×1.06 | paper: the competitor field |
| `5_.../Phase10_uf_next_*` | the round-3 starting point (NextM, NextB) | background |
| `6_.../Phase11_final_suite_*` | the four-algorithm suite: islands win sparse/medium, Ethereum, Solana; tie dense; lose Sui | **paper** |

Era 1 was measured on the old harness (~1 ms Go clock on Windows, no throttling opt-out, mean of
500 calls), in ms, against a Bitmap that allocated and zeroed its index every call.
