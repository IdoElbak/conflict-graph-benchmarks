> **Historical record** of the pseudo-code of every algorithm, live and archived (was ALGORITHMS.md; the four live ones are in ../ALGORITHMS.tex), moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# Algorithm Pseudo-Code Reference

Every conflict-graph construction algorithm in `internal/algorithms/`: the parity reference at its root, the Union-Find family in `islands/`, the matrix-output algorithms (Bitmap and the competitors) in `competitors/`, retired ones in `archive/`.
**Correctness contract (verified bit-for-bit against NaiveSequential on every block of every run):** edge (Ti, Tj) exists iff their access sets have a W-W or R-W overlap. No R-R edges, no missed edges, deterministic result.

**Timed (main suite, `go run .`; `-algs=...` selects a subset; since 2026-10-13):** BitmapTiny and HybridJoinTiny (the two strongest competitors, §3, §5c–d), IslandHub and IslandBipartite (§4e). NaiveSequential runs once per block, untimed, as the parity reference.
**Proving ground (`go run . -suite=uf`):** IslandHub and IslandBipartite with their knobs moved one at a time, against the two competitors (registry `ufarena.go`); see `UF_ROUND3_RESULTS.md`.
**Competitor proving ground (`go run . -suite=cmp`):** HybridJoinTiny with HybridJoin's knobs moved one at a time, against BitmapTiny (registry `cmparena.go`); see `PHASE3_RESULTS.md`.
**Archived (`internal/algorithms/archive/{islands,competitors}/`, build-ignored — see its README):** IslandLean, IslandLeanHub, the `uflab` hub rules, the `ufnext` experiment package, SortJoin (2026-10-13; §4a–b, §4d and §5b keep their pseudo-code); IDBucket and HybridBitmap (2026-10-11, §2 and §5a); NaiveParallel, IslandNativeShattered, IslandTopologyShattered (2026-10-02); IslandNaive, IslandTxnShattered, IslandObjectShattered, IslandDynamicShattered (2026-07-18).
**Knobs:** Bitmap declares compile-time consts in its own file (tuned by `tune_hyperparams.py`); the island algorithms take an `IslandConfig` (tuned in the proving ground, which times many settings in one run).

**Common implementation standard** — every algorithm gets the same treatment:
- **No allocation per call:** all scratch is allocated once and reused; arrays grow only when a block is larger than any before.
- **Live-sized rows:** `ConflictGraph` rows, index rows and local matrices are `ceil(n/64)` words for the current block.
- **Each algorithm owns its output initialization:** row-owning algorithms overwrite every row; algorithms that set sparse bits clear what they write themselves, inside the timed call.
- **Same parallel discipline:** `concurrency.ParallelFor(total, chunk, body)` — workers claim chunks through one atomic counter, the caller is worker 0, no channel, fixed cost ~1–2 µs. Every parallel algorithm has a sequential threshold and a chunk knob.
- **Same access filters (Phase 4):** only objects that can produce an edge are indexed and swept. A read of a never-written object conflicts with nobody, and neither does a written object that no second transaction touches. Bitmap, the competitors and the island algorithms all apply them.

Shared primitive used by the pairwise paths:

```
HasConflict(A, B):                            # transaction.go
    if A.writes = ∅ and B.writes = ∅: return false
    return sortedSetsOverlap(A.writes, B.writes)   # W-W
        or sortedSetsOverlap(A.writes, B.reads)    # W-R
        or sortedSetsOverlap(A.reads,  B.writes)   # R-W
    # two-pointer scans over the pre-sorted access arrays
```

---

## 1. NaiveSequential — `naive_sequential.go` (the parity reference)

```
graph.Reset(n)
for i in 0..n-1:
    for j in i+1..n-1:
        if HasConflict(T[i], T[j]): AddEdge(i, j)     # upper triangle
SyncSymmetry()                                        # mirror to lower triangle
```
O(n² · setSize). Deliberately never parallelised or otherwise optimised: it is the definitional
reference every other algorithm is checked against. Not timed.

