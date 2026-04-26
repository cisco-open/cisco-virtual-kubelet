#!/usr/bin/env bash
# Test 01 verify. Asserts the §5 baseline plus the test-specific
# device-side Loopback9999 surface and the NETCONF transactional
# transport-proof metric.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-01-netconf-transactional"
EXPECTED_DESC="cisco-vk release-blocker test 01 — wave 1A-fu"
EXPECTED_IP="10.255.255.99"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_family_state interface_loopback InSync 1
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift

# Transport proof per §3.1: the transactional commit counter must
# show at least one outcome=commit attributed to transport=netconf.
# Pre-fix this counter did not exist; the only check was "the
# Loopback exists", which a non-transactional write would also
# satisfy. The new metric is the production-confidence signal.
baseline_assert_metric_counter \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="commit"' \
  1
# A discarded transaction is failure for this test; the counter
# value at outcome=discard must be exactly zero.
baseline_assert_metric_counter \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="discard"' \
  0
fail="${baseline_failures}"

# Device-side check.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

body="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9999")"

actual_desc="$(echo "${body}" | jq -r '.["Cisco-IOS-XE-native:Loopback"].description // ""')"
actual_ip="$(echo "${body}" | jq -r '.["Cisco-IOS-XE-native:Loopback"].ip.address.primary.address // ""')"

if [[ "${actual_desc}" != "${EXPECTED_DESC}" ]]; then
  baseline_fail "device description=${actual_desc}, want ${EXPECTED_DESC}"
else
  baseline_ok "device description matches"
fi

if [[ "${actual_ip}" != "${EXPECTED_IP}" ]]; then
  baseline_fail "device IP=${actual_ip}, want ${EXPECTED_IP}"
else
  baseline_ok "device IP matches"
fi

# Optional: surface any Discard call from the cisco-vk pod logs.
pod="$(kubectl get pod -n "${NAMESPACE}" -l app="${DEVICE_NAME}" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -n "${pod}" ]]; then
  if kubectl logs -n "${NAMESPACE}" "${pod}" --tail 200 2>/dev/null \
     | grep -i 'discard\|rollback' >/dev/null; then
    echo "WARN: pod logs mention discard/rollback — investigate"
  fi
fi

baseline_summary
exit "${baseline_failures}"
