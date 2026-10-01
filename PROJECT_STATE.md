# Project State — Conflict Graph Construction Benchmarks

*Snapshot: 2026-09-28 (end of Phase 2; §0 has the changelog). Companion files: `ALGORITHMS.md` (pseudo-code, knobs), `ANOMALIES.md` (measurement investigation), `PHASE4_DIRECTIONS.md` (next steps).*

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

`main.go` registers the algorithms (`newAlgorithms`, each with its own reusable scratch; `-algs` selects a subset) and `pipeline_utils.go` streams six traces (Ethereum, Ethereum without the fee hub, Solana, Sui, YCSB, TPC-C style). Per block and algorithm it records the **median of 5 rounds × 100 calls** (after one warm-up call and a GC; calls slower than 2 ms get fewer iterations per round) into `benchmark_results.json`. On Windows the harness times with QueryPerformanceCounter, opts out of power throttling and pins to the performance cores (`bench_platform_*.go`; see `ANOMALIES.md`). Blocks larger than `MaximumGraphSize` are skipped. Every algorithm's output is compared bit-for-bit with **NaiveSequential**, run untimed on every block (mismatch = fatal); Topology's output is reconstructed into a matrix untimed for this check.
Tooling: `show_results.py` (text tables), `plot_metrics.py` (graphs, per-panel x axes), `trace_statistics.py`, `tune_hyperparams.py` (short tuning pass, one command, resumable).
Measurement caveat (2026-09-28): whole-run scores of unchanged code drift up to ×1.11 between runs on the laptop (more for parallel code), so compare algorithms within one run, and compare parallel against sequential paired within one process.

## 4. Current results (Phase 2, 2026-09-28; before = commit c9fdd2b code, after = Phase 2 code, run back to back on AC)

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
Where the old lead went (Bitmap ablation, same process): per-call allocation + 47-word rows ×2.5–8.1, per-call pool ×1.0–5.5 (largest on small blocks). What survives: at ≤ 0.3% density the island family beats Bitmap ×1.5–2.9 on YCSB/TPC at every size; the curves cross near 1% density. Plots: `optimization_log/Phase7_*`.

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
| Live algorithms | `internal/algorithms/*.go` — Bitmap, IDBucket, NaiveParallel, NaiveSequential, Native, Topology; each declares its own knobs; island-family helpers + hub knobs in `island_shared.go` (see `ALGORITHMS.md`) |
| Archived algorithms | `internal/algorithms/archive/` (build-ignored; README documents revival) |
| Data model & scratch | `internal/model/` (`island_graph.go` = BlockTopology; `constants.go` = MaximumGraphSize 3000) |
| Trace digestion | `internal/digestion/` (eth/solana parsers; `eth_hub_filter.go` no-hub trace generator; h5 extractors in Python) |
| Harness | `main.go` (algorithm registry, `-algs`), `pipeline_utils.go`, `bench_platform_*.go`; pool in `internal/concurrency/worker_pool.go` |
| Tuning | `tune_hyperparams.py` → `tuning_results.json` (Phase 2 short pass, 22 runs; gitignored) |
| Analysis | `show_results.py`, `plot_metrics.py`, `trace_statistics.py` |
