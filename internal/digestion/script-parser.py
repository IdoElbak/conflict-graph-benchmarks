"""Extracts one chain's HDF5 block dump into the raw JSON array the Go parsers read.

Each H5 record is [block_number, ...chain-specific payload]. Records are
streamed straight to disk (never held in memory all at once), so large dumps
fit in RAM.

Usage (from internal/digestion/):
  python script-parser.py data/Sui_150001000_150003000.h5
  python script-parser.py data/Solana_391000000_391000999.h5 --out data/Solana_raw.json
Default output: data/<Chain>_raw_<start>_<end>.json, where end is inclusive.
"""
import argparse
import json
import re
import zlib
from pathlib import Path
from typing import Any, Iterator, List, Tuple

import h5py


def parse_block_range_from_filename(h5_path: Path) -> Tuple[int, int]:
    """
    Extract the inclusive block range from a filename like
      'Sui_150001000_150003000.h5' -> (150001000, 150002999)
    """
    m = re.search(r"(\d+)_(\d+)", h5_path.stem)
    if not m:
        raise ValueError(f"Could not parse block range from filename: {h5_path.name}")
    return int(m.group(1)), int(m.group(2)) - 1


def load_chunk(ds, index: int) -> List[Any]:
    """
    Load and decompress a single chunk from an HDF5 dataset of uint8 arrays.

    ds[index] is assumed to be a 1D uint8 array containing zlib-compressed bytes
    that decode to UTF-8 JSON.
    """
    compressed_bytes = ds[index].tobytes()
    return json.loads(zlib.decompress(compressed_bytes).decode("utf-8"))


def iter_records(h5_path: Path, start_block: int, end_block: int,
                 dataset_name: str = "dataset") -> Iterator[Any]:
    """Yields the records whose block number lies in [start_block, end_block]."""
    with h5py.File(h5_path, "r") as f:
        ds = f[dataset_name]
        for i in range(ds.shape[0]):
            for record in load_chunk(ds, i):
                if start_block <= record[0] <= end_block:
                    yield record


def stream_blocks_to_json(h5_path: Path, output_path: Path) -> int:
    """Writes the in-range records as one JSON array; returns the block count.

    Records must arrive in ascending block order (the Go parsers stream them
    in file order), so an out-of-order dump fails loudly instead of silently.
    """
    start_block, end_block = parse_block_range_from_filename(h5_path)
    count, previous = 0, None
    with output_path.open("w", encoding="utf-8") as out:
        out.write("[\n")
        for record in iter_records(h5_path, start_block, end_block):
            if previous is not None and record[0] <= previous:
                raise ValueError(f"block {record[0]} arrived after block {previous}: dump is not sorted")
            if count:
                out.write(",\n")
            json.dump(record, out)
            previous = record[0]
            count += 1
        out.write("\n]\n")
    return count


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("h5", type=Path, help="HDF5 dump, e.g. data/Sui_150001000_150003000.h5")
    parser.add_argument("--out", type=Path, help="output JSON path")
    args = parser.parse_args()

    start, end = parse_block_range_from_filename(args.h5)
    chain = args.h5.stem.split("_")[0]
    out_path = args.out or args.h5.with_name(f"{chain}_raw_{start}_{end}.json")
    blocks = stream_blocks_to_json(args.h5, out_path)
    print(f"Extracted {blocks} blocks ({start}-{end}) from {args.h5} into {out_path}")
