"""Plots benchmark_results.json (show_results.py prints the same data as text).

Figure 1 - execution time vs block size, one panel per trace in a 2x3 grid.
           Each panel has its own log axes fitted to its own data (Sui's 2-44
           txns and 0.3 us next to 2000-txn synthetic blocks and 300 us), so
           the algorithms of a panel spread over its full height; equal
           vertical gaps are equal speed ratios within a panel. Line = median
           per size bin, band = 5th-95th percentile of the bin. Bins are
           log-spaced over each trace's own size range, about one per
           BLOCKS_PER_BIN blocks (6-40 per trace); bins with fewer than
           MIN_BIN_BLOCKS blocks are dropped. Empty blocks (0 txns) are left out.
Figure 2 - synthetic contention sweep: median time vs realized density for
           YCSB and TPC, split into small / medium / large block-size bands,
           each panel with its own log time axis. Times are in microseconds.

Usage: python plot_metrics.py [results.json] [--save PREFIX] [--algs A,B,...]
       (--save writes PREFIX_by_size.png and PREFIX_contention.png instead of showing)
"""
import argparse
from collections import defaultdict

import matplotlib.pyplot as plt
import numpy as np
from matplotlib.ticker import FuncFormatter, LogLocator, MaxNLocator, NullFormatter

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


# Size bins per trace: about one per BLOCKS_PER_BIN blocks, between 6 and 40,
# log-spaced over the trace's own size range; sparser bins are dropped.
BLOCKS_PER_BIN = 8
MIN_BIN_BLOCKS = 3


def size_bin_edges(blocks):
    """Log-spaced bin edges over the blocks' own size range."""
    sizes = [b["num_txns"] for b in blocks]
    bins = int(np.clip(len(blocks) / BLOCKS_PER_BIN, 6, 40))
    return np.geomspace(min(sizes), max(sizes) + 1, bins + 1)


def binned_series(blocks, alg):
    """Median and 5th/95th percentile per size bin, placed at the bin's median
    size (bins with fewer than MIN_BIN_BLOCKS blocks are dropped)."""
    edges = size_bin_edges(blocks)
    grouped = defaultdict(list)
    for b in blocks:
        grouped[np.searchsorted(edges, b["num_txns"], side="right")].append(b)
    bins = sorted(k for k, v in grouped.items() if len(v) >= MIN_BIN_BLOCKS) or sorted(grouped)
    times = {k: [parse_us(b["times"][alg]) for b in grouped[k]] for k in bins}
    return ([np.median([b["num_txns"] for b in grouped[k]]) for k in bins],
            [np.median(times[k]) for k in bins],
            [np.percentile(times[k], 5) for k in bins],
            [np.percentile(times[k], 95) for k in bins])


def parse_us(time_str):
    """Converts a Go duration string to microseconds."""
    return parse_ms(time_str) * 1000.0


def plot_size_panel(ax, blocks, algs, colors):
    """Binned median line and percentile band per algorithm; returns the
    medians drawn, which the panel's time axis is fitted to."""
    medians = []
    for alg in algs:
        present = [b for b in blocks
                   if alg in b["times"] and parse_ms(b["times"][alg]) > 0 and b["num_txns"] > 0]
        if not present:
            continue
        x, mid, low, high = binned_series(present, alg)
        ax.plot(x, mid, color=colors[alg], linewidth=2, marker="o", markersize=3, label=alg)
        ax.fill_between(x, low, high, color=colors[alg], alpha=0.12, linewidth=0)
        medians += mid
    return medians


def plot_empty_panel(ax, title):
    ax.text(0.5, 0.5, f"{title}: no trace yet", transform=ax.transAxes,
            ha="center", va="center", color=MUTED, fontsize=11)


def tick_log_axis(axis, low, high):
    """Ticks a log axis spanning [low, high] at 1-2-5 steps, or at round linear
    steps when it spans under a decade."""
    narrow = low > 0 and high / low < 10
    axis.set_major_locator(MaxNLocator(5) if narrow else LogLocator(subs=(1, 2, 5)))
    axis.set_major_formatter(FuncFormatter(lambda value, _: f"{value:g}"))
    axis.set_minor_formatter(NullFormatter())


