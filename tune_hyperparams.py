"""Hyperparameter grid search for the conflict-graph algorithms.

Tunes the three live compile-time constants (all in island_shared.go since the
2026-07-18 cleanup):
  - PairwiseIslandMax   - native + topology
  - SequentialBlockMax  - native + topology
  - ObjectHubThreshold  - all island algorithms

Pipeline per grid point (exactly as specified):
  1. rewrite the const literals in the two Go files
  2. go clean -cache
  3. delete benchmark_results.json
  4. go run .           (full benchmark, includes bit-for-bit parity check)
  5. summarize benchmark_results.json into tuning_results.json (crash-safe)
  6. sleep 3 minutes to let the CPU cool down

Phases:
  Phase 0: very wide one-factor-at-a-time scans + a coarse full factorial.
  Phase 1..N (REFINE_ROUNDS): once the previous phase is fully done, a finer
  3x3x3 lattice is generated around the best config found so far (geometric
  midpoints toward the neighboring tested values on each axis). Deterministic,
  so Ctrl+C + rerun always resumes exactly where it left off.

Already-completed configs (keyed by parameter triple) are skipped on rerun.
The tuned in-code constants (64 / 3000 / 10**9, the 2026-07-11 grid-search
winner) are always restored on exit, including on Ctrl+C or crash.

Usage:
  python tune_hyperparams.py            run / resume the search
  python tune_hyperparams.py --plan     list pending configs without running
  python tune_hyperparams.py --report   print the leaderboard from saved results
  python tune_hyperparams.py --check    verify the const regexes match the sources
"""

import argparse
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

SHARED_GO = os.path.join(REPO_DIR, "internal", "algorithms", "island_shared.go")

# (file, regex-with-one-capture-group-for-the-prefix) per parameter.
CONST_PATTERNS = {
    "PairwiseIslandMax": (SHARED_GO, re.compile(r"(const PairwiseIslandMax = )\d+")),
    "SequentialBlockMax": (SHARED_GO, re.compile(r"(const SequentialBlockMax uint32 = )\d+")),
    "ObjectHubThreshold": (SHARED_GO, re.compile(r"(const ObjectHubThreshold uint32 = )\d+")),
}

# Restored on every exit; must stay in sync with the tuned in-code values,
# otherwise a tuning session silently downgrades the sources it touched.
BASELINE = {"PairwiseIslandMax": 64, "SequentialBlockMax": 3000, "ObjectHubThreshold": 10**9}

ALGORITHMS = [
    "Bitmap",
    "IslandNativeShattered",
    "IslandTopologyShattered",
]

COOLDOWN_SEC = 180
GO_RUN_TIMEOUT_SEC = 3600
REFINE_ROUNDS = 2

# Deliberately wide ranges (uint32-safe): 10**9 hub threshold means "no hubs",
# SequentialBlockMax 0 means "always parallel", 3000 means "always sequential".
PW_WIDE = [2, 3, 4, 6, 8, 12, 16, 24, 32, 64]
SEQ_WIDE = [0, 64, 128, 256, 384, 512, 768, 1024, 1536, 3000]
HUB_WIDE = [5, 10, 20, 35, 50, 75, 100, 150, 300, 10**9]

PW_COARSE = [2, 6, 16, 64]
SEQ_COARSE = [0, 384, 1024, 3000]
HUB_COARSE = [10, 50, 150, 10**9]


def config_key(cfg):
    return f"pw={cfg['PairwiseIslandMax']}|seq={cfg['SequentialBlockMax']}|hub={cfg['ObjectHubThreshold']}"


def phase0_configs():
    configs = []
    for pw in PW_WIDE:
        configs.append({**BASELINE, "PairwiseIslandMax": pw})
    for seq in SEQ_WIDE:
        configs.append({**BASELINE, "SequentialBlockMax": seq})
    for hub in HUB_WIDE:
        configs.append({**BASELINE, "ObjectHubThreshold": hub})
    for pw in PW_COARSE:
        for seq in SEQ_COARSE:
            for hub in HUB_COARSE:
                configs.append({
                    "PairwiseIslandMax": pw,
                    "SequentialBlockMax": seq,
                    "ObjectHubThreshold": hub,
                })
    return dedupe(configs)


def dedupe(configs):
    seen, out = set(), []
    for cfg in configs:
        key = config_key(cfg)
        if key not in seen:
            seen.add(key)
            out.append(cfg)
    return out


