#!/usr/bin/env bash
# Prints data race reports the Go race detector wrote under GORACE=log_path=<dir>/race and fails if there are
# any.
#
# The Ginkgo test targets run with --output-interceptor-mode=none (see the Makefile), so a race report a test
# process prints outside of a spec never reaches Ginkgo's output. The process still exits non-zero, but without
# this script the job would fail with every spec passing and no explanation.
set -euo pipefail

dir="${1:?usage: $0 <race log dir>}"

shopt -s nullglob
logs=("$dir"/race.*)
if [ "${#logs[@]}" -eq 0 ]; then
	exit 0
fi

echo "The race detector reported data races (${#logs[@]} log file(s) in $dir):" >&2
for log in "${logs[@]}"; do
	echo "==> $log" >&2
	cat "$log" >&2
done
exit 1
