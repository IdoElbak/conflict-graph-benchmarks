"""League table averaged over several results files of the same algorithms,
each from a binary with a different code layout.

On the laptop's P-cores, moving the same code to other addresses (an unused
function added elsewhere) shifts an algorithm's speed by up to +-10%, so a
difference below that between two algorithms with different code is not
trustworthy from one build. Each file gives, per column, the median per-block
speed-up vs the baseline; this script prints their geometric mean over the
files, and the spread (min-max) across files.

Usage: python layout_league.py --baseline ALG [--txns LO-HI] FILE [FILE ...]
       (--txns keeps only blocks of LO..HI transactions)
"""
import argparse
import math
import statistics
from collections import defaultdict

from show_results import SYNTHETIC, TRACES, load_by_trace, parse_ms

BANDS = [(0, 0.01, "<1%"), (0.01, 0.10, "1-10%"), (0.10, 1.01, ">=10%")]


def columns(by_trace):
    """(label, blocks) per league column, as in show_results' league."""
    cols = []
    for trace, _ in TRACES:
        blocks = by_trace.get(trace, [])
        if trace in SYNTHETIC:
            for lo, hi, name in BANDS:
                cols.append((trace + name, [b for b in blocks if lo <= b["density"] < hi]))
        else:
            cols.append((trace, blocks))
    return cols


def speedups(path, baseline, txns=(0, float("inf"))):
    """{alg: {column: median per-block speed-up vs baseline}} for one file,
    over the blocks of txns[0]..txns[1] transactions."""
    out = defaultdict(dict)
    for label, blocks in columns(load_by_trace(path)):
        blocks = [b for b in blocks if txns[0] <= b["num_txns"] <= txns[1]]
        per_alg = defaultdict(list)
        for b in blocks:
            base = parse_ms(b["times"].get(baseline, "0"))
            if base <= 0:
                continue
            for alg, t in b["times"].items():
                if (ms := parse_ms(t)) > 0:
                    per_alg[alg].append(base / ms)
        for alg, values in per_alg.items():
            out[alg][label] = statistics.median(values)
    return out


def geo(values):
    return math.exp(sum(math.log(v) for v in values) / len(values)) if values else float("nan")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--txns", help="LO-HI: only blocks of LO..HI transactions")
    parser.add_argument("files", nargs="+")
    args = parser.parse_args()
    txns = tuple(map(int, args.txns.split("-"))) if args.txns else (0, float("inf"))
    runs = [speedups(f, args.baseline, txns) for f in args.files]
    labels = [label for label, _ in columns(load_by_trace(args.files[0]))]
    algs = sorted(set.intersection(*(set(r) for r in runs)))
    print(f"=== median per-block speed-up vs {args.baseline}, geometric mean over {len(runs)} layouts"
          " (min-max over layouts in the last column) ===")
    print(f"{'Algorithm':24}" + "".join(f"{label:>14}" for label in labels) + f"{'geo':>8}{'spread':>14}")
    for alg in algs:
        cells, per_run_geo = [], []
        for label in labels:
            vals = [r[alg][label] for r in runs if label in r[alg]]
            cells.append(f"{'x%.2f' % geo(vals) if vals else '-':>14}")
        for r in runs:
            per_run_geo.append(geo([r[alg][label] for label in labels if label in r[alg]]))
        print(f"{alg:24}" + "".join(cells) + f"{'x%.2f' % geo(per_run_geo):>8}"
              f"{'x%.2f-%.2f' % (min(per_run_geo), max(per_run_geo)):>14}")


if __name__ == "__main__":
    main()