def midpoint(a, b):
    """Geometric midpoint (log-scale friendly for the huge hub range)."""
    if a == 0 or b == 0:
        return (a + b) // 2
    return int(round(math.sqrt(a * b)))


def refine_configs(prev_configs, results):
    """A finer 3x3x3 lattice around the best completed config of the previous
    phases: on each axis, the best value plus geometric midpoints toward its
    nearest tested neighbors. Deterministic given the previous phase set."""
    completed = [c for c in prev_configs if get_ok_result(results, c)]
    if not completed:
        return []
    best = max(completed, key=lambda c: get_ok_result(results, c)["score"])

    axes = {}
    for param in BASELINE:
        tested = sorted({c[param] for c in prev_configs})
        val = best[param]
        idx = tested.index(val)
        values = {val}
        if idx > 0:
            values.add(midpoint(tested[idx - 1], val))
        if idx < len(tested) - 1:
            values.add(midpoint(val, tested[idx + 1]))
        axes[param] = sorted(values)

    configs = []
    for pw in axes["PairwiseIslandMax"]:
        for seq in axes["SequentialBlockMax"]:
            for hub in axes["ObjectHubThreshold"]:
                configs.append({
                    "PairwiseIslandMax": pw,
                    "SequentialBlockMax": seq,
                    "ObjectHubThreshold": hub,
                })
    prev_keys = {config_key(c) for c in prev_configs}
    return [c for c in dedupe(configs) if config_key(c) not in prev_keys]


def build_phases(results):
    """Phase 0 is fixed; each refinement phase is derived only from the phases
    before it, so the sequence is reproducible across restarts. Refinement is
    generated only once the previous phases are fully completed."""
    phases = [phase0_configs()]
    for _ in range(REFINE_ROUNDS):
        prev = [c for phase in phases for c in phase]
        if not all(get_result(results, c) for c in prev):
            break  # previous phase still has pending work
        nxt = refine_configs(prev, results)
        if not nxt:
            break
        phases.append(nxt)
    return phases


# --- constant rewriting -----------------------------------------------------

def set_constants(cfg):
    for param, value in cfg.items():
        path, pattern = CONST_PATTERNS[param]
        with open(path, "r", encoding="utf-8") as f:
            src = f.read()
        new_src, count = pattern.subn(lambda m: m.group(1) + str(value), src)
        if count != 1:
            raise RuntimeError(
                f"Expected exactly 1 match for {param} in {os.path.basename(path)}, found {count}. "
                "The const declaration changed - update CONST_PATTERNS.")
        if new_src != src:
            with open(path, "w", encoding="utf-8") as f:
                f.write(new_src)


def check_patterns():
    for param, (path, pattern) in CONST_PATTERNS.items():
        with open(path, "r", encoding="utf-8") as f:
            src = f.read()
        matches = pattern.findall(src)
        current = pattern.search(src)
        print(f"  {param:<20} matches={len(matches)}  line: {current.group(0) if current else 'NOT FOUND'}")
        if len(matches) != 1:
            return False
    return True


# --- benchmark run + summary ------------------------------------------------

def parse_ms(time_str):
    s = time_str.replace("Â", "").strip()
    if "µs" in s or "us" in s:
        return float(s.replace("µs", "").replace("us", "")) / 1000.0
    if "ns" in s:
        return float(s.replace("ns", "")) / 1e6
    if "ms" in s:
        return float(s.replace("ms", ""))
    if "s" in s:
        return float(s.replace("s", "")) * 1000.0
    return 0.0


def percentile(sorted_vals, p):
    if not sorted_vals:
        return 0.0
    k = (len(sorted_vals) - 1) * p / 100.0
    f = int(k)
    c = min(f + 1, len(sorted_vals) - 1)
    return sorted_vals[f] + (sorted_vals[c] - sorted_vals[f]) * (k - f)


def ratio_stats(blocks, alg_num, alg_den):
    """Per-block alg_num/alg_den time ratios: median + win rate of alg_den."""
    ratios, wins = [], 0
    for b in blocks:
        num = parse_ms(b["times"][alg_num])
        den = parse_ms(b["times"][alg_den])
        if den > 0:
            ratios.append(num / den)
            if den < num:
                wins += 1
    if not ratios:
        return None
    return {
        "median_ratio": round(statistics.median(ratios), 4),
        "win_rate": round(wins / len(ratios), 4),
    }


