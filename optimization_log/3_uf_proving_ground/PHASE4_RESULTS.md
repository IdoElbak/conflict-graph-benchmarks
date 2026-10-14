> **Historical record** of the Phase 4 (Union-Find proving ground) results, moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# Phase 4 results — the Union-Find proving ground (2026-10-01)

Phase 4 was done before Phase 3: Phase 3 starts from the Gemini Deep Research report, which is
not in yet. The Phase 4 tricks that also help other algorithms (the access filters below) are given
to them here, so Phase 3 competitors can be built to the final standard straight away.

> **Cleanup after Phase 4 (2026-10-02).** The winners now live in `internal/algorithms`
> (`island_lean.go`, `island_lean_hub.go`, shared code in `island_shared.go`, data structures in
> `model/island_topology.go`). The access filters are folded into Bitmap and IDBucket themselves
> (BitmapLive and IDBucketLive are gone). Native, Topology and NaiveParallel are archived. The
> timed suite is Bitmap, IDBucket, IslandLean and IslandLeanHub; NaiveSequential stays as the
> untimed parity reference.
>
> **The merged Bitmap is faster than BitmapLive was**: Ethereum 2.48 µs vs 2.93 µs. It applies the
> same filter, but the filter's state now lives inside the index's own build, without a call per
> access. So the island family's lead on the Ethereum traces is **smaller than first measured**.
> The summary below is the definitive run after the cleanup. Sections 2–4 record the Phase 4 lab
> runs (uflab code, names of that time); their ratios between island configurations still hold.
> A same-run A/B confirmed the refactored island code matches the lab code (×1.01).

## Summary

Two island algorithms, **IslandLean** and **IslandLeanHub**, replace Native and Topology. Fairness
applies throughout: every Phase 4 trick that also applies to Bitmap or IDBucket was given to them.
**Bitmap** and **IDBucket** below already include those filters.

Definitive run (main suite, all six traces, every block parity-checked against NaiveSequential,
laptop on AC, 2026-10-02). Median per-block speed-up vs Bitmap, with win rate; synthetic traces
split by realized density:

| vs Bitmap | Ethereum | Eth, fee hub removed | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| **IslandLeanHub** | ×0.97 (43%) | ×1.05 (69%) | **×1.93** (98%) | ×0.75 | **×2.55 / 1.21** / 0.74 | **×2.52 / 1.51 / 1.10** | **×1.30** |
| IslandLean | ×0.59 | ×0.93 | ×1.98 | ×0.75 | ×2.85 / 1.24 / 0.68 | ×2.91 / 1.56 / 1.03 | ×1.25 |
| IDBucket | ×0.08 | ×0.38 | ×1.07 | ×0.39 | ×0.57 / 0.69 / 0.31 | ×0.72 / 0.79 / 0.42 | ×0.46 |

For reference, from the Phase 4 run (pre-cleanup), against the same filtered Bitmap logic:
- the filters made Bitmap ×1.20 faster than unfiltered Bitmap, and IDBucket ×1.02 faster;
- Topology, the Phase 2 champion, ran at ×0.65 of filtered Bitmap.

Median time per block (µs), definitive run:

| | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% |
|---|---|---|---|---|---|---|
| Bitmap | 2.48 | 1.98 | 77.2 | 0.18 | 14.3 / 37.7 / 30.7 | 37.0 / 50.8 / 33.0 |
| IDBucket | 31.2 | 5.16 | 68.2 | 0.47 | 31.1 / 45.4 / 71.9 | 50.9 / 62.3 / 75.8 |
| IslandLean | 4.20 | 2.23 | 35.1 | 0.23 | 4.52 / 17.4 / 48.7 | 8.85 / 24.8 / 29.6 |
| **IslandLeanHub** | 2.62 | **1.88** | **36.5** | 0.22 | **4.80 / 16.7** / 40.4 | **9.52 / 25.4 / 27.8** |

(A median time over a band of mixed block sizes can swing with which block lands in the middle;
the speed-up table is the median of per-block ratios and is the robust number.)

**The conditional claim** — this is what the paper can say:
- **Ahead on large blocks:** Solana ×1.93, the biggest real blocks, winning 98% of them.
- **Ahead at low and medium contention:** ×1.2–2.9 up to ~10% density on YCSB, and at every
  density on TPC-style blocks (dense TPC ×1.10).
