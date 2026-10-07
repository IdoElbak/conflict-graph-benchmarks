"""Prints benchmark_results.json as plain-text tables (no GUI needed).

Tables (the text twin of plot_metrics.py):
  1. overview   - one row per trace, all six (Sui shown empty until its trace
                  exists): size range, median density, median time per
                  algorithm, median per-block speedup vs Bitmap.
  2. per trace  - median / mean / p95 per algorithm and win rate vs Bitmap.
  3. structure  - density, conflicted-txn ratio and island statistics.
  4. contention - synthetic traces only: median time per size band x density
                  level, so the point where each algorithm's curve bends shows.

  league        - (--league) one row per algorithm, one column per trace and
                  synthetic density band: median per-block speedup vs the
                  baseline and win rate; for the uf suite's many variants.

Usage: python show_results.py [results.json] [--baseline ALG] [--league]
"""
import argparse
import json
import math
import statistics
import sys
from collections import defaultdict

# (blockchain label in the results file, display title), in presentation order.
TRACES = [
    ("Ethereum", "Ethereum"),
    ("EthereumNoHub", "Ethereum, fee hub removed"),
    ("Solana", "Solana"),
    ("Sui", "Sui"),
    ("YCSB", "YCSB (synthetic)"),
    ("TPC", "TPC-C style (synthetic)"),
]
SYNTHETIC = ("YCSB", "TPC")
BASELINE = "Bitmap"
# Must match digestion.DensityLevels.
DENSITY_LEVELS = [0.001, 0.003, 0.01, 0.03, 0.1, 0.2, 0.4, 0.6, 0.8]
SIZE_BANDS = [(0, 200), (200, 1000), (1000, 2001)]


def parse_ms(time_str):
    """Converts a Go duration string to milliseconds."""
    s = time_str.replace("Â", "").strip()  # strip Windows encoding artifacts
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


def load_by_trace(path="benchmark_results.json"):
    """Returns {blockchain label: [block metrics]} from a results file."""
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    by_trace = defaultdict(list)
    for block in data:
        by_trace[block["blockchain"]].append(block)
    return by_trace


def algorithms_in(by_trace):
    """Every algorithm in the results, baseline first, then by name."""
    names = {alg for blocks in by_trace.values() for b in blocks for alg in b["times"]}
    return sorted(names, key=lambda a: (a != BASELINE, a))


def short_name(alg):
    return alg.replace("Island", "").replace("Shattered", "") or alg


def times(blocks, alg):
    return [parse_ms(b["times"][alg]) for b in blocks if alg in b["times"]]


def speedup(blocks, alg):
    """Median per-block Bitmap/alg ratio and win count (blocks timed at 0 are skipped)."""
    ratios, wins = [], 0
    for b in blocks:
        if alg not in b["times"] or BASELINE not in b["times"]:
            continue
        t, base = parse_ms(b["times"][alg]), parse_ms(b["times"][BASELINE])
        if t > 0:
            ratios.append(base / t)
            wins += t < base
    if not ratios:
        return None
    return statistics.median(ratios), wins, len(ratios)


def nearest_level(density):
    """Nearest target density level in log space (None for density 0)."""
    if density <= 0:
        return None
    return min(DENSITY_LEVELS, key=lambda lvl: abs(math.log(density / lvl)))


def print_overview(by_trace, algs):
    others = [a for a in algs if a != BASELINE]
    print("\n=== 1. Overview (ms: median over blocks of each block's per-call time; speedup = median per-block Bitmap/alg) ===")
    header = f"{'Trace':<27}{'blocks':>7}{'txns':>11}{'med dens':>10}"
    header += "".join(f"{short_name(a) + ' ms':>13}" for a in algs)
    header += "".join(f"{short_name(a) + ' x':>12}" for a in others)
    print(header)
    for label, title in TRACES:
        blocks = by_trace.get(label, [])
        if not blocks:
            print(f"{title:<27}{'no data yet':>18}")
            continue
        sizes = [b["num_txns"] for b in blocks]
        row = f"{title:<27}{len(blocks):>7}{f'{min(sizes)}-{max(sizes)}':>11}"
        row += f"{statistics.median(b['density'] for b in blocks):>10.4f}"
        row += "".join(f"{statistics.median(times(blocks, a)):>13.5f}" if times(blocks, a) else f"{'-':>13}"
                       for a in algs)
        for a in others:
            s = speedup(blocks, a)
            row += f"{'x%.2f' % s[0]:>12}" if s else f"{'-':>12}"
        print(row)


