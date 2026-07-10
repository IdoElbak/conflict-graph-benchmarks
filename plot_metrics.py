import json
import matplotlib.pyplot as plt
import numpy as np
from collections import defaultdict


def parse_time_to_ms(time_str):
    """Converts Go time strings to milliseconds."""
    # Strip weird Windows encoding artifacts
    time_str = time_str.replace("Â", "")

    if "µs" in time_str or "us" in time_str:
        return float(time_str.replace("µs", "").replace("us", "")) / 1000.0
    elif "ns" in time_str:
        return float(time_str.replace("ns", "")) / 1000000.0
    elif "ms" in time_str:
        return float(time_str.replace("ms", ""))
    elif "s" in time_str:
        return float(time_str.replace("s", "")) * 1000.0
    return 0.0

def plot_trace(trace_data, title):
    if not trace_data:
        return

    algorithms = list(trace_data[0]['times'].keys())

    # Determine dynamic bin width to create roughly 40 smooth points across the x-axis
    sizes = [d['num_txns'] for d in trace_data]
    min_size, max_size = min(sizes), max(sizes)
    bin_width = max(1, (max_size - min_size) // 40)

    plt.figure(figsize=(12, 7))

    for alg in algorithms:
        # Group execution times by BINNED block size to mitigate noise
        grouped_data = defaultdict(list)
        for d in trace_data:
            # Round down to the nearest bin threshold
            binned_size = (d['num_txns'] // bin_width) * bin_width
            time_ms = parse_time_to_ms(d['times'][alg])
            grouped_data[binned_size].append(time_ms)

        # Filter out bins with too few data points to prevent isolated spikes
        valid_bins = {k: v for k, v in grouped_data.items() if len(v) >= 2}
        if not valid_bins:
            valid_bins = grouped_data # fallback

        sorted_bins = sorted(valid_bins.keys())

        # Calculate median and the 5th/95th percentiles matching the paper's methodology
        y_center = [np.median(valid_bins[size]) for size in sorted_bins]
        y_low = [np.percentile(valid_bins[size], 5) for size in sorted_bins]
        y_high = [np.percentile(valid_bins[size], 95) for size in sorted_bins]

        # Plot the smoothed median line with subtle markers
        line, = plt.plot(sorted_bins, y_center, label=alg, linewidth=2, marker='o', markersize=3)

        # Shade the percentile variance using the line's exact color
        plt.fill_between(sorted_bins, y_low, y_high, color=line.get_color(), alpha=0.15)

    plt.title(f"{title}: Algorithm Execution Time vs Block Size (Smoothed)", fontsize=14)
    plt.xlabel(f"Block Size (Binned by ~{bin_width} txns)", fontsize=12)
    plt.ylabel("Execution Time (ms) [Line: Median, Shaded: 5th-95th Percentile]", fontsize=12)
    plt.legend(bbox_to_anchor=(1.05, 1), loc='upper left')
    plt.grid(True, linestyle='--', alpha=0.5)
    plt.tight_layout()
    plt.show()

if __name__ == "__main__":
    try:
        with open('benchmark_results.json', 'r', encoding='utf-8') as f:
            data = json.load(f)

        eth_data = [d for d in data if d['blockchain'] == 'Ethereum']
        sol_data = [d for d in data if d['blockchain'] == 'Solana']

        print(f"Loaded {len(eth_data)} Ethereum blocks and {len(sol_data)} Solana blocks.")

        plot_trace(eth_data, "Ethereum")
        plot_trace(sol_data, "Solana")

    except FileNotFoundError:
        print("benchmark_results.json not found. Run the Go pipeline first.")