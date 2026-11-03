#!/usr/bin/env bash
# Runs every *_test.sh in this directory against the recording terraform stub,
# then the real-binary smoke test when terraform or tofu is installed.
set -euo pipefail

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
failed=0

for t in "${TESTS_DIR}"/*_test.sh; do
  echo "== $(basename "${t}")"
  if ! bash "${t}"; then
    failed=$((failed + 1))
  fi
done

echo "== e2e_real_binary.sh"
if ! bash "${TESTS_DIR}/e2e_real_binary.sh"; then
  failed=$((failed + 1))
fi

if [ "${failed}" -ne 0 ]; then
  echo "${failed} test file(s) failed"
  exit 1
fi
echo "all integration tests passed"
