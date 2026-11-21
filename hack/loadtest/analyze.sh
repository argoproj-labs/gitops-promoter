#!/usr/bin/env bash
# Summarize the pprof profiles written by hack/loadtest.
#
#   hack/loadtest/analyze.sh <loadtest-out-dir> [binary]
#
# Writes <out>/analysis.md with, per level: top heap (inuse_space) by function and
# cumulative, top CPU while idle and during the promotion wave.
set -euo pipefail

out=${1:?usage: analyze.sh <loadtest-out-dir> [controller-binary]}
bin=${2:-$out/promoter}
[[ -x "$bin" ]] || bin=""
report="$out/analysis.md"
nodes=${NODES:-25}

pp() { go tool pprof -nodecount="$nodes" "$@" $bin 2>/dev/null | sed -n '/^ *flat/,$p' || true; }

{
  echo "# Profile analysis"
  echo
  echo "Source: \`$out\`"
  for dir in "$out"/L*; do
    lvl=$((10#${dir##*/L}))
    echo
    echo "## $lvl PromotionStrategies"
    if [[ -f $dir/heap-idle.pb.gz ]]; then
      echo
      echo "### Heap in use while idle, by function (flat)"
      echo '```'
      pp -top -sample_index=inuse_space "$dir/heap-idle.pb.gz"
      echo '```'
      echo
      echo "### Heap in use while idle, cumulative"
      echo '```'
      pp -top -cum -sample_index=inuse_space "$dir/heap-idle.pb.gz"
      echo '```'
    fi
    if [[ -f $dir/cpu-idle.pb.gz ]]; then
      echo
      echo "### CPU while idle (cumulative)"
      echo '```'
      pp -top -cum "$dir/cpu-idle.pb.gz"
      echo '```'
    fi
    for w in "$dir"/cpu-wave*.pb.gz; do
      [[ -f $w ]] || continue
      echo
      echo "### CPU during $(basename "$w" .pb.gz) (cumulative)"
      echo '```'
      pp -top -cum "$w"
      echo '```'
    done
    for w in "$dir"/heap-wave*.pb.gz; do
      [[ -f $w ]] || continue
      echo
      echo "### Allocations during $(basename "$w" .pb.gz) (alloc_space, flat, since process start)"
      echo '```'
      pp -top -sample_index=alloc_space "$w"
      echo '```'
    done
  done
} > "$report"
echo "wrote $report"
