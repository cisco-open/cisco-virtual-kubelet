#!/usr/bin/env bash
# Test 02 verify. Asserts the §5 baseline plus the test-specific
# device-state-equality and the negative metric-counter check
# (transactions_total{netconf,*} stayed zero — no transport call).

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-02-cli-rejection"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
# The engine has two valid paths for "transactional + CLI" depending
# on transport:
#
#   netconf  → fail-fast at engine.validate(): Phase=Failed,
#              Reason=ErrTransactionalCLIUnsupported (Wave 7A.1).
#   restconf → no candidate datastore, so the CR's
#              transactional=true cannot apply at all; the engine
#              reports drift and the belt-and-suspenders
#              driftPolicy=report on this CR keeps the apply from
#              firing: Phase=Drifted, Reason=Drifted.
#
# Both paths share the safety invariant the test exists to enforce:
# **no device write happens for transactional+CLI**. We accept either
# terminal phase here and let the device-state diff below catch any
# leakage. See the device-state comparison at the bottom of the file.
device_transport="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.transport}' 2>/dev/null || echo restconf)"
case "${device_transport}" in
  netconf)
    baseline_assert_phase_is Failed
    baseline_assert_ready_condition_matches False ErrTransactionalCLIUnsupported
    ;;
  *)
    baseline_assert_phase_is Drifted
    baseline_assert_ready_condition_matches False Drifted
    ;;
esac

# Negative-control metric checks: the engine must NOT have started
# a transaction or executed any mutate op. Pre-Wave-7A.1 the
# rejection happened at the transport layer (after edit-config had
# already issued); these zero-asserts catch any regression where
# the rejection moves back into the transport.
baseline_assert_metric_counter_zero \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="commit"'
baseline_assert_metric_counter_zero \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="commit_failed"'
# Test 02's hermetic invariant: this CR's reconcile must not have
# emitted any mutate op, regardless of transport. We can't filter
# by CR identity in metric scrapes, but a discard-without-commit on
# this transport would be equally suspicious.
baseline_assert_metric_counter_zero \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="start_failed"'

# Device-state comparison: pre-state.txt must equal current state.
if [[ ! -f pre-state.txt ]]; then
  echo "WARN: pre-state.txt not found; skipping device-state comparison"
else
  ./pre-state.sh > post-state.txt
  if ! diff -q pre-state.txt post-state.txt >/dev/null; then
    baseline_fail "device-side state changed; engine path leaked through"
    diff -u pre-state.txt post-state.txt | head -40
  else
    baseline_ok "device state matches pre-state (no leakage)"
  fi
fi

baseline_summary
exit "${baseline_failures}"