- **Even on Ethereum's ~160-transaction blocks:** ×0.97 with the fee hub, ×1.05 without it. Rows
  are only ~3 words there, so the filtered Bitmap's sweep is already cheap. On raw Ethereum the
  hub layer is what makes it even; without hubs, IslandLean runs at ×0.59.
- **Behind** on two regimes, for structural reasons:
  - **Sui, ×0.75.** Blocks of ~10 transactions fit one 64-bit word per row, so there is nothing
    for a partition to save. At 0.2 µs the noise floor is ±20%: IslandLean and IslandLeanHub run
    identical code on every Sui block, yet differ by that much between runs.
  - **Zipfian YCSB above 10% density, ×0.74.** One island holds ~86% of the block, so the
    partition is pure overhead on top of a Bitmap-like sweep. The loss holds at every block size,
    including 0–200 transactions where both algorithms run sequentially.
- Compared with Phase 2, the crossover against Bitmap-class algorithms moved from **~1% density
  (Topology) to ~10%**.

Figures: `optimization_log/3_uf_proving_ground/Phase8_uf_lean_by_size.png`, `optimization_log/3_uf_proving_ground/Phase8_uf_lean_contention.png`.

## 1. The proving ground

`go run . -suite=uf` runs the uf suite (registry: `ufarena.go`), writing
`benchmark_results_uf.json` (gitignored); `-traces`, `-blocks` and `-algs` narrow a run. During
Phase 4 it held Native and Topology as references plus every lab configuration. Since the cleanup
it holds IslandLean with its knobs moved one at a time, and IslandLeanHub with every hub rule: the
prefix rule from `internal/algorithms`, the counting rules from `internal/algorithms/uflab`.

A mini sweep is several configurations of one variant **in the same run**. Each one is timed on every
block, back to back with the others. This sidesteps the ×1.11 run-to-run drift found in Phase 2.

`python show_results.py benchmark_results_uf.json --baseline Lean --league` prints:
- one row per configuration;
- speed-ups per trace, with synthetic traces split into density bands;
- the island statistics: islands, largest island as a share of the block, transactions in islands,
  and hub touchers.

The harness records richer island statistics for lab variants (size histogram, `hub_touched`),
read off the output untimed.

The final registry re-runs the whole Phase 4 sweep on the final code in one command: the ablation
in §3 and the heuristics in §4. That makes the Phase 5 server re-tune a single command too.

## 2. What made the difference, round by round

Rounds 1–6 were run on the evolving code, with 300-block caps. Each figure is the geometric mean of
the median per-block speed-up over the 10 trace/density columns of the round's own baseline.

| Round | Change | Result |
|---|---|---|
| 1 | **Lean front half**: no counting pass (it only fed hub detection), root tracked per transaction (one Find per union), islands read off circular member lists threaded through the union-find (no Find per txn, no counting sort), each island built → swept → wiped while hot, one-word rows for islands ≤ 64, reads of never-written objects skipped everywhere | ×1.55 vs Topology |
| 1 | + **skip private objects** (a written object no second transaction touches; the partition marks objects shared for free) | ×1.69 |
| 1 | + **pairwise HasConflict only up to 4 members** (the one-word sweep wins from 5) | ×1.76 |
| 1 | hub rules with a full counting pre-pass and Topology-style reintegration ideas | only raw Ethereum gains |
| 2 | **branchless compaction** of each island's live accesses (store every access, advance the cursor by the live bit), so build/sweep/wipe have no unpredictable branches | ×2.04 |
| 2 | **hub layer** (bit-matrix over hub touchers) + **prefix detector** (≥50% of the first 16 transactions write it) | ×2.09; raw Ethereum ×2.30 vs Topology |
| 3 | **parallel path in ONE pool pass** (wide builds first; small islands in groups on per-worker one-word indexes; wide sweeps wait on atomic per-island flags) + no hub detection below 64 transactions | ×2.16 |
| 4 | 7 hub rules × thresholds (vs Lean) | only the prefix rule pays overall (×1.05); full pre-passes cost too much on sparse blocks |
| 5 | moderate prefix rule: ≥4 of the first 64 transactions write it | ×1.07 vs Lean |
| 6 | **cost-based dispatch**: parallel only above 300 transactions **and** ≥8000 words of local matrices (a proxy for sweep work) — keeps Solana sequential, sends dense blocks to the pool | ≈ +2–7% on dense synthetic blocks |

Measured along the way:
- **Each pool pass costs a wake-up of parked workers**, several µs on this laptop. The first
  parallel path used three passes per block and lost to sequential on Solana; one pass fixed most
  of that.
