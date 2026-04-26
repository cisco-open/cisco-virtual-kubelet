#!/usr/bin/env bash
# Test 10 rollback. Delete the CR; engine drives empty-intent for
# interface_loopback and removes Loopback 9995. Manual RESTCONF
# DELETE as fallback if the deletion finalizer doesn't converge.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

kubectl delete -f 00-apply.yaml --ignore-not-found
sleep 10

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9995")"
if [[ "${status}" == "200" ]]; then
  echo "Loopback 9995 still on device; manual RESTCONF DELETE."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9995"
fi

# Counter snapshot file is local-only — clean up.
rm -f "$(dirname "$0")/confirmed-counter-pre.txt"

echo "rollback complete"
