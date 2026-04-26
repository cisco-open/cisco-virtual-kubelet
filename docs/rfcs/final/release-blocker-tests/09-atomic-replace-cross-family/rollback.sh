#!/usr/bin/env bash
# Test 09 rollback. If phase 2 succeeded, rollback IS the final
# state — all three test entities are already gone. We delete the
# CR + ConfigMap. If phase 2 failed, the engine's deferred
# Discard runs and leaves phase-1 state on the device; this script
# then issues manual RESTCONF DELETEs as the cleanup fallback.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

kubectl delete -f 01-apply-empty-with-atomic.yaml --ignore-not-found
kubectl delete -f 00-apply-establish.yaml --ignore-not-found
sleep 5

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

# Defensive RESTCONF DELETEs — order matters for residual cleanup
# (loopback before vrf because the loopback's vrf binding holds a
# reference). 204/404 on each is the success signal.
del() {
  local path="$1"
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/${path}" >/dev/null || true
}

echo "Defensive RESTCONF DELETEs (no-op if already absent)..."
del "Cisco-IOS-XE-native:native/interface/Loopback=9996"
del "Cisco-IOS-XE-native:native/vrf/definition=TEST-09-VRF"
del "Cisco-IOS-XE-native:native/vlan/vlan-list=998"
echo "rollback complete; verify with pre-state.sh"