def print_per_trace(by_trace, algs):
    print("\n=== 2. Per trace: median / mean / p95 (ms), win rate vs Bitmap ===")
    for label, title in TRACES:
        blocks = by_trace.get(label, [])
        if not blocks:
            continue
        print(f"\n  [{title}] {len(blocks)} blocks")
        for alg in algs:
            vals = sorted(times(blocks, alg))
            if not vals:
                continue
            line = (f"    {alg:<26}{statistics.median(vals):>9.5f}{statistics.fmean(vals):>9.5f}"
                    f"{percentile(vals, 95):>9.5f}")
            s = speedup(blocks, alg) if alg != BASELINE else None
            if s:
                line += f"   x{s[0]:.3f}, wins {s[1]}/{s[2]} ({100.0 * s[1] / s[2]:.1f}%)"
            print(line)


def print_structure(by_trace):
    print("\n=== 3. Structure: density, conflicted-txn ratio, islands (IslandLean) ===")
    print(f"{'Trace':<27}{'density min/med/mean/p95/max':>36}{'<10%':>7}{'conflicted med/p95':>20}"
          f"{'med objs':>10}{'med isl':>9}{'largest/n':>11}")
    for label, title in TRACES:
        blocks = by_trace.get(label, [])
        if not blocks:
            print(f"{title:<27}{'no data yet':>18}")
            continue
        dens = sorted(b["density"] for b in blocks)
        conf = sorted(b.get("conflicted_txns_ratio", float("nan")) for b in blocks)
        isl = [b.get("island_metrics", {}).get("IslandLean") for b in blocks]
        counts = [i["count"] if i else 0 for i in isl]
        shares = [i["max_size"] / b["num_txns"] for i, b in zip(isl, blocks) if i and b["num_txns"]]
        dens_txt = (f"{dens[0]:.4f}/{statistics.median(dens):.4f}/{statistics.fmean(dens):.4f}/"
                    f"{percentile(dens, 95):.4f}/{dens[-1]:.4f}")
        print(f"{title:<27}{dens_txt:>36}{sum(d < 0.1 for d in dens):>7}"
              f"{f'{statistics.median(conf):.3f}/{percentile(conf, 95):.3f}':>20}"
              f"{statistics.median(b['unique_objects'] for b in blocks):>10.0f}"
              f"{statistics.median(counts):>9.0f}"
              f"{(statistics.median(shares) if shares else 0):>11.3f}")


# Density bands of the synthetic traces in the league table.
DENSITY_BANDS = [("<1%", 0.0, 0.01), ("1-10%", 0.01, 0.1), (">=10%", 0.1, 2.0)]


def league_columns(by_trace):
    """(label, blocks) per league column: every real trace whole, every
    synthetic trace split into density bands."""
    columns = []
    for label, _ in TRACES:
        blocks = by_trace.get(label, [])
        if not blocks:
            continue
        if label not in SYNTHETIC:
            columns.append((label, blocks))
            continue
        for band, lo, hi in DENSITY_BANDS:
            cell = [b for b in blocks if lo <= b["density"] < hi]
            if cell:
                columns.append((f"{label}{band}", cell))
    return columns