## 2. IDBucket — `archive/competitors/id_bucket_construction.go` (the "optimized hash-map"; archived 2026-10-11)

Archived after Phase 3: ×0.46 geo vs Bitmap, beaten by SortJoin and HybridJoin on every trace
(`PHASE3_RESULTS.md` §5). Kept here as the spec's hash-map algorithm and as row-wise Gustavson.

The project spec's "concurrent hash-map of object → (ReadSet, WriteSet)": object IDs are dense
`uint32`s (interned by `Manager.Populate`), so the map is a CSR array (`model.ObjectIdBucket`).

```
bucket.Build(block):       # counting sort into reusable arenas
    writers(obj) = every writer, ascending
    readers(obj) = every reader of a WRITTEN object, ascending   # reads of never-written
                                                                # objects are left out
if n <= IDBucketSequentialMax: rows [0, n) inline
else ParallelFor over rows, chunks of IDBucketChunkRows:
    for i in chunk:
        row = graph.Row(i); clear(row)
        for obj in T[i].writes: set bit j for j in writers(obj) ∪ readers(obj)
        for obj in T[i].reads:  set bit j for j in writers(obj)
        clear bit i
```
O(accesses + 2·edges). Each worker owns whole rows and writes both triangles directly, so there is
no symmetry pass. Emits edges one bit at a time where Bitmap ORs whole words. The read filter came
with Phase 4 (×1.04); private writes already cost it only one self bit.

## 3. Bitmap — `competitors/bitmap.go` (the baseline)

```
index.Prepare(numObjects, n)                       # model.ObjectInvertedIndex, reusable,
index.Build(block):                                # rows ceil(n/64) words
    for every write (t, obj): state[obj] = t+1 if 0 else state[obj] | shared;  set writer bit
    for every read (t, obj) with state[obj] != 0: state[obj] |= shared;         set reader bit
if n <= BitmapSequentialMax: rows [0, n) inline
else ParallelFor over rows, chunks of BitmapChunkRows:
    for i in chunk:
        row = graph.Row(i); clear(row)             # swept straight into the output row
        for obj in T[i].writes with shared(obj): row |= writers(obj) | readers(obj)
        for obj in T[i].reads  with shared(obj): row |= writers(obj)
        clear bit i
index.Wipe(block)                                  # zero exactly what Build set, O(accesses)
```
Sweeps are `ceil(n/64)`-word contiguous OR loops — pure memory bandwidth. Both triangles are
produced directly, and every row is overwritten, so the output needs no clearing. The shared-object
filter came with Phase 4 (×1.18 geo). Compacting each row's shared accesses branch-free first,
as the island algorithms do, was measured and makes Bitmap slower (×0.85): it touches each access
once per row, so the extra pass cannot pay.

---

## 4. The island family — IslandHub and IslandBipartite (since 2026-10-13)

The live pair is §4e: IslandLeanHub's partition and kernels (§4a–b, now archived), with the
giant-island kernel (§4c) on and the UF next round's tiny path and dense fallback (§4d). §4a–b
describe the parts they inherited.

The island algorithms partition the block's transactions into **islands** (connected components of the conflict
relation; with hubs, of the conflict relation minus the hub objects) and build one small local
matrix per island. Their output is a `model.IslandTopology`:

- `Islands[i]` — the members of island i (global transaction indices); `LocalGraphs[i]` — its
  symmetric conflict matrix, `len(island)` rows × `LocalWidths[i]` words, in local coordinates
  (bit p = `Islands[i][p]`).
- `HubTouchers`, `HubGraph`, `HubWidth` — the matrix hub layer (IslandHub): a symmetric matrix
  over the transactions that touch a hub object, holding the conflicts through hub objects.
- `HubWriters`, `HubReaders` — the bipartite hub layer (IslandBipartite): per hub object, its
  writer and reader lists.

The union of both parts is the conflict graph (an edge may appear in both). Transactions in no
island and no hub row have no conflicts. The islands can be graph-colored independently, and the
hub layer resolves the collisions. `Reconstruct` rebuilds the global matrix, untimed, for the
parity check.

