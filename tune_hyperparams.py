"""Short hyperparameter tuning pass for every benchmarked algorithm.

One command, resumable, re-runnable on other hardware:
    python tune_hyperparams.py            run / resume, then write the winners into the sources
    python tune_hyperparams.py --plan     list the runs of the current stage
    python tune_hyperparams.py --report   print the choices from saved results
    python tune_hyperparams.py --check    verify every knob regex matches its source once

Each run rewrites the knob literals in the Go sources, runs `go run . -algs=...`
(full benchmark on all six traces, parity-verified against NaiveSequential)
and stores every per-block time plus the run's [Platform] line in
tuning_results.json. Every algorithm has its own knobs and one algorithm's
knobs never change another's timing, so one run can test a different value
for each algorithm at once.

Stages (each derived only from the results before it, so a rerun resumes):
  1. parallel runs: every sequential threshold 0 (always parallel), one run per
     chunk size; sequential runs: every threshold at the maximum, one run per
     island pairwise cut-off (the non-island algorithms repeat, which measures
     run-to-run noise).
  2. per algorithm: best chunk, best pairwise cut-off, and the sequential
     threshold chosen block by block from the parallel vs sequential times;
     then a coarse hub-threshold scan for the island algorithms.
  3. validation run of the chosen config plus neighbour runs (thresholds x0.5
     and x2, pairwise cut-offs between the tested values); a neighbour replaces
     the stage-2 value only if it wins by more than NOISE_MARGIN against a run
     with the same mix of algorithms.
Score of an algorithm in a run = geometric mean over the six traces of the
geometric mean per-block time (every trace and every block weigh the same).
The winners are written into the sources at the end (--no-apply to skip).

Known limitation (Phase 2, 2026-09-28): the sequential thresholds come from
comparing different runs, and on the development laptop parallel code drifted
~25-35% more than sequential code between runs, biasing the thresholds
towards "sequential". The shipped thresholds were re-measured paired (both
modes back to back on every block in one process); see ALGORITHMS.md.
"""

import argparse
import bisect
import json
import math
import os
import re
import statistics
import subprocess
import sys
import time
from datetime import datetime

REPO_DIR = os.path.dirname(os.path.abspath(__file__))
RESULTS_FILE = os.path.join(REPO_DIR, "tuning_results.json")
BENCH_FILE = os.path.join(REPO_DIR, "benchmark_results.json")
ALG_DIR = os.path.join(REPO_DIR, "internal", "algorithms")

TRACES = ["Ethereum", "EthereumNoHub", "Solana", "Sui", "YCSB", "TPC"]
MAX_THRESHOLD = 3000  # = model.MaximumGraphSize: "always sequential"
HUB_OFF = 10**9

# knob -> (file, regex with one capture group for the prefix)
KNOBS = {}
for _file, _names in {
    "bitmap.go": ["BitmapSequentialMax", "BitmapChunkRows"],
    "id_bucket_construction.go": ["IDBucketSequentialMax", "IDBucketChunkRows"],
    "naive_parallel.go": ["NaiveParallelSequentialMax", "NaiveParallelChunkRows"],
    "island_native_shattered.go": ["NativePairwiseMax", "NativeSequentialMax", "NativeSplitRows"],
    "island_topology_shattered.go": ["TopologyPairwiseMax", "TopologySequentialMax", "TopologySplitRows"],
    "island_shared.go": ["ObjectHubThreshold"],
}.items():
    for _name in _names:
        KNOBS[_name] = (os.path.join(ALG_DIR, _file),
                        re.compile(r"(const " + _name + r"(?: uint32)? = )\d+"))

# Benchmark names and their knobs.
ALGS = {
    "Bitmap": {"seq": "BitmapSequentialMax", "chunk": "BitmapChunkRows"},
    "IDBucket": {"seq": "IDBucketSequentialMax", "chunk": "IDBucketChunkRows"},
    "NaiveParallel": {"seq": "NaiveParallelSequentialMax", "chunk": "NaiveParallelChunkRows"},
    "IslandNativeShattered": {"seq": "NativeSequentialMax", "pw": "NativePairwiseMax",
                              "chunk": "NativeSplitRows"},
    "IslandTopologyShattered": {"seq": "TopologySequentialMax", "pw": "TopologyPairwiseMax",
                                "chunk": "TopologySplitRows"},
}
ISLAND_ALGS = ["IslandNativeShattered", "IslandTopologyShattered"]
ROW_ALGS = ["Bitmap", "IDBucket"]

