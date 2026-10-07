# Optimization log

One entry per milestone: `PhaseN_<what changed>_*.png`, rendered by `plot_metrics.py`.
Phases 0–6 (Ethereum + Solana only) were measured on the pre-2026-09-27 harness (Go's
~1 ms clock on Windows, no power-throttling opt-out, mean of 500 calls; see `ANOMALIES.md`),
so their absolute times are 1.5–2× too high and are not comparable with Phase 7.

## Phase 8 — the Union-Find proving ground: IslandLean / IslandLeanHub (2026-10-01)

`Phase8_uf_lean_by_size.png`, `Phase8_uf_lean_contention.png`: the definitive run after the
Phase 4 cleanup (2026-10-02). It shows Bitmap and IDBucket, both with the Phase 4 access filters,
and IslandLean and IslandLeanHub. Size bins are fitted per trace, there are no raw dots, and every
panel has its own time axis in µs.

The island algorithms were built in the Phase 4 proving ground (`go run . -suite=uf`, see
`PHASE4_RESULTS.md`). IslandLeanHub vs Bitmap, median per-block speed-up in the same run:

| Ethereum | Eth, fee hub removed | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% |
|---|---|---|---|---|---|
| ×0.97 | ×1.05 | ×1.93 | ×0.75 | ×2.55 / 1.21 / 0.74 | ×2.52 / 1.51 / 1.10 |

The island family wins on large blocks and up to ~10% density (the crossover was ~1% in Phase 7). It
ties on Ethereum and loses on ~10-transaction Sui blocks and on zipfian blocks above 10% density,
where one island holds most of the block.

## Phase 7 — equalized baselines (2026-09-28)

`Phase7_equalized_baselines_by_size.png`, `Phase7_equalized_baselines_contention.png`

Every algorithm got the same implementation standard (reusable scratch, no allocation per
call, rows sized to the live block, one chunked worker pool with a tuned sequential
threshold) and a short tuning pass. All six traces, six algorithms, parity-verified against
NaiveSequential on every block. Topology vs Bitmap, median per-block speed-up in the same run:

| Ethereum | Eth, fee hub removed | Solana | Sui | YCSB | TPC |
|---|---|---|---|---|---|
| ×0.47 | ×0.67 | ×0.96 | ×0.52 | ×0.64 | ×0.71 |

The Union-Find family now loses on every trace (Solana ≈ tie, 41/100 blocks won). The old
×5–16 leads came from Bitmap's per-call allocation and 47-word rows (×2.5–8) and its per-call
pool (×1.0–5.5, largest on small blocks). What survives is the low-contention regime: at ≤ 0.3% density the island
family still beats Bitmap ×1.5–2.9 (contention figure); the curves cross near 1% density.