Code (`islands/`): `island_shared.go` (the core: `IslandConfig`, anchors, islands, kernels,
parallel path, the giant-island kernel), `island_hubs.go` (hub rule, partitions, both hub layers,
tiny path, dense fallback), `island_hub.go` (IslandHub), `island_bipartite.go` (IslandBipartite);
data structures in `model/island_topology.go` (`IslandTopology`, `IslandForest`, `IslandIndex`,
`LiveAccesses`).

### 4a. IslandLean — `archive/islands/island_lean.go` (archived 2026-10-13; the partition and kernels live on)

```
anchor[obj] = 0 for all objects (cleared per call); forest: parent = self, next = self
WRITER PASS: for t: root = t
    for obj in W(t): a = anchor[obj]
        a == 0 -> anchor[obj] = root+1            # first writer
        else   -> anchor[obj] |= shared; root = link(find(a-1), root)
READER PASS: for t: for obj in R(t) with anchor[obj] != 0:
        anchor[obj] |= shared; root = link(find(a-1), root of t, looked up once)
# link swaps the two roots' next pointers: every set is a circular member list
ISLANDS: every root with size >= 2; members read off its list (no Find per txn, no sort)
live(obj) = anchor[obj] is shared and not a hub   # only these can produce an edge
per island:
    k == 2       -> the edge is implied by the union
    k <= 4       -> pairwise HasConflict
    k <= 64      -> compact the members' live accesses (branch-free), then build / sweep /
                    wipe one-word index rows: row[p] = OR of writers|readers of live writes
                    and writers of live reads
    k > 64       -> the same with ceil(k/64)-word rows
parallel path (more than SequentialMax txns and >= ParallelWords words of local matrices):
    ONE ParallelFor pass: wide-island builds first, small islands in groups on each worker's
    own one-word index, wide-island sweeps in SplitRows ranges (each waits on an atomic
    "built" flag); wide rows wiped inline afterwards
```

Zero allocation per call once the scratch has grown. Why each step is there, and what it is worth,
is measured in `PHASE4_RESULTS.md` (rounds and ablation).

### 4b. IslandLeanHub — `archive/islands/island_lean_hub.go` (archived 2026-10-13; IslandHub is its successor)

IslandLean with hub objects kept out of the partition, which splits the islands they glued. The
conflicts through hubs go into the **hub layer**, a bit-matrix over the transactions that touch a
hub (toucher-local coordinates), built like Bitmap over hub objects only. Topology's per-edge hub
list cost one 8-byte edge per conflict; the hub layer costs one bit.

```
if n >= HubMinTxns: hubs = rule.Hubs(block)        # HubRule; default PrefixHubs:
                                                   # written by >= 4 of the first 64 txns
mark hub h: anchor[obj] = hubBase + h
partition as IslandLean; a hub access unions nothing: it numbers its txn as a toucher (first
    hub access order) and is recorded (toucher, hub)
hub layer: writer/reader bitsets of each hub over the touchers; row(q) = OR over q's written
    hubs of writers|readers, over q's read hubs of writers; clear self bit
    (parallel path: accesses grouped by toucher, rows handed out in units of 64 touchers
    inside the same pool pass as the islands)
    if some hub is written by every toucher (Ethereum's fee recipient), every two touchers
    conflict: the rows are filled with ones and the diagonal cleared instead of ORed
islands as IslandLean (hub objects are not live)
```

`HubRule` is a one-method interface (`Hubs(block, hubs) []uint32`). The proving ground plugs in the
other rules (`uflab.CountedHubs`: accesses, writers, write-weighted, split, relative, sampled);
`PHASE4_RESULTS.md` §4 compares them.

---

### 4c. The object-major wide-island kernel — `islands/island_shared.go` (on for giant islands since 2026-10-13)