def print_league(by_trace, algs):
    columns = league_columns(by_trace)
    print(f"\n=== League: median per-block speedup vs {BASELINE} (win rate); geo = geometric mean over the columns ===")
    print(f"{'Algorithm':<30}" + "".join(f"{label:>13}" for label, _ in columns) + f"{'geo':>8}")
    for alg in algs:
        row, logs = f"{alg:<30}", []
        for _, blocks in columns:
            s = speedup(blocks, alg) if alg != BASELINE else None
            if alg == BASELINE:
                row += f"{'baseline':>13}"
            elif s:
                row += f"{'x%.2f %3.0f%%' % (s[0], 100.0 * s[1] / s[2]):>13}"
                logs.append(math.log(s[0]))
            else:
                row += f"{'-':>13}"
        row += f"{'x%.2f' % math.exp(statistics.fmean(logs)):>8}" if logs else f"{'':>8}"
        print(row)
    print("\n  median ms per block:")
    print(f"{'Algorithm':<30}" + "".join(f"{label:>13}" for label, _ in columns))
    for alg in algs:
        row = f"{alg:<30}"
        for _, blocks in columns:
            vals = times(blocks, alg)
            row += f"{statistics.median(vals):>13.5f}" if vals else f"{'-':>13}"
        print(row)


def print_island_league(by_trace, algs):
    """Median island statistics per algorithm and trace (algorithms that record them)."""
    print("\n=== Islands per trace: median count / median largest share of the block / txns in islands / hub-touched ===")
    for label, title in TRACES:
        blocks = [b for b in by_trace.get(label, []) if b["num_txns"] > 0]
        if not blocks:
            continue
        print(f"  [{title}]")
        for alg in algs:
            stats = [(b["island_metrics"].get(alg), b["num_txns"]) for b in blocks]
            if not any(s for s, _ in stats):
                continue
            count = statistics.median(s["count"] if s else 0 for s, _ in stats)
            share = statistics.median((s["max_size"] if s else 0) / n for s, n in stats)
            inisl = sum(s.get("in_islands", 0) if s else 0 for s, _ in stats) / sum(n for _, n in stats)
            hub = sum(s.get("hub_touched", 0) if s else 0 for s, _ in stats) / sum(n for _, n in stats)
            print(f"    {alg:<30}{count:>6.0f}{share:>8.3f}{inisl:>8.3f}{hub:>8.3f}")


def print_contention(by_trace, algs):
    others = [a for a in algs if a != BASELINE]
    print("\n=== 4. Synthetic contention sweep: median ms per size band x realized density level ===")
    for label in SYNTHETIC:
        blocks = by_trace.get(label, [])
        if not blocks:
            continue
        print(f"\n  [{dict(TRACES)[label]}]")
        print(f"    {'txns':<11}{'level':>7}{'blocks':>7}{'med dens':>10}"
              + "".join(f"{short_name(a) + ' ms':>13}" for a in algs)
              + "".join(f"{short_name(a) + ' x':>12}" for a in others))
        for lo, hi in SIZE_BANDS:
            band = [b for b in blocks if lo <= b["num_txns"] < hi]
            for level in [None] + DENSITY_LEVELS:
                cell = [b for b in band if nearest_level(b["density"]) == level]
                if not cell:
                    continue
                row = (f"    {f'{lo}-{hi - 1}':<11}{'0' if level is None else f'{level:g}':>7}{len(cell):>7}"
                       f"{statistics.median(b['density'] for b in cell):>10.4f}")
                row += "".join(f"{statistics.median(times(cell, a)):>13.5f}" for a in algs)
                for a in others:
                    s = speedup(cell, a)
                    row += f"{'x%.2f' % s[0]:>12}" if s else f"{'-':>12}"
                print(row)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("results", nargs="?", default="benchmark_results.json")
    parser.add_argument("--baseline", default=BASELINE, help="algorithm the speedups are relative to")
    parser.add_argument("--league", action="store_true", help="print the league and island tables only")
    args = parser.parse_args()
    BASELINE = args.baseline
    try:
        by_trace = load_by_trace(args.results)
    except FileNotFoundError:
        print(f"{args.results} not found. Run the Go pipeline first.")
        raise SystemExit(1)

    algs = algorithms_in(by_trace)
    if args.league:
        print_league(by_trace, algs)
        print_island_league(by_trace, algs)
        raise SystemExit(0)
    print_overview(by_trace, algs)
    print_per_trace(by_trace, algs)
    print_structure(by_trace)
    print_contention(by_trace, algs)
