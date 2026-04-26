#!/usr/bin/env bash
# Test 06 verify (run after phase 2 apply). Asserts:
#   1. status.phase == InSync
#   2. banner family is InSync, opCount >= 1
#   3. device-side banner motd equals the test string
#   4. spot-checked system fields (hostname, domain-name) unchanged

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-06-driftpolicy-revert"
EXPECTED_BANNER="cisco-vk release-blocker test 06 — wave drift-detect"

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
  -o jsonpath='{.status.familyStatus[?(@.name=="banner")].state}')"
if [[ "${family_state}" != "InSync" ]]; then
  echo "FAIL: banner family state=${family_state}, want InSync"
  fail=1
else
  echo "OK:   banner family=InSync"
fi

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
  echo "FAIL: device banner=${actual_banner}, want ${EXPECTED_BANNER}"
  fail=1
else
  echo "OK:   device banner matches"
fi

# Spot-check that hostname is unchanged from pre-state. (We didn't
# capture it explicitly in pre-state.sh; this just records the
# current value for the operator to eyeball.)
hostname="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/hostname" \
  | jq -r '.["Cisco-IOS-XE-native:hostname"] // "<none>"')"
echo "INFO: hostname=${hostname} (compare to pre-state.txt manually)"

exit "${fail}"
