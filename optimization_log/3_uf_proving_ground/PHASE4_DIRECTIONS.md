> **Historical record** of the Phase 4 plan, moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# Phase 4 directions — conclusions of the 2026-09-28 discussion

Written at the end of Phase 2. The discussion happened in side questions during the tuning run
(the questions were recovered from the prompt history; the answers were not saved), so each
conclusion below is restated from the question and checked against the Phase 2 measurements.
Where a later measurement changed the picture, it says so.

## Where the Union-Find family stands after Phase 2

With every algorithm on the same implementation standard, Topology vs Bitmap (median per-block
speed-up, same run): Ethereum ×0.47, Ethereum without the fee hub ×0.67, Solana ×0.96 (41/100
blocks won), Sui ×0.52, YCSB ×0.64, TPC ×0.71. The family still wins where contention is very
low: at ≤ 0.3% density ×1.5–2.9 on YCSB and TPC at every block size, crossing Bitmap near 1%.

Why it loses elsewhere: Topology does everything Bitmap does (index build, row sweeps, wipe) plus
a front half Bitmap does not have. Phase shares of Topology's own time (sequential, pairwise
cut-off 8, per trace):

| Phase | Share |
|---|---|
| access counting (feeds hub detection only) | 7–12% |
| union-find partition | 14–31% |
| grouping islands | 3–15% |
| lazy index build | 9–14% |
| index wipe | 8–10% |
| per-block preparation | 1–13% |
| **serial front half + wipe, total** | **50–74%** |
| per-island sweeps (the parallelizable part) | 25–50% |

On raw Ethereum every block is one island, so the front half is pure overhead there.

## Conclusions, question by question

**1. "A heuristic that actually helps would let island batching / island parallelization pay
off."** Only partly. A better heuristic yields smaller islands, and smaller islands carry *less*
work each, which makes parallel dispatch pay *less*, not more: the pool's fixed cost is ~1–2 µs
per call, while a size-2 island costs nanoseconds. The payoff of a heuristic has to come from
less total work (more size-2 and pairwise islands, narrower local rows), and it must cost less
than it saves. Today's hub heuristic does not: every threshold tried loses to hubs off
(Topology ×1.06 slower at 2000, ×1.97 slower at 20), because re-adding hub conflicts as sparse
edges costs more than the smaller islands save.

**2. "Parallelism isn't a good direction for UF, unless it's the front half; with a better
heuristic the islands are small and sequential anyway; so the heuristic pays a little and the
front half must get cheaper."** Confirmed, with one correction. The front half is 50–74% of the
time and serial, so it is the main lever. Correction from the paired measurement made after this
question: splitting large islands into row ranges does make the parallel path pay a little on
big blocks (at ≥ 1,200 txns Native ×1.13–1.21; Topology ×1.08–1.09 on YCSB/TPC but ×0.95 on
Solana; thresholds now 1146 / 1347). It is about break-even at 700–1,200 txns, loses below, and
its ceiling is ×1.3–1.5 (Amdahl on the serial front half).

**3. "What about one island per thread instead of splitting big islands?"** That is the
dispatch the family already had. It is capped by the largest island, which carries 65–98% of the
island work on every trace (Solana: the largest island is ~24% of the transactions but ~95% of
the work), so its Amdahl ceiling is ×1.01–1.17 even with free threads. Many small islands in
parallel do run concurrently, but each is too cheap to cover the dispatch cost. It pays only when
a block has several comparably large islands, which none of the traces show.

**4. Summary you proposed: "find an optimization to the UF pipeline, make the current pipeline
do less, or find a heuristic that splits more islands with minimal overhead in the front part,
e.g. one that uses results of cheaper sweeps, or fewer sweeps."** Agreed, and it is the Phase 4
plan below.

## Phase 4 candidates

Fairness rule for every item: if a trick also applies to Bitmap or IDBucket, they get it too.

**A. Make the front half do less (no heuristic needed)**
1. Skip the counting pass when hubs are off. Counts only feed hub detection, so that is 7–12%
   for free. It does not apply to Bitmap, which never counts.
2. Fuse counting into the writer pass of the partition: one pass over all accesses fewer.
3. Skip reads of never-written objects (`AnchorOf[obj] == 0` after the writer pass) in the
   reader union pass, the index build, the sweeps and the wipe. On Solana that is 76.8% of all
   reads. It **applies to Bitmap too** (a never-written object's writer row is all zeros), so
   Bitmap gets it in the same change.
4. Fold the `Find` pass of grouping into the partition, or group only transactions that were
   ever unioned (singletons are skipped anyway).
5. Don't carve local matrices for size-2 islands; their edge is implied by the union.

**B. Parallel front half** (only worth it once A has shrunk it; measure first)
- Counting: per-worker partial counts over 64-aligned transaction chunks.
- Union-find: concurrent lock-free union-find. The partition is deterministic even if the tree
  shape is not; grouping must then order islands by smallest member, not by root.
- Index build and wipe by island: race-free once item A3 removes read-only object rows, the
  only case where local bits of different islands can share a word.

**C. Heuristics** (each with its own mini sweep, as the plan requires)
- Write-weighted hub scoring. Use a crossing test (`old <= T && old+W > T`); the current
  `== T+1` trigger never fires with weighted increments.
- Hub = written *and* hot; never hub a read-only object.
- Separate read and write thresholds; thresholds relative to block size.
- Transaction-axis hubs (the archived TxnShattered idea, on the Topology skeleton).
- Cost-based pairwise/sweep dispatch: island size × access-set size instead of the fixed cut-off 8.
- Your "cheaper sweeps" idea: decide hubs from statistics the pipeline already computes
  (counts, anchors, writer counts per object), so the heuristic adds no extra pass.

**D. Evaluation**
- Report results conditionally: the family wins at ≤ 0.3% density and loses above ~1%. That is
  the paper's claim, and the heuristics should be judged per contention band.
- ~~Output-format fairness~~ — **rejected by the user (2026-10-01), not to be done.** The downstream colouring pipeline needs
  the islands. Topology produces them; Bitmap would need an extra connected-components pass
  over its matrix. Comparing "Bitmap + components" with Topology is the fair end-to-end test.
- Tuning method — **parked by the user (2026-10-01) with the server phase.** Whole-run scores drifted up to ×1.11 between runs,
  and parallel code drifted more than sequential code, which biased the tuner's thresholds
  towards "sequential". Timing both modes back to back in one process removes that. A tuning
  mode in the harness would let the Phase 4/5 tuner do this itself.
