#!/usr/bin/env bash
# Test 10 verify. Asserts the §5 baseline + ConfirmedCommitUsed
# event + outcome=confirmed counter increment + Loopback 9995
# present on device. Crucially asserts NO ConfirmedCommitFallback
# Warning event (catches the "silently fell back to plain Commit"
# regression).

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-10-confirmed-commit-happy"
EXPECTED_DESC="cisco-vk release-blocker test 10 — confirmed-commit happy path"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

baseline_assert_observed_generation_synced
baseline_assert_phase_is InSync
baseline_assert_ready_condition_matches True Succeeded
baseline_assert_family_state interface_loopback InSync 1
baseline_assert_no_unexpected_apply_errors
baseline_assert_no_unexpected_drift

# Headline assertion: outcome=confirmed counter incremented by AT
# LEAST 1 from pre-test. Read the absolute counter and compare to
# pre-test snapshot.
pod="$(kubectl get pod -n "${NAMESPACE}" -l app="${DEVICE_NAME}" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
if [[ -n "${pod}" ]]; then
  post="$(kubectl exec -n "${NAMESPACE}" "${pod}" -- \
    sh -c "wget -qO- http://localhost:8080/metrics 2>/dev/null || curl -s http://localhost:8080/metrics 2>/dev/null" 2>/dev/null \
    | grep -F 'cisco_vk_config_transactions_total{device="'"${DEVICE_NAME}"'",transport="netconf",outcome="confirmed"}' \
    | awk '{print $NF}' || echo 0)"
  post="${post%.*}"
  post="${post:-0}"
  pre=0
  [[ -f "$(dirname "$0")/confirmed-counter-pre.txt" ]] && pre="$(cat "$(dirname "$0")/confirmed-counter-pre.txt")"
  if (( post > pre )); then
    baseline_ok "outcome=confirmed counter: pre=${pre} → post=${post} (delta +$((post-pre)))"
  else
    baseline_fail "outcome=confirmed counter did not increment: pre=${pre} → post=${post}"
  fi
else
  baseline_fail "could not locate cisco-vk pod for metric scrape"
fi

# Negative assertion: NO ConfirmedCommitFallback event. If the
# engine fell back to plain Commit, this Warning event would be
# present and the auto-revert path was not exercised.
fallback_events="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=ConfirmedCommitFallback \
  -o jsonpath='{.items[*].message}' 2>/dev/null || true)"
if [[ -n "${fallback_events}" ]]; then
  baseline_fail "ConfirmedCommitFallback event present — engine fell back to plain Commit: ${fallback_events}"
else
  baseline_ok "no ConfirmedCommitFallback event (auto-revert path engaged)"
fi

# Positive assertion: ConfirmedCommitUsed event present.
used_events="$(kubectl get events -n "${NAMESPACE}" \
  --field-selector involvedObject.name="${TEST_CR}",reason=ConfirmedCommitUsed \
  -o jsonpath='{.items[*].message}' 2>/dev/null || true)"
if [[ -n "${used_events}" ]]; then
  baseline_ok "ConfirmedCommitUsed event present"
else
  baseline_fail "no ConfirmedCommitUsed event on IOSXEConfig/${TEST_CR}"
fi

# Device-side check.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

actual_desc="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9995" \
  | jq -r '.["Cisco-IOS-XE-native:Loopback"].description // ""')"
if [[ "${actual_desc}" == "${EXPECTED_DESC}" ]]; then
  baseline_ok "Loopback 9995 description matches"
else
  baseline_fail "Loopback 9995 description = ${actual_desc}, want ${EXPECTED_DESC}"
fi

baseline_summary
exit "${baseline_failures}"
