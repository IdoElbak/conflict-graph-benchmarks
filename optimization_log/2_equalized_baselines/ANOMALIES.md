> **Historical record** of the Phase 1b measurement investigation, moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# Anomalies — Phase 1b investigation (2026-09-27)

Two anomalies were reported after the 2026-07-18 professor meeting: a sudden time drop around
~280-transaction Ethereum blocks affecting all algorithms, and "palpitating" Solana timings that
appeared between the Phase 5 and Phase 6 milestone plots even for Bitmap, whose code did not
change. Investigating them uncovered four more issues. Every verdict below is backed by a
measurement, not by a plausible story.

## Summary

| # | Anomaly | Verdict | Action |
|---|---|---|---|
| A | ~280-txn Ethereum dip, all algorithms | **Measurement artifact**: per-block timing noise inside a 2-block plot bin | Fixed by the harness hardening (H) |
| B | Solana "palpitation" Phase 5 → 6, incl. Bitmap | **Measurement artifact**: the machine's state during the Phase 6 run (power throttling onto slow cores), made visible by 2–3-block bins | Fixed by H |
| C | Go's clock on Windows ticks every ~1 ms | **Measurement bug** | Fixed: timing uses QueryPerformanceCounter |
| D | Hybrid CPU + Windows power throttling moves the benchmark onto cores up to 3× slower | **Measurement environment** — the root cause behind A and B | Fixed: power throttling off, high priority, pinned to performance cores, median of 5 rounds |
| E | GC work from one algorithm leaking into the next one's timing | **Ruled out** (< 3% median effect) | `runtime.GC()` kept before each timed loop as cheap hygiene |
| F | Island-family cliffs: Ethereum bump at n≈60–70, Ethereum-no-hub drop at n≈150, TPC hump at 1–3% density | **Real algorithm property**: `PairwiseIslandMax = 64` sends islands of up to 64 txns to the pairwise path, which is 2–3.5× slower than sweeping at 40–64 | Not changed in 1b — the constant must be re-tuned (Phase 2/4) |

H = the harness hardening described in its own section below. Also checked and found clean: the
Solana block-size field (the parser's header count vs the parsed transaction count differ on 0 of
100 blocks), and the synthetic generators' independent edge counter vs Bitmap's density (equal on
360/360 blocks).

## Data and method

- **Result files**: `benchmark_results_phase6_prerefactor.json` (2026-07-11, the Phase 6 run),
  `benchmark_results_postrefactor.json` (2026-07-18, identical code for Bitmap / Native /
  Topology), a 2026-09-27 run on the old harness with all six traces, and two back-to-back runs on
  the hardened harness. Phase 5 raw data was not preserved; only its plots
  (`optimization_log/1_lineage_unequal_baseline/Phase5_*.png`) remain.
- **Paired diagnostics** (throwaway tools, not kept in the repo): a copy of the module that, for
  every Ethereum, Solana and Sui block, measured the *same* 500-iteration loop with both Go's clock
  and QueryPerformanceCounter, then re-measured each algorithm after a warm-up call and
  `runtime.GC()`, alternating order per block. A second tool measured every block twice back to
  back with three timing methods under three process settings. Pairing removes run-to-run drift
  from every comparison.
- **Core probe**: a compute loop and a 32 MB memory walk pinned to each logical CPU in turn.
- **Roughness** of a size curve: median over blocks of |log2(time / median of the 24 nearest-size
  blocks)| — how jagged the unbinned curve is.
- `analyze_anomalies.py` (the prescribed script) was run on the two preserved files.

## C — Go's clock on Windows is quantized to ~1 ms

Go's runtime on windows/amd64 reads its monotonic clock from `_INTERRUPT_TIME`
(`runtime/sys_windows_amd64.s`), which only advances once per timer interrupt.

- Probe: in 2 s the clock took 2004 distinct steps, median step 1.0005 ms. A fixed ~20 µs workload
  measured `0s` in 15 of 15 attempts.
- Paired diagnostic (Go clock vs QueryPerformanceCounter on the same loop):

| Trace / algorithm | true per-call time | Go clock read 0 | median error | p95 error |
|---|---|---|---|---|
| Ethereum Bitmap | 100 µs | 0% | 0.5% | 2.3% |
| Ethereum Topology | 15 µs | 0% | 3.5% | 16% |
| Solana (all) | 160–750 µs | 0% | ≤0.3% | ≤1.4% |
| Sui Native / Topology | 0.8–1 µs | **45–51%** | **100%** | >200% |

Verdict: a bug in the measurement. Sui would have been unusable and Ethereum's island numbers
carried up to ±16% per-block error. Fix: `bench_platform_windows.go` times with
QueryPerformanceCounter; other platforms keep `time.Now`, which is nanosecond-resolution there.

## D — Hybrid CPU and power throttling (root cause of A and B)

The laptop is an Intel Core Ultra 7 255H with three core types. Core probe, compute loop / memory
walk:

