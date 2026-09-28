# Project State — Conflict Graph Construction Benchmarks

*Snapshot: 2026-07-18 (cleanup applied after the professor meeting; previous snapshot 2026-07-11). Companion file: `ALGORITHMS.md` (pseudo-code for every algorithm).*

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
   - **NativeShattered** (frozen, deny-protected): zero-allocation rewrite — reusable `BlockTopology` scratch, anchor-based union-find in O(accesses), lazy index, counting-sort grouping, size-2/pairwise/sweep dispatch, sequential path for small blocks. Output = global matrix. First to beat Bitmap (2026-07-06).
   - **TopologyShattered** (current champion): same front half; output = **per-island local bit-matrices + sparse hub-edge list**, no global matrix writes, local-coordinate index (sweeps shrink to ceil(k/64)). This IS the "Masked Island" deliverable the downstream coloring pipeline wants.

## 3. Harness & guarantees

`main.go` → `pipeline_utils.go`: streams six traces (Ethereum, Ethereum without the fee hub, Solana, Sui, YCSB, TPC-C style), and per block and algorithm records the **median of 5 rounds × 100 calls** (after one warm-up call and a GC) into `benchmark_results.json`. Since Phase 1b the harness times with QueryPerformanceCounter on Windows, opts out of power throttling and pins to the performance cores (`bench_platform_*.go`; see `ANOMALIES.md` for why). Blocks larger than `MaximumGraphSize` are skipped. Every algorithm's graph is compared word-for-word to Bitmap's (`AreGraphsEqual`; mismatch = fatal). Topology's output is reconstructed into a matrix *untimed* for this check. `island_native_shattered.go` is protected by deny rules in `.claude/settings.json` (frozen reference).
Tooling: `show_results.py` (text tables), `plot_metrics.py` (graphs), `trace_statistics.py`, `tune_hyperparams.py` (grid search, resumable, restores the tuned consts on exit), `analyze_anomalies.py` (raw per-block diagnostics).

## 4. Current results

**Baseline constants (pw=6, seq=384, hub=50):** Topology beats Bitmap ×1.85 (Eth) / ×1.94 (Sol) and Native ×1.15 / ×1.30, parity-verified on all 1100 blocks.

**Hyperparameter tuning (96 configs, ~16h, every run parity-verified):** best = `pw=64, seq=3000, hub=OFF` → score ×7.86 (Eth ×8.58, Sol ×7.21 vs Bitmap). Median times at the best config: Eth — Native 0.0120ms, Topology 0.0080ms (vs 0.0431/0.0362 at baseline); Sol — Native 0.1188ms, Topology 0.0740ms. Clear signal: **disabling hub-shattering and running sequentially made everything much faster**, which contradicted the old intuition — and led to the data findings below.

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
| Live algorithms | `internal/algorithms/*.go` — Bitmap, Native (frozen), Topology; shared helpers + hyper-params in `island_shared.go` (see `ALGORITHMS.md`) |
| Archived algorithms | `internal/algorithms/archive/` (build-ignored; README documents revival) |
| Data model & scratch | `internal/model/` (`island_graph.go` = BlockTopology; `constants.go` = MaximumGraphSize 3000) |
| Trace digestion | `internal/digestion/` (eth/solana parsers; `eth_hub_filter.go` no-hub trace generator; h5 extractors in Python) |
| Harness | `main.go`, `pipeline_utils.go` |
| Tuning | `tune_hyperparams.py` → `tuning_results.json` (96 completed configs) |
| Analysis | `show_results.py`, `plot_metrics.py`, `trace_statistics.py`, `analyze_anomalies.py` |
