#!/usr/bin/env bash
# Test 06 verify (run after phase 2 apply). Asserts the §5 baseline
# plus the test-specific banner-equals check and the engine-emitted-
# at-least-one-mutate-op metric proof.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-06-driftpolicy-revert"
EXPECTED_BANNER="cisco-vk release-blocker test 06 — wave drift-detect"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_family_state banner InSync 1
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift
fail="${baseline_failures}"

# Device-side check.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

actual_banner="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/banner/motd" \
  | jq -r '.["Cisco-IOS-XE-native:motd"].banner // ""')"

if [[ "${actual_banner}" != "${EXPECTED_BANNER}" ]]; then
  baseline_fail "device banner=${actual_banner}, want ${EXPECTED_BANNER}"
else
  baseline_ok "device banner matches"
fi

# Spot-check that hostname is unchanged from pre-state.
hostname="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/hostname" \
  | jq -r '.["Cisco-IOS-XE-native:hostname"] // "<none>"')"
echo "INFO: hostname=${hostname} (compare to pre-state.txt manually)"

# Engine emitted at least one mutate op for the banner family. We
# allow either REPLACE or MERGE since the engine's choice depends
# on whether banner motd is empty or pre-existing on the device.
baseline_assert_metric_counter \
  cisco_vk_config_mutate_ops_total \
  'device="'"${DEVICE_NAME}"'",transport="restconf",verb="REPLACE"' \
  1 || \
  baseline_assert_metric_counter \
    cisco_vk_config_mutate_ops_total \
    'device="'"${DEVICE_NAME}"'",transport="restconf",verb="MERGE"' \
    1

baseline_summary
exit "${baseline_failures}"
