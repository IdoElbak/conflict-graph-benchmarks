#!/usr/bin/env bash
# Builds one benchmark binary per layout seed: each build adds an unused,
# differently sized function at the front of several algorithm packages (so
# their hot loops land at other code addresses), then removes it again.
# Usage (from this folder): bash layout_builds.sh . OUT_DIR SEED...
# then run each OUT_DIR/bench_L<seed>.exe with the same flags and -out, and
# average with: python layout_league.py --baseline HybridJoinTiny FILE...
set -euo pipefail
mod=$1; out=$2; shift 2
pkgs=(internal/algorithms/islands internal/algorithms/competitors internal/model)
cleanup() { for p in "${pkgs[@]}"; do rm -f "$mod/$p/aa_layoutpad.go"; done; }
trap cleanup EXIT
for seed in "$@"; do
  i=0
  for p in "${pkgs[@]}"; do
    i=$((i + 1))
    name=$(basename "$p")
    lines=$(( (seed * 37 + i * 101) % 97 ))
    {
      echo "package $name"
      echo
      echo "// LayoutPad$seed is a benchmark layout perturbation (temporary)."
      echo "var LayoutPad$seed = layoutPad$seed"
      echo
      echo "//go:noinline"
      echo "func layoutPad$seed(x []uint64) uint64 {"
      echo "	var s uint64"
      for ((k = 0; k < lines; k++)); do
        echo "	s += x[$k%len(x)] ^ (s >> $(( (k % 7) + 1 )))"
      done
      echo "	return s"
      echo "}"
    } > "$mod/$p/aa_layoutpad.go"
  done
  (cd "$mod" && go build -o "$out/bench_L$seed.exe" .)
  cleanup
  echo "built layout $seed"
done