- **False sharing**: building one wide island's rows from several workers wrote neighbouring words of
  the same cache lines. Building each wide island on one worker and using per-worker index arrays
  for small islands removed it.

## 3. Ablation on the final code (one idea switched off at a time)

Consolidated sweep: `go run . -suite=uf -blocks=300`, 33 configurations in one run, saved as
`benchmark_results_uf_phase4_sweep.json`. Median per-block speed-up vs **IslandLean** (`Lean`). Values
below 1 mean that switching the idea off costs that much.

| Configuration | Eth | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| index private objects too | ×0.78 | ×0.87 | ×0.88 | ×0.85 | ×0.88 / 0.78 / 0.90 | ×0.95 / 0.77 / 0.70 | **×0.83** |
| no compaction (branchy kernels) | ×0.95 | ×0.93 | ×0.85 | ×0.96 | ×0.92 / 0.73 / 0.89 | ×0.90 / 0.76 / 0.75 | **×0.86** |
| pairwise cut-off 2 / 8 / 16 (4 chosen) | ×1.00 / 1.01 / 1.01 | ×0.97 / 0.97 / 0.95 | ×0.99 / 1.01 / 1.01 | ×1.00 / 0.89 / 0.63 | ≈1 | ≈1 (×0.95 at <1% for 8, 16) | ×1.00 / 0.98 / 0.94 |
| never parallel | ×1.01 | ×1.00 | ×1.00 | ×1.00 | ×1.00 / 1.00 / 0.95 | ×1.03 / 1.02 / 0.97 | ×1.00 |
| always parallel | ×0.45 | ×0.47 | ×0.85 | ×0.57 | ×0.84 / 0.82 / 0.90 | ×0.82 / 0.88 / 0.83 | ×0.72 |
| dispatch at 4k / 16k words (8k chosen) | ×1.01 / 1.00 | ×0.99 / 1.00 | ×1.02 / 1.00 | ×1.01 / 0.99 | ≈1 | ×1.06 / 1.03 at <1% | ×1.01 / 1.01 |

Reading it:
- **Skipping private objects** is worth +20% and **branchless compaction** +16%. Both pay everywhere.
- **The pairwise cut-off** only matters for Sui's 5–16-member islands: there HasConflict is ×1.6
  slower than the one-word sweep.
- **The parallel path pays only on dense synthetic blocks** (+3–5%). Most blocks stay below the
  dispatch threshold, and running them in parallel anyway would cost up to ×2.2. The dispatch
  threshold is flat between 4k and 16k words.
- **The round-1 front-half changes** (no counting pass, root tracking, list grouping, per-island
  build-sweep-wipe, one-word islands) are structural and can't be switched off one by one. Together
  they took Topology to ×1.55 (round 1).

## 4. Heuristics — each with its own mini sweep (Phase 4 requirement)

Every rule runs on IslandLean with the hub layer, no hub detection below 64 transactions, and in the
same run as the ablation. Each rule has a mini sweep of 2–5 settings. Only written objects can be
hubs: never-written objects are free already.

Speed-up vs IslandLean:

| Rule (settings swept) | Eth | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| **prefix**: written by ≥ k of the first P txns (P/k = 16/8, 32/4, **64/4**, 64/8, 128/8) | **×1.59** | **×1.18** | ×0.98 | ×0.99 | ×0.96 / 1.01 / 1.01 | ×0.97 / 0.98 / 1.02 | **×1.06** |
| prefix 32/4 | ×1.56 | ×1.16 | ×0.99 | ×1.00 | ×0.99 / 0.99 / 0.98 | ×0.99 / 1.00 / 0.99 | ×1.05 |
| **sampled**: accesses of every 4th txn > T/4 (T = **16**, 32, 64) | ×1.44 | ×1.04 | ×0.95 | ×0.91 | ×0.79 / 0.98 / 1.03 | ×0.78 / 0.86 / 0.94 | ×0.96 |
| **writers**: writes > T, pre-pass over writes (T = **4**, 8, 16) | ×1.42 | ×1.02 | ×0.93 | ×0.99 | ×0.53 / 0.86 / 1.02 | ×0.61 / 0.80 / 0.97 | ×0.89 |
| **write-weighted**: 4·writes + reads > T (T = **32**, 64, 128) | ×1.34 | ×0.98 | ×0.95 | ×0.99 | ×0.46 / 0.74 / 0.94 | ×0.59 / 0.74 / 0.93 | ×0.83 |
| **split**: writes > T or reads > 8T (T = **8**, 16) | ×1.28 | ×0.92 | ×0.86 | ×1.01 | ×0.43 / 0.72 / 0.90 | ×0.59 / 0.69 / 0.91 | ×0.80 |
| **accesses**: reads + writes > T (T = **16**, 32, 64) | ×1.22 | ×0.87 | ×0.90 | ×1.00 | ×0.44 / 0.70 / 0.91 | ×0.57 / 0.68 / 0.88 | ×0.79 |
| **relative**: accesses > max(8, f·n) (f = **0.10**, 0.25) | ×1.20 | ×0.85 | ×0.82 | ×0.90 | ×0.45 / 0.70 / 0.85 | ×0.57 / 0.69 / 0.76 | ×0.75 |