def use_own_log_x(ax):
    """Log x axis fitted to the panel's own data (call after plotting)."""
    ax.set_xscale("log")
    tick_log_axis(ax.xaxis, *ax.get_xlim())


def fit_log_y(ax, values):
    """Log time axis fitted to the medians drawn in the panel, with a little
    room above and below; percentile bands beyond it are clipped."""
    ax.set_yscale("log")
    if not values:
        return
    low, high = min(values) / 1.25, max(values) * 1.25
    ax.set_ylim(low, high)
    tick_log_axis(ax.yaxis, low, high)


def figure_by_size(by_trace, algs, colors):
    fig, axes = plt.subplots(2, 3, figsize=(17, 9.5))
    for ax, (label, title) in zip(axes.flat, TRACES):
        blocks = by_trace.get(label, [])
        ax.set_title(title if not blocks else f"{title}  ({len(blocks)} blocks)", fontsize=12)
        medians = []
        if blocks:
            medians = plot_size_panel(ax, blocks, algs, colors)
        else:
            plot_empty_panel(ax, title)
        ax.grid(True, linestyle="--", alpha=0.35)
        use_own_log_x(ax)
        fit_log_y(ax, medians)
        ax.set_xlabel("Block size (transactions, log)")
    for ax in axes[:, 0]:
        ax.set_ylabel("Time per block (µs, log)")
    add_shared_legend(fig, axes, "Median per size bin (bins fitted to each trace), 5th-95th percentile band")
    return fig


def figure_contention(by_trace, algs, colors):
    fig, axes = plt.subplots(2, 3, figsize=(17, 9.5))
    for row, label in zip(axes, SYNTHETIC):
        blocks = by_trace.get(label, [])
        for ax, (lo, hi) in zip(row, SIZE_BANDS):
            band = [b for b in blocks if lo <= b["num_txns"] < hi]
            ax.set_title(f"{dict(TRACES)[label]}, {lo}-{hi - 1} txns  ({len(band)} blocks)", fontsize=12)
            ax.grid(True, linestyle="--", alpha=0.35)
            if not band:
                plot_empty_panel(ax, dict(TRACES)[label])
                continue
            medians = []
            for alg in algs:
                points = []
                for lvl in DENSITY_LEVELS:
                    # Zero timings (below clock resolution) cannot sit on a log axis.
                    cell = [parse_us(b["times"][alg]) for b in band
                            if nearest_level(b["density"]) == lvl and parse_ms(b["times"][alg]) > 0]
                    if cell:
                        points.append((lvl, np.median(cell)))
                ax.plot(*zip(*points), color=colors[alg], linewidth=2, marker="o", markersize=5, label=alg)
                medians += [m for _, m in points]
            fit_log_y(ax, medians)
    for ax in axes.flat:
        ax.set_xscale("log")
        ax.set_xlabel("Realized density = conflict edges / all pairs (log)")
    for ax in axes[:, 0]:
        ax.set_ylabel("Median time per block (µs, log)")
    add_shared_legend(fig, axes, "Blocks grouped by nearest target density level")
    return fig


def add_shared_legend(fig, axes, note):
    """One legend for the whole figure, built from the first panel that has lines."""
    rows = 1
    for ax in axes.flat:
        handles, labels = ax.get_legend_handles_labels()
        if handles:
            columns = min(len(labels), 5)
            rows = -(-len(labels) // columns)
            fig.legend(handles, labels, loc="upper center", ncol=columns, frameon=False,
                       fontsize=11, bbox_to_anchor=(0.5, 1.0))
            break
    fig.text(0.5, 0.005, note, ha="center", color=MUTED, fontsize=9)
    fig.tight_layout(rect=(0, 0.02, 1, 0.97 - 0.025 * rows))


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("results", nargs="?", default="benchmark_results.json")
    parser.add_argument("--save", metavar="PREFIX", help="write PNGs instead of opening windows")
    parser.add_argument("--algs", help="comma-separated algorithms to draw (default: all)")
    args = parser.parse_args()

    try:
        by_trace = load_by_trace(args.results)
    except FileNotFoundError:
        print(f"{args.results} not found. Run the Go pipeline first.")
        raise SystemExit(1)

    algs = algorithms_in(by_trace)
    if args.algs:
        algs = [a for a in algs if a in args.algs.split(",")]
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