ROW_CHUNKS = [16, 64, 256]
NAIVE_CHUNKS = [2, 8, 32]
PAIRWISE = [64, 2, 4, 8, 16, 24, 32, 48]
HUBS = [20, 50, 150, 500, 2000]

GO_RUN_TIMEOUT_SEC = 3 * 3600

# Whole-run scores of an unchanged algorithm drift by up to ~x1.1 between runs
# on the development laptop, so stage 3 only overrides a stage-2 choice that
# the neighbour beats by more than this factor.
NOISE_MARGIN = 1.05


# --- run plan -------------------------------------------------------------------

def run_spec(name, stage, config, algs):
    return {"name": name, "stage": stage, "config": config, "algs": algs}


def all_seq(value):
    return {ALGS[a]["seq"]: value for a in ALGS}


def stage1():
    runs = []
    for i, (row_chunk, naive_chunk) in enumerate(zip(ROW_CHUNKS, NAIVE_CHUNKS)):
        cfg = {**all_seq(0), "ObjectHubThreshold": HUB_OFF,
               "BitmapChunkRows": row_chunk, "IDBucketChunkRows": row_chunk,
               "NativeSplitRows": row_chunk, "TopologySplitRows": row_chunk,
               "NaiveParallelChunkRows": naive_chunk,
               "NativePairwiseMax": 64, "TopologyPairwiseMax": 64}
        runs.append(run_spec(f"P{i}-chunk{row_chunk}/{naive_chunk}", 1, cfg, list(ALGS)))
    for i, pw in enumerate(PAIRWISE):
        cfg = {**all_seq(MAX_THRESHOLD), "ObjectHubThreshold": HUB_OFF,
               "NativePairwiseMax": pw, "TopologyPairwiseMax": pw}
        algs = ROW_ALGS + ISLAND_ALGS
        if i == 0:
            algs = algs + ["NaiveSequential"]  # the sequential-mode time of NaiveParallel
        runs.append(run_spec(f"S{i}-pw{pw}", 1, cfg, algs))
    # Interleave P and S runs so slow thermal drift hits both modes alike.
    p_runs, s_runs = runs[:len(ROW_CHUNKS)], runs[len(ROW_CHUNKS):]
    return [r for pair in zip(p_runs, s_runs) for r in pair] + s_runs[len(p_runs):]


def stage2(results):
    choice = choose_stage1(results)
    runs = []
    for hub in HUBS:
        cfg = {**choice["config"], "ObjectHubThreshold": hub}
        runs.append(run_spec(f"H-hub{hub}", 2, cfg, ISLAND_ALGS))
    return runs


def stage3(results):
    choice = choose_stage2(results)
    cfg = choice["config"]
    runs = [run_spec("V-chosen", 3, cfg, list(ALGS))]
    for factor, label in ((0.5, "half"), (2, "double")):
        scaled = {**cfg}
        for a in ALGS:
            knob = ALGS[a]["seq"]
            if 0 < cfg[knob] < MAX_THRESHOLD:
                scaled[knob] = min(MAX_THRESHOLD, max(1, int(cfg[knob] * factor)))
        runs.append(run_spec(f"V-seq-{label}", 3, scaled, list(ALGS)))
    runs.append(run_spec("V-pw-center", 3, cfg, ISLAND_ALGS))  # same load mix as its neighbours
    for side in ("lower", "upper"):
        pw_cfg = {**cfg}
        for a in ISLAND_ALGS:
            knob = ALGS[a]["pw"]
            pw_cfg[knob] = pairwise_neighbour(cfg[knob], side)
        runs.append(run_spec(f"V-pw-{side}", 3, pw_cfg, ISLAND_ALGS))
    return runs


def pairwise_neighbour(value, side):
    """Midpoint between value and its neighbour among the tested cut-offs."""
    tested = sorted(set(PAIRWISE))
    i = tested.index(value)
    j = i - 1 if side == "lower" else i + 1
    if j == len(tested):
        return value * 3 // 2  # the largest tested value won: look beyond it
    if j < 0:
        return value
    return (value + tested[j]) // 2


