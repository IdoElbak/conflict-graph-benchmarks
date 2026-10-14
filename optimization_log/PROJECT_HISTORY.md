> **Historical record** of the project changelog (was PROJECT_STATE.md), moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# Project State — Conflict Graph Construction Benchmarks

*Snapshot: 2026-10-13 (UF round 3 done: the UF family optimized, the top two promoted as IslandHub and IslandBipartite, the main suite cut to four algorithms; §0 has the changelog). Companion files: `ALGORITHMS.md` (pseudo-code, knobs), `ANOMALIES.md` (measurement investigation), `PHASE4_DIRECTIONS.md` (Phase 4 plan), `PHASE4_RESULTS.md` (Phase 4 results), `PHASE3_RESULTS.md` (Phase 3 results), `UF_NEXT_RESULTS.md` (UF next round), `UF_ROUND3_RESULTS.md` (UF round 3).*

## Next steps (agreed 2026-10-13)

1. **Final UF tuning (in progress):** 2–3 hours over every UF-family knob, mainly the hub and
   density heuristics (prefix length, hub threshold, hub-writer share, dense minimum size), with
   per-algorithm defaults and, where it pays, knobs that depend on block size. Before it: more
   YCSB/TPC blocks of 500–1000 transactions, and any last algorithm ideas. After it the UF
   algorithms are final.
2. **Tidy the folder.** No algorithm changes after this point.
3. The user commits and reviews the code.
4. **Presentation 1:** the changes of the last month, for the professor.
5. **Presentation 2:** the whole project, for an audience new to the field.
6. **Before the final presentation:** the full 1000-block Solana trace (only after the tuning; it
   is slow), then Phase 5 on the many-core server.

## 0. Changelog 2026-10-13 (UF round 3) — details in `UF_ROUND3_RESULTS.md`

- **UF family optimized** (3-layout rounds, vs HybridJoinTiny):
  - kept: the giant-island object-major kernel (`IslandConfig.GiantShare` 0.3), the dense fallback
    only from 512 transactions, and hub rule 64/3 for the matrix variant;
  - rejected with evidence: a branch-free batched partition (×0.85–0.90), deciding "dense" before
    clearing the anchors (neutral), the prefix-giant detector (misses dense YCSB) and three other
    prefix statistics (none separates dense TPC from Ethereum), and no fallback at all (a trade).
- **Promoted** to `islands/` as **IslandHub** (matrix hub layer, contract unchanged) and
  **IslandBipartite** (bipartite hub layer): `island_shared.go` (core), `island_hubs.go` (shared
  algorithm), `island_hub.go`, `island_bipartite.go`. Identical knobs; only the hub layer differs.
- **Archived** (build-ignored, `internal/algorithms/archive/`): IslandLean, IslandLeanHub, the
  Phase 4 core (`island_lean_shared.go`), `uflab/`, `ufnext/` (every experiment and probe of both
  UF rounds), and SortJoin (its `emitPairs` moved to `hybrid_join.go`).
- **Main suite = four algorithms**: BitmapTiny, HybridJoinTiny, IslandHub, IslandBipartite.
  - Competitor ranking: HybridJoinTiny > HybridJoin ×0.93 > BitmapTiny ×0.85 > Bitmap > SortJoin.
  - HybridJoin is HybridJoinTiny without the one-word kernel, so the second competitor is
    BitmapTiny.
  - Bitmap and HybridJoin stay as kernels and are no longer registered on their own.
  - `ufarena.go` sweeps the two island algorithms' knobs; `cmparena.go` sweeps HybridJoinTiny's;
    `parity_test.go` forces every island path (mutation-checked).
