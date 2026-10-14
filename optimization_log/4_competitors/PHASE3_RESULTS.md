> **Historical record** of the Phase 3 (competitors) results, moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# Phase 3 results — competitor algorithms (2026-10-07)

Input: the two Gemini Deep Research reports (`gemini_search/` at the project root; the questions
are in `GEMINI_2_NEW_ALGORITHMS.md` and `GEMINI_3_UF_HEURISTICS.md`), checked against our code and
data, plus our own probes. Code: `internal/algorithms/competitors/` (own package); pseudo-code in
`ALGORITHMS.md` §5; knobs tuned in the competitor proving ground, `go run . -suite=cmp`
(`cmparena.go`).

## Update 2026-10-11: the field cut to three, and the placement fix

Both decisions of §4 and §5 were approved and applied:
- **Archived** IDBucket and HybridBitmap (`internal/algorithms/archive/competitors/`). The main suite is now Bitmap, SortJoin, HybridJoin, IslandLean and
  IslandLeanHub. With HybridBitmap gone, `objectSpan` lost its `dense` field (16 → 12 bytes per
  object), which SortJoin and HybridJoin share.
- **Harness:** every algorithm is instantiated 9 times; per block, one 100-call round runs on each
  copy (each after its own warm-up call) and the median is recorded. Every copy is parity-checked.
  See §4 for the validation.

Definitive run (`benchmark_results.json`, all six traces, ~4 min). Median per-block speed-up, win
rate in brackets:

| vs Bitmap | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| **HybridJoin** | ×1.02 | ×1.03 | **×1.65** (94%) | ×1.01 | ×1.11 / 1.24 / **1.18** | ×1.33 / 1.33 / **1.30** | **×1.20** |
| SortJoin | ×0.13 | ×0.75 | ×1.43 | ×0.82 | ×1.09 / 0.97 / 0.33 | ×1.32 / 1.13 / 0.47 | ×0.70 |
| IslandLean | ×0.62 | ×0.95 | ×1.82 | ×0.73 | ×2.58 / 1.26 / 0.67 | ×3.11 / 1.62 / 1.04 | ×1.25 |
| **IslandLeanHub** | ×1.01 | ×1.09 | **×1.78** | ×0.74 | **×2.43 / 1.25** / 0.71 | **×2.66 / 1.55** / 1.07 | **×1.30** |

| vs HybridJoin | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| IslandLeanHub | ×0.99 | ×1.05 | ×1.09 (84%) | ×0.74 | **×2.10** / 0.99 / **0.61** | **×1.85 / 1.09** / **0.85** | ×1.06 |

What changed against the 2026-10-07 runs below: Bitmap's own times barely moved, but HybridJoin
went from ×1.10 to **×1.20** over it, and IslandLeanHub's lead over HybridJoin shrank from ×1.16
to **×1.06**. Two causes, measured separately on Solana, YCSB and TPC (two processes per variant):
- the new harness: HybridJoin with the old 16-byte spans measured ×1.23–1.24 geo on those columns,
  against ~×1.15 in the old runs. The single-instance harness had under-measured it;
- the 12-byte spans: another ×1.04–1.07 (×1.27–1.33), within the run-to-run noise of these
  2-algorithm runs (Solana alone moved ×1.62–1.88 between processes).

**What the paper can now say** (replaces the list below):
- **The island family and HybridJoin split the field.** Overall IslandLeanHub leads ×1.06, which is
  not a decisive lead: it comes from the sparse regime.
- **Islands win clearly** on sparse blocks (×1.85–2.1 below 1% density) and modestly on Solana
  (×1.09) and medium-density TPC (×1.09).
- **HybridJoin wins** above 10% density (YCSB ×0.61, TPC ×0.85 for the islands) and ties at 1–10%
  on YCSB. In the contention figure the curves cross at ~3% density for 1000–2000 transactions and
  at 3–10% for 200–999.
- **Ethereum is a tie** (HybridJoin and IslandLeanHub ×1.0 of Bitmap); **Sui** belongs to Bitmap's
  one-word rows (HybridJoin runs that code there), and the islands lose ×0.74.

