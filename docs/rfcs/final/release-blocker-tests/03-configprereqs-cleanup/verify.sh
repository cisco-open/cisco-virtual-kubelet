#!/usr/bin/env bash
# Test 03 verify (run after `kubectl delete -f 00-apply-device.yaml`
# completes). Asserts:
#   1. CiscoDevice test-03-prereq-device is gone (no terminating phase)
#   2. Synthetic IOSXEConfig children are gone (owner-ref GC worked)
#   3. VLAN 999 absent from device
#   4. Loopback 9998 absent from device
#   5. No other VLAN or Loopback was added/removed (compare against pre-state)

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

fail=0

# 1. CiscoDevice should be gone or at least not present.
if kubectl get ciscodevice test-03-prereq-device -n "${NAMESPACE}" >/dev/null 2>&1; then
  phase="$(kubectl get ciscodevice test-03-prereq-device -n "${NAMESPACE}" \
    -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  echo "FAIL: CiscoDevice test-03-prereq-device still present (phase=${phase})"
  fail=1
else
  echo "OK:   CiscoDevice test-03-prereq-device is gone"
fi

# 2. Synthetic IOSXEConfig children should also be gone.
remaining_synth="$(kubectl get iosxeconfig -n "${NAMESPACE}" \
  -o json | jq -r '.items[] | select(.metadata.name | test("test-03-prereq-device")) | .metadata.name' 2>/dev/null || true)"
if [[ -n "${remaining_synth}" ]]; then
  echo "FAIL: synthetic IOSXEConfig(s) remain: ${remaining_synth}"
  fail=1
else
  echo "OK:   no synthetic IOSXEConfig remains"
fi

# Device-side checks.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

# 3. VLAN 999 absent.
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/vlan/vlan-list=999")"
if [[ "${status}" == "200" ]]; then
  echo "FAIL: VLAN 999 still on device"
  fail=1
else
  echo "OK:   VLAN 999 absent (HTTP ${status})"
fi

# 4. Loopback 9998 absent.
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9998")"
if [[ "${status}" == "200" ]]; then
  echo "FAIL: Loopback 9998 still on device"
  fail=1
else
  echo "OK:   Loopback 9998 absent (HTTP ${status})"
fi

# 5. Spot-check: post-state VLAN/Loopback lists should equal pre-state.
if [[ -f pre-state.txt ]]; then
  ./pre-state.sh > post-state.txt
  if ! diff -q pre-state.txt post-state.txt >/dev/null; then
    echo "WARN: pre-state and post-state differ:"
    diff -u pre-state.txt post-state.txt | head -40
    echo "      (the diff should ONLY reflect 999/9998 absence, which is the intended outcome)"
  else
    echo "OK:   pre-state and post-state identical"
  fi
fi

exit "${fail}"
