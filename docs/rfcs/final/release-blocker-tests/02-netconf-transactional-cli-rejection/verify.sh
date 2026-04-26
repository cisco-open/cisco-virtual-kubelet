#!/usr/bin/env bash
# Test 02 verify. Asserts:
#   1. status.phase == Failed
#   2. status.conditions[Ready].reason == ErrTransactionalCLIUnsupported
#   3. device-side state matches pre-state (no banner change)

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
TEST_CR="test-02-cli-rejection"

fail=0

phase="$(kubectl get iosxeconfig "${TEST_CR}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.phase}')"
if [[ "${phase}" != "Failed" ]]; then
  echo "FAIL: phase=${phase}, want Failed"
  fail=1
else
  echo "OK:   phase=Failed"
fi

reason="$(kubectl get iosxeconfig "${TEST_CR}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}')"
if [[ "${reason}" != "ErrTransactionalCLIUnsupported" ]]; then
  echo "FAIL: Ready/reason=${reason}, want ErrTransactionalCLIUnsupported"
  fail=1
else
  echo "OK:   Ready/reason=ErrTransactionalCLIUnsupported"
fi

# Device-state comparison: pre-state.txt must equal current state.
if [[ ! -f pre-state.txt ]]; then
  echo "WARN: pre-state.txt not found; skipping device-state comparison."
else
  ./pre-state.sh > post-state.txt
  if ! diff -q pre-state.txt post-state.txt >/dev/null; then
    echo "FAIL: device-side state changed; engine path leaked through"
    diff -u pre-state.txt post-state.txt | head -40
    fail=1
  else
    echo "OK:   device state matches pre-state (no leakage)"
  fi
fi

exit "${fail}"
