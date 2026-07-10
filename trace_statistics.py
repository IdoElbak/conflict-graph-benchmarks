import json
import os

def print_trace_statistics(file_path, blockchain_name):
    """
    Reads a cleaned JSON block stream and prints the average and maximum block size.
    """
    if not os.path.exists(file_path):
        print(f"[Warning] File not found: {file_path}")
        return

    total_txns = 0
    max_txns = 0
    blocks_processed = 0

    print(f"Loading {blockchain_name} data from {file_path}...")

    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            # The Go pipeline exports the blocks as a JSON array
            blocks = json.load(f)

            for block in blocks:
                # Extract the num_transactions field defined in digestion.CleanedBlock
                num_txns = block.get('num_transactions', 0)

                total_txns += num_txns
                if num_txns > max_txns:
                    max_txns = num_txns
                blocks_processed += 1

        # Print the final statistics
        if blocks_processed > 0:
            avg_txns = total_txns / blocks_processed
            print(f"    [{blockchain_name} Stats] Processed: {blocks_processed} blocks | Avg Size: {avg_txns:.2f} txns | Max Size: {max_txns} txns\n")
        else:
            print(f"    [{blockchain_name} Stats] No blocks found in file.\n")

    except json.JSONDecodeError as e:
        print(f"[Error] Failed to parse JSON for {blockchain_name}: {e}\n")


if __name__ == "__main__":
    # File paths based on your Go digestion pipeline constants
    eth_file_path = "internal/digestion/data/Eth_cleaned_blocks_21297000_21297999.json"
    solana_file_path = "internal/digestion/data/Solana_cleaned_blocks_391000000_391000998.json"

    print("--- Blockchain Trace Statistics ---")
    print_trace_statistics(eth_file_path, "Ethereum")
    print_trace_statistics(solana_file_path, "Solana")