`IslandConfig.JoinDense > 0` builds every island wider than 64 members (with rows of at least
`JoinMinWidth` words) with HybridJoin's kernel (§5c) in local coordinates, instead of index build
→ row sweep → wipe. Every accessor of a live object lies in the object's island, so the island's
compacted live accesses are counting-sorted by object and emitted object-major. It is the
fairness transfer of HybridJoin's trick to the island family; on the laptop it is neutral (geo
×1.00–1.03; ×1.10 on 1–10% YCSB, ×0.92 on Solana, whose wide islands are only ~5 words), so the
default keeps it off. The proving ground keeps `Lean-join1`, `Lean-join1-w8` and `Hub-join1`.
Re-measured layout-averaged on 2026-10-11 (`UF_NEXT_RESULTS.md` §2.1): Hub-join1 ×1.08 vs Hub
×1.07 geo vs HybridJoin. Still neutral overall: +6–13% at 1–10% density, −7% on Solana.

**2026-10-13: on for giant islands only** (`IslandConfig.GiantShare`, default 0.3): islands
holding at least 30% of the block (and more than 64 members) take the object-major kernel, the
rest keep the Bitmap-style one. Solana's widest islands hold 21–28% of the block, so they keep
their kernel; YCSB at 3–10% density forms a giant of ~76%, where the Bitmap-style sweep cost what
Bitmap costs. YCSB 1–10% ×1.02 → ×1.12 vs HybridJoinTiny (`UF_ROUND3_RESULTS.md`). The knob
`JoinMinWidth` is gone; `JoinDense` (default 1) is the kernel's dense factor.

### 4d. The UF next round — `archive/islands/ufnext/` (2026-10-11 and 2026-10-13, experiments; see `UF_NEXT_RESULTS.md`, `UF_ROUND3_RESULTS.md`)

`Next` (`next_core.go` + `next_hub.go`) is IslandLeanHub forked, with every idea as a knob of
`NextConfig`. With all knobs off it is IslandLeanHub. Each knob that changes the partition loop has
its own loop: folding them into one loop cost the default path 13% on the P-cores (code layout).

```
Next.Run(block):
    if TinyMax ≥ n and repeats(block) ≥ TinyMinRepeats · n:     # repeats = Σ accesses − distinct objects
        TinyBitmap → whole block as ONE island; return           # §5d
    clear anchors
    hubs = objects written by ≥ PrefixMinWrites of the first PrefixTxns txns   # prefix rule, count array
    if n ≥ DenseMinTxns:                                         # dense fallback, decided before the partition
        share = (prefix txns writing a hub) / PrefixTxns         # counted while re-zeroing the prefix counts
        if share ≥ DenseHubShare and (DenseDensity = 0 or density(prefix, one-word kernel) ≥ DenseDensity):
            HybridJoin → whole block as ONE island; return
    partition (IslandLeanHub's two passes; hub accesses recorded, union nothing)
    collect islands, build them (IslandLean's kernels, parallel path)
    hub layer:
        matrix    (default): IslandLeanHub's bit-matrix over the hub touchers
        Bipartite:           per hub object, its writer list and reader list (global txn ids) —
                             IslandTopology.HubWriters / HubReaders; no toucher numbering, no matrix
```

The whole-block island (`IslandTopology.SetWholeBlock`, members 0..n-1, the n×⌈n/64⌉ matrix) is
exact. It gives the colouring no parallelism inside the block, but such blocks would be ~10
transactions or one giant island anyway.

Other knobs, measured and not kept as defaults:
- `FusedPrefix`: the prefix counts live in the anchors (`countTag + c`), so there is no count array
  and no re-zero pass. +1–3% on Ethereum, within noise elsewhere, and it disables the dense
  detector.
- `PrefixCountReads`: reads count toward the prefix rule (read-hot objects become hubs). Loses.
- `ConcurrentMin` (`next_cas.go`): report B's concurrent union-find. Link-by-index CAS unions, CAS
  path halving and a CAS first-writer claim, in two pool passes; then islands grouped canonically
  (by root, which is the smallest member; members ascending) for determinism. Solana ×0.56 on 6
  P-cores. A Phase 5 candidate.

The wrappers and rules around it:
- `Adaptive` (`adaptive.go`) wraps any island algorithm with the tiny path and a sampled-density
  fallback (one-word kernel on 32–64 strided transactions). It works, but the sample costs 10–18%
  on sparse blocks; superseded by the prefix detector.
