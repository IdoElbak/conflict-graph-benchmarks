# What we tried: algorithms, heuristics, implementation ideas

A one-stop summary of every idea and its verdict. Ratios are median per-block speed-ups (geo
over the trace columns unless a column is named), from the report in brackets, where the full
evidence is. Reports: `[P2]` `../2_equalized_baselines/` (and `ANOMALIES.md`), `[P4]`
`../3_uf_proving_ground/PHASE4_RESULTS.md`, `[P3]` `../4_competitors/PHASE3_RESULTS.md`, `[N]`
`../5_uf_next/UF_NEXT_RESULTS.md`, `[R3]` `../6_uf_round3/UF_ROUND3_RESULTS.md`. Pseudo-code of
every algorithm, live and archived: `../ALGORITHM_LINEAGE.md`. Archived code:
`internal/algorithms/archive/`.

## 1. Construction algorithms

| Algorithm | Idea | Verdict |
|---|---|---|
| NaiveSequential | all pairs, `HasConflict` | the untimed parity reference |
| NaiveParallel | all pairs on the pool (≈ the SCT paper's Algorithm 5) | archived: O(n²), 10–100× slower |
| **Bitmap → BitmapTiny** | per-object writer/reader bitsets, each row the OR of its objects' bitsets | **live**, the baseline |
| IDBucket | the spec's "hash-map": per-object CSR lists, rows bit by bit (= row-wise Gustavson) | archived: ×0.46 vs Bitmap [P3] |
| SortJoin | the DB sort-based conflict join: counting sort by object, emit pairs | archived: ×0.6 vs Bitmap [P3, R3] |
| HybridBitmap | Roaring-style list-or-bitset per object | archived: ×1.00–1.02 vs Bitmap [P3] |
| **HybridJoin → HybridJoinTiny** | SortJoin + a word-wide dense kernel per hot object | **live**, the strongest competitor: ×1.20 vs Bitmap [P3] |
| TinyBitmap | Bitmap with one-word rows, no filter, for ≤ 64 txns | live kernel, given to everyone: ×1.8 vs Bitmap on Sui [N] |
| Gustavson SpGEMM, PPJoin, Four Russians, Roaring proper | from the Deep Research reports | rejected without a build: = IDBucket; nothing to prune at overlap 1; a probe saved ≤ 10% word loads; = HybridBitmap at our sizes [P3] |
| IslandNaive / TxnShattered / ObjectShattered / DynamicShattered | the July UF lineage | archived 2026-07: pairwise islands explode; wrong hub axis; allocation per call |
| IslandNativeShattered | zero-allocation UF, global matrix output | archived: its ×4–15 lead was Bitmap's handicap; ×0.49–0.70 vs a fair Bitmap [P2] |
| IslandTopologyShattered | Native with island graphs + hub edge list | archived: ×0.47–0.96 vs a fair Bitmap [P2]; its output contract lives on |
| IslandLean / IslandLeanHub | the Phase 4 rewrite (below), without / with the hub layer | archived: ×1.25 / ×1.30 vs Bitmap [P4]; IslandLeanHub only ×1.06 vs HybridJoin [P3] |
| NextM / NextB | IslandLeanHub + tiny path + dense fallback (+ bipartite hubs) | promoted after round 3 as **IslandHub / IslandBipartite** [N, R3] |

## 2. What made the island family fast (kept)

| Idea | Worth | Where |
|---|---|---|
| Two-pass partition (writes, then reads), each transaction's root tracked, islands read off circular member lists (no Find per txn, no sort), one-word rows for islands ≤ 64 | ×1.55 vs Topology | [P4] round 1 |
| Skip what cannot conflict: reads of never-written objects, objects one txn touches (given to Bitmap too: ×1.18 for it) | +20% | [P4] ablation |
| Pairwise `HasConflict` only for 3–4 members (one-word sweep from 5) | +4% (Sui ×1.6 at 5–16) | [P4] |
| Branch-free compaction of each island's live accesses | +16% | [P4] ablation |
| Hub layer as a bit-matrix over hub touchers (Topology: one 8-byte edge per conflict), with the complete-matrix shortcut for a hub written by everyone (Ethereum's fee recipient) | raw Ethereum ×2.3 vs Topology; shortcut +5% | [P4] |
| Parallel path in ONE pool pass, only above 300 txns and 8000 words of island matrices | +3–5% on dense synthetic; always-parallel ×0.72 | [P4] |
| Tiny path: ≤ 64 txns with repeated objects → TinyBitmap whole | Sui ×0.72 → ×1.65 vs HybridJoin (×0.94 vs HybridJoinTiny, which got the same kernel) | [N] §3.1 |
| Dense fallback: ≥ 512 txns and ≥ 50% of the prefix writes a hub → HybridJoin whole | YCSB ≥10% ×0.63 → ×0.93–0.99 vs HybridJoin | [N] §3.2, [R3] |
| Giant-island kernel: islands ≥ 30% of the block built object-major (HybridJoin's kernel) | YCSB 1–10% ×1.02 → ×1.12 | [R3] |
| Bipartite hub layer (per-hub writer/reader lists) | ×1.03–1.04 over the matrix layer, Ethereum ×1.15; contract change | [N] §3.5 |

## 3. Heuristics

**Hub detection** (which objects stay out of the partition) [P4 §4, N §3.5–3.6]:

| Rule | Result |
|---|---|
| **prefix: written by ≥ k of the first P txns** | the only rule that pays overall (a few hundred operations). Kept: 64/3 (64/4 for IslandLeanHub) |
| whole-block counts: accesses, writers, write-weighted, split read/write, relative to n | ×0.75–0.89 vs no hubs: the counting pass costs more than smaller islands save on sparse blocks |
| sampled (every 4th txn) | ×0.96 |
| Space-Saving sketch | ×0.37–0.82 |
| reads count toward the prefix (read-hot objects as hubs) | loses |
| fused prefix (counts kept in the anchors) | within noise |
| no hubs below 64 transactions | kept |

The aggressive rules (writers > 4, write-weighted 32) cut the largest island much further (YCSB 86%
→ 47–59% of the block) at ×0.83–0.89 construction speed: the knob if downstream coloring
parallelism matters more than construction time.

**Dense-block detection** [N §3.2, R3 §3.3]:

| Detector | Result |
|---|---|
| **share of the 64 prefix txns writing a hub ≥ 0.5, blocks ≥ 512** | kept; free when it says no. Misfires on some Solana blocks |
| sampled density (one-word kernel on 32–64 strided txns) | works, but costs 10–18% on sparse blocks |
| two-stage (share gate, then prefix density) | closes the 10–20%-density gap, misfires on Solana; ×1.28–1.30 vs ×1.31 |
| prefix giant component, scaled prefix degree, share without near-universal hubs, prefix hub count | none separates dense TPC from Ethereum |
| no fallback (the giant kernel instead) | better everywhere except dense YCSB (×0.75); a trade |

**Tiny-block selector** [N §3.1]: repeated accesses (Σ accesses − distinct objects) ≥ 0.5·n. Objects per
transaction cannot replace it (Sui and sparse synthetic tiny blocks overlap).

## 4. Implementation ideas rejected

- Branch-free batched partition (the first-touch branch mispredicts): ×0.85–0.90 [R3 §3.1].
- Deciding the fallback before clearing the anchors: neutral [R3 §3.2].
- Concurrent CAS union-find (Jayanti–Tarjan style): Solana ×0.56 on 6 cores; a Phase 5 candidate [N §3.4].
- RCM member reordering, transaction vertex cuts: probes put the ceiling under 5% [N §3.7–3.8].
- Compacting Bitmap's rows branch-free: ×0.85 for Bitmap [P4].
- The object-major kernel on every wide island (not only giants): neutral, −7% on Solana [P3, N].

## 5. Measurement lessons

- **Fairness first:** an unequal baseline produced the whole ×5–15 "island lead" of July
  (allocation per call, 47-word rows, a pool per call) [P2].
- Windows timer, power throttling and hybrid cores moved times ×3: QPC timer, P-core pinning,
  throttling off [ANOMALIES].
- Scratch placement: ±15% on small blocks → 9 scratch copies per algorithm [P3 §4].
- Code layout: ±10% → three layout-perturbed builds per final number [N §2.1].
- Whole runs drift ×1.1 → compare within one run [P2].
- go-test benchmarks (not pinned) moved ±30%: profiles only [R3 §4].
