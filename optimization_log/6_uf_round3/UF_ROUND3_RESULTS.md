> **Historical record** of the UF round 3 results, moved here on 2026-10-14. File names and paths are as they were then: `island_hubs.go` is now part of `island_shared.go`, `island_hub.go` and `island_bipartite.go`; `bitmap.go`, `hybrid_join.go` and `object_lists.go` are now `bitmap_tiny.go` and `hybrid_join_tiny.go`; the result files named here were deleted unless listed in `7_final_build/`.

# UF round 3 — optimizing the Union-Find family, and the four-algorithm suite (2026-10-13)

Brief: get better results for the UF family only (tuning or new variations, no new competitor
versions), then keep the top 2 UF algorithms in `islands/` and the top 2 competitors in the main
suite.

**Result:** the UF finalists of the next round (`UF_NEXT_RESULTS.md`) gained 2–4% overall, from
the giant-island kernel and a stricter dense fallback, and were promoted as **IslandBipartite**
(×1.21 vs HybridJoinTiny) and **IslandHub** (×1.17). The main suite is now four algorithms:
BitmapTiny, HybridJoinTiny, IslandHub and IslandBipartite.

## 1. Final numbers (main suite, `benchmark_results.json`)

`go run .` on all blocks of the six traces, every block parity-checked, on three builds: the plain
build plus two layout-perturbed ones (`benchmark_results_final_L31/L32.json`). Each cell is the
median per-block speed-up vs **HybridJoinTiny**, as a geometric mean over the three builds
(`layout_league.py`); the spread of the geo over the builds was ±0.01.

| | Eth | Eth no fee hub | Solana | Sui | YCSB <1% / 1–10% / ≥10% | TPC <1% / 1–10% / ≥10% | geo |
|---|---|---|---|---|---|---|---|
| BitmapTiny | ×0.98 | ×0.97 | ×0.60 | ×1.00 | ×0.92 / 0.86 / 0.90 | ×0.73 / 0.78 / 0.83 | ×0.85 |
| HybridJoinTiny | ×1.00 | ×1.00 | ×1.00 | ×1.00 | ×1.00 / 1.00 / 1.00 | ×1.00 / 1.00 / 1.00 | ×1.00 |
| **IslandHub** | ×1.04 | ×1.10 | ×1.06 | ×0.94 | ×2.10 / 1.12 / 0.93 | ×1.81 / 1.08 / 0.98 | **×1.17** |
| **IslandBipartite** | ×1.20 | ×1.25 | ×1.07 | ×0.94 | ×2.10 / 1.15 / 0.95 | ×1.79 / 1.11 / 1.01 | **×1.21** |

Against BitmapTiny: IslandBipartite ×1.48, IslandHub ×1.40, HybridJoinTiny ×1.18.

Figures: `optimization_log/6_uf_round3/Phase11_final_suite_by_size.png` and
`Phase11_final_suite_contention.png`. `python show_results.py` prints the same data as text
(baseline now HybridJoinTiny).

Where the islands win, tie and lose:
- **Win:** sparse blocks (<1% density) ×1.8–2.1; Ethereum ×1.04–1.25; Solana ×1.06–1.07;
  medium density (1–10%) ×1.08–1.15.
- **Tie:** dense blocks (≥10%), ×0.93–1.01. They are built whole by HybridJoin there, so the
  ceiling is ×1.0.
- **Lose:** Sui ×0.94. Its ~10-transaction blocks are built whole by TinyBitmap, the same kernel
  as the competitor; the 6% is the selector and the wrapper (§3.6).

## 2. What changed in the algorithms

Starting point: NextM / NextB of the UF next round, ×1.12–1.13 / ×1.18–1.19 vs HybridJoinTiny in this
session's own runs (3 layouts each).