- `uflab.SpaceSavingHubs`: report B's Space-Saving sketch as a hub rule. Loses ×0.37–0.82.

The finalists are combinations: **NextM** (matrix hub layer, prefix 64/4) and **NextB** (bipartite,
prefix 64/3), both with TinyMax 64, TinyMinRepeats 0.5 and the dense fallback. Round 3 added
`GiantShare`, `Batched`, `EarlyDense` and `DenseGiant` (verdicts in `UF_ROUND3_RESULTS.md`) and
promoted NextM / NextB with the giant kernel as IslandHub / IslandBipartite (§4e).

### 4e. IslandHub and IslandBipartite — `islands/island_hubs.go` (live, 2026-10-13)

One implementation (`hubIslands`), two hub layers. Both use the same knobs (`HubConfig`;
defaults `DefaultIslandHubConfig` = `DefaultIslandBipartiteConfig`).

```
Run(block):
    if n ≤ TinyMax (64) and Σ accesses − distinct objects ≥ ⌈TinyMinRepeats (0.5) · n⌉:
        TinyBitmap → whole block as ONE island; return                       # §5d
    clear anchors
    if n ≥ HubMinTxns (64):
        hubs = objects written by ≥ PrefixMinWrites (3) of the first PrefixTxns (64) txns
        if n ≥ DenseMinTxns (512):
            share = (prefix txns writing a hub) / prefix     # counted while re-zeroing the counts
            if share ≥ DenseHubShare (0.5): HybridJoin → whole block as ONE island; return
    partition (§4a; hub accesses are recorded and union nothing):
        no hubs → plain loop;  IslandHub → toucher-numbered;  IslandBipartite → txn ids
    collect islands; build each:
        k == 2 → implied edge;  k ≤ 4 → pairwise;  k ≤ 64 → one-word index kernel
        k > 64 and k ≥ GiantShare (0.3) · n → object-major kernel (§4c)
        else → ⌈k/64⌉-word index kernel           (parallel path as §4a)
    hub layer:
        IslandHub:       bit-matrix over the hub touchers (§4b)
        IslandBipartite: per hub, writers and readers in ascending txn order (counting sort)
```

IslandBipartite changes the output contract of the hub layer (lists instead of an explicit
graph); IslandHub keeps it. Results: `UF_ROUND3_RESULTS.md`.

---

## 5. Phase 3 competitors — `internal/algorithms/competitors/`

Conflict-graph constructions from the literature, to the common standard (no allocation per call,
live-sized rows, the shared pool where a parallel path pays, the access filters). All of them
produce the full matrix. Knobs are a run-time config tuned in `-suite=cmp`.

Shared by SortJoin and HybridJoin, `objectLists.build` (`object_lists.go`) is the
"sort" of a sort-based join. Object IDs are dense, so it is a counting sort:

```
count writers(obj) for every write; readers(obj) for reads of written objects only
prefix sum over the objects: per object [lo, mid) writers, [mid, hi) readers
fill the writers, then the readers (ascending transaction order)
```

### 5a. HybridBitmap — `archive/competitors/hybrid_bitmap.go` (Roaring-style containers; archived 2026-10-11)

Archived after Phase 3: ×1.00–1.02 geo vs Bitmap, beaten by HybridJoin on every column outside
placement noise. Its archived `object_lists.go` still marks the dense objects.

```
if row words < MinWords (8): run Bitmap's code
objectLists.build; an object is dense when accessors > DenseFactor (1) × row words
dense objects: writer and reader bitsets built from their lists
per row (ParallelFor above 518 txns, as Bitmap):
    write of obj: dense -> row |= writers | readers (word OR); else set one bit per accessor
    read  of obj: dense -> row |= writers;                     else set one bit per writer
    (a write of a private object, one accessor, is skipped); clear self bit
```

