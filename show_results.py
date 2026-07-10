"""Prints benchmark_results.json as plain-text tables (no GUI needed).

Shows per-chain median/mean/p95 execution time per algorithm, plus the
head-to-head Bitmap vs IslandNativeShattered comparison (speedup ratio and
per-block win rate) that drives the optimization loop.
"""
import json
import statistics
from collections import defaultdict


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


def summarize(chain, blocks):
    times = defaultdict(list)  # alg -> [ms per block]
    for b in blocks:
        for alg, t in b["times"].items():
            times[alg].append(parse_ms(t))

    print(f"\n=== {chain} ({len(blocks)} blocks) ===")
    print(f"{'Algorithm':<24} {'median':>10} {'mean':>10} {'p95':>10}")
    for alg in sorted(times):
        vals = sorted(times[alg])
        print(f"{alg:<24} {statistics.median(vals):>9.4f}ms {statistics.fmean(vals):>9.4f}ms {percentile(vals, 95):>9.4f}ms")

    if "Bitmap" in times and "IslandNativeShattered" in times:
        ratios, wins = [], 0
        for b in blocks:
            bm = parse_ms(b["times"]["Bitmap"])
            nat = parse_ms(b["times"]["IslandNativeShattered"])
            if nat > 0:
                ratios.append(bm / nat)
                if nat < bm:
                    wins += 1
        ratios.sort()
        print(f"\n  Native vs Bitmap: median speedup x{statistics.median(ratios):.3f} "
              f"(p5 x{percentile(ratios, 5):.3f}, p95 x{percentile(ratios, 95):.3f}) | "
              f"Native wins {wins}/{len(ratios)} blocks ({100.0 * wins / len(ratios):.1f}%)")


if __name__ == "__main__":
    try:
        with open("benchmark_results.json", "r", encoding="utf-8") as f:
            data = json.load(f)
    except FileNotFoundError:
        print("benchmark_results.json not found. Run the Go pipeline first.")
        raise SystemExit(1)

    for chain in ("Ethereum", "Solana"):
        blocks = [d for d in data if d["blockchain"] == chain]
        if blocks:
            summarize(chain, blocks)