def build_plan(results):
    """Every stage whose inputs are complete, in order."""
    plan = stage1()
    for next_stage in (stage2, stage3):
        if not all(r["name"] in results["runs"] for r in plan):
            break
        plan += next_stage(results)
    return plan


# --- scoring --------------------------------------------------------------------

def geomean(values):
    return math.exp(statistics.fmean(math.log(max(v, 1e-9)) for v in values))


def score(times_by_trace):
    """Geomean over traces of the geomean per-block time (ms)."""
    per_trace = [geomean(t) for t in times_by_trace.values() if t]
    return geomean(per_trace) if per_trace else float("inf")


def run_times(results, name, alg):
    run = results["runs"].get(name)
    return run["times"].get(alg) if run else None


def best_run(results, names, alg):
    scored = [(score(run_times(results, n, alg)), n) for n in names if run_times(results, n, alg)]
    return min(scored)[1] if scored else None


def median_times(results, names, alg):
    """Per-block median over several runs of the same mode."""
    runs = [run_times(results, n, alg) for n in names if run_times(results, n, alg)]
    return {tr: [statistics.median(vals) for vals in zip(*(r[tr] for r in runs))] for tr in runs[0]}


def best_threshold(sizes, seq_times, par_times):
    """(log score, T) of the sequential threshold T minimising the score when
    blocks of size <= T run sequentially and larger ones in parallel (prefix
    sums over blocks sorted by size: one bisection per trace and candidate)."""
    tables = []
    for tr in sizes:
        if not sizes[tr]:
            continue
        rows = sorted(zip(sizes[tr], seq_times[tr], par_times[tr]))
        ns = [n for n, _, _ in rows]
        seq_prefix, par_suffix = [0.0], [0.0]
        for _, s, _ in rows:
            seq_prefix.append(seq_prefix[-1] + math.log(max(s, 1e-9)))
        for _, _, p in reversed(rows):
            par_suffix.append(par_suffix[-1] + math.log(max(p, 1e-9)))
        tables.append((ns, seq_prefix, par_suffix[::-1]))
    candidates = sorted({0, MAX_THRESHOLD} | {n for tr in sizes for n in sizes[tr]})
    best = None
    for threshold in candidates:
        total = 0.0
        for ns, seq_prefix, par_suffix in tables:
            k = bisect.bisect_right(ns, threshold)  # blocks [0, k) run sequentially
            total += (seq_prefix[k] + par_suffix[k]) / len(ns)
        if best is None or total < best[0]:
            best = (total, threshold)
    return best


def choose_stage1(results):
    """Chunk, pairwise cut-off and sequential threshold per algorithm."""
    p_runs = [r["name"] for r in stage1() if r["name"].startswith("P")]
    s_runs = [r["name"] for r in stage1() if r["name"].startswith("S")]
    sizes = results["sizes"]
    cfg = {"ObjectHubThreshold": HUB_OFF}
    why = {}
    for alg, knobs in ALGS.items():
        if alg in ISLAND_ALGS:
            best_s = best_run(results, s_runs, alg)
            cfg[knobs["pw"]] = results["runs"][best_s]["config"][knobs["pw"]]
            seq_times = run_times(results, "S0-pw64", alg)  # same cut-off as the P runs
        elif alg == "NaiveParallel":
            seq_times = run_times(results, "S0-pw64", "NaiveSequential")
        else:
            seq_times = median_times(results, s_runs, alg)
        # Chunk and threshold are chosen jointly: a large chunk alone would
        # "win" on small blocks just by leaving a single worker.
        _, threshold, best_p = min(best_threshold(sizes, seq_times, run_times(results, p, alg)) + (p,)
                                   for p in p_runs)
        cfg[knobs["chunk"]] = results["runs"][best_p]["config"][knobs["chunk"]]
        # Sequential up to the largest measured block = never parallel here.
        largest = max(n for tr in sizes for n in sizes[tr])
        cfg[knobs["seq"]] = MAX_THRESHOLD if threshold >= largest else threshold
        why[alg] = f"chunk from {best_p}" + (f", pw from {best_s}" if alg in ISLAND_ALGS else "")
    return {"config": cfg, "why": why}


