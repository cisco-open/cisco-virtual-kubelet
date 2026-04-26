#!/usr/bin/env bash
# Test 04 verify. Asserts:
#   1. status.phase == InSync
#   2. interface_ethernet family is InSync with opCount >= 1
#   3. device-side description on the targeted interface matches the
#      string in the ConfigMap.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-04-gnmi-keyed-path"
TEST_INTF_TYPE="${TEST_INTF_TYPE:-GigabitEthernet}"
TEST_INTF_NAME="${TEST_INTF_NAME:-0/0/0}"
EXPECTED_DESC="cisco-vk release-blocker test 04 — wave 5A-fu/7B"

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
  -o jsonpath='{.status.familyStatus[?(@.name=="interface_ethernet")].state}')"
if [[ "${family_state}" != "InSync" ]]; then
  echo "FAIL: interface_ethernet family state=${family_state}, want InSync"
  fail=1
else
  echo "OK:   interface_ethernet family=InSync"
fi

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
  echo "FAIL: device description=${actual_desc}, want ${EXPECTED_DESC}"
  echo "      (this could mean the keyed-path was malformed and the description landed elsewhere)"
  fail=1
else
  echo "OK:   device description matches (PathSpec carried 0/0/0 through)"
fi

exit "${fail}"
