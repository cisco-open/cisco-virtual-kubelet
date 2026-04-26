#!/usr/bin/env bash
# Test 13 rollback. If phase 2 succeeded, rollback IS the final
# state. If phase 2 failed (auto-revert restored phase-1 state),
# this script clears phase-1 state via defensive RESTCONF DELETEs.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

kubectl delete -f 01-apply-empty.yaml --ignore-not-found
kubectl delete -f 00-apply-establish.yaml --ignore-not-found
sleep 5

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

del() {
  local path="$1"
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/${path}" >/dev/null || true
}

echo "Defensive RESTCONF DELETEs (no-op if already absent)..."
del "Cisco-IOS-XE-native:native/interface/Loopback=9993"
del "Cisco-IOS-XE-native:native/vrf/definition=TEST-13-VRF"
del "Cisco-IOS-XE-native:native/vlan/vlan-list=997"
echo "rollback complete; verify with pre-state.sh"
