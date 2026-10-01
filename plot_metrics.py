"""Plots benchmark_results.json (show_results.py prints the same data as text).

Figure 1 - execution time vs block size, one panel per trace in a 2x3 grid.
           Each panel has its own log x axis fitted to its trace's sizes (Sui's
           2-44 txns stay readable next to 2000-txn synthetic blocks); shared
           log y makes equal vertical gaps equal speed ratios. Dots =
           raw blocks, line = median per log-spaced size bin, band = 5th-95th
           percentile of the bin. Empty blocks (0 txns) are left out.
Figure 2 - synthetic contention sweep: median time vs realized density for
           YCSB and TPC, split into small / medium / large block-size bands.

Usage: python plot_metrics.py [results.json] [--save PREFIX]
       (--save writes PREFIX_by_size.png and PREFIX_contention.png instead of showing)
"""
import argparse
from collections import defaultdict

import matplotlib.pyplot as plt
import numpy as np
from matplotlib.ticker import LogLocator, MaxNLocator, NullFormatter, ScalarFormatter

from show_results import (BASELINE, DENSITY_LEVELS, SIZE_BANDS, SYNTHETIC, TRACES,
                          algorithms_in, load_by_trace, nearest_level, parse_ms)

# Kept under its old name for analyze_anomalies.py.
parse_time_to_ms = parse_ms

# Categorical slots in fixed order; an algorithm keeps its color on every panel.
PALETTE = ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"]
MUTED = "#52514e"


def algorithm_colors(algs):
    """Baseline first, then by name, each mapped to the next fixed slot."""
    return {alg: PALETTE[i % len(PALETTE)] for i, alg in enumerate(algs)}


# Log-spaced size bin edges over 1-2000 txns (~12 per decade).
SIZE_BIN_EDGES = np.geomspace(1, 2001, 41)


def binned_series(blocks, alg):
    """Median and 5th/95th percentile per log-spaced size bin, placed at the
    bin's median size (bins with <2 blocks are dropped)."""
    grouped = defaultdict(list)
    for b in blocks:
        grouped[np.searchsorted(SIZE_BIN_EDGES, b["num_txns"], side="right")].append(b)
    bins = sorted(k for k, v in grouped.items() if len(v) >= 2) or sorted(grouped)
    times = {k: [parse_ms(b["times"][alg]) for b in grouped[k]] for k in bins}
    return ([np.median([b["num_txns"] for b in grouped[k]]) for k in bins],
            [np.median(times[k]) for k in bins],
            [np.percentile(times[k], 5) for k in bins],
            [np.percentile(times[k], 95) for k in bins])


def plot_size_panel(ax, blocks, algs, colors):
    """Raw dots, binned median line and percentile band per algorithm."""
    for alg in algs:
        present = [b for b in blocks
                   if alg in b["times"] and parse_ms(b["times"][alg]) > 0 and b["num_txns"] > 0]
        if not present:
            continue
        ax.scatter([b["num_txns"] for b in present], [parse_ms(b["times"][alg]) for b in present],
                   s=4, alpha=0.18, color=colors[alg], linewidths=0)
        x, mid, low, high = binned_series(present, alg)
        ax.plot(x, mid, color=colors[alg], linewidth=2, label=alg)
        ax.fill_between(x, low, high, color=colors[alg], alpha=0.12, linewidth=0)


def plot_empty_panel(ax, title):
    ax.text(0.5, 0.5, f"{title}: no trace yet", transform=ax.transAxes,
            ha="center", va="center", color=MUTED, fontsize=11)


def use_own_log_x(ax):
    """Log x axis fitted to the panel's own data (call after plotting), ticked
    at 1-2-5 steps, or at round linear steps when it spans under a decade."""
    ax.set_xscale("log")
    low, high = ax.get_xlim()
    narrow = low > 0 and high / low < 10
    ax.xaxis.set_major_locator(MaxNLocator(5) if narrow else LogLocator(subs=(1, 2, 5)))
    ax.xaxis.set_major_formatter(ScalarFormatter())
    ax.xaxis.set_minor_formatter(NullFormatter())