| Logical CPUs | Core type | Compute | Memory walk |
|---|---|---|---|
| 0, 1, 10–13 | performance | 2.5 ms (×1) | ×1 |
| 2–9 | efficiency | 3.6 ms (×1.45) | ~×1.05 |
| 14, 15 | low-power efficiency | 7.7 ms (×3.1) | ×3 |

On the Balanced power plan, Windows' power throttling (EcoQoS) moves a background console process
between these cores. Measured with the old settings, two back-to-back measurements of the same
block disagreed by ×0.45–×2.3 (p5–p95) for the island algorithms. The disagreement was:

- **correlated between Native and Topology on the same block** (0.38–0.45), much less with Bitmap
  (0.09–0.25): compute-bound code suffers more on a slow core than memory-bound code;
- **not persistent across blocks** (lag-1 autocorrelation ≈ 0): the placement changes within
  milliseconds.

Test-retest noise (factor between two back-to-back measurements of the same block, median / p95),
with every timing on QueryPerformanceCounter:

| Setting | Ethereum Topology, mean of 500 | Ethereum Topology, median of 5×100 | Solana Bitmap, median of 5×100 |
|---|---|---|---|
| default (old harness) | ×1.18 / ×2.10 | ×1.11 / ×2.64 | ×1.08 / ×1.25 |
| power throttling off + high priority | ×1.01 / ×1.20 | ×1.006 / ×1.12 | ×1.035 / ×1.12 |
| … + pinned to the 6 performance cores | ×1.02 / ×1.20 | ×1.008 / ×1.10 | ×1.017 / ×1.08 |

Opting out of throttling removes most of the noise and makes the island algorithms 1.4–1.8×
faster. Pinning mostly helps Bitmap: unpinned, its parallel workers include stragglers on the
low-power cores (Solana Bitmap 558 → 424 µs when pinned). Minimum-of-5 was as stable as
median-of-5 but is biased towards the luckiest round, so the median was chosen.

## A — The ~280-transaction Ethereum dip

- **It is a single sparse bin.** The Phase 6 plot bins by 10 txns and draws any bin with ≥ 2
  blocks; the dip is the 290 bin, which held **2 blocks** (neighbours: 2–6 blocks each).
- **The unbinned curve has no dip.** On the post-refactor run the 25-block sliding-window medians
  rise monotonically through 230–413 for all three algorithms. In both runs the 230–330 window is
  *slower* than its flanks (inside/flank ×1.31–1.53), not faster.
- **The blocks are not lighter.** The 230–330 window has *more* objects than its flanks (median
  711 vs 548; density is 1.0 everywhere on raw Ethereum).
- **One block was timed during a fast moment.** The whole Phase 6 run was ×2.2 (Bitmap) to ×2.9
  (Native) slower than the post-refactor run on the same blocks. One of the two blocks in the 290
  bin, 21297054, was measured at only ×0.89–1.03 of its post-refactor time. With a 2-block median,
  that single lucky measurement is the dip.
- **It does not reproduce.** The Phase 5 plot (same blocks, same Bitmap code) has no dip at 290.
  The paired diagnostic's fine-clock and isolated measurements show no dip in 280–299. The hardened
  runs show none either.

Verdict: measurement artifact (noise from D, sampled by a 2-block bin). Not a data property, not a
bug in any algorithm.

## B — Solana "palpitation" between Phase 5 and Phase 6

- In the Phase 6 plot all five algorithms, **including Bitmap**, rise and fall together bin by bin
  (swings up to ×2). The Phase 5 plot of the **same 100 blocks** is smooth, so the data cannot be
  the cause.
- Solana has 100 blocks spread over ~40 bins: 2–3 blocks per bin, so per-block noise shows up
  directly as a zig-zag along the size axis.
- Measured roughness of the unbinned Solana curves: Phase 6 run ×1.47–1.60 vs post-refactor run
  ×1.07–1.16 — same code for these algorithms, so the difference is the machine's state during
  the Phase 6 run.
- Per block the algorithms moved together (cross-algorithm correlation 0.83–0.97), and the same
  block's times agreed only weakly between the two runs (Spearman 0.68–0.78), which is the
  signature of D.

Verdict: measurement artifact from D. The earlier idea that the slowdown came from GC pacing (after
the `graph` export stopped inflating the heap) was tested in E and is refuted.

## E — GC leakage between algorithms (ruled out)

The paired diagnostic compared the plain loop against the same loop preceded by one warm-up call
and `runtime.GC()`. Median isolated/plain per trace and algorithm: 0.97–1.02, faster in 43–58% of
blocks. The spread (p5–p95 ×0.45–×2.3) is the noise from D, not GC. Isolation is kept in the
harness because it costs about a second per run and guarantees every algorithm starts from the
same heap.

## F — `PairwiseIslandMax = 64` cliffs (real, not an artifact)

Once the noise was gone, the hardened curves showed three features that reproduce exactly between
runs (per-block Spearman 0.96–0.999). All come from the dispatch rule in the island family:
islands of 3–64 txns run pairwise `HasConflict`, larger ones use bitset sweeps.

