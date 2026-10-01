# Optimization log

One entry per milestone: `PhaseN_<what changed>_*.png`, rendered by `plot_metrics.py`.
Phases 0–6 (Ethereum + Solana only) were measured on the pre-2026-09-27 harness (Go's
~1 ms clock on Windows, no power-throttling opt-out, mean of 500 calls; see `ANOMALIES.md`),
so their absolute times are 1.5–2× too high and are not comparable with Phase 7.

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
