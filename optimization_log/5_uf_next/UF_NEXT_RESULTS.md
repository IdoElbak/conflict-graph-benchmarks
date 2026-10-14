> **Historical record** of the UF next round results, moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# UF next round — Union-Find improvements from report B and our own (2026-10-11)

The island family after Phase 4 (IslandLean, IslandLeanHub) led the best competitor, HybridJoin, by
only ×1.06–1.07 geo. This round tried every Union-Find idea of Gemini report B
(`gemini_search/Union-Find Algorithm Improvements.docx`) plus our own, in a new package
`internal/algorithms/islands/ufnext/`. IslandLean and IslandLeanHub are untouched for comparison.

**Status (2026-10-13): resolved by UF round 3** (`UF_ROUND3_RESULTS.md`). NextM and NextB, with the
giant-island kernel and the fallback from 512 transactions, were promoted as IslandHub and
IslandBipartite. Everything else, including `ufnext/` itself, is archived. The paths below that
name `islands/ufnext/` or `uflab/` are now under `internal/algorithms/archive/islands/`.

## 1. Headline

Round 5: three code layouts, 300 blocks per trace, every block parity-checked. Each cell is the
median per-block speed-up vs **HybridJoin**, as a geometric mean over the layouts. The geo spread
over the layouts was ±0.01. Rounds 6 and 7 replicate it: NextB ×1.29–1.31, NextM ×1.23–1.25,
Hub ×1.07–1.08.

| | Eth | Eth no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo vs HybridJoin | geo vs HybridJoinTiny |
|---|---|---|---|---|---|---|---|---|
| Hub (IslandLeanHub, today) | ×1.00 | ×1.06 | ×1.07 | ×0.72 | ×2.22 / 1.01 / 0.63 | ×1.81 / 1.09 / 0.86 | ×1.07 | ×0.96 |
| HybridJoinTiny (fair bar) | ×1.00 | ×1.00 | ×1.00 | ×1.82 | ×1.04 / 1.05 / 1.04 | ×1.02 / 1.02 / 1.02 | ×1.08 | — |
| **NextM** (matrix hub layer: contract unchanged) | ×1.01 | ×1.09 | ×1.06 | ×1.65 | ×2.18 / 1.03 / 0.97 | ×1.87 / 1.13 / 0.98 | **×1.24** | **×1.13** |
| **NextB** (bipartite hub layer, prefix 64/3) | ×1.16 | ×1.25 | ×1.08 | ×1.65 | ×2.23 / 1.18 / 0.99 | ×1.84 / 1.14 / 1.01 | **×1.31** | **×1.18** |

Both finalists combine:
- **the tiny path**: TinyBitmap whole-block for ≤ 64 transactions, when accesses − distinct
  objects ≥ 0.5 per transaction (§3.1);
- **the dense fallback**: HybridJoin whole-block when ≥ 50% of the 64-transaction prefix writes a
  hub, from 256 transactions (§3.2);
- **the island core** of IslandLeanHub, unchanged.

NextB adds the bipartite hub layer with a more aggressive prefix rule (§3.5). Against the fair bar
they lose only where they become HybridJoin or TinyBitmap: the dense columns and Sui, ×0.93–0.98,
which is the wrapper overhead. Everywhere else they win or tie.

Median time per block (µs, layout L7):

| | Eth | no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% |
|---|---|---|---|---|---|---|
| HybridJoin | 2.67 | 2.15 | 38.8 | 0.19 | 10.9 / 17.1 / 18.6 | 21.1 / 27.6 / 23.3 |
| HybridJoinTiny | 2.66 | 2.15 | 38.7 | 0.10 | 10.7 / 17.1 / 17.1 | 21.5 / 27.2 / 24.1 |
| Hub | 2.63 | 2.00 | 37.6 | 0.24 | 5.09 / 16.1 / 34.8 | 9.25 / 26.5 / 29.9 |
| NextM | 2.61 | 1.92 | 37.5 | 0.11 | 5.04 / 14.8 / 18.3 | 9.31 / 24.7 / 27.6 |
| NextB | 2.21 | 1.67 | 37.0 | 0.11 | 5.34 / 15.2 / 19.4 | 9.80 / 25.9 / 25.5 |