| Where | Pairwise side | Sweep side |
|---|---|---|
| Raw Ethereum (every block is one island of all n txns) | n 50–64: Topology 7.1 µs | n 65–80: **3.3 µs** (Bitmap rises 21 → 26 µs) |
| Ethereum no-hub, n 110–190 | largest island 40–64: 16.9 µs | largest island 65–90: **4.8 µs** |
| TPC, 200–999 txns | 3% density, largest island 46: Native 0.29 ms | 10% density, largest island 239: **0.18 ms** |

In the no-hub trace the share of blocks whose largest island is ≤ 64 falls from 191/221
(n 110–140) to 78/325 (n 150–190) — the drop near 150 in the plot. For the synthetic traces a
simple cost model (pairwise work C(k,2) × mean access-set size over islands ≤ 64, plus 0.3 × sweep
work) correlates with the Native time at 0.93 (TPC) and 0.95 (YCSB). YCSB shows no hump because
its islands exceed 64 already at 0.3% density.

Verdict: a real property of the algorithm. Sweeps already win at islands of 40–64, so 64 is too
high. It was picked by the 2026-07-11 grid search, which ran on the old harness (C and D), so
**all three tuned constants (64 / 3000 / 10⁹) and `tuning_results.json` are unreliable** and must
be re-tuned on the hardened harness (Phase 2/4). A cost-based dispatch (island size × access-set
size) is a candidate for Phase 4.

## H — Harness hardening (applied)

| | Before | After |
|---|---|---|
| Clock (Windows) | `time.Now`, ~1 ms ticks | QueryPerformanceCounter (`bench_platform_windows.go`) |
| Process (Windows) | default: throttled, all 16 cores | power throttling off, high priority, pinned to the most performant core class detected via `GetSystemCpuSetInformation` (here 6 cores, mask 0x3c03, GOMAXPROCS=6); no-op on homogeneous CPUs and other OSes |
| Per block and algorithm | mean of 500 calls | one warm-up call + `runtime.GC()`, then the median of 5 rounds × 100 calls (since 2026-10-11: one round of 100 calls on each of 9 scratch copies, each after its own warm-up call; `PHASE3_RESULTS.md` §4) |
| Blocks > `MaximumGraphSize` | would corrupt neighbouring rows | skipped and counted |

Effect, per-block agreement between two runs of the same code:

| | Old harness (Jul-11 vs Jul-18) | Hardened (two back-to-back runs) |
|---|---|---|
| Whole-run shift | ×2.2–2.9 (Ethereum), ×1.1 (Solana) | ×0.98–1.02 on every trace |
| Per-block disagreement, median / p95 | ×1.1–2.9 / ×2.1–4.0 | ×1.03–1.08 / ×1.10–1.30 |
| Per-block Spearman | 0.68–0.78 | 0.94–0.999 (Solana Bitmap 0.80) |

A full run now takes 4 min 40 s (was 7 min 48 s).

## Consequences for earlier numbers

Every number measured before 2026-09-27 (`PROJECT_STATE.md` §4, the `optimization_log/` plots,
`tuning_results.json`, the ×7.9 headline) came from the old harness. Absolute times are 1.5–2×
too high, and the island family was penalised more than Bitmap, so the old ratios are not
comparable with new ones. Current hardened numbers (median ms; speedup = median per-block
Bitmap/alg):

| Trace | Bitmap | Native | Topology | Native × | Topology × |
|---|---|---|---|---|---|
| Ethereum | 0.05401 | 0.01205 | 0.00815 | ×4.48 | ×6.66 |
| Ethereum, fee hub removed | 0.05666 | 0.00958 | 0.00656 | ×7.10 | ×10.67 |
| Solana | 0.43792 | 0.13158 | 0.07950 | ×3.29 | ×5.49 |
| Sui | 0.00796 | 0.00061 | 0.00059 | ×13.77 | ×14.16 |
| YCSB | 0.27325 | 0.05003 | 0.03827 | ×4.77 | ×6.29 |
| TPC-C style | 0.56290 | 0.08316 | 0.07289 | ×5.44 | ×6.46 |

These still contain the Bitmap fairness problems scheduled for Phase 2 (per-call allocation,
fixed 47-word rows, `ParallelRun` overhead — the latter dominates Sui's 10-transaction blocks).

## Still open

- **Solana Bitmap** is the least reproducible series (Spearman 0.80 between hardened runs vs
  ≥ 0.94 elsewhere); its per-call goroutine spawning in `ParallelRun` is the likely source.
  Revisit after the Phase 2 pool rework.
- **Absolute speed still depends on the power plan and AC power.** Runs here were on AC,
  "Balanced". Recording the plan with each run, or benchmarking in "Best performance", would make
  laptop numbers easier to compare with the server (Phase 5).
- **Phase 5 raw data was never preserved**, so the Phase 5 vs Phase 6 comparison rests on the
  plots plus the Jul-11 vs Jul-18 file comparison.
- **The plot's rule of drawing bins with ≥ 2 blocks** is kept, but raw blocks are now drawn as
  dots, so a sparse bin can no longer pass for a trend.
