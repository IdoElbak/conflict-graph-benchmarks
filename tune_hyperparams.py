"""Short hyperparameter tuning pass for the row algorithm Bitmap (IDBucket was
archived 2026-10-11).

One command, resumable, re-runnable on other hardware:
    python tune_hyperparams.py            run / resume, then write the winners into the sources
    python tune_hyperparams.py --plan     list the runs of the current stage
    python tune_hyperparams.py --report   print the choices from saved results
    python tune_hyperparams.py --check    verify every knob regex matches its source once

The island algorithms (IslandLean, IslandLeanHub) are not tuned here: their
knobs are a run-time config, so the proving ground times their settings side
by side in one run (`go run . -suite=uf`, registry in ufarena.go).

Each run rewrites the knob literals in the Go sources, runs
`go run . -algs=Bitmap` (all six traces, parity-verified against
NaiveSequential) and stores every per-block time plus the run's [Platform]
line in tuning_results.json.

Stages (each derived only from the results before it, so a rerun resumes):
  1. parallel runs: both sequential thresholds 0 (always parallel), one run per
     chunk size; sequential runs: both thresholds at the maximum, repeated
     SEQUENTIAL_REPEATS times (interleaved with the parallel runs; the repeats
     also measure run-to-run noise).
  2. per algorithm: the best chunk and the sequential threshold chosen block by
     block from the parallel vs the median sequential times; then a validation
     run of that choice plus neighbour runs (thresholds x0.5 and x2). A
     neighbour replaces the stage-1 value only if it wins by more than
     NOISE_MARGIN.
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
ALG_DIR = os.path.join(REPO_DIR, "internal", "algorithms", "competitors")

TRACES = ["Ethereum", "EthereumNoHub", "Solana", "Sui", "YCSB", "TPC"]
MAX_THRESHOLD = 3000  # = model.MaximumGraphSize: "always sequential"

# knob -> (file, regex with one capture group for the prefix)
KNOBS = {}
for _file, _names in {
    "bitmap.go": ["BitmapSequentialMax", "BitmapChunkRows"],
}.items():
    for _name in _names:
        KNOBS[_name] = (os.path.join(ALG_DIR, _file),
                        re.compile(r"(const " + _name + r"(?: uint32)? = )\d+"))

# Benchmark names and their knobs.
ALGS = {
    "Bitmap": {"seq": "BitmapSequentialMax", "chunk": "BitmapChunkRows"},
}

CHUNKS = [16, 64, 256]
SEQUENTIAL_REPEATS = 3

GO_RUN_TIMEOUT_SEC = 3600

# Whole-run scores of an unchanged algorithm drift by up to ~x1.1 between runs
# on the development laptop, so stage 2 only overrides a stage-1 choice that
# the neighbour beats by more than this factor.
NOISE_MARGIN = 1.05


# --- run plan -------------------------------------------------------------------

def run_spec(name, stage, config):
    return {"name": name, "stage": stage, "config": config, "algs": list(ALGS)}


def all_seq(value):
    return {ALGS[a]["seq"]: value for a in ALGS}


def stage1():
    p_runs = [run_spec(f"P{i}-chunk{chunk}", 1, {**all_seq(0), **{ALGS[a]["chunk"]: chunk for a in ALGS}})
              for i, chunk in enumerate(CHUNKS)]
    s_runs = [run_spec(f"S{i}", 1, all_seq(MAX_THRESHOLD)) for i in range(SEQUENTIAL_REPEATS)]
    # Interleave P and S runs so slow thermal drift hits both modes alike.
    return [r for pair in zip(p_runs, s_runs) for r in pair] + p_runs[len(s_runs):] + s_runs[len(p_runs):]


def stage2(results):
    cfg = choose_stage1(results)["config"]
    runs = [run_spec("V-chosen", 2, cfg)]
    for factor, label in ((0.5, "half"), (2, "double")):
        scaled = {**cfg}
        for a in ALGS:
            knob = ALGS[a]["seq"]
            if 0 < cfg[knob] < MAX_THRESHOLD:
                scaled[knob] = min(MAX_THRESHOLD, max(1, int(cfg[knob] * factor)))
        runs.append(run_spec(f"V-seq-{label}", 2, scaled))
    return runs


def build_plan(results):
    """Every stage whose inputs are complete, in order."""
    plan = stage1()
    if all(r["name"] in results["runs"] for r in plan):
        plan += stage2(results)
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
    """Chunk and sequential threshold per algorithm."""
    p_runs = [r["name"] for r in stage1() if r["name"].startswith("P")]
    s_runs = [r["name"] for r in stage1() if r["name"].startswith("S")]
    sizes = results["sizes"]
    largest = max(n for tr in sizes for n in sizes[tr])
    cfg, why = {}, {}
    for alg, knobs in ALGS.items():
        seq_times = median_times(results, s_runs, alg)
        # Chunk and threshold are chosen jointly: a large chunk alone would
        # "win" on small blocks just by leaving a single worker.
        _, threshold, best_p = min(best_threshold(sizes, seq_times, run_times(results, p, alg)) + (p,)
                                   for p in p_runs)
        cfg[knobs["chunk"]] = results["runs"][best_p]["config"][knobs["chunk"]]
        # Sequential up to the largest measured block = never parallel here.
        cfg[knobs["seq"]] = MAX_THRESHOLD if threshold >= largest else threshold
        why[alg] = f"chunk from {best_p}"
    return {"config": cfg, "why": why}


def choose_final(results):
    """Stage-1 choice, a threshold moved to a neighbour value only when the
    neighbour beats it by more than NOISE_MARGIN."""
    cfg = dict(choose_stage1(results)["config"])
    for alg, knobs in ALGS.items():
        if not run_times(results, "V-chosen", alg):
            continue
        best_score, best_value = score(run_times(results, "V-chosen", alg)) / NOISE_MARGIN, cfg[knobs["seq"]]
        for name in ("V-seq-half", "V-seq-double"):
            t = run_times(results, name, alg)
            if t and score(t) < best_score:
                best_score, best_value = score(t), results["runs"][name]["config"][knobs["seq"]]
        cfg[knobs["seq"]] = best_value
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
    algs = list(ALGS)
    print(f"\n{'run':<22}{'min':>6}" + "".join(f"{a:>14}" for a in algs))
    for name, run in runs.items():
        cells = "".join(f"{score(run['times'][a]):>14.5f}" if a in run["times"] else f"{'':>14}" for a in algs)
        print(f"{name:<22}{run['duration_sec'] / 60:>6.1f}{cells}")

    # Run-to-run noise: the sequential runs repeat the same code unchanged.
    s_runs = [n for n in runs if n.startswith("S")]
    for alg in ALGS:
        scores = [score(runs[n]["times"][alg]) for n in s_runs if alg in runs[n]["times"]]
        if len(scores) > 1:
            print(f"\n  noise, {alg} repeated {len(scores)}x: max/min score x{max(scores) / min(scores):.3f}")

    if all(r["name"] in runs for r in stage1()):
        print("\nStage-1 choice:", choose_stage1(results)["config"])
    plan = build_plan(results)
    if all(r["name"] in runs for r in plan) and any(r["stage"] == 2 for r in plan):
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
