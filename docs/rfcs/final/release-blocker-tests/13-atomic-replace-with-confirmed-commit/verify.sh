#!/usr/bin/env bash
# Test 13 verify (run after phase 2 apply completes). Asserts:
#   1. status.phase == InSync (post-phase-2)
#   2. all three test entities absent post-phase-2
#   3. ConfirmedCommitUsed event count >= 2 (one per phase)
#   4. ConfirmedCommitFallback event count == 0
#   5. outcome=auto_reverted counter == 0

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-13-combined"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift

# Device-side: all three absent.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

absent() {
  local label="$1" path="$2"
  local code
  code="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --header 'Accept: application/yang-data+json' \
    --output /dev/null --write-out '%{http_code}' \
    "https://${ADDR}/restconf/data/${path}")"
  case "${code}" in
    204|404) baseline_ok "${label} absent (HTTP ${code})" ;;
    200) baseline_fail "${label} STILL on device after phase 2" ;;
    *) baseline_fail "${label}: unexpected HTTP ${code}" ;;
  esac
}
absent "vlan-997" "Cisco-IOS-XE-native:native/vlan/vlan-list=997"
absent "vrf-TEST-13-VRF" "Cisco-IOS-XE-native:native/vrf/definition=TEST-13-VRF"
absent "loopback-9993" "Cisco-IOS-XE-native:native/interface/Loopback=9993"

# Combined-mode invariants — count ConfirmedCommitUsed events.
# Both phases should have produced one each, total >= 2.
used_count="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=ConfirmedCommitUsed \
  -o json 2>/dev/null | jq '.items | length' 2>/dev/null || echo 0)"
if (( used_count >= 2 )); then
  baseline_ok "ConfirmedCommitUsed event count = ${used_count} (>= 2; both phases engaged)"
else
  baseline_fail "ConfirmedCommitUsed event count = ${used_count}, want >= 2 (one per phase)"
fi

# No fallback events at all.
fallback="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=ConfirmedCommitFallback \
  -o jsonpath='{.items[*].message}' 2>/dev/null || true)"
if [[ -n "${fallback}" ]]; then
  baseline_fail "ConfirmedCommitFallback event present: ${fallback}"
else
  baseline_ok "no ConfirmedCommitFallback events (both phases took auto-revert path)"
fi

# No auto-revert outcomes.
baseline_assert_metric_counter_zero \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="auto_reverted"'

baseline_summary
exit "${baseline_failures}"
