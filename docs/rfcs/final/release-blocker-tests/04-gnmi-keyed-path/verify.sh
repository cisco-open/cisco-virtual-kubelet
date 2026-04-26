#!/usr/bin/env bash
# Test 04 verify. Asserts the §5 baseline plus the test-specific
# device-side description and the gNMI transport-proof metric.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-04-gnmi-keyed-path"
TEST_INTF_TYPE="${TEST_INTF_TYPE:-GigabitEthernet}"
TEST_INTF_NAME="${TEST_INTF_NAME:-0/0/0}"
EXPECTED_DESC="cisco-vk release-blocker test 04 — wave 5A-fu/7B"

# Source the §5 baseline helpers and wire the test identifiers.
. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_family_state interface_ethernet InSync 1
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift

# Transport proof per §3.2 of the enrichment plan: the
# mutate_ops_total counter must show at least one Replace/Merge op
# attributed to transport=gnmi. Pre-fix, a silent fallback to
# RESTCONF could land the description on the device too — only the
# transport-labelled metric distinguishes the two.
baseline_assert_metric_counter \
  cisco_vk_config_mutate_ops_total \
  'device="'"${DEVICE_NAME}"'",transport="gnmi",verb="REPLACE"' \
  1 || \
  baseline_assert_metric_counter \
    cisco_vk_config_mutate_ops_total \
    'device="'"${DEVICE_NAME}"'",transport="gnmi",verb="MERGE"' \
    1

# Backward-compatible single-value tally retained for older shells
# that source this script and read $fail. The baseline_failures
# counter is the source of truth.
fail="${baseline_failures}"

# Device-side check.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

ENC_NAME="$(printf '%s' "${TEST_INTF_NAME}" | python3 -c "import sys, urllib.parse as p; print(p.quote(sys.stdin.read(), safe=''))")"

actual_desc="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${TEST_INTF_TYPE}=${ENC_NAME}/description" \
  | jq -r '.["Cisco-IOS-XE-native:description"] // "<unset>"')"

if [[ "${actual_desc}" != "${EXPECTED_DESC}" ]]; then
  baseline_fail "device description=${actual_desc}, want ${EXPECTED_DESC} (keyed-path may be malformed and the description may have landed on a different interface)"
else
  baseline_ok "device description matches (PathSpec carried ${TEST_INTF_NAME} through)"
fi

baseline_summary
exit "${baseline_failures}"
