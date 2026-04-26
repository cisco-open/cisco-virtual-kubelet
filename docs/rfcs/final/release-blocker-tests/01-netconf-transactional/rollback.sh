#!/usr/bin/env bash
# Test 01 rollback. Two-phase:
#   1. Delete the test CR. The reconciler's deletion-finalizer empties
#      the resolved intent for interface_loopback and (because the
#      test's sole loopback is the one it added) the family-empty
#      reconcile removes Loopback9999 from the device.
#   2. Verify Loopback9999 is no longer present. If it remains
#      (race with another reconcile or finalizer skip), issue a
#      manual RESTCONF DELETE as the fallback.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

kubectl delete -f 00-apply.yaml --ignore-not-found

# Allow the reconciler to drive the empty-intent path.
sleep 10

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9999")"

if [[ "${status}" == "200" ]]; then
  echo "Loopback9999 still on device; issuing manual RESTCONF DELETE."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9999"
elif [[ "${status}" == "204" || "${status}" == "404" ]]; then
  echo "OK: Loopback9999 removed."
else
  echo "WARN: unexpected HTTP ${status} when checking Loopback9999"
fi
