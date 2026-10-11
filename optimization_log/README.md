# Optimization log

One folder per era of the project, oldest first. Each figure was rendered by `plot_metrics.py`
from the run of its milestone. Every figure has a verdict:
- **paper**: a result the paper or the presentation can show as is;
- **background**: useful to tell the story (lineage, a lesson), never as a current number;
- **skip**: an incremental step whose point another figure makes better.

| Era | Folder | Dates | Harness and baseline | Use |
|---|---|---|---|---|
| 1 | `1_lineage_unequal_baseline/` | 2026-06 → 07 | old harness, Ethereum + Solana only, **unequal Bitmap** | background only |
| 2 | `2_equalized_baselines/` | 2026-09 | hardened harness, six traces, every algorithm to one standard | paper |
| 3 | `3_uf_proving_ground/` | 2026-10 | + the access filters for everyone; IslandLean / IslandLeanHub | paper |
| 4 | `4_competitors/` | 2026-10 | + the Phase 3 competitors; harness times 9 scratch copies | paper |

## Era 1 — the island family's lineage, measured against an unequal baseline (2026-06 → 07)

Measured on the pre-2026-09-27 harness: Go's ~1 ms clock on Windows, no power-throttling opt-out,
mean of 500 calls (see `ANOMALIES.md`). Absolute times are 1.5–2× too high and are in ms. Worse,
Bitmap allocated and zeroed its index on every call, with fixed 47-word rows, and ran a per-call
pool: every "island beats Bitmap" in this era is that asymmetry (era 2 measured it at
×2.5–8 for the allocation and rows, ×1.0–5.5 for the pool). **Never quote a ratio from this era
as a result.**

| Figure | What it shows | Verdict |
|---|---|---|
| `Phase0_Baseline_*` | the start: IslandNaive (pairwise islands) and IslandObjectShattered lose to Bitmap; on Solana by ×2–10 | background: "a naive partition loses" |
| `Phase2_bitmap_for_large_islands_*` | building large islands with Bitmap-style sweeps instead of pairwise checks removes the O(k²) cliff (Solana ObjectShattered 6 ms → 1 ms) | background: the key design step of the family |
| `Phase4_improved_heap_allocation_*` | the zero-allocation rewrite (IslandNativeShattered) is the first to beat Bitmap | background, **only next to era 2**: the "win" that the fair baseline erased |
| `Phase5_independent_graphs_*` | IslandTopologyShattered: per-island local graphs instead of one global matrix (the output contract used ever since) | background: the output-contract pivot |
| `minor_steps/Phase1_remade_reintegration_*` | hub reintegration rewritten; islands still ×2–6 behind on Solana | skip |
| `minor_steps/Phase3_improved_idea_from_phase2_*` | IslandDynamicShattered: live-width sweeps, toucher-only reintegration | skip (Phase 2 makes the point) |
| `minor_steps/Phase6_tuned_with_no_shatter_*` | the tuner switched hub shattering off; the ~290-txn Ethereum dip is a data property (`ANOMALIES.md`) | skip: superseded by the hub layer of era 3 |

## Era 2 — equalized baselines (2026-09-28)

`Phase7_equalized_baselines_by_size.png`, `Phase7_equalized_baselines_contention.png` — **paper**
(the fairness section: what the old lead was made of).

Every algorithm got the same implementation standard (reusable scratch, no allocation per call,
rows sized to the live block, one chunked worker pool with a tuned sequential threshold) and a
short tuning pass. All six traces, six algorithms, parity-verified against NaiveSequential on
every block. Topology vs Bitmap, median per-block speed-up in the same run:

| Ethereum | Eth, fee hub removed | Solana | Sui | YCSB | TPC |
|---|---|---|---|---|---|
| ×0.47 | ×0.67 | ×0.96 | ×0.52 | ×0.64 | ×0.71 |

The Union-Find family lost on every trace (Solana ≈ tie, 41/100 blocks won). The old ×5–16 leads
came from Bitmap's per-call allocation and 47-word rows (×2.5–8) and its per-call pool (×1.0–5.5,
largest on small blocks). What survived was the low-contention regime: at ≤ 0.3% density the
island family still beat Bitmap ×1.5–2.9 (contention figure); the curves crossed near 1% density.

## Era 3 — the Union-Find proving ground: IslandLean / IslandLeanHub (2026-10-01)

`Phase8_uf_lean_by_size.png`, `Phase8_uf_lean_contention.png` — **paper** (the island family's
result before Phase 3). The definitive run after the Phase 4 cleanup (2026-10-02): Bitmap and
IDBucket, both with the Phase 4 access filters, and IslandLean and IslandLeanHub. Size bins are
fitted per trace and every panel has its own time axis in µs.

The island algorithms were built in the Phase 4 proving ground (`go run . -suite=uf`, see
`PHASE4_RESULTS.md`). IslandLeanHub vs Bitmap, median per-block speed-up in the same run:

| Ethereum | Eth, fee hub removed | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% |
|---|---|---|---|---|---|
| ×0.97 | ×1.05 | ×1.93 | ×0.75 | ×2.55 / 1.21 / 0.74 | ×2.52 / 1.51 / 1.10 |

The island family won on large blocks and up to ~10% density (the crossover was ~1% in era 2). It
tied on Ethereum and lost on ~10-transaction Sui blocks and on zipfian blocks above 10% density,
where one island holds most of the block.

## Era 4 — Phase 3 competitors (2026-10-07)

`Phase9_competitors_by_size.png`, `Phase9_competitors_contention.png` — **paper** (the competitor
field). The final field after the 2026-10-11 cut (Bitmap, SortJoin, HybridJoin; IDBucket and
HybridBitmap archived) against IslandLean and IslandLeanHub, measured on the placement-fixed
harness (9 scratch copies per algorithm; `PHASE3_RESULTS.md` §4).

Median per-block speed-up vs Bitmap, same run:

| | Ethereum | Eth, fee hub removed | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| HybridJoin | ×1.02 | ×1.03 | ×1.65 | ×1.01 | ×1.11 / 1.24 / 1.18 | ×1.33 / 1.33 / 1.30 | ×1.20 |
| IslandLeanHub | ×1.01 | ×1.09 | ×1.78 | ×0.74 | ×2.43 / 1.25 / 0.71 | ×2.66 / 1.55 / 1.07 | ×1.30 |

HybridJoin is the strongest competitor. The island family leads it overall by only ×1.06: it wins
the sparse regime (×1.85–2.1 below 1% density) and Solana (×1.09), and loses above 10% density.
In the contention figure the curves cross at ~3% density for 1000–2000 transactions and at
3–10% for 200–999. Era-3 numbers were measured on the single-instance harness.
