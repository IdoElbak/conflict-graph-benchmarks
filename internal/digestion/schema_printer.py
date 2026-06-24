import ijson

def is_hex_map(d):
    """Detects if a dictionary is a map of Ethereum addresses or storage keys."""
    if not d:
        return False
    first_key = list(d.keys())[0]
    return isinstance(first_key, str) and first_key.startswith("0x") and len(first_key) > 20

def print_schema(data, indent="", is_root=False):
    if isinstance(data, dict):
        print("{")

        if is_hex_map(data):
            # It's an address/storage map. Show only the structure of the FIRST key.
            first_key = list(data.keys())[0]
            print(f"{indent}  [Hex Map (showing 1 of {len(data)} keys)] {first_key}: ", end="")
            print_schema(data[first_key], indent + "  ")
        else:
            # It's a standard struct. Print ALL keys so nothing is missed.
            for key, value in data.items():
                print(f"{indent}  {key}: ", end="")
                print_schema(value, indent + "  ")

        print(f"{indent}")

    elif isinstance(data, list):
        if len(data) == 0:
            print("[]")
        elif is_root:
            # Print EVERY index of the top-level block array
            print(f"[ (Root Tuple of {len(data)} items)")
            for i, item in enumerate(data):
                print(f"{indent}  Index {i} -> ", end="")
                print_schema(item, indent + "  ", is_root=False)
            print(f"{indent}]")
        else:
            # Inner array (like the 245 transactions). They all share a schema, so just show the first one.
            print(f"[ (Array of {len(data)} items)")
            if len(data) > 0:
                print(f"{indent}  [Showing schema of first item only] -> ", end="")
                print_schema(data[0], indent + "  ", is_root=False)
            print(f"{indent}]")
    else:
        # Base case: print the type (int, str, bool, etc.)
        print(type(data).__name__)

if __name__ == "__main__":
    file_path = "data/Solana_raw_391000000_391000998.json"

    print(f"Streaming {file_path} to find the first block...")

    with open(file_path, "rb") as f:
        objects = ijson.items(f, 'item')

        try:
            first_block = next(objects)
            print("\n--- Schema for the First Block ---\n")
            # is_root=True ensures the top-level array [blockNum, transactions, etc.] is fully mapped
            print_schema(first_block, is_root=True)
        except StopIteration:
            print("File is empty or not formatted as a JSON array.")