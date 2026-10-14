# Final build: results

The four algorithms of the main suite as of 2026-10-13 (UF round 3). Pseudo-code:
`../../ALGORITHMS.tex`. Everything tried on the way: `WHAT_WE_TRIED.md`.

## Headline

Median per-block speed-up vs **HybridJoinTiny** (the strongest competitor), all blocks of the six
traces, every block parity-checked. Synthetic traces are split by realized density. Each cell is
the geometric mean over three builds with different code layouts (spread of the geo ±0.01).

| | Eth | Eth no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| BitmapTiny | ×0.98 | ×0.97 | ×0.60 | ×1.00 | ×0.92 / 0.86 / 0.90 | ×0.73 / 0.78 / 0.83 | ×0.85 |
| HybridJoinTiny | ×1.00 | ×1.00 | ×1.00 | ×1.00 | ×1.00 / 1.00 / 1.00 | ×1.00 / 1.00 / 1.00 | ×1.00 |
| **IslandHub** | ×1.04 | ×1.10 | ×1.06 | ×0.94 | ×2.10 / 1.12 / 0.93 | ×1.81 / 1.08 / 0.98 | **×1.17** |
| **IslandBipartite** | ×1.20 | ×1.25 | ×1.07 | ×0.94 | ×2.10 / 1.15 / 0.95 | ×1.79 / 1.11 / 1.01 | **×1.21** |

Against BitmapTiny: IslandBipartite ×1.48, IslandHub ×1.40, HybridJoinTiny ×1.18.

- **Win:** sparse blocks (<1% density) ×1.8–2.1; Ethereum ×1.04–1.25; Solana ×1.06–1.07;
  medium density ×1.08–1.15.
- **Tie:** dense blocks (≥10%), ×0.93–1.01. The dense fallback builds them whole with
  HybridJoin, so the ceiling is ×1.0.
- **Lose:** Sui ×0.94. Its ~10-transaction blocks run the same one-word kernel as the
  competitors; the 6% is the selector and publishing the single island.
- IslandBipartite vs IslandHub: ×1.03–1.04 overall, ×1.15 on Ethereum. It changes the hub layer's
  output contract (per-hub lists instead of explicit edges); whether the coloring pipeline accepts
  it is open.

## Files

- `results/benchmark_results_L0.json`, `_L1`, `_L2`: the three builds' runs (gitignored).
  Rebuild the table with
  `python layout_league.py --baseline HybridJoinTiny optimization_log/7_final_build/results/*.json`.
- Figures: `../6_uf_round3/Phase11_final_suite_by_size.png` (time vs block size per trace) and
  `Phase11_final_suite_contention.png` (synthetic: time vs density per size band), from L0.
- The professor deck's chart and table come from the same files
  (`presentations/make_figures.py` in the project folder).

## How it was measured

- The laptop's 6 performance cores (pinned, power throttling off, QueryPerformanceCounter timer),
  on AC, nothing else CPU-heavy.
- Per block and algorithm: 9 scratch copies, one round of 100 calls on each after a warm-up call,
  median recorded (scratch placement alone moved small-block times ±15% before).
- Three builds with different code layouts (`layout_builds.sh`): moving code shifts results up to
  ±10%.
- Caveats:
  - these runs used the synthetic traces from before the 500–1000-transaction band was
    expanded (2026-10-13; the old traces are `syn_data/*_2026-09-24*.bak`);
  - the source files were merged on 2026-10-14 (code moved, not changed), which changes the
    layout. The next final run will replace these numbers.

## Final tuning check (2026-10-14): defaults kept

A short round on the two knobs with a real cliff, in the expanded 500–1000-transaction band
(144 YCSB + 144 TPC + 3 Solana blocks there): `DenseMinTxns` 384 / 768 / 1024 and `PrefixTxns`
48 / 96 / 128, each for both algorithms, against the defaults (512, 64). Ran on three layout
builds, with traces Ethereum, Eth no fee hub, Solana, YCSB and TPC, and up to 300 blocks per
trace. Sui was skipped because neither knob acts below 64 transactions. Results:
`results/benchmark_results_tune_L41–43.json`.

Geo vs HybridJoinTiny (3 layouts; layout spread ±0.01–0.02):

| Variant | Bip: 500–1000 | Bip: all | Hub: 500–1000 | Hub: all |
|---|---|---|---|---|
| **defaults (512, 64)** | ×1.31 | ×1.26 | ×1.30 | ×1.22 |
| DenseMinTxns 384 | ×1.34 | ×1.27 | ×1.32 | ×1.22 |
| DenseMinTxns 768 | ×1.31 | ×1.26 | ×1.25 | ×1.19 |
| DenseMinTxns 1024 | ×1.29 | ×1.25 | ×1.18 | ×1.17 |
| PrefixTxns 48 | ×1.33 | ×1.26 | ×1.30 | ×1.22 |
| PrefixTxns 96 | ×1.32 | ×1.26 | ×1.32 | ×1.21 |
| PrefixTxns 128 | ×1.34 | ×1.25 | ×1.30 | ×1.20 |

- **DenseMinTxns:** the cliff is real above 512. At 768 and 1024, dense YCSB in the band drops
  from ×0.91–0.96 to ×0.53–0.74. 384 gains at most +0.02–0.03 in the band and +0.01 overall,
  within the noise: in the same band, columns where the fallback never fires (YCSB <1%) moved
  ±0.09 between variants. Ethereum's largest block has 407 transactions, so 384 would bring back
  the fallback misfires on raw Ethereum that 512 was chosen to avoid.
- **PrefixTxns:** flat. 128 helps the band a little and costs Ethereum (×1.16 vs ×1.22 for
  Bip).
- **Verdict:** no variant beats the defaults by more than the layout noise, so they stay.
- On the expanded synthetic traces, both island algorithms lead HybridJoinTiny by ×1.30–1.31 in
  the 500–1000 band (Sui excluded from these geos).

## Open

- The dense fallback misfires on some Solana blocks (×0.99 where the islands give ~×1.08); no
  cheap prefix statistic separates them from dense synthetic blocks.
- Phase 5 (many-core server): re-tune the parallel thresholds, retry the concurrent CAS partition.
- Run the full 1000-block Solana trace (only 100 blocks so far).