def summarize_benchmark():
    with open(BENCH_FILE, "r", encoding="utf-8") as f:
        data = json.load(f)

    chains = {}
    for chain in ("Ethereum", "Solana"):
        blocks = [d for d in data if d["blockchain"] == chain]
        if not blocks:
            continue
        algs = {}
        for alg in ALGORITHMS:
            vals = sorted(parse_ms(b["times"][alg]) for b in blocks if alg in b["times"])
            if vals:
                algs[alg] = {
                    "median_ms": round(statistics.median(vals), 5),
                    "mean_ms": round(statistics.fmean(vals), 5),
                    "p95_ms": round(percentile(vals, 95), 5),
                }
        chains[chain] = {
            "blocks": len(blocks),
            "algs": algs,
            "topo_vs_bitmap": ratio_stats(blocks, "Bitmap", "IslandTopologyShattered"),
            "topo_vs_native": ratio_stats(blocks, "IslandNativeShattered", "IslandTopologyShattered"),
            "native_vs_bitmap": ratio_stats(blocks, "Bitmap", "IslandNativeShattered"),
        }

    # Score: geometric mean across chains of Topology's median speedup vs
    # same-run Bitmap (Bitmap has no hyperparameters, so it doubles as a
    # per-run machine-noise normalizer). Higher is better.
    speedups = [chains[c]["topo_vs_bitmap"]["median_ratio"]
                for c in chains if chains[c].get("topo_vs_bitmap")]
    score = round(math.prod(speedups) ** (1 / len(speedups)), 4) if speedups else 0.0
    return chains, score


def run_command(args, timeout=None):
    print(f"    $ {' '.join(args)}", flush=True)
    proc = subprocess.run(args, cwd=REPO_DIR, capture_output=True, text=True, timeout=timeout)
    if proc.returncode != 0:
        tail = "\n".join((proc.stdout + "\n" + proc.stderr).strip().splitlines()[-15:])
        raise RuntimeError(f"{' '.join(args)} failed (exit {proc.returncode}):\n{tail}")


def run_config(cfg, results):
    key = config_key(cfg)
    started = time.time()
    set_constants(cfg)
    run_command(["go", "clean", "-cache"])
    if os.path.exists(BENCH_FILE):
        os.remove(BENCH_FILE)
    run_command(["go", "run", "."], timeout=GO_RUN_TIMEOUT_SEC)
    chains, score = summarize_benchmark()
    results["runs"][key] = {
        "params": cfg,
        "status": "ok",
        "started": datetime.now().isoformat(timespec="seconds"),
        "duration_sec": round(time.time() - started, 1),
        "score": score,
        "chains": chains,
    }
    save_results(results)


# --- results persistence -----------------------------------------------------

