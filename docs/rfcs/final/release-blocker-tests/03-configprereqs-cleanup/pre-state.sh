#!/usr/bin/env bash
# Test 03 pre-state. Captures VLAN list and Loopback list, asserts
# 999/9998 are NOT present pre-test.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

echo "vlan-ids:"
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/vlan" \
  | jq -r '.["Cisco-IOS-XE-native:vlan"]["vlan-list"][]?.id' | sort -n

echo ""
echo "vlan-999-exists:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/vlan/vlan-list=999")"
case "${status}" in
  200) echo "yes — VLAN 999 already exists; ABORT TEST 03" ;;
  204|404) echo "no" ;;
  *)   echo "unexpected HTTP ${status}" ;;
esac

echo ""
echo "loopback-names:"
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback" \
  | jq -r '.["Cisco-IOS-XE-native:Loopback"][]?.name // empty' | sort -n

echo ""
echo "loopback-9998-exists:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9998")"
case "${status}" in
  200) echo "yes — Loopback 9998 already exists; ABORT TEST 03" ;;
  204|404) echo "no" ;;
  *)   echo "unexpected HTTP ${status}" ;;
esac