Per access it costs min(row words, accessors): Bitmap and IDBucket are its two extremes. This is
Roaring's array-vs-bitmap container choice per object, and SpGEMM's dense-vs-sparse accumulator
choice. A second version that kept Bitmap's build and extracted the sparse objects' lists from
their bitsets lost (×0.64 on Solana: two popcount passes over every shared object's bitsets).

### 5b. SortJoin — `archive/competitors/sort_join.go` (the classical sort-based conflict join; archived 2026-10-13)

```
objectLists.build
graph.Reset(n)
for obj with a writer and >= 2 accessors:              # object-major (outer product)
    for each writer w, for each later writer and every reader p: set (w, p) and (p, w)
```

O(accesses + edges·multiplicity), no index rows. Sequential by default: the parallel path (objects
split among workers, atomic ORs, the matrix cleared in a first pool pass) is deterministic, because
OR commutes, but loses on 6 cores on every trace.

### 5c. HybridJoin — `hybrid_join.go` (SortJoin with a dense kernel per hot object)

```
if row words < MinWords (4): run Bitmap's code
objectLists.build; graph.Reset(n)
for obj with a writer and >= 2 accessors (nw writers, nr readers):
    pairBits = nw(nw-1) + 2·nw·nr
    if pairBits <= DenseFactor (1) × (nw+nr) × row words:  emit the pairs as bits (SortJoin)
    else: W = bitset of the writers, A = W | readers, in two scratch rows (L1-hot)
          each writer's row |= A (self bit cleared); each reader's row |= W; clear W, A
```

Sequential. The cost per object is min(pair bits, accessors × row words). Hot objects are swept
word-wide as in Bitmap, but from two scratch rows instead of an index of `objects × row words`.
Cold objects cost their pairs.

### 5d. TinyBitmap — `tiny_bitmap.go` (2026-10-11; blocks of at most 64 transactions)

Bitmap specialized to one-word rows. At that width the shared-object filter costs more than the
ORs it saves, and it is not needed: a private object's bits are the transaction's own, cleared
with the self bit, and a never-written object has no writer bits.

```
TinyBitmap(txns ≤ 64):
    for t: for o in W(t): writers[o] |= bit(t);  for o in R(t): readers[o] |= bit(t)   # one pass, no state
    for t: row = 0
           for o in W(t): row |= writers[o] | readers[o]
           for o in R(t): row |= writers[o]
           out[t] = row &^ bit(t)
    zero the index: word-wide (clear) if objects ≤ 16 · txns, else access by access
```

On Sui it runs ×1.8 faster than Bitmap. It is also the islands' tiny path (§4d) and their prefix
density estimator. Fairness counterparts: **BitmapTiny** and **HybridJoinTiny** (Bitmap /
HybridJoin with TinyBitmap below 65 transactions), in `-suite=cmp` and as bars in the uf arena.

### Measured and rejected (Phase 3)

- **Gustavson SpGEMM with a sparse accumulator**: it is IDBucket. IDBucket is row-by-row
  Gustavson, with the output row as a bit-vector accumulator, so it is not a new competitor.
- **PPJoin / AllPairs prefix filtering**: with an overlap threshold of 1 (any shared object is an
  edge), the prefix is the whole set and nothing is filtered.
- **Four Russians (M4RM)** over the hottest objects: a probe counted word loads against Bitmap's.
  The best stripe count saves 10% on YCSB and is worse everywhere else (Ethereum ×1.2–6 more,
  Solana ≈ equal), because a transaction rarely touches two hot objects of the same 8-object stripe.
- **Roaring bitmaps proper**: blocks of ≤ 2,000 transactions fit one 65,536-entry chunk, so
  Roaring reduces to the per-object array-or-bitmap choice, which is HybridBitmap.

---

## 6. Archived algorithms (lineage, see `archive/README.md`; files in `archive/islands/`)

**Island family skeleton** that the archived variants share; each changes the marked steps:

```
① INDEX+COUNT   build per-object access info and per-object access counts
② HUB DETECT    mark "hub" objects/txns that would glue everything together
③ PARTITION     union-find over txns via shared objects, SKIPPING hubs
④ GROUP         collect multi-member islands (singletons dropped)
⑤ PER-ISLAND    build each island's edges independently (parallel across islands)
⑥ REINTEGRATE   add back the conflicts that flow through hub objects/txns
```

- **IslandNaive** (archived 2026-07-18): no hubs; ⑤ pairwise `HasConflict`. Giant hub-glued islands made O(k²) explode — the reason shattering was invented.
- **IslandTxnShattered** (2026-07-18): hubs are huge *transactions*; the wrong hub axis for our traces.
- **IslandObjectShattered** (2026-07-18): object hubs, fixed-width Bitmap-style sweeps, reintegration for all n transactions.
- **IslandDynamicShattered** (2026-07-18): + live-width sweeps and toucher-only reintegration, but allocated per call.
- **IslandNativeShattered** (2026-10-02): the zero-allocation rewrite with global-matrix output — fused counting + hub detection, anchor-based union-find, counting-sort grouping, lazy index, size-2 / pairwise / sweep dispatch, row-range splitting. Superseded by IslandLean.
- **IslandTopologyShattered** (2026-10-02): Native's front half with the topology output (per-island local matrices + a sparse hub-edge list). Its output contract lives on as `model.IslandTopology`, with the hub edges now a bit-matrix. Superseded by IslandLean / IslandLeanHub.
- **NaiveParallel** (2026-10-02): the all-pairs check on the worker pool (≈ Algorithm 5 of the Parallel-SCT paper); dropped from the timed suite as O(n²).

---

## Knobs

| Knob | File | Value | Meaning |
|---|---|---|---|
| `BitmapSequentialMax` / `BitmapChunkRows` | competitors/bitmap.go | 518 / 16 | blocks ≤ this run inline / rows per claimed chunk |
| `DefaultIslandConfig.PairwiseMax` | islands/island_shared.go | 4 | islands of 3..4 members use pairwise `HasConflict` |
| `DefaultIslandConfig.SequentialMax` | islands/island_shared.go | 300 | blocks ≤ this always run inline |
| `DefaultIslandConfig.ParallelWords` | islands/island_shared.go | 8000 | larger blocks go parallel once their local matrices hold this many words |
| `DefaultIslandConfig.SplitRows` | islands/island_shared.go | 16 | rows per parallel sweep unit of islands wider than 64 |
| `DefaultIslandConfig.GroupTxns` | islands/island_shared.go | 256 | small islands per parallel unit, in members |
| `DefaultIslandConfig.GiantShare` | islands/island_shared.go | 0.3 | islands of ≥ 30% of the block (and > 64 members) use the object-major kernel (§4c) |
| `DefaultIslandConfig.JoinDense` | islands/island_shared.go | 1 | the object-major kernel's dense factor |
| `PrefixTxns` / `PrefixMinWrites` | islands/island_hub.go, island_bipartite.go | 64 / 3 | an object is a hub when ≥ 3 of the first 64 transactions write it |
| `TinyMax` / `TinyMinRepeats` | same | 64 / 0.5 | tiny path (§4e) |
| `DenseHubShare` / `DenseMinTxns` | same | 0.5 / 512 | dense fallback (§4e) |
| `HubMinTxns` | islands/island_hubs.go | 64 | smaller blocks get no hubs |
| `hubRowsPerUnit` | islands/island_hubs.go | 64 (not tuned) | hub-matrix rows per parallel unit |
| `DefaultHybridJoinConfig` | competitors/hybrid_join.go | MinWords 4, DenseFactor 1 | §5c |

Bitmap's and IDBucket's chunks come from `tune_hyperparams.py` (Phase 2). Their sequential
thresholds were then re-measured *paired*, sequential and parallel back to back on every block in
one process: whole-run scores of unchanged code drifted by up to ×1.11 between runs, and parallel
code by ~25–35% more than sequential code, which biased the tuner's cross-run comparison towards
"sequential". The island knobs come from the Phase 4 proving ground: pairwise cut-offs 2–8 are
within noise (16 loses ×0.63 on Sui); the dispatch threshold is flat between 4k and 16k words; the
parallel path pays only on dense synthetic blocks (+3–5%).