(Best setting of each rule in bold; the other settings are monotonically worse. The full rows are
in the results file. This sweep ran before the exact "complete hub matrix" shortcut, which added
another ~5% on raw Ethereum.)

Island statistics, median per block: islands / largest island as a share of the block / share of
transactions touching a hub:

| Rule | Eth | no fee hub | Solana | YCSB | TPC |
|---|---|---|---|---|---|
| no hubs (IslandLean) | 1 / 1.00 / 0 | 10 / 0.43 / 0 | 13 / 0.24 / 0 | 2 / 0.86 / 0 | 2 / 0.29 / 0 |
| prefix 64/4 | 18 / 0.07 / 1.00 | 18 / 0.07 / 0.35 | 14 / 0.23 / 0.06 | 3 / 0.74 / 0.44 | 10 / 0.27 / 0.48 |
| prefix 32/4 | 16 / 0.16 / 1.00 | 16 / 0.16 / 0.27 | 13 / 0.24 / 0.02 | 2 / 0.80 / 0.36 | 5 / 0.29 / 0.38 |
| prefix 16/8 | 14 / 0.28 / 1.00 | 14 / 0.28 / 0.17 | 13 / 0.24 / 0.00 | 2 / 0.86 / 0.15 | 2 / 0.29 / 0.06 |
| sampled 16 | 15 / 0.17 / 1.00 | 15 / 0.17 / 0.29 | 20 / 0.21 / 0.21 | 10 / 0.67 / 0.57 | 16 / 0.27 / 0.64 |
| writers 4 | 18 / 0.04 / 1.00 | 18 / 0.04 / 0.44 | 26 / 0.17 / 0.27 | 22 / 0.47 / 0.62 | 26 / 0.10 / 0.72 |
| write-weighted 32 | 18 / 0.07 / 1.00 | 18 / 0.07 / 0.39 | 31 / 0.14 / 0.26 | 14 / 0.59 / 0.59 | 21 / 0.19 / 0.68 |
| split 8 | 18 / 0.07 / 1.00 | 18 / 0.07 / 0.39 | 26 / 0.17 / 0.25 | 11 / 0.66 / 0.57 | 21 / 0.20 / 0.65 |
| accesses 16 | 16 / 0.15 / 1.00 | 16 / 0.15 / 0.30 | 26 / 0.16 / 0.23 | 11 / 0.65 / 0.57 | 17 / 0.28 / 0.65 |
| relative 0.10 | 16 / 0.16 / 1.00 | 16 / 0.16 / 0.28 | 20 / 0.21 / 0.17 | 4 / 0.74 / 0.44 | 4 / 0.28 / 0.49 |

Conclusions:
- **For construction speed, only the prefix rules pay.** They cost a few hundred operations and
  catch the hubs that matter: Ethereum's fee recipient, written by every transaction, plus other
  objects written by many. Every rule that counts over the whole block pays a full pass over the
  accesses. On sparse blocks that pass costs more than the smaller islands save (×0.4–0.6 at
  <1% density).
- **Every rule pays on raw Ethereum** (×1.2–1.6). The fee hub makes the whole block one island
  otherwise.
- **For downstream colouring parallelism, the aggressive rules are the knob.** writers > 4 and
  write-weighted 32 cut the largest island further for ×0.83–0.89 construction speed:
  - TPC: 29% of the block → 10–19%;
  - YCSB: 86% → 47–59%;
  - Solana: 24% → 14–17%;
  - Eth-no-hub: 43% → 4–7%.

  Construction speed and colouring parallelism want different settings, as the plan expected. The
  paper should report both.
- **Write-weighting helps only a little** over plain access counts (×0.83 vs ×0.79 at the best
  settings). Splitting read and write thresholds lands in between (×0.80). Relative thresholds are
  the worst (×0.75): on large blocks they hub fewer objects than absolute ones do.
