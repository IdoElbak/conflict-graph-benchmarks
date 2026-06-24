import re
import h5py
import zlib
import json
from pathlib import Path
from typing import List, Any, Optional


def parse_block_range_from_filename(h5_path: Path) -> List[Any]:
    """
    Extract the start block from a filename like:
      '18001000_18002000_preState_compressed.h5'

    We just grab the first integer at the beginning of the stem.
    """
    stem = h5_path.stem
    m = re.search(r"(\d+)_(\d+)", stem)
    if not m:
        raise ValueError(f"Could not parse block range from filename: {h5_path.name}")

    start_block = int(m.group(1))
    end_block = int(m.group(2))-1
    return start_block, end_block


def load_chunk(ds, index: int) -> List[Any]:
    """
    Load and decompress a single chunk from an HDF5 dataset of uint8 arrays.

    ds[index] is assumed to be a 1D uint8 array containing zlib-compressed bytes
    that decode to UTF-8 JSON.
    """
    compressed_chunk = ds[index]          # shape (N,), dtype=uint8
    compressed_bytes = compressed_chunk.tobytes()
    decompressed_bytes = zlib.decompress(compressed_bytes)
    return json.loads(decompressed_bytes.decode("utf-8"))


def extract_blocks_for_file(
    h5_path: Path,
    dataset_name: str = "dataset",
    blocks_per_file: int = 1000,
    output_path: Optional[Path] = None,
) -> List[Any]:
    """
    Extract `blocks_per_file` blocks from this H5, where:

      - start_block is derived from the filename
      - end_block = start_block + blocks_per_file - 1

    Assumes each record looks like:
        [block_number, preState_1, preState_2, ...]
    and that block_number is record[0].
    """
    start_block, end_block = parse_block_range_from_filename(h5_path)

    results: List[Any] = []

    with h5py.File(h5_path, "r") as f:
        ds = f[dataset_name]
        num_chunks = ds.shape[0]

        for i in range(num_chunks):
            chunk_data = load_chunk(ds, i)

            for record in chunk_data:
                block_number = record[0]

                if block_number < start_block:
                    continue
                if block_number > end_block:
                    # We can early-exit if blocks are in order
                    continue

                results.append(record)

    # Sort by block number just to be safe
    results.sort(key=lambda r: r[0])

    if output_path is not None:
        with output_path.open("w") as f_out:
            json.dump(results, f_out, indent=2)

    return results


if __name__ == "__main__":
    # Path to your HDF5 file
    H5_PATH = Path("data/Solana_391000000_391000999.h5")

    # Output filename based on start block (optional)
    start_block, end_block = parse_block_range_from_filename(H5_PATH)
    OUT_PATH = Path(f"data/Solana_raw_{start_block}_{end_block}.json")

    blocks = extract_blocks_for_file(
        h5_path=H5_PATH,
        dataset_name="dataset",
        blocks_per_file=1000,
        output_path=OUT_PATH,
    )

    print(
        f"Extracted {len(blocks)} blocks "
        f"from {start_block} to {end_block - 1} into {OUT_PATH}"
    )