Figures: `optimization_log/5_uf_next/Phase10_uf_next_by_size.png`, `Phase10_uf_next_contention.png`.
Results files (gitignored): `benchmark_results_ufnext_r1…r7*.json`. Rounds 3–7 have `_L<seed>`
per layout.

Read it as:
- **Bipartite** = report B's bipartite hub output, which changes the output contract (§3.5).
  **NextM** keeps the matrix hub layer, so it is the contract-preserving variant.
- All numbers are layout-averaged (§2): the geometric mean over 3 builds whose code sits at
  different addresses.
- **HybridJoinTiny** is the fair bar. It is HybridJoin with the new tiny-block kernel, the fairness
  counterpart of the islands' tiny path (§3.1).

## 2. Methodology changes found on the way (important for the paper)

### 2.1 Code layout moves results by ±10% on the P-cores

The forked island core (`Next` with every experiment off) runs the same loops as IslandLeanHub. In
one build it measured ×0.87 vs Hub on Ethereum, with a 3% win rate. Then:
- **A verbatim copy of the original partition** in the fork: ×1.02.
- **The rewritten partition** in a go-test binary (not P-core pinned): it profiled equal to the
  original (10.45 s vs 10.49 s).
- **An unused function added elsewhere in the package**: the same `Next` went from ×0.98 / ×0.89 to
  ×1.02 / ×1.02 (Ethereum / EthereumNoHub).
- **Two registrations of identical code** (Hub vs Hub-prefix64-4) measure ×1.00, so the effect is
  per code address, not per instance. The 9 scratch copies average out memory placement, not code
  placement.

The P-cores' front end is sensitive to where the tiny hot loops land. This is the
"measurement bias from code layout" result of Mytkowicz et al. (ASPLOS 2009).

Consequences:
- A difference under ~10% between two *different* pieces of code is not evidence from one build.
  That may apply to some earlier "neutral / ±5%" verdicts, such as Phase 3's JoinDense.
- **Protocol from round 3 on:**
  - build 3 binaries, each adding a differently sized unused function at the front of four
    algorithm packages (removed after the build);
  - run all three;
  - `python layout_league.py --baseline X FILE...` prints the geometric mean over the layouts and
    the spread.

  After averaging, the spread of the geo was ±0.01.
- Code shape matters too. Folding the experiments into one partition loop slowed the default path
  by 13%, so every experiment has its own loop (`next_hub.go`).

### 2.2 Parity cannot catch a performance bug

Round 3's dense detector had a bug: it re-zeroed the counts while scanning them, so it counted
distinct hubs, not hub writers. It never fired, and it stayed bit-exact. Fallback rates are now
checked from the island statistics: a whole-block island has count 1 and size n.

### 2.3 The go-test trace benchmarks are profiling tools only

`ufnext/trace_bench_test.go` loads real blocks for `go test -bench` + pprof. It is not pinned to
P-cores: identical code differed ×1.5 there on Solana. Use it for profiles only.

## 3. What was tried

Columns are median per-block speed-up vs HybridJoin, layout-averaged unless marked "1 build".

### 3.1 Tiny blocks (Sui): one-word kernel — **keep** (report B's "Bitmap bypass", made a new kernel)

Profiling Sui (~10 transactions): partition 29%, compaction 15%, one-word kernel 14%. A
partition can only add work when every row is one word. `competitors/tiny_bitmap.go`
(**TinyBitmap**) is Bitmap specialized to ≤ 64 transactions:
- **no shared-object filter.** At one word, the filter costs more than the ORs it saves, and it is
  not needed for correctness: a private object's bits are the transaction's own, cleared with the
  self bit, and a never-written object has no writer bits to give;
- **one pass sets both index rows**, a second pass sweeps;
- **the index is cleared word-wide**, or wiped per access when the block has more than 16 objects
  per transaction.