def choose_stage2(results):
    """Stage-1 choice plus the hub threshold of the island family."""
    choice = choose_stage1(results)
    s_best = {alg: best_run(results, [r["name"] for r in stage1() if r["name"].startswith("S")], alg)
              for alg in ISLAND_ALGS}
    # Hub off is represented by each algorithm's best sequential run; the shared
    # hub knob is decided by Topology, the family's champion.
    alg = "IslandTopologyShattered"
    candidates = [(score(run_times(results, s_best[alg], alg)), HUB_OFF)]
    for hub in HUBS:
        t = run_times(results, f"H-hub{hub}", alg)
        if t:
            candidates.append((score(t), hub))
    choice["config"]["ObjectHubThreshold"] = min(candidates)[1]
    return choice


def choose_final(results):
    """Stage-2 choice, moved to a neighbour value only when the neighbour beats
    it by more than NOISE_MARGIN in runs with the same load mix (a run's mix of
    algorithms shifts every score in it, so only like runs are compared)."""
    cfg = dict(choose_stage2(results)["config"])
    centers = {"seq": "V-chosen", "pw": "V-pw-center"}
    for alg, knobs in ALGS.items():
        for kind, center in centers.items():
            if kind not in knobs or not run_times(results, center, alg):
                continue
            best_score, best_value = score(run_times(results, center, alg)) / NOISE_MARGIN, cfg[knobs[kind]]
            for name in (r["name"] for r in stage3(results) if r["name"].startswith(f"V-{kind}-")):
                t = run_times(results, name, alg)
                if t and name != center and score(t) < best_score:
                    best_score, best_value = score(t), results["runs"][name]["config"][knobs[kind]]
            cfg[knobs[kind]] = best_value
    return cfg


# --- sources --------------------------------------------------------------------

def read_constants():
    values = {}
    for knob, (path, pattern) in KNOBS.items():
        with open(path, "r", encoding="utf-8") as f:
            m = pattern.search(f.read())
        values[knob] = int(m.group(0)[len(m.group(1)):]) if m else None
    return values


def set_constants(cfg):
    for knob, value in cfg.items():
        path, pattern = KNOBS[knob]
        with open(path, "r", encoding="utf-8") as f:
            src = f.read()
        new_src, count = pattern.subn(lambda m: m.group(1) + str(value), src)
        if count != 1:
            raise RuntimeError(f"expected exactly 1 match for {knob} in {os.path.basename(path)}, found {count}")
        if new_src != src:
            with open(path, "w", encoding="utf-8", newline="\n") as f:
                f.write(new_src)


def check_patterns():
    ok = True
    for knob, (path, pattern) in KNOBS.items():
        with open(path, "r", encoding="utf-8") as f:
            matches = pattern.findall(f.read())
        print(f"  {knob:<28} {os.path.basename(path):<30} matches={len(matches)}")
        ok &= len(matches) == 1
    return ok


# --- running --------------------------------------------------------------------

def parse_ms(time_str):
    s = time_str.replace("Â", "").strip()
    for unit, scale in (("µs", 1e-3), ("us", 1e-3), ("ns", 1e-6), ("ms", 1.0), ("s", 1e3)):
        if s.endswith(unit):
            return float(s[:-len(unit)]) * scale
    return 0.0


def execute(run, results):
    started = time.time()
    set_constants(run["config"])
    if os.path.exists(BENCH_FILE):
        os.remove(BENCH_FILE)
    args = ["go", "run", ".", "-algs=" + ",".join(run["algs"])]
    print(f"    $ {' '.join(args)}", flush=True)
    proc = subprocess.run(args, cwd=REPO_DIR, capture_output=True, text=True,
                          encoding="utf-8", errors="replace", timeout=GO_RUN_TIMEOUT_SEC)
    if proc.returncode != 0:
        tail = "\n".join((proc.stdout + proc.stderr).strip().splitlines()[-15:])
        raise RuntimeError(f"go run failed (exit {proc.returncode}):\n{tail}")
    platform = next((l for l in proc.stdout.splitlines() if l.startswith("[Platform]")), "")

    with open(BENCH_FILE, "r", encoding="utf-8") as f:
        blocks = json.load(f)
    sizes = {tr: [b["num_txns"] for b in blocks if b["blockchain"] == tr] for tr in TRACES}
    if results.setdefault("sizes", sizes) != sizes:
        raise RuntimeError("block list differs from earlier runs")
    times = {alg: {tr: [parse_ms(b["times"][alg]) for b in blocks if b["blockchain"] == tr]
                   for tr in TRACES} for alg in run["algs"]}
    results["runs"][run["name"]] = {
        **run, "platform": platform, "finished": datetime.now().isoformat(timespec="seconds"),
        "duration_sec": round(time.time() - started, 1), "times": times,
    }
    save_results(results)