def load_results():
    if os.path.exists(RESULTS_FILE):
        with open(RESULTS_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    return {"runs": {}}


def save_results(results):
    tmp = RESULTS_FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(results, f, indent=1)
    os.replace(tmp, RESULTS_FILE)


def get_result(results, cfg):
    return results["runs"].get(config_key(cfg))


def get_ok_result(results, cfg):
    entry = results["runs"].get(config_key(cfg))
    return entry if entry and entry.get("status") == "ok" else None


# --- reporting ----------------------------------------------------------------

def print_report(results):
    entries = [e for e in results["runs"].values() if e.get("status") == "ok"]
    if not entries:
        print("No completed runs in tuning_results.json yet.")
        return
    entries.sort(key=lambda e: e["score"], reverse=True)

    print(f"\n=== Leaderboard ({len(entries)} completed configs, score = geomean Topology speedup vs Bitmap) ===")
    print(f"{'rank':<5}{'pw':>5}{'seq':>6}{'hub':>12}{'score':>8}   "
          f"{'Eth t-vs-b':>11}{'Sol t-vs-b':>11}{'Eth t-vs-n':>11}{'Sol t-vs-n':>11}")
    for rank, e in enumerate(entries[:15], 1):
        p = e["params"]
        cells = []
        for chain in ("Ethereum", "Solana"):
            for comp in ("topo_vs_bitmap", "topo_vs_native"):
                stat = e["chains"].get(chain, {}).get(comp)
                cells.append(f"x{stat['median_ratio']:.3f}" if stat else "-")
        # reorder: Eth bitmap, Sol bitmap, Eth native, Sol native
        eth_b, eth_n, sol_b, sol_n = cells
        print(f"{rank:<5}{p['PairwiseIslandMax']:>5}{p['SequentialBlockMax']:>6}"
              f"{p['ObjectHubThreshold']:>12}{e['score']:>8.4f}   "
              f"{eth_b:>11}{sol_b:>11}{eth_n:>11}{sol_n:>11}")

    print("\n=== Best config per algorithm (by median ms, per chain) ===")
    for chain in ("Ethereum", "Solana"):
        print(f"  {chain}:")
        for alg in ALGORITHMS:
            best = None
            for e in entries:
                stat = e["chains"].get(chain, {}).get("algs", {}).get(alg)
                if stat and (best is None or stat["median_ms"] < best[0]):
                    best = (stat["median_ms"], e["params"])
            if best:
                ms, p = best
                print(f"    {alg:<26} {ms:>9.4f}ms  @ pw={p['PairwiseIslandMax']} "
                      f"seq={p['SequentialBlockMax']} hub={p['ObjectHubThreshold']}")

    best = entries[0]
    p = best["params"]
    print(f"\n>>> BEST OVERALL: pw={p['PairwiseIslandMax']} seq={p['SequentialBlockMax']} "
          f"hub={p['ObjectHubThreshold']}  (score x{best['score']:.4f})")
    failed = [k for k, e in results["runs"].items() if e.get("status") != "ok"]
    if failed:
        print(f"\n[!] {len(failed)} failed configs (delete their entries from "
              f"tuning_results.json to retry): {', '.join(failed)}")


# --- main ----------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--plan", action="store_true", help="list pending configs and exit")
    parser.add_argument("--report", action="store_true", help="print leaderboard and exit")
    parser.add_argument("--check", action="store_true", help="verify const regexes and exit")
    args = parser.parse_args()

    if args.check:
        ok = check_patterns()
        sys.exit(0 if ok else 1)

    results = load_results()

    if args.report:
        print_report(results)
        return

    phases = build_phases(results)
    pending = [(idx, cfg) for idx, phase in enumerate(phases)
               for cfg in phase if not get_result(results, cfg)]
    total = sum(len(phase) for phase in phases)
    done = total - len(pending)

    print(f"Phases generated: {len(phases)} | configs: {total} total, {done} done, {len(pending)} pending")
    if args.plan:
        for idx, cfg in pending:
            print(f"  phase {idx}: {config_key(cfg)}")
        if len(phases) <= REFINE_ROUNDS:
            print("  (+ finer refinement phase(s) will be generated once the current phase completes)")
        return

    if not check_patterns():
        sys.exit("Const patterns did not match - aborting before touching anything.")

    durations = []
    try:
        while True:
            if not pending:
                # A completed phase may unlock the next refinement round.
                phases = build_phases(results)
                pending = [(idx, cfg) for idx, phase in enumerate(phases)
                           for cfg in phase if not get_result(results, cfg)]
                if not pending:
                    break

            idx, cfg = pending.pop(0)
            key = config_key(cfg)
            n_left = len(pending) + 1
            eta = ""
            if durations:
                avg = statistics.fmean(durations) + COOLDOWN_SEC
                eta = f" | ~{n_left * avg / 3600:.1f}h remaining"
            print(f"\n[phase {idx}] {key}  ({n_left} to go{eta})", flush=True)

            t0 = time.time()
            try:
                run_config(cfg, results)
                entry = results["runs"][key]
                durations.append(entry["duration_sec"])
                best = max((e for e in results["runs"].values() if e.get("status") == "ok"),
                           key=lambda e: e["score"])
                print(f"    -> score x{entry['score']:.4f} in {entry['duration_sec']:.0f}s "
                      f"| best so far x{best['score']:.4f} ({config_key(best['params'])})")
            except (RuntimeError, subprocess.TimeoutExpired, json.JSONDecodeError) as err:
                print(f"    -> FAILED: {err}")
                results["runs"][key] = {
                    "params": cfg,
                    "status": "failed",
                    "started": datetime.now().isoformat(timespec="seconds"),
                    "duration_sec": round(time.time() - t0, 1),
                    "error": str(err)[:500],
                }
                save_results(results)

            if pending or len(phases) <= REFINE_ROUNDS:
                print(f"    cooling down {COOLDOWN_SEC}s...", flush=True)
                time.sleep(COOLDOWN_SEC)
    except KeyboardInterrupt:
        print("\nInterrupted - progress is saved; rerun to resume.")
    finally:
        set_constants(BASELINE)
        print("Restored tuned constants (pw=64, seq=3000, hub=10**9).")

    print_report(results)


if __name__ == "__main__":
    main()
