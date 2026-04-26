#!/usr/bin/env bash
# Test 08 verify. Waits up to 60 seconds for the auto-revert to
# complete, then asserts the four conditions listed in expected.md.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-08-confirmed-commit-auto-revert"
MGMT_INTF_TYPE="${MGMT_INTF_TYPE:-GigabitEthernet}"
MGMT_INTF_NAME="${MGMT_INTF_NAME:-0/0}"

. "$(dirname "$0")/../lib/baseline.sh"
baseline_namespace="${NAMESPACE}"
baseline_cr="${TEST_CR}"

# Phase first — should reach Failed within ~60s of apply.
echo "Polling for status.phase=Failed (auto-revert path completion)..."
for i in $(seq 1 60); do
  phase="$(kubectl get iosxeconfig "${TEST_CR}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  if [[ "${phase}" == "Failed" ]]; then
    baseline_ok "phase=Failed at t+${i}s"
    break
  fi
  sleep 1
done
[[ "${phase}" == "Failed" ]] || baseline_fail "phase=${phase} after 60s, want Failed"

# Err message must mention "auto-revert".
err_msg="$(kubectl get iosxeconfig "${TEST_CR}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null || true)"
if [[ "${err_msg}" == *"auto-revert"* ]]; then
  baseline_ok "Ready condition message mentions auto-revert"
else
  baseline_fail "Ready condition message does not mention auto-revert: ${err_msg}"
fi

# Metric: outcome=auto_reverted >= 1, outcome=confirmed == 0.
baseline_assert_metric_counter \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="auto_reverted"' \
  1
baseline_assert_metric_counter_zero \
  cisco_vk_config_transactions_total \
  'device="'"${DEVICE_NAME}"'",transport="netconf",outcome="confirmed"'

# Device-side state: ACL binding restored to pre-state.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

ENC_NAME="$(printf '%s' "${MGMT_INTF_NAME}" | python3 -c "import sys, urllib.parse as p; print(p.quote(sys.stdin.read(), safe=''))")"

# Test ACL must NOT be on the device.
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/ip/access-list/extended=TEST-08-MGMT-LOCKOUT")"
if [[ "${status}" == "200" ]]; then
  baseline_fail "TEST-08-MGMT-LOCKOUT ACL still on device after auto-revert window"
else
  baseline_ok "TEST-08-MGMT-LOCKOUT ACL absent from device (HTTP ${status})"
fi

# Mgmt-interface ACL binding restored to pre-test state.
PRESTATE="$(dirname "$0")/acl-binding-pre.json"
post_status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /tmp/test-08-post.json \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${MGMT_INTF_TYPE}=${ENC_NAME}/ip/access-group")"
if [[ -f "${PRESTATE}" ]]; then
  if diff -q /tmp/test-08-post.json "${PRESTATE}" >/dev/null 2>&1; then
    baseline_ok "mgmt ACL binding restored to pre-state"
  else
    baseline_fail "mgmt ACL binding diverges from pre-state"
    diff -u "${PRESTATE}" /tmp/test-08-post.json | head -40 || true
  fi
else
  if [[ "${post_status}" == "204" || "${post_status}" == "404" ]]; then
    baseline_ok "no mgmt ACL binding pre or post (consistent)"
  else
    baseline_fail "no pre-state ACL binding but post-test shows one (HTTP ${post_status})"
  fi
fi

baseline_summary
exit "${baseline_failures}"