Figures (regenerated from this run): `optimization_log/4_competitors/Phase9_competitors_by_size.png`,
`optimization_log/4_competitors/Phase9_competitors_contention.png`.

## Summary of the 2026-10-07 runs (old harness, seven algorithms; superseded by the update above)

Main suite, all six traces, every block parity-checked against NaiveSequential, **run twice** in
separate processes (`benchmark_results_phase3_run1.json`, `benchmark_results_phase3_run2.json`).
Median per-block speed-up vs Bitmap, with win rate, run 1 / run 2 where they differ by more than
0.03:

| vs Bitmap | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| **HybridJoin** (new) | ×1.00 | ×1.00 | **×1.47 / 1.53** (91%) | ×1.14 / 1.02 † | ×1.02–1.05 / 1.00–1.07 / **1.05–1.07** | **×1.17 / 1.07–1.10 / 1.06–1.10** | **×1.10** |
| HybridBitmap (new) | ×1.00 / 0.97 | ×0.98 | ×1.18 / 1.23 | ×1.06 / 1.01 † | ×0.93–1.01 / 0.95–0.99 / 0.92–0.98 | ×1.03 / 1.01 / 1.01 | ×1.00 / 1.02 |
| SortJoin (new) | ×0.11 | ×0.64 / 0.69 | ×1.37 / 1.35 | ×0.55 / 0.79 | ×0.90–0.95 / 0.82–0.85 / 0.28–0.32 | ×1.16 / 0.88 / 0.39 | ×0.58 / 0.62 |
| IDBucket | ×0.08 | ×0.36 | ×1.01 / 1.03 | ×0.44 | ×0.55–0.59 / 0.69–0.76 / 0.33–0.35 | ×0.69–0.77 / 0.74–0.78 / 0.39–0.44 | ×0.46 |
| IslandLean | ×0.60 | ×0.95 | ×1.74 / 1.77 | ×0.88 / 0.78 | ×2.95 / 1.23–1.26 / 0.68 | ×2.90 / 1.35–1.39 / 0.97 | ×1.22–1.24 |
| **IslandLeanHub** | ×1.00 | ×1.06 | **×1.70 / 1.76** | ×0.87 / 0.75 | **×2.56–2.83 / 1.22–1.34** / 0.69 | **×2.59–2.70 / 1.34–1.43** / 1.05 | **×1.30** |

† Sui blocks have one-word rows, so HybridBitmap and HybridJoin run Bitmap's own code there: these
differences are the scratch-placement effect described below, not algorithmic.

**IslandLeanHub vs HybridJoin**, the strongest competitor (same runs):

| | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| IslandLeanHub | ×1.00 | ×1.06 | ×1.12 (89%) | ×0.73–0.76 | ×2.66–2.76 / 1.20–1.23 / **0.65–0.67** | ×2.02–2.10 / 1.22–1.23 / **0.94–0.95** | ×1.16–1.17 |

**What the paper can now say:**
- **The island family still leads overall** (×1.16 over the best competitor), but by less than
  over Bitmap (×1.30): HybridJoin closes most of the gap on Solana (islands ×1.12, was ×1.7) and
  overtakes the islands on dense blocks.
- **Islands win** on sparse and medium blocks (×2–2.8 below 1% density, ×1.2 at 1–10%) and on
  large real blocks (Solana ×1.12).