| Change | Effect (3 layouts, vs HybridJoinTiny) | Kept |
|---|---|---|
| **Giant-island kernel**: islands holding ≥ 30% of the block (and > 64 members) are built object-major (HybridJoin's kernel in local coordinates, `IslandConfig.GiantShare`) | YCSB 1–10% ×1.02 → ×1.08–1.12; NextB ×1.19 → ×1.21, NextM ×1.13 → ×1.14–1.15 | yes, both |
| **Dense fallback only from 512 transactions** (`DenseMinTxns` 256 → 512) | B ×1.19 → ×1.21 (Eth, EthNoHub, TPC); M flat | yes, both |
| **IslandHub's hub rule 64/3** (was 64/4) | M ×1.14 → ×1.15–1.16 | yes |
| Tiny path builds into its own rows, integer repeats test | Sui unchanged within noise (×0.93–0.95) | yes (identical output, less code) |

Both promoted algorithms now share one implementation (`islands/island_hubs.go`) with the same
knobs (§5); only the hub layer differs.

## 3. What was tried and rejected

### 3.1 Branch-free batched partition — rejected (×0.85–0.90)

Hypothesis: on Ethereum and Solana 55–63% of writes are first touches, so the partition's
"first touch or shared" branch is close to a coin flip and mispredicts. The batched partition
(`Batched`, `archive/islands/ufnext/next_batched.go`) classifies every access with conditional
moves, stores it to a share buffer and a hub buffer whose cursors advance by its class, then
unions over the shares in a separate pass. It lost everywhere: NextB ×0.90 and NextM ×0.85
overall, Ethereum ×0.75–0.88, Solana ×0.78–0.82. Mispredictions are not the partition's
bottleneck. The extra stores, and the union pass losing the access loop's locality, cost more.

### 3.2 Dense decision before the anchors are cleared — neutral

`EarlyDense` decides the fallback from the prefix counts alone (hubs keep their count until every
prefix write is checked), so a block taking the fallback never clears the anchors. It came out
×1.19 vs ×1.19 and ×1.13 vs ×1.13. Clearing 2–25 KB is not where the fallback's overhead is.

### 3.3 Better dense detectors — none beats the hub-writer share

The hub-writer share (fall back when ≥ 50% of the 64-transaction prefix writes a hub) misfires:
- **On Ethereum**, every block of ≥ 256 transactions fell back (19/300, at ×0.91–0.93, where
  the islands win). The fee recipient makes the share 1.0.
- **On Solana**, 18% of blocks fell back, at ×0.99 where the islands give ×1.08.

Four replacements were probed (`archive/islands/ufnext/*_probe_test.go`, `UFNEXT_STATS=1`):

| Detector | What it measures | Verdict |
|---|---|---|
| prefix giant (`DenseGiant`, built and measured) | largest union-find set among the first 64 txns, hubs out | fixes the misfires (Solana ×1.09), but misses dense YCSB: ×0.76–0.81 vs ×0.94 there. 64 transactions are too few for the giant to show once the hubs are out |
| scaled prefix degree | 2·(prefix joins)·n / 64² | does not separate: Solana reaches 111 with a giant of 0.2 (joins with an already-joined root add no connectivity) |
| hub share without near-universal hubs | the share counting only hubs written by < 48 / 32 / 16 prefix txns | Ethereum's big blocks keep 0.48–0.78: other hot contracts besides the fee recipient |
| number of prefix hubs | distinct hubs among the prefix's writes | overlaps: dense YCSB 7–19, Ethereum 5–12 |

The probes also show that **dense TPC and Ethereum look the same** to every cheap prefix
statistic: prefix giant 0.03–0.22 vs 0.03–0.20. A rule that separated them would be fitted to
these traces. What was kept is the hub-writer share with a larger minimum block size (512), which
removes the Ethereum misfires (no Ethereum block reaches 512) without touching the synthetic
traces.

### 3.4 No fallback at all (the giant kernel instead) — a trade, not kept

Without the fallback, the giant kernel builds dense blocks inside the partition:

| | Eth | Solana | YCSB ≥10% | TPC ≥10% | geo |
|---|---|---|---|---|---|
| B with fallback | ×1.15 | ×1.06 | ×0.94 | ×0.98 | ×1.19 |
| B without fallback | ×1.18 | ×1.10 | ×0.75 | ×1.03 | ×1.18 |
| M without fallback | ×1.00 | ×1.07 | ×0.66 | ×0.88 | ×1.09 |

It is better everywhere except dense YCSB, where the partition itself costs ~20%. With the
512-transaction minimum, the fallback keeps dense YCSB and loses little elsewhere.

### 3.5 Knob sweeps (flat, defaults kept)

| Knob | Values | Result |
|---|---|---|
| `GiantShare` | 0.2 / 0.3 / 0.5 | 0.2 ≈ 0.3 (±0.01); 0.5 −0.01 on B |
| giant kernel dense factor `JoinDense` | 0.5 / 1 / 2 | within ±0.01 |
| `DenseHubShare` (with giant kernel) | 0.5 / 0.6 / 0.7 / 0.8 | 0.6–0.7 tie 0.5 + min512 within 0.01; 0.8 loses dense YCSB |
| `DenseMinTxns` | 128 / 256 / 512 | 128 loses Ethereum (×1.10 vs ×1.18, prefix-giant variant); 512 best with the hub share |
| `SequentialMax` = never parallel | — | ties the default (×1.17 vs ×1.17, prefix-giant variant) |

### 3.6 Sui (×0.94): nothing left to take

Sui blocks are ~10 transactions and ~0.1 µs. The islands and HybridJoinTiny run the same
TinyBitmap kernel. The gap is the repeats selector (one pass summing ≤ 64 slice lengths) and
publishing the single island. Objects per transaction cannot replace the selector: Sui blocks
range over 2.5–18 objects per transaction and the sparse synthetic tiny blocks over 7–18, so no
threshold separates them.

## 4. Methodology

- Every round ran on 3 layout-perturbed builds (the scratchpad `layout_builds.sh`, now over
  `islands`, `competitors` and `model`), 300 blocks per trace, every block parity-checked.
  Leagues were made with `layout_league.py --baseline HybridJoinTiny`. Result files (gitignored):
  `benchmark_results_uf3_r1…r4_L21–23.json`.
- go-test benchmarks (`archive/islands/ufnext/trace_bench_test.go`) were used for profiles only.
  Identical code moved ±30% between go-test runs (not P-core pinned); one go-test comparison
  wrongly suggested the batched partition lost 60% on Ethereum.
- The laptop was on AC for every run, with nothing else CPU-heavy running.

## 5. Reorganization

**`internal/algorithms/islands/`** (the top two UF algorithms):
- `island_shared.go`: the island core, `IslandConfig`, the kernels, and the giant-island
  object-major kernel. It is the `ufnext` fork of the Phase 4 core: the old `JoinDense` for every
  wide island and `JoinMinWidth` are gone; `GiantShare` and `JoinDense` (the dense factor) are new.
- `island_hubs.go`: the shared algorithm `hubIslands` (prefix hub rule, dense fallback, tiny
  path, partitions, both hub layers, whole-block paths) and `HubConfig`.
- `island_hub.go`: **IslandHub**, the matrix hub layer (output contract unchanged).
- `island_bipartite.go`: **IslandBipartite**, the bipartite hub layer (per-hub writer and reader
  lists; this changes the contract).

Defaults, identical for both: prefix 64/3, TinyMax 64, TinyMinRepeats 0.5, DenseHubShare 0.5,
DenseMinTxns 512, GiantShare 0.3, JoinDense 1, and the Phase 4 core knobs.

**Archived** (`internal/algorithms/archive/`, build-ignored; see its README):
- in `islands/`: `island_lean.go`, `island_lean_hub.go`, `island_lean_shared.go` (the Phase 4
  core), `uflab/` and `ufnext/` (with every experiment and probe of both UF rounds);
- in `competitors/`: `sort_join.go`.

**Competitors, top two.** Ranking (3 layouts, geo vs HybridJoinTiny): HybridJoinTiny ×1.00 >
HybridJoin ×0.93 > BitmapTiny ×0.84–0.85 > Bitmap > SortJoin (×0.6 of Bitmap, Phase 3).
HybridJoin is HybridJoinTiny without the one-word kernel. It differs only on Sui (×0.56), so as a
second competitor it would add nothing but an unfair Sui column. **The main suite keeps
HybridJoinTiny and BitmapTiny**, the two distinct algorithms: the strongest competitor and the
classical baseline, both with the fairness kernel. HybridJoin and Bitmap stay as code: they are
the kernels inside the two (and HybridJoin is the islands' dense fallback). They are no longer
registered on their own.

**Registries:**
- `main.go`: BitmapTiny, HybridJoinTiny, IslandHub, IslandBipartite.
- `ufarena.go` (`-suite=uf`): the two competitors as bars, then both island algorithms with
  every knob moved one at a time (names `Hub…` and `Bip…`).
- `cmparena.go` (`-suite=cmp`): BitmapTiny, and HybridJoinTiny with HybridJoin's knobs.
- `parity_test.go`: the competitors, and both island algorithms with each path forced (12
  configurations each). Mutation-checked: breaking the giant kernel, the bipartite layer, the
  tiny path or the fallback each fails the test.

**Scripts:** `show_results.py` defaults to the baseline HybridJoinTiny and reads the island
statistics of IslandHub.

## 6. Open

- **Dense fallback misfires on Solana** (some blocks at ×0.99 instead of ~×1.08). No cheap
  prefix statistic separates them (§3.3). A post-partition rescue (the giant kernel) already
  covers what the fallback misses. The remaining choice is between a stricter fallback (worse
  dense YCSB) and the misfires.
- **Phase 5 (many-core server):** re-tune `SequentialMax` / `ParallelWords` and the CAS
  partition (`archive/islands/ufnext/next_cas.go`, ×0.56 on 6 cores).
- **Contract:** IslandBipartite is ×1.03–1.04 over IslandHub overall, and ×1.14–1.15 on Ethereum.
  Whether the professor's pipeline accepts per-hub lists is still open (`UF_NEXT_RESULTS.md` §6).