- **Definitive run** (`benchmark_results.json` plus two layout builds, `benchmark_results_final_L31/L32.json`; the previous suite's run is kept as `benchmark_results_2026-10-11_old_suite.json`), geo vs HybridJoinTiny:

  | Algorithm | geo |
  |---|---|
  | IslandBipartite | ×1.21 |
  | IslandHub | ×1.17 |
  | BitmapTiny | ×0.85 |

  Figures in `optimization_log/6_uf_round3/`. `show_results.py` now defaults to the baseline HybridJoinTiny.

## 0. Changelog 2026-10-11 (UF next round, after the Phase 3 commit) — details in `UF_NEXT_RESULTS.md`

- **New package `internal/algorithms/islands/ufnext/`** (proving ground only; IslandLean and IslandLeanHub are untouched):
  - `Next` (`next_core.go`, `next_hub.go`): IslandLeanHub forked, with every idea as a knob. Tiny path, dense fallback by the prefix's hub writers, bipartite hub layer, fused prefix, read-counting prefix, concurrent CAS partition (`next_cas.go`).
  - `Adaptive` (`adaptive.go`, sampled-density fallback); shared whole-block paths (`whole_block.go`).
  - Probes as tests (`UFNEXT_STATS=1`): trace statistics, the RCM span ceiling, vertex cuts, prefix hub statistics. Trace-driven `go test -bench` for pprof profiles only (not P-core pinned).
- **New competitor kernel `competitors/tiny_bitmap.go`**: TinyBitmap, one-word Bitmap without the filter, ×1.8 vs Bitmap on Sui. Fairness counterparts BitmapTiny and HybridJoinTiny are in `-suite=cmp` and the uf arena. Not in the main suite (decision pending).
- **`uflab/space_saving.go`**: report B's Space-Saving hub rule (loses).
- **Model, additive only**:
  - `ConflictGraph.Matrix`;
  - `IslandTopology.SetWholeBlock`, plus `HubWriters`/`HubReaders` for the bipartite hub layer (`Reconstruct` expands them);
  - `IslandForest.Size`.
- **Parity test**: every ufnext path forced, mutation-checked; plus an empty block (it caught a crash on Sui's empty blocks).
- **Arena**: Bitmap, HybridJoin and their tiny counterparts are bars in `-suite=uf` (`newUFNextArena` in `ufarena.go`).
- **Measurement finding: code layout swings results ±10% on the P-cores.** An unused function added elsewhere moved a variant from ×0.89 to ×1.02. New protocol: 3 layout-perturbed builds plus `layout_league.py` (geo over layouts; spread ±0.01).
- **Result** (3 layouts, vs HybridJoin):

  | Variant | geo | vs HybridJoinTiny |
  |---|---|---|
  | NextM (contract unchanged) | ×1.24 | ×1.13 |
  | NextB (bipartite hub output) | ×1.31 | ×1.18 |
  | Hub (IslandLeanHub) | ×1.07 | ×0.96 |

  Rejected with evidence: CAS partition on 6 cores, Space-Saving, the Afforest writer-pass check, RCM (probe ceiling ≤ 5%), vertex cuts (probe), read-counting hubs. Fused prefix: within noise.
- `optimization_log/5_uf_next/` (era 5).

## 0. Changelog 2026-10-11 (Phase 3 decisions applied)

- **`internal/algorithms/` reorganized by family**: the root keeps NaiveSequential and `parity_test.go`; `islands/` (package `islands`) holds IslandLean, IslandLeanHub, `island_shared.go` (now including the object-major join kernel, formerly `island_join_kernel.go`) and `uflab/`; `competitors/` holds Bitmap (moved from the root), SortJoin and HybridJoin; `archive/` has one subfolder per family and one README (the former `competitors/archive/` merged into it). No algorithm changed.

- **Archived** IDBucket (its `ObjectIdBucket` in `internal/model/archive/`) and HybridBitmap (both in `internal/algorithms/archive/competitors/`, with the dense-marking `object_lists.go` it needs). Timed suite: Bitmap, SortJoin, HybridJoin, IslandLean, IslandLeanHub. `tune_hyperparams.py` tunes Bitmap only. `objectSpan` lost HybridBitmap's `dense` field (12 bytes per object, worth ~×1.05 to HybridJoin on large blocks).
- **Harness, placement fix**: every algorithm is instantiated `scratchCopies = 9` times; per block, one 100-call round per copy (each after its own warm-up call), median recorded; every copy parity-checked. Three identical Bitmaps now agree within ×0.98–1.03 on Ethereum and Sui (were ×0.83–1.06; 5 copies still split Sui ×1.14). Runs take ~1.8× as long per algorithm (main suite ~4 min with five algorithms).
- **Definitive run** (`benchmark_results.json`; the 2026-10-07 runs are kept as `benchmark_results_phase3_run1/2.json`): HybridJoin ×1.20 geo vs Bitmap (was ×1.10: the old harness under-measured it); IslandLeanHub ×1.30 vs Bitmap, but only **×1.06 vs HybridJoin** (was ×1.16): it wins below 1% density (×1.85–2.1) and on Solana (×1.09), and loses above 10% (YCSB ×0.61, TPC ×0.85) and on Sui. `PHASE3_RESULTS.md` (update section); Phase 9 figures regenerated.

## 0. Changelog 2026-10-07 (Phase 3: competitors from the Gemini reports)

- **New package `internal/algorithms/competitors/`**: **HybridJoin** (object-major join with a dense kernel per hot object; the strongest competitor, ×1.10 geo vs Bitmap, Solana ×1.5, dense YCSB ×1.06), **SortJoin** (the classical sort-based conflict join; sequential), **HybridBitmap** (Roaring-style per-object list-or-bitset containers; ×1.00–1.02). All in the main suite and `parity_test.go` (with forced-parallel, all-list and all-dense configs, mutation-checked).
- **Competitor proving ground** `go run . -suite=cmp` (`cmparena.go`): each competitor's knobs, one run.
- **Rejected after checking**: Gustavson/SPA (= IDBucket), PPJoin prefix filtering (no pruning at overlap 1), Four Russians (a probe on the real data: ≤ 10% fewer word loads at best), Roaring proper (= HybridBitmap at our block sizes). Details: `PHASE3_RESULTS.md` §1.
- **Fairness transfer to the islands**: HybridJoin's kernel as an island wide-island kernel (`IslandConfig.JoinDense`, `island_join_kernel.go`, merged into `island_shared.go` on 2026-10-11, off by default). Neutral (×1.01–1.03); kept in the UF proving ground.
- **Result** (two runs): IslandLeanHub stays ahead of the best competitor, ×1.16 geo over HybridJoin (×1.30 over Bitmap): it wins sparse/medium blocks and Solana (×1.12), and loses above 10% density (YCSB ×0.66, TPC ×0.95) and on Sui.
- **Measurement finding**: an algorithm's scratch memory placement biases its small-block timings by up to ~±15% per process (three identical Bitmaps: ×0.83–1.06 on Ethereum; ×1.00 when they share memory). Small-block differences under ~15% are not evidence. A harness fix is proposed (`PHASE3_RESULTS.md` §4).
- **`optimization_log/`** sorted into era folders with a verdict per figure; new figures `4_competitors/Phase9_*`. the Phase 3 runs are now `benchmark_results_phase3_run1/2.json`; the Phase 4 run is kept as `benchmark_results_phase4.json`.

## 0. Changelog 2026-10-02 (cleanup after Phase 4)

- **The access filters are folded into Bitmap and IDBucket** (`bitmap.go`, `id_bucket_construction.go`; the state lives in `model.ObjectInvertedIndex` / `model.ObjectIdBucket`). `bitmap_live.go` and `id_bucket_live.go` are deleted. The merged Bitmap is faster than BitmapLive was (Ethereum 2.48 vs 2.93 µs): same filter, without a per-access call. Compacting Bitmap's rows branch-free was measured and loses (×0.85).
- **The island algorithms moved out of the lab:** `island_lean.go`, `island_lean_hub.go`, and a new `island_shared.go` (knobs `IslandConfig`, partition, kernels, parallel path). The data structures moved to `model/island_topology.go` (`IslandTopology`, `IslandForest`, `IslandIndex`, `LiveAccesses`, generic `Resize`). The options Phase 4 settled are no longer options (private-object skipping and compaction always on). Hub detection is a `HubRule` interface; the default `PrefixHubs` is called directly, since the dynamic call cost ~2%.
- **Archived:** IslandNativeShattered, IslandTopologyShattered and their `island_shared.go`, plus NaiveParallel, to `internal/algorithms/archive/`; `model/island_graph.go` (BlockTopology) and `model/union_find.go` to `internal/model/archive/`. All are build-ignored, with READMEs.
- **Timed suite:** Bitmap, IDBucket, IslandLean, IslandLeanHub. NaiveSequential stays as the untimed parity reference.
- **Proving ground:** `ufarena.go` now sweeps IslandLean's knobs and IslandLeanHub's hub rules. `uflab` holds only the counting-based rules (`hub_rules.go`).
- **Tests and tooling:** `internal/algorithms/parity_test.go` (seeded random blocks, all algorithms and rules vs NaiveSequential). `tune_hyperparams.py` now tunes Bitmap and IDBucket only (the island knobs are tuned in the proving ground). `plot_metrics.py` gives every panel its own time axis, in µs. Stale result files were removed.
- **Definitive numbers** (§4): IslandLeanHub vs Bitmap ×1.30 geo. Same-run A/B against the lab code: the refactor costs nothing (×1.01).

## 0. Changelog 2026-10-01 (Phase 4: the Union-Find proving ground, done before Phase 3)

- **Proving ground**: `go run . -suite=uf` times only island variants against each other (Native and Topology as references, lab variants from the new package `internal/algorithms/uflab/`, registry `ufarena.go`), with `-traces`, `-blocks`, `-out`; `show_results.py --baseline X --league` prints one row per configuration with island statistics. The final registry re-runs the whole Phase 4 sweep (ablation + 7 hub heuristics with mini sweeps) in one command.
- **Two new island algorithms in the main suite** (Native and Topology untouched): **IslandLean** (Topology contract; two-pass anchor partition with per-transaction root tracking; islands read off circular member lists; dead reads and private objects never indexed; branchless compaction of each island's live accesses; one-word rows for islands ≤ 64; single-pass parallel path with cost-based dispatch) and **IslandLeanHub** (IslandLean + hub layer: hub objects out of the partition, their conflicts as a bit-matrix over hub touchers; hubs = objects written by ≥ 4 of the first 64 transactions).
- **Fairness counterparts**: **BitmapLive** and **IDBucketLive** get the access filters (Bitmap ×1.18 geo from them; IDBucket ×1.04). BitmapLive is the fair baseline from now on.
- **Result** (§4): IslandLeanHub vs BitmapLive ×1.37 geo; ahead on Solana (×1.82), both Ethereum traces (×1.12 / ×1.22) and YCSB/TPC up to ~10% density (×1.3–2.8); behind on Sui (×0.75–0.90, ~10-txn blocks, ±20% noise at 0.3 µs) and zipfian YCSB above 10% density (×0.72). Superseded by the 2026-10-02 run in §4: the merged Bitmap is faster than BitmapLive was, so the Ethereum leads shrank to ×0.97 / ×1.05.
- Harness: island statistics read off lab outputs untimed (`IslandStat` gained `in_islands`, `hub_touched`, size histogram); `plot_metrics.py` bins per trace (no raw dots) and takes `--algs`.

## 0. Changelog 2026-09-28 (Phase 2: every algorithm equalized)

- **Same standard for all six algorithms**: reusable scratch (no allocation per call), rows sized to the live block (`ConflictGraph` and the inverted index lost the fixed 47-word stride), each algorithm clears its own output inside the timed call, one chunked pool (`concurrency.ParallelFor`, atomic chunk claiming, ~1–2 µs fixed cost) replacing `ParallelRun` (one channel op per unit), and a tuned sequential threshold + chunk per algorithm. Bitmap sweeps straight into its output rows; IDBucket became a CSR bucket writing full rows (no symmetry pass); Native (no longer frozen) and Topology split large sweep islands into row ranges for the parallel phase.
- **Harness**: all six algorithms registered (`go run . -algs=A,B` selects); NaiveSequential is the untimed parity reference on every block; a 200 ms round cap keeps O(n²) algorithms measurable.
- **Tuning** (`tune_hyperparams.py`, ~1.6 h): per-algorithm knobs. Sequential thresholds were then re-measured paired in one process (cross-run drift biased them; see `ALGORITHMS.md`).
- **Result**: the island family's lead was implementation asymmetry. It now loses to Bitmap on every trace (Solana ≈ tie) and keeps a ×1.5–2.9 lead only at ≤ 0.3% density (§4).
- Archived variants reviewed, none revived (`internal/algorithms/archive/README.md`).

## 0. Changelog 2026-07-18 (post-professor-meeting cleanup)

- **Archived** IslandNaive, IslandTxnShattered, IslandObjectShattered, IslandDynamicShattered → `internal/algorithms/archive/` (build-ignored, revivable; see its README). Active set is now **Bitmap + IslandNativeShattered (frozen) + IslandTopologyShattered (champion)**.
- **Consolidated shared code**: all island-family helpers and ALL live hyper-parameters (`PairwiseIslandMax`, `SequentialBlockMax`, `ObjectHubThreshold`) now live in `island_shared.go`. `island_native_shattered.go` keeps only its own algorithm logic (one approved edit; deny rules restored after).
- `tune_hyperparams.py` retargeted at `island_shared.go`; its exit-restore values now match the tuned config (64/3000/10^9) so a tuning session can no longer silently downgrade the sources.
- Professor decisions: keep Eth trace as-is + add a no-hub variation (`internal/digestion/eth_hub_filter.go`); topology-output direction approved; heuristic v2 (write-weighted) gated on doc approval; anomalies (~280-txn Eth dip, Solana palpitation) under investigation via `analyze_anomalies.py`.
- Pre-refactor benchmark preserved as `benchmark_results_phase6_prerefactor.json`; fresh post-refactor run in `benchmark_results_postrefactor.json`.

## 1. Goal

Build undirected prestate conflict graphs (edge ⟺ W-W or R-W overlap; never R-R; deterministic; zero missed/extra edges) for blockchain blocks, faster than the brute-force Bitmap baseline, and in a shape that serves the downstream pipeline of `Papers/Parallel_SCT_Execution.pdf`: independent sub-graphs that can be graph-colored in parallel.

## 2. How the algorithms evolved (full lineage)

1. **NaiveSequential / NaiveParallel** — all-pairs `HasConflict` scans, O(n²·setSize). NaiveParallel ≈ the SCT paper's "Algorithm 5". Retired as baselines once beaten.
2. **Hash-map → IDBucket**: the spec's "concurrent hash-map object → (readers, writers)" was optimized away in three steps: (a) `Manager.Populate` interns address strings to dense `uint32` IDs once per block, so the hash-map becomes two plain slices indexed by objID — no hashing in the hot path; (b) a counting pre-pass `Preallocate`s each bucket exactly — no append growth; (c) edge generation is parallel and lock-free via row ownership (only record `txnIdx < peerIdx`). Still lost to Bitmap: it emits edges bit-by-bit (branchy) vs Bitmap's word-wide ORs. Retired.
3. **Bitmap (the baseline & parity oracle)** — per-object reader/writer bitsets over txns; each txn ORs whole 64-txn words into its matrix row. Pure contiguous memory math. Everything is verified bit-for-bit against it every run.
4. **Union-Find family** — partition txns into "islands" (connected components of the conflict relation) and build each island independently. Variants differ in *what counts as a hub*, *how islands are processed*, and *memory discipline*:
   - **IslandNaive**: no hubs, pairwise islands → giant hub-glued islands made O(k²) explode.
   - **TxnShattered**: hubs = huge *transactions* — wrong axis for our traces. Retired.
   - **ObjectShattered**: hubs = hot *objects* (>50 accesses); islands via bitmap-style sweeps; reintegrates hubs for ALL n txns.
   - **DynamicShattered**: sweep width shrinks to ceil(n/64); reintegration only for actual hub touchers.
   - **NativeShattered** (global-matrix output; frozen until 2026-09-28, then optimized in Phase 2): zero-allocation rewrite — reusable `BlockTopology` scratch, anchor-based union-find in O(accesses), lazy index, counting-sort grouping, size-2/pairwise/sweep dispatch, sequential path for small blocks. Output = global matrix. First to beat Bitmap (2026-07-06).
   - **TopologyShattered** (current champion): same front half; output = **per-island local bit-matrices + sparse hub-edge list**, no global matrix writes, local-coordinate index (sweeps shrink to ceil(k/64)). This IS the "Masked Island" deliverable the downstream coloring pipeline wants.

## 3. Harness & guarantees

`main.go` registers the timed algorithms (`newAlgorithms`: Bitmap, SortJoin, HybridJoin, IslandLean, IslandLeanHub, each with its own reusable scratch; `-algs` selects a subset; `-suite=uf` switches to the proving ground of `ufarena.go`, `-suite=cmp` to the competitors' `cmparena.go`) and `pipeline_utils.go` streams six traces (Ethereum, Ethereum without the fee hub, Solana, Sui, YCSB, TPC-C style). Every algorithm is instantiated 9 times (`scratchCopies`); per block and algorithm it records the **median of 9 rounds × 100 calls, one round on each copy** (after a GC, each round after an untimed warm-up call on its copy; calls slower than 2 ms get fewer iterations per round), so where an instance's scratch landed in memory averages out (`PHASE3_RESULTS.md` §4) into `benchmark_results.json`. On Windows the harness times with QueryPerformanceCounter, opts out of power throttling and pins to the performance cores (`bench_platform_*.go`; see `ANOMALIES.md`). Blocks larger than `MaximumGraphSize` are skipped. Every copy's output is compared bit-for-bit with **NaiveSequential**, run untimed on every block (mismatch = fatal); the island algorithms' topologies are reconstructed into a matrix untimed for this check. `go test ./...` runs the same check on seeded random blocks in under a second.
Tooling: `show_results.py` (text tables; `--league --baseline X` for the proving ground), `plot_metrics.py` (graphs, per-panel axes), `trace_statistics.py`, `tune_hyperparams.py` (Bitmap's knobs, one command, resumable).
Measurement caveat (2026-09-28): whole-run scores of unchanged code drift up to ×1.11 between runs on the laptop (more for parallel code), so compare algorithms within one run, and compare parallel against sequential paired within one process.

## 4. Current results (2026-10-02, after the Phase 4 cleanup; full traces, one run, on AC)

**Superseded:** the current numbers are in §0 (2026-10-13) and `UF_ROUND3_RESULTS.md` §1. This section is kept as the Phase 4 record.

Median per-block speed-up vs **Bitmap** (which includes the Phase 4 access filters), win rate in
parentheses; synthetic traces split by realized density; geo = geometric mean over the 10 columns:

| vs Bitmap | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| IslandLeanHub | ×0.97 (43%) | ×1.05 (69%) | ×1.93 (98%) | ×0.75 | ×2.55 / 1.21 / 0.74 | ×2.52 / 1.51 / 1.10 | ×1.30 |
| IslandLean | ×0.59 | ×0.93 | ×1.98 | ×0.75 | ×2.85 / 1.24 / 0.68 | ×2.91 / 1.56 / 1.03 | ×1.25 |
| IDBucket | ×0.08 | ×0.38 | ×1.07 | ×0.39 | ×0.57 / 0.69 / 0.31 | ×0.72 / 0.79 / 0.42 | ×0.46 |

Medians (µs) Bitmap / IslandLeanHub: Ethereum 2.48 / 2.62, no fee hub 1.98 / 1.88, Solana 77.2 / 36.5, Sui 0.18 / 0.22. In the Phase 4 run, Topology (the Phase 2 champion) ran at ×0.65 of the filtered Bitmap logic. Details, rounds, ablation and heuristics: `PHASE4_RESULTS.md`; plots `optimization_log/3_uf_proving_ground/Phase8_*`.

### Phase 2 results (2026-09-28; before = commit c9fdd2b code, after = Phase 2 code, run back to back on AC)

Median per-block speed-up vs Bitmap **in the same run** (>1 = faster than Bitmap):

| Trace | Native before → after | Topology before → after | IDBucket after | NaiveParallel after | NaiveSequential after |
|---|---|---|---|---|---|
| Ethereum | ×4.26 → ×0.49 | ×6.58 → ×0.47 | ×0.10 | ×0.08 | ×0.06 |
| Ethereum, fee hub removed | ×6.73 → ×0.67 | ×10.14 → ×0.67 | ×0.49 | ×0.05 | ×0.02 |
| Solana | ×3.60 → ×0.70 | ×5.80 → ×0.96 | ×1.06 | ×0.03 | ×0.01 |
| Sui | ×14.97 → ×0.55 | ×15.96 → ×0.52 | ×0.53 | ×0.49 | ×0.48 |
| YCSB | ×4.98 → ×0.63 | ×6.71 → ×0.64 | ×0.58 | ×0.04 | ×0.01 |
| TPC | ×5.77 → ×0.62 | ×6.56 → ×0.71 | ×0.71 | ×0.05 | ×0.01 |

Median ms after (Bitmap / Topology): Ethereum 0.00375 / 0.00781, no-hub 0.00293 / 0.00443, Solana 0.07014 / 0.07013, Sui 0.00027 / 0.00051, YCSB 0.02830 / 0.03275, TPC 0.05027 / 0.05613. Bitmap itself got ×6–34 faster; Topology ×1.0–1.2 (within run-to-run drift).
Where the old lead went (Bitmap ablation, same process): per-call allocation + 47-word rows ×2.5–8.1, per-call pool ×1.0–5.5 (largest on small blocks). What survives: at ≤ 0.3% density the island family beats Bitmap ×1.5–2.9 on YCSB/TPC at every size; the curves cross near 1% density. Plots: `optimization_log/2_equalized_baselines/Phase7_*`.

*History:* the 2026-07-11 grid search (old harness) reported Topology ×7.86 over Bitmap (Eth ×8.58, Sol ×7.21) at `pw=64, seq=3000, hub=OFF`; the Phase 1b hardened harness measured ×6.7 / ×5.5. Both used the unequal Bitmap.

## 5. Why "no shattering" won — the data findings (all verified on the traces)

1. **Ethereum conflict graphs are complete: density = 1.0000 on every block.** The block-builder fee account (e.g. `0x4838b106…` = Titan Builder) appears in the prestate diff of *every* transaction (fee credit → balance change → counted as a write). Every pair of txns therefore W-W conflicts. Consequences: (a) Eth "islands" only ever existed because hub-removal happened to cut the fee account; (b) hub reintegration then re-added ~12,700 dense fee edges per block through the *sparse* hub path — the worst representation for dense conflicts (bit-extraction + per-edge appends vs bulk row ORs). Turning hubs off just builds the dense graph with bulk ORs — hence Eth Native 0.043→0.012ms.
2. **On Solana, hub machinery was ~all waste:** 45.7% of hub objects are read-only (programs/sysvars) and 76.8% of ALL read accesses hit never-written objects — zero possible edges, zero unions — yet at hub=50 they made **99.8% of txns "hub touchers"**, each paying full-width hub sweeps. And islands stay small even *without* hub removal (largest: avg 298 / max 751 of ~1180 txns), because the would-be glue is read-only.
3. **The old "shattering wins" intuition dates from the pairwise-island era** (O(k²) `HasConflict` per island). Sweep-based island processing removed that cliff: a 750-txn island costs roughly what Bitmap pays for those rows, so preventing big islands no longer buys construction speed on real traces. (It may still matter for *downstream coloring parallelism* and for synthetic high-contention workloads — that's a scheduling-quality metric, not a construction-time one.)

**⚠ Discrepancy with the ICBC statistics paper** (`Papers/Ethereum_Statistics_ICBC.pdf`, same research group, same trace pipeline): the paper reports **median prestate density 0.017**, but its own method applied to our raw traces gives 1.0. Verified on both sides: the paper's open-source code (`github.com/dbiton/EthGrapher`, `parsers.py`) builds writes as `set(pre) ∪ set(post)` with **no coinbase/fee-recipient exclusion anywhere**, and our raw H5-derived traces have the fee recipient in pre+post of **245/245 txns of block 21297000** (balance-only delta = the fee credit). Most likely explanation: their figure-generating runs used a geth version whose `prestateTracer` diffMode did **not** yet include the coinbase fee credit (this geth behavior changed over versions), while the H5 we have does include it. Must be raised with the professor — it affects both papers' comparability and our Eth results.

## 6. Next steps (agreed 2026-07-11)

1. **Professor discussion (blocks everything Eth-related):**
   - Should the fee-recipient account be excluded in `eth_parser.go` (as real engines do — fee payments are commutative)? Without it, Eth graphs are complete and parallel execution is impossible by construction.
   - The density-0.017 vs 1.0 discrepancy above (geth prestate version?).
   - Which algorithms to keep long-term (plan: keep only Native as frozen reference + Topology; rename; retire the rest).
2. **Heuristic v2** (new file alongside Topology for comparison, *after* the cleanup/rename): (a) hub predicate = "object is *written* AND hot" — never hub a read-only object; (b) skip read accesses to never-written objects in all sweeps/index work via the existing `AnchorOf[obj]==0` check (single load, zero extra memory; skips ~77% of Solana read-access sweep work). Expected: keeps shattering available for genuinely hot *written* state without today's overhead.
3. **Synthetic scale benchmark** — the "does this solve a real problem" answer. The SCT paper: construction <0.25s at 3,500 txns (56 threads, synthetic e-commerce/p2p, 6ms/txn simulated execution) and calls DAG creation "a relatively major time consuming activity". Our Bitmap: ~0.6ms at 1,141 txns. The bottleneck is real but only bites at scale ⇒ generate YCSB-style zipfian blocks (5k–50k txns, conflict ratios 10–80% like the paper), raise `MaximumGraphSize` (breaks the 3000-txn assumption baked into `BucketSize` — audit every user), show where Bitmap's O(n²/64) matrix explodes (50k txns ≈ 312MB matrix) while Topology scales with output size.
4. **SOTA baselines** for the comparison table: (a) sort-based conflict join (sort all (obj, txn, isWrite) accesses, per-object emit writer×accessor pairs) — the classic DB-style approach, strong at scale; (b) label NaiveParallel as the SCT paper's Algorithm 5 in reports; (c) for the writeup only (they schedule without building graphs): Block-STM (optimistic execution + validation, Aptos), Aria's reservation tables, Solana Sealevel's per-account queues (≈ our IDBucket).
5. **Tuning refinements** when re-tuning after v2: record island stats (count/max size) per config so construction speed can be traded off against downstream coloring parallelism, not just raw time.

## 7. Key files

| | |
|---|---|
| Live algorithms | `internal/algorithms/` — `naive_sequential.go` (parity reference); `islands/` — `island_lean.go`, `island_lean_hub.go` (+ shared `island_shared.go`); `parity_test.go` (see `ALGORITHMS.md`) |
| Competitors | `internal/algorithms/competitors/` — `bitmap.go` (the baseline), `hybrid_join.go`, `sort_join.go`, shared `object_lists.go`; proving ground `cmparena.go` (`go run . -suite=cmp`) |
| Proving ground | `ufarena.go` (`go run . -suite=uf`); extra hub rules in `internal/algorithms/islands/uflab/hub_rules.go` |
| Archived algorithms | `internal/algorithms/archive/` — `islands/` (the UF lineage) and `competitors/` (NaiveParallel, IDBucket, HybridBitmap); build-ignored, the README documents revival |
| Data model & scratch | `internal/model/` (`island_topology.go` = island output + forest + index + live accesses; `inverted_index.go` = Bitmap's scratch; `constants.go` = MaximumGraphSize 3000); `internal/model/archive/` = BlockTopology, UnionFind, ObjectIdBucket |
| Trace digestion | `internal/digestion/` (eth/solana parsers; `eth_hub_filter.go` no-hub trace generator; h5 extractors in Python) |
| Harness | `main.go` (algorithm registry, `-algs`), `pipeline_utils.go`, `bench_platform_*.go`; pool in `internal/concurrency/worker_pool.go` |
| Tuning | `tune_hyperparams.py` → `tuning_results.json` (Bitmap's knobs; gitignored); island knobs via the proving ground |
| Analysis | `show_results.py` (`--league --baseline X` for the proving ground), `plot_metrics.py` (`--algs`), `trace_statistics.py` |