- **Islands lose** to HybridJoin above 10% density on both synthetic families (zipfian YCSB
  ×0.66, TPC ×0.95) and on tiny blocks (Sui, where nothing beats Bitmap's one-word rows).
- **Ethereum is a tie for everyone**: its ~160-transaction blocks have 3-word rows, where
  HybridBitmap and HybridJoin fall back to Bitmap's code, and IslandLeanHub's hub layer exactly
  pays for itself.

(The Phase 9 figures were regenerated from the 2026-10-11 run.)

## 1. Candidates: what was taken from the reports, and what was not

**Report A** (new algorithms) ranked five candidates. Checked one by one:

| Candidate (report A rank) | Verdict | Why |
|---|---|---|
| 1. Gustavson SpGEMM with a sparse accumulator (SPA) | **already have it** | IDBucket *is* row-by-row Gustavson with the output row as a bit-vector accumulator. The report's claim that it "replaces IDBucket's counting sort" is wrong: the counting sort builds the transposed operand, which Gustavson needs too. |
| 2. Frequency-ordered PPJoin+ (prefix filtering) | **rejected** | Prefix filtering prunes pairs that cannot reach an overlap threshold t. Here any single shared object is an edge (t = 1), so the prefix is the whole set and nothing is pruned (the report notes this, then proposes the method anyway). |
| 3. Four Russians (M4RM) | **rejected by a probe** | Counted the word loads of M4RM tables (8-object stripes over the hottest live objects, Gray-code tables) against Bitmap's sweep on the real data. The best stripe count saves only 10% on YCSB and costs more everywhere else (Ethereum ×1.2–6, Solana ≈ equal, TPC ≥ 0.98): a transaction almost never touches two hot objects of one stripe, so a table lookup replaces one OR, not eight. The report's "eightfold reduction" assumes dense rows. |
| 4. Wait-free CAS union-find | **a Union-Find idea, not a competitor** | Left for the Union-Find step (`PHASE4_RESULTS.md` §7: break-even on 6 cores; revisit on the server). |
| 5. Roaring bitmaps | **implemented as HybridBitmap** | Blocks of ≤ 2,000 transactions fit inside one 65,536-entry Roaring chunk, so Roaring reduces to its container choice per object: a sorted array or a bitmap. That choice is HybridBitmap. |

The report's related work (Monad/Sei OCC, Calvin, BOHM, Aria, Fabric++/FabricSharp) is scheduling
or reordering, out of scope as competitors. One item matters for the paper: **Tiga** (adaptive
two-level parallel execution) decomposes the dependency graph into connected components with a
union-find, which is close to our partition. It must be cited as related work for the island
idea.

**Our own candidates:**
- **SortJoin**, the classical DB sort-based conflict join (named in the plan): counting-sort the
  accesses by object, then emit writer × accessor pairs per object (object-major, the outer-product
  form of W·Wᵀ ∨ W·Rᵀ ∨ R·Wᵀ).
- **HybridJoin**, our hybrid of SortJoin and Bitmap, built after the first SortJoin results: per
  object, the cheaper of pair emission and word-wide ORs of the object's bitsets, which are built in
  two L1-resident scratch rows. In SpGEMM terms it chooses a sparse or a dense accumulator per unit
  of work.

## 2. How the competitors were tuned (`-suite=cmp`, 300-block cap, three sweeps)

1. **HybridBitmap, first version** (row-major; per-object lists from the counting sort, bitsets
   for dense objects): Solana ×1.21–1.25, but ×0.6–0.9 at 256–1,023 transactions. There the build
   dominates: Bitmap spends 57% of its time in build + wipe and only 20% sweeping. The fix was
   `MinWords` = 8: below 8-word rows it runs Bitmap's code.
2. **HybridBitmap, second version**, which kept Bitmap's build and extracted each sparse shared
   object's list from its bitsets: **×0.64 on Solana**. The popcount and extraction passes over
   every shared object's bitsets cost more than the sweep saves. Reverted to the first version.
3. **SortJoin**: the parallel path (objects split among workers, atomic ORs) lost on every column
   (×0.08–0.9). Sequential SortJoin, the default now, beat Bitmap on Solana (×1.37–1.47) and on
   sparse TPC (×1.26–1.34), and lost badly on hot objects (Ethereum ×0.13, dense YCSB ×0.3). That
   pattern led to HybridJoin.
4. **HybridJoin**: the knob sweep was nearly flat. DenseFactor 0.25–2 and MinWords 2–8 all fell
   within ×1.08–1.12 geo; DenseFactor 4 and MinWords 1 were worse (×1.06). The defaults are
   DenseFactor 1, MinWords 4.

Knob tables are in `ALGORITHMS.md`.

## 3. Fairness transfers

Every trick that applies to another algorithm was given to it and measured:
- **Per-object container choice → Bitmap.** That is HybridBitmap itself, kept as its own algorithm
  so that Bitmap stays the fixed baseline. It ties Bitmap overall (×1.00–1.02): it wins on Solana
  (×1.2) and loses a little on YCSB.
- **HybridJoin's object-major kernel → the island family's wide islands** (`IslandConfig.JoinDense`,
  `island_shared.go`; UF proving ground, `go run . -suite=uf`). Every accessor of a live
  object is in the object's island, so each wide island's compacted live accesses are
  counting-sorted by object and emitted in local coordinates. Measured against IslandLean:

  | Config | Ethereum | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
  |---|---|---|---|---|---|---|---|
  | Lean-join1 | ×1.05–1.06 | ×1.01–1.04 | ×0.92–0.93 | ×0.99 | ×1.01–1.06 / 1.09–1.10 / 1.00–1.02 | ×1.00–1.03 / 1.03–1.04 / 0.97–1.01 | ×1.01–1.03 |
  | Lean-join1-w8 (islands of ≥ 8-word rows only) | ×1.00 | ×0.98 | ×1.00 | ×1.00 | ×1.02 / 1.03 / 1.01 | ×1.02 / 1.03 / 0.99 | ×1.01 |
  | Hub-join1 vs Hub-prefix64-4 | ×1.00 | ×1.00 | ×0.93 | ×0.94 | ×1.03 / 1.10 / 1.03 | ×0.99 / 1.02 / 1.01 | ×1.01 |

  **Neutral.** The island family's wide rows are already narrow (Solana's widest islands have
  ~5-word rows), so the index sweep it replaces is cheap and the counting sort costs as much as it
  saves. The knob stays off by default and stays in the proving ground for the many-core re-tune.