def load_results():
    if os.path.exists(RESULTS_FILE):
        with open(RESULTS_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    return {"runs": {}}


def save_results(results):
    tmp = RESULTS_FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8", newline="\n") as f:
        json.dump(results, f)
    os.replace(tmp, RESULTS_FILE)


# --- reporting ------------------------------------------------------------------

def print_report(results):
    runs = results["runs"]
    if not runs:
        print("No completed runs yet.")
        return
    print(f"\n=== {len(runs)} runs; score = geomean over traces of geomean per-block ms ===")
    platforms = {r["platform"] for r in runs.values()}
    for p in platforms:
        print(f"  {p}")
    algs = list(ALGS) + ["NaiveSequential"]
    print(f"\n{'run':<22}{'min':>6}" + "".join(f"{a.replace('Island', '').replace('Shattered', ''):>14}" for a in algs))
    for name, run in runs.items():
        cells = "".join(f"{score(run['times'][a]):>14.5f}" if a in run["times"] else f"{'':>14}" for a in algs)
        print(f"{name:<22}{run['duration_sec'] / 60:>6.1f}{cells}")

    # Run-to-run noise: the sequential runs repeat Bitmap and IDBucket unchanged.
    s_runs = [n for n in runs if n.startswith("S")]
    for alg in ROW_ALGS:
        scores = [score(runs[n]["times"][alg]) for n in s_runs if alg in runs[n]["times"]]
        if len(scores) > 1:
            print(f"\n  noise, {alg} repeated {len(scores)}x: max/min score x{max(scores) / min(scores):.3f}")

    if all(r["name"] in runs for r in stage1()):
        print("\nStage-1 choice:", choose_stage1(results)["config"])
    if all(r["name"] in runs for r in stage1() + stage2(results)):
        print("Stage-2 choice:", choose_stage2(results)["config"])
    plan = build_plan(results)
    if all(r["name"] in runs for r in plan) and any(r["stage"] == 3 for r in plan):
        print("Final choice:  ", choose_final(results))


# --- main -----------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--plan", action="store_true", help="list pending runs and exit")
    parser.add_argument("--report", action="store_true", help="print the results and exit")
    parser.add_argument("--check", action="store_true", help="verify the knob regexes and exit")
    parser.add_argument("--no-apply", action="store_true", help="do not write the winners into the sources")
    args = parser.parse_args()

    if args.check:
        sys.exit(0 if check_patterns() else 1)
    results = load_results()
    if args.report:
        print_report(results)
        return
    if args.plan:
        for r in build_plan(results):
            state = "done" if r["name"] in results["runs"] else "pending"
            print(f"  stage {r['stage']} {r['name']:<22} {state:<8} algs={','.join(r['algs'])}")
        return
    if not check_patterns():
        sys.exit("knob patterns did not match - aborting before touching anything")

    original = read_constants()
    final = None
    try:
        while True:
            pending = [r for r in build_plan(results) if r["name"] not in results["runs"]]
            if not pending:
                break
            run = pending[0]
            print(f"\n[stage {run['stage']}] {run['name']} ({len(pending)} pending in this stage)", flush=True)
            execute(run, results)
            print(f"    -> {results['runs'][run['name']]['duration_sec'] / 60:.1f} min", flush=True)
        final = choose_final(results)
    except KeyboardInterrupt:
        print("\nInterrupted - progress is saved; rerun to resume.")
    finally:
        set_constants(final if final and not args.no_apply else original)
        print("Sources set to the " + ("tuned" if final and not args.no_apply else "original") + " knob values.")
    print_report(results)


if __name__ == "__main__":
    main()
