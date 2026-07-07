#!/usr/bin/env bash
#
# Fail if any Go standard-library test functions exist in the module.
#
# This project uses Ginkgo exclusively. The only `func TestXxx(t *testing.T)`
# functions allowed are the per-package Ginkgo bootstraps, which are identified
# by a call to RunSpecs in their body. Any other `func TestXxx(t *testing.T)`
# (i.e. a real Go standard-library test) is rejected so that contributors don't
# accidentally mix testing styles.
#
# Native Go fuzz targets (`func FuzzXxx(f *testing.F)`) are intentionally allowed
# (see the fuzz-* targets in the Makefile).
set -euo pipefail

cd "$(dirname "$0")/.."

violations=""

# All tracked Go test files (read line-by-line for portability with bash 3.2).
while IFS= read -r f; do
	[ -n "$f" ] || continue
	# Walk each `func TestXxx(... *testing.T ...)` function, tracking brace depth
	# to find its body, and report the file:line of any whose body never calls
	# RunSpecs (i.e. a Go standard-library test rather than a Ginkgo bootstrap).
	out=$(awk '
		/^func Test[A-Za-z0-9_]*\(/ && /\*testing\.T/ {
			infn = 1
			depth = 0
			hasrunspecs = 0
			startline = NR
		}
		infn {
			if ($0 ~ /RunSpecs/) hasrunspecs = 1
			opens = gsub(/{/, "{")
			closes = gsub(/}/, "}")
			depth += opens - closes
			if (depth <= 0 && NR >= startline && (opens + closes) > 0) {
				if (!hasrunspecs) print startline
				infn = 0
			}
		}
	' "$f")
	if [ -n "$out" ]; then
		while IFS= read -r ln; do
			violations+="  $f:$ln"$'\n'
		done <<<"$out"
	fi
# --cached picks up tracked files; --others --exclude-standard adds new,
# not-yet-committed files (respecting .gitignore) so a freshly added standard
# test is caught before it is even committed.
done < <(git ls-files --cached --others --exclude-standard '*_test.go')

if [ -n "$violations" ]; then
	{
		echo "ERROR: found Go standard-library test function(s)."
		echo
		echo "This project uses Ginkgo exclusively. Convert the following"
		echo "'func TestXxx(t *testing.T)' functions into Ginkgo specs. The only"
		echo "allowed TestXxx is a Ginkgo bootstrap that calls RunSpecs."
		echo
		printf '%s' "$violations"
	} >&2
	exit 1
fi