- Not transferable: SortJoin's and HybridJoin's object-major emission needs no partition. The
  island family's narrow per-island rows need one.

## 4. A measurement finding: scratch placement shifts small-block timings

Three *identical* Bitmap instances, registered side by side in one run, measured ×0.83–1.06 of each
other on the Ethereum traces (2–3 µs blocks) with consistent win rates (7–16%, so not noise). On
Solana-size blocks they agreed within 1%. The slow instance changed when the registration order
changed. When the three shared one graph and one index, they measured ×1.00 (50% win rate) in two
runs. So each algorithm's **scratch memory placement** adds a per-instance, per-process bias of up
to ~±15% on ~2 µs blocks. The likely cause is cache-set or 4K aliasing between an algorithm's
index and output arrays. It also plausibly explains the whole-run drift of ×1.1 measured in
Phase 2.

Consequences:
- Differences under ~15% on Ethereum and Sui are not evidence either way: IslandLeanHub's ×0.97 vs
  ×1.00 on Ethereum, and HybridJoin's ×1.14 on Sui while it runs Bitmap's code, are examples.
- Solana and the synthetic traces from ~200 transactions up are unaffected.
- **Fixed 2026-10-11**: the harness now times one round on each of 9 scratch copies of every
  algorithm and records the median. The same three identical Bitmaps (Ethereum, Sui; 400 blocks;
  three processes each) then agree within ×0.98–1.03. With 5 copies Ethereum agreed within 3%, but
  Sui still split up to ×1.14, so 9 it is. Runs take ~1.8× as long per algorithm.

## 5. Which competitors to keep (the plan: best three; applied 2026-10-11)

| Algorithm | geo vs Bitmap | Dominated by? |
|---|---|---|
| HybridJoin | ×1.10 | — |
| HybridBitmap | ×1.00–1.02 | HybridJoin on every column except within placement noise |
| Bitmap | ×1.00 | — (the baseline) |
| SortJoin | ×0.58–0.62 | HybridJoin everywhere; it is HybridJoin without the dense kernel |
| IDBucket | ×0.46 | SortJoin and HybridJoin everywhere |

Recommendation: keep **Bitmap** (the baseline), **HybridJoin** (the strongest), and **SortJoin**
(the classical literature algorithm, and the cleanest illustration of object-major emission). Archive
HybridBitmap and IDBucket with a note. **Approved and done 2026-10-11** (archive READMEs say how to
revive them).
