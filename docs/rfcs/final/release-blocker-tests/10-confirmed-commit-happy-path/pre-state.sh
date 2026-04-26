#!/usr/bin/env bash
# Test 10 pre-state. Asserts Loopback 9995 is absent and records
# the pre-test value of cisco_vk_config_transactions_total{
# outcome="confirmed"} so verify can assert a +1 delta.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

echo "loopback-9995-exists:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9995")"
case "${status}" in
  200) echo "yes — Loopback 9995 already exists; ABORT TEST 10" ;;
  204|404) echo "no" ;;
  *)   echo "unexpected HTTP ${status}" ;;
esac

# Capture pre-test metric value so verify.sh can assert +1.
echo ""
echo "pre-test confirmed-counter:"
pod="$(kubectl get pod -n "${NAMESPACE}" -l app="${DEVICE_NAME}" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
if [[ -n "${pod}" ]]; then
  pre="$(kubectl exec -n "${NAMESPACE}" "${pod}" -- \
    sh -c "wget -qO- http://localhost:8080/metrics 2>/dev/null || curl -s http://localhost:8080/metrics 2>/dev/null" 2>/dev/null \
    | grep -F 'cisco_vk_config_transactions_total{device="'"${DEVICE_NAME}"'",transport="netconf",outcome="confirmed"}' \
    | awk '{print $NF}' || echo 0)"
  pre="${pre%.*}"
  pre="${pre:-0}"
  echo "${pre}"
  echo "${pre}" > "$(dirname "$0")/confirmed-counter-pre.txt"
else
  echo "<no pod, skipping>"
fi