- **Not evaluated:** transaction-axis hubs (the archived TxnShattered idea). Access sets average
  4–15 accesses per transaction in every trace, but whether a few huge transactions exist was not
  measured, so this stays open.

## 5. Fairness counterparts

- **Bitmap with the filters** (called BitmapLive during Phase 4; now `bitmap.go` itself). The build
  tracks each object's first writer and marks objects touched by a second transaction as shared.
  It sets reader bits only for written objects, and the sweep skips every object that is not
  shared. This is exactly the filter of the island algorithms. It won ×1.18 over unfiltered Bitmap
  (geo), most on sparse and medium blocks, so **the filtered Bitmap is the fair baseline**.
- **IDBucket with the filter** (IDBucketLive during Phase 4; now `id_bucket_construction.go`
  itself). Its CSR leaves out reads of never-written objects. Only ×1.04: IDBucket already pays
  little for dead reads (empty writer lists), and private writes cost it one self bit.
- Not given to Bitmap, because they need a partition: one-word rows per island, skipping singletons'
  rows, and the hub layer.
- **Branchless compaction does not help Bitmap.** It was measured in a Bitmap variant that compacts
  each row's shared accesses before sweeping, on full traces in one run: ×0.85 geo, slower on
  every column. Bitmap touches each access once per row, while the island kernels touch it three
  times (build, sweep, wipe).
- **After the cleanup** the filters live inside Bitmap's and IDBucket's own index builds, which made
  Bitmap faster than the BitmapLive measured above (see the note at the top).

## 6. Correctness and determinism

Every configuration of every run was compared bit-for-bit with NaiveSequential on every block. That
covers the always-parallel configuration, which ran the parallel path on every block, including
Sui's. No mismatch occurred in any round.

The parallel path is race-free by construction:
- units write disjoint words;
- small islands use per-worker index arrays;
- wide-island sweeps start only after an atomic "built" flag (release/acquire), and only read the
  shared index;
- the wipe runs after the pool pass returns.

A seeded unit test, `internal/algorithms/parity_test.go` (`go test ./...`, under a second),
compares Bitmap, IDBucket, IslandLean, IslandLeanHub, the forced-parallel path, a larger pairwise
cut-off and five hub rules (including the complete hub matrix) with NaiveSequential, on 80 random
blocks of up to 500 transactions. Deliberately breaking Bitmap's read filter, the one-word island
sweep, the diagonal of the complete hub matrix or the wipe of the one-word index makes it fail.

`go test -race` is not available on this machine (no C compiler, cgo off). Outputs are deterministic:
the island partition and member order come from the sequential partition, and the hub toucher
numbering is in transaction order of first hub access.

## 7. Ideas measured and dropped, and ideas left for later

- **Parallel (CAS) union-find.** About 25–40 cycles per access with atomics, against ~10
  sequentially, plus two extra pool passes. Break-even at Solana sizes on 6 cores. Revisit on the
  server (Phase 5).
- **Full counting pre-pass** for hub detection: too costly on sparse blocks (all count-based rules
  lose overall in §4).
- **Sweeping only the non-empty word range** of an object row, and BFS-ordered member numbering, to
  shorten wide-island sweeps. Not tried; the wide-island sweep is still ~1/3 of Solana's time.
- **Hub summaries** instead of a hub matrix: for example "these k transactions all write object X"
  instead of k² bits. That would win raw Ethereum outright, but it changes the output contract
  (the hub part would no longer be an explicit graph). Needs your and the professor's decision
  before it is built.
- **Density-adaptive fallback**: when the writer pass already shows one island holding most of the
  block, skip the rest of the partition and build that block as a single island. It could at best
  reach parity with the filtered Bitmap on dense zipfian blocks.
- **Fusing compaction with the one-word index build** (masked OR, one pass less): measured ×0.97
  geo (Sui ×0.91), so it was dropped. Touching dead objects' words costs more than the pass saved.
- **Kept:** an exact shortcut for a hub written by every toucher (Ethereum's fee recipient). The hub
  matrix is then complete, so its rows are filled instead of ORed: +5% on raw Ethereum.
- **Sui** stays a near-tie (0.27 vs 0.26 µs). Its time is spread over the partition, compaction and
  the one-word kernel, with no single overhead left to cut. With ~10-transaction blocks every row
  is one word for every algorithm, so a partition can only add work.