| | Sui |
|---|---|
| Bitmap / HybridJoin (HybridJoin runs Bitmap's code there) | ×1.00 |
| **BitmapTiny / HybridJoinTiny** (fairness counterparts) | **×1.80** |
| islands with the tiny path (Next/NextA, TinyMax 64) | ×1.71–1.74 |

So on Sui the islands now tie the fair competitor. The new kernel is a contribution of its own:
×1.8 over Bitmap on tiny blocks.

The tiny path costs on tiny blocks that are nearly conflict-free (synthetic, <1% density): there
the partition finds nothing to build and beats the kernel (×0.5–0.7). **TinyMinRepeats** fixes
most of it. The kernel runs only if accesses − distinct objects ≥ r·n, where summing ≤ 64 slice
lengths is cheap:

| r | Sui | YCSB <1% | TPC <1% | geo |
|---|---|---|---|---|
| 0 (always tiny) | ×1.74 | ×2.07 | ×1.63 | ×1.22 |
| 0.25 | ×1.65 | ×2.20 | ×1.81 | ×1.24 |
| **0.5** | ×1.66 | ×2.22 | ×1.88 | **×1.25** |
| 1 | ×1.44 | ×2.20 | ×1.91 | ×1.24 |

### 3.2 Dense blocks: fallback to HybridJoin — **keep** (report B's "largest-component skipping")

Above 10% density, zipfian blocks form one island of 86–92% of the block. The partition is then
pure overhead on top of a Bitmap-like sweep. The fallback builds such blocks whole with HybridJoin,
the best dense construction, as a single island. Three detectors were tried:

| Detector | Cost when it says no | Result |
|---|---|---|
| A: sampled density (one-word kernel on 32–64 strided transactions, `Adaptive`) | ~10–18% on sparse blocks (YCSB <1%: ×1.75–1.86 vs ×2.26 without) | works on dense blocks (×0.92–0.98); ×1.19–1.20 geo |
| B: Afforest's criterion (largest set at a checkpoint of the writer pass) | the running max of set sizes | **never fired**: the hubs are already out, and the giant forms through the reads; dropped |
| **C: the prefix's hub writers** (share of the 64 prefix transactions writing a hub, counted while the prefix rule re-zeroes its counts) | ~0 | fires on 48/58 dense YCSB and 54/56 dense TPC blocks, 0/32 below 1% |
| D: C as a gate (0.1–0.2), then the prefix's own density by the one-word kernel (≥ 3–10%) | ~0 below the gate | closes the 10–20% gap on 200–999-transaction blocks (contention figure) but misfires on Solana (×0.97–1.04 vs ×1.07); ×1.28–1.30 vs C's ×1.31; a tuning option (`DenseDensity`) |

Why C works:

| YCSB density | prefix hub writers |
|---|---|
| < 1% | 0 |
| 1–3% | 0.06 |
| 3–10% | 0.17 |
| 10–20% | 0.38 |
| ≥ 20% | 0.78 |

TPC is the same (0 … 0.84). On Solana it is 0.12–0.16, but some blocks reach 0.4–0.5, so at a
threshold of 0.4 the fallback also takes 9–10 Solana blocks per 100. The threshold is a deep-tuning
knob (`DenseHubShare`, swept 0.2–0.6).

Dense columns: YCSB ≥10% ×0.63 → ×0.93–0.99, TPC ≥10% ×0.85 → ×0.96–1.02.

### 3.3 Fused prefix hub detection — **archive** (own idea; within noise)

The prefix counts are kept in the anchors themselves (`countTag + c`), so there is no count array
and no re-zero pass; the partition's writer pass overwrites them.
- Ethereum: +1–3%.
- Elsewhere: ≈ 0.
- Geo: ×1.16 vs ×1.15 (layout-averaged).

It is also incompatible with the dense detector, which needs the re-zero pass.

### 3.4 Concurrent CAS union-find — **archive for now, Phase 5 candidate** (report B's Jayanti-Tarjan)

`next_cas.go`:
- link-by-index CAS unions, CAS path halving and a CAS first-writer claim on the anchors;
- two pool passes (writes, then reads);
- canonical grouping for determinism: islands ordered by root, which is the smallest member;
  members ascending; hub accesses concatenated in unit order, then touchers numbered as in the
  sequential pass.

Bit-exact, deterministic, mutation-checked.

On 6 P-cores (1 build): Solana ×0.56 vs ×1.08 sequential. The atomics, two pool wake-ups and the
grouping pass cost far more than the parallel unions save, as Phase 4 §7 predicted (break-even
needs many more cores). Keep it in mind for the server (Phase 5).

### 3.5 Bipartite hub output — **your decision** (report B; changes the output contract)

The hub layer becomes, per hub object, its writer list and its reader list (global transaction
ids, ascending): `IslandTopology.HubWriters/HubReaders`. There is no toucher numbering and no hub
matrix. `Reconstruct` expands it for the parity check: each writer conflicts with every other
writer and every reader.

Layout-averaged, vs the same code with the matrix:
- Ethereum +10–14%, EthereumNoHub +10%, TPC ≥10% +8–11%;
- ≈ 0 elsewhere;
- geo ×1.15 vs ×1.10.

It also enables more aggressive hub rules. A hub now costs one list entry per access instead of a
clique among its touchers:

| Prefix rule (bipartite, with tiny + dense fallback) | Eth | EthNoHub | Solana | YCSB 1–10% | TPC 1–10% | geo |
|---|---|---|---|---|---|---|
| 64 / 4 (default) | ×1.14 | ×1.21 | ×1.07 | ×1.09 | ×1.10 | ×1.26 |
| **64 / 3** | ×1.17 | ×1.26 | ×1.04 | ×1.19 | ×1.12 | **×1.28** |
| 64 / 2 | ×1.19 | ×1.31 | ×1.00 | ×1.23 | ×1.01 | ×1.27 |
| 128 / 4 | ×1.09 | ×1.17 | ×1.07 | ×1.15 | ×1.10 | ×1.24 |
| 256 / 4 | ×1.08 | ×1.16 | ×1.10 | ×1.22 | ×0.95 | ×1.21 |
| reads counted too, 64 / 4 | ×1.03 | ×1.06 | ×1.02 | ×1.10 | ×1.10 | ×1.20 |

Counting reads (read-hot objects as hubs) loses: rejected.

Downstream, the hub layer then holds lists, not an explicit graph. A colouring pass works on the
lists directly (report B argues that is cheaper for Jones-Plassmann). But it changes the contract
your professor's pipeline expects, so it is your and the professor's call. NextM is the best
variant without it.

### 3.6 Space-Saving sketch for hub detection — **archive** (report B)

`uflab/space_saving.go`: K counters with deterministic min-eviction, over every (or every 4th)
transaction's writes. Hubs are the objects counted ≥ T.

Results (1 build):

| Sketch | geo vs HybridJoin |
|---|---|
| K = 16, every transaction | ×0.37 |
| K = 8, every transaction | ×0.50 |
| K = 8, every 4th transaction | ×0.82 |

Hub (prefix rule): ×1.08. Two reasons:
- each write scans K counters, where the object IDs are dense, so a count array costs one
  increment (and that full counting pass already lost in Phase 4);
- the bounded sketch misses hubs the prefix rule catches: Ethereum islands are twice as large
  (mean 9.8 vs 4.4 members).

The report's premise (hash-map counting is expensive) does not hold for dense IDs.

### 3.7 RCM / bandwidth-reducing member order — **not built** (report B; probe says the ceiling is too low)

`span_probe_test.go` measured, for islands wider than 64 members, the words a span-limited sweep
would OR per access (the object's non-empty word range), as a share of the full row:

| Member order | Solana | YCSB <10% | TPC <10% |
|---|---|---|---|
| list order (current) | 0.70 | 0.71 | 0.62 |
| ascending | 0.77 | 0.78 | 0.81 |
| BFS (Cuthill-McKee start) | 0.63 | 0.51 | 0.49 |

Hot objects span the whole row whatever the order. The wide sweep is ~14% of Solana's time, so even
a free BFS order would save ≤ 5%. The BFS itself costs a counting sort plus a traversal per wide
island.

### 3.8 Transaction-level vertex cuts — **not built** (report B; probe says nothing to cut)

`vertex_cut_probe_test.go` gives an upper bound: the cut transactions simply vanish, with no
reintegration cost. Largest island share:

| Trace | no cut | top 1% of transactions cut | top 5% cut |
|---|---|---|---|
| dense YCSB | 0.90 | 0.89 | 0.86 |
| dense TPC | 0.69 | 0.67 | 0.64 |
| Solana | 0.21 | 0.20 | 0.14 |

The giants come from hot objects (already the hub layer's job), not from bridging transactions.
Maximum accesses per transaction: YCSB 12, TPC 35, Solana 64.

## 4. Fairness

- **TinyBitmap** goes to the competitors as BitmapTiny and HybridJoinTiny (`competitors/tiny_bitmap.go`).
  They are registered in `-suite=cmp` and in the uf arena as bars. Not yet in the main suite: see
  §6.
- The dense fallback *is* HybridJoin, and the islands use it only where it wins. There is nothing
  to give back.
- Bipartite output, the hub rules and the partition experiments need a partition. Nothing applies
  to Bitmap or HybridJoin.
- **The bar is tuned.** HybridJoin's knobs were re-checked on the new harness (`-suite=cmp`,
  `benchmark_results_cmp_2026-10-11.json`, 1 build). Every alternative is within ×0.98–1.02 geo
  (DenseFactor 0.25–4, MinWords 1–8), inside the layout noise. The islands' ratios are not inflated
  by a mistuned competitor.

## 5. Profile notes (go-test pprof, for orientation)

The per-pass cost of iterating each transaction's short access slices dominates small and medium
blocks. The partition's loop headers alone are ~40% of IslandLeanHub on Ethereum:
- 55% of Ethereum's writes are private first touches;
- 96% of its reads hit never-written objects.

Shares of IslandLeanHub's time:

| Trace | Partition | Other |
|---|---|---|
| Solana | 30% | wide sweep 14%, compaction 8%, wide build + wipe 10% |
| Dense YCSB | 19% | parallel-pool overhead ~5–8% (gone with the fallback) |

## 6. Decisions for you

**1. Bipartite hub output (NextB) or not (NextM)?**
- NextB is +6% geo over NextM and +15% on Ethereum.
- But the hub layer becomes per-hub writer/reader lists instead of an explicit graph (§3.5). It is
  a contract change for your professor's pipeline.
- My suggestion: keep both as finalists for now, ask the professor, and drop one after the deep
  tuning.

**2. Keep as finalists for the deep tuning:**
- NextM and NextB (`ufnext.Next` with the finalist configs, `NextM-s0.5` / `NextB-s0.5` in the
  arena);
- Hub (IslandLeanHub) as the reference.

Knobs worth the deep sweep:

| Knob | Values | Notes |
|---|---|---|
| `DenseHubShare` | 0.4–0.6 | flat in round 5 |
| two-stage `DenseDensity` | — | closes the 200–999-transaction, 10–20%-density gap in the contention figure, but misfires on some Solana blocks; ×1.28–1.30 vs ×1.31 |
| `DenseMinTxns` | — | |
| `TinyMax` | 16–64 | |
| `TinyMinRepeats` | 0.25–1 | maybe gated by objects/txn to save Sui's 6% |
| prefix rule | 64/2 – 96/3 | |
| `SequentialMax` | never parallel | ≥ default in round 6: with the dense fallback the parallel path no longer pays |
| `PairwiseMax`, `ParallelWords` | — | flat |

**3. Archive (or delete) with the evidence in §3** (the memory note records each):

| What | Verdict |
|---|---|
| CAS partition (`next_cas.go`) | archive; Phase 5 candidate |
| Space-Saving (`uflab/space_saving.go`) | lost |
| fused prefix | within noise |
| read-counting prefix | lost |
| `Adaptive` + the sampled detector (`adaptive.go`) | superseded by the in-partition detector |

Already dropped from the code, with the reason recorded:
- the writer-pass giant check, which never fired;
- RCM and vertex cuts, which were never built: their probes put the ceiling too low.

**4. Promote the finalists into package `islands`** as new files (e.g. `island_next.go`), keeping
IslandLean and IslandLeanHub, after you pick. Then retire the `ufnext` fork, keeping its probes as
tests.

**5. Fairness in the main suite** (needs your approval: it changes the main suite):
- add HybridJoinTiny, or let HybridJoin and Bitmap use TinyBitmap below 65 transactions by
  default (a change to `competitors/bitmap.go` and `hybrid_join.go`);
- add the finalist(s).

TinyBitmap is a ×1.8 Sui result for every algorithm. Without it, the islands' Sui win would be
unfair.

**6. Methodology:** make layout averaging the standard for final numbers (§2.1). The build script
is outside the repo now (it temporarily adds `aa_layoutpad.go` files). If you want it in the repo,
I'd add it as `layout_builds.sh` next to `layout_league.py`.
