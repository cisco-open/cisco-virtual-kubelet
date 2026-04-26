#!/usr/bin/env bash
# Test 11 pre-state. Asserts Loopback 9994 absent.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

echo "loopback-9994-exists:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9994")"
case "${status}" in
  200) echo "yes — Loopback 9994 already exists; ABORT TEST 11" ;;
  204|404) echo "no" ;;
  *)   echo "unexpected HTTP ${status}" ;;
esac
