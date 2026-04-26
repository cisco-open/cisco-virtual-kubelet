#!/usr/bin/env bash
# Test 11 verify. Asserts the §5 baseline + ConfirmedCommitFallback
# Warning event with the "non-transactional reconcile" reason +
# Loopback 9994 device-side present + NO ConfirmedCommitUsed.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-11-restconf-fallback"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_family_state interface_loopback InSync 1
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift

# Headline: ConfirmedCommitFallback Warning event present, with
# the "non-transactional reconcile" reason in the message.
fallback_msg="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=ConfirmedCommitFallback \
  -o jsonpath='{.items[*].message}' 2>/dev/null || true)"
if [[ -z "${fallback_msg}" ]]; then
  baseline_fail "no ConfirmedCommitFallback Warning event on IOSXEConfig/${TEST_CR}"
elif [[ "${fallback_msg}" == *"non-transactional reconcile"* ]]; then
  baseline_ok "ConfirmedCommitFallback event present with expected reason"
else
  baseline_fail "ConfirmedCommitFallback present but message lacks 'non-transactional reconcile': ${fallback_msg}"
fi

# Negative assertion: NO ConfirmedCommitUsed event (auto-revert
# path was not engaged).
used="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=ConfirmedCommitUsed \
  -o jsonpath='{.items[*].message}' 2>/dev/null || true)"
if [[ -n "${used}" ]]; then
  baseline_fail "ConfirmedCommitUsed event present — engine took the auto-revert path on a RESTCONF/non-transactional CR (regression)"
else
  baseline_ok "no ConfirmedCommitUsed event (correct on the fallback path)"
fi

# Confirmed-commit metric counter MUST NOT have incremented.
baseline_assert_metric_counter_zero \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="confirmed"'

# Device-side check.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9994")"
if [[ "${status}" == "200" ]]; then
  baseline_ok "Loopback 9994 present on device (RESTCONF non-transactional apply succeeded)"
else
  baseline_fail "Loopback 9994 absent (HTTP ${status}) — apply did not land"
fi

baseline_summary
exit "${baseline_failures}"
