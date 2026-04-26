#!/usr/bin/env bash
# Test 11 rollback. Delete CR; engine runs empty-intent for
# interface_loopback and removes Loopback 9994. Manual RESTCONF
# DELETE as the fallback.

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
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9994")"
if [[ "${status}" == "200" ]]; then
  echo "Loopback 9994 still on device; manual RESTCONF DELETE."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9994"
fi
echo "rollback complete"