def figure_by_size(by_trace, algs, colors):
    fig, axes = plt.subplots(2, 3, figsize=(17, 9.5), sharey=True)
    for ax, (label, title) in zip(axes.flat, TRACES):
        blocks = by_trace.get(label, [])
        ax.set_title(title if not blocks else f"{title}  ({len(blocks)} blocks)", fontsize=12)
        if blocks:
            plot_size_panel(ax, blocks, algs, colors)
        else:
            plot_empty_panel(ax, title)
        ax.grid(True, linestyle="--", alpha=0.35)
        use_own_log_x(ax)
        ax.set_xlabel("Block size (transactions, log)")
    axes[0, 0].set_yscale("log")
    for ax in axes[:, 0]:
        ax.set_ylabel("Time per block (ms, log)")
    add_shared_legend(fig, axes, "Median line, 5th-95th percentile band, raw blocks as dots")
    return fig


def figure_contention(by_trace, algs, colors):
    fig, axes = plt.subplots(2, 3, figsize=(17, 9.5), sharey=True)
    for row, label in zip(axes, SYNTHETIC):
        blocks = by_trace.get(label, [])
        for ax, (lo, hi) in zip(row, SIZE_BANDS):
            band = [b for b in blocks if lo <= b["num_txns"] < hi]
            ax.set_title(f"{dict(TRACES)[label]}, {lo}-{hi - 1} txns  ({len(band)} blocks)", fontsize=12)
            ax.grid(True, linestyle="--", alpha=0.35)
            if not band:
                plot_empty_panel(ax, dict(TRACES)[label])
                continue
            for alg in algs:
                points = []
                for lvl in DENSITY_LEVELS:
                    # Zero timings (below clock resolution) cannot sit on a log axis.
                    cell = [parse_ms(b["times"][alg]) for b in band
                            if nearest_level(b["density"]) == lvl and parse_ms(b["times"][alg]) > 0]
                    if cell:
                        points.append((lvl, np.median(cell)))
                ax.plot(*zip(*points), color=colors[alg], linewidth=2, marker="o", markersize=5, label=alg)
    for ax in axes.flat:
        ax.set_xscale("log")
        ax.set_xlabel("Realized density = conflict edges / all pairs (log)")
    axes[0, 0].set_yscale("log")
    for ax in axes[:, 0]:
        ax.set_ylabel("Median time per block (ms, log)")
    add_shared_legend(fig, axes, "Blocks grouped by nearest target density level")
    return fig


def add_shared_legend(fig, axes, note):
    """One legend for the whole figure, built from the first panel that has lines."""
    for ax in axes.flat:
        handles, labels = ax.get_legend_handles_labels()
        if handles:
            fig.legend(handles, labels, loc="upper center", ncol=len(labels), frameon=False,
                       fontsize=11, bbox_to_anchor=(0.5, 1.0))
            break
    fig.text(0.5, 0.005, note, ha="center", color=MUTED, fontsize=9)
    fig.tight_layout(rect=(0, 0.02, 1, 0.95))


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("results", nargs="?", default="benchmark_results.json")
    parser.add_argument("--save", metavar="PREFIX", help="write PNGs instead of opening windows")
    args = parser.parse_args()

    try:
        by_trace = load_by_trace(args.results)
    except FileNotFoundError:
        print(f"{args.results} not found. Run the Go pipeline first.")
        raise SystemExit(1)

    algs = algorithms_in(by_trace)
    colors = algorithm_colors(algs)
    print("Loaded " + ", ".join(f"{len(by_trace.get(label, []))} {label}" for label, _ in TRACES) + " blocks.")

    figures = {"by_size": figure_by_size(by_trace, algs, colors)}
    if any(by_trace.get(label) for label in SYNTHETIC):
        figures["contention"] = figure_contention(by_trace, algs, colors)

    if args.save:
        for name, fig in figures.items():
            fig.savefig(f"{args.save}_{name}.png", dpi=110, bbox_inches="tight")
            print(f"saved {args.save}_{name}.png")
    else:
        plt.show()


if __name__ == "__main__":
    main()
