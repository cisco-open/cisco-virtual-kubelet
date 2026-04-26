#!/usr/bin/env bash
# Test 01 verify. Asserts:
#   1. status.phase == InSync
#   2. interface_loopback family is InSync
#   3. device-side: Loopback9999 exists with the test description + IP
#   4. (optional, log-based) transactional sequence ran cleanly: warn
#      if Discard appears; fail-soft if logs unavailable.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-01-netconf-transactional"
EXPECTED_DESC="cisco-vk release-blocker test 01 — wave 1A-fu"
EXPECTED_IP="10.255.255.99"

fail=0

phase="$(kubectl get iosxeconfig "${TEST_CR}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.phase}')"
if [[ "${phase}" != "InSync" ]]; then
  echo "FAIL: phase=${phase}, want InSync"
  fail=1
else
  echo "OK:   phase=InSync"
fi

family_state="$(kubectl get iosxeconfig "${TEST_CR}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.familyStatus[?(@.name=="interface_loopback")].state}')"
if [[ "${family_state}" != "InSync" ]]; then
  echo "FAIL: interface_loopback family state=${family_state}, want InSync"
  fail=1
else
  echo "OK:   interface_loopback family=InSync"
fi

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
  echo "FAIL: device description=${actual_desc}, want ${EXPECTED_DESC}"
  fail=1
else
  echo "OK:   device description matches"
fi

if [[ "${actual_ip}" != "${EXPECTED_IP}" ]]; then
  echo "FAIL: device IP=${actual_ip}, want ${EXPECTED_IP}"
  fail=1
else
  echo "OK:   device IP matches"
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

exit "${fail}"
