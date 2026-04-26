#!/usr/bin/env bash
# Test 13 pre-state. Confirms VLAN 997, VRF TEST-13-VRF, Loopback
# 9993 absent before the test runs.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

probe() {
  local label="$1" path="$2"
  local code
  code="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --header 'Accept: application/yang-data+json' \
    --output /dev/null --write-out '%{http_code}' \
    "https://${ADDR}/restconf/data/${path}")"
  echo "${label}: HTTP ${code}"
  case "${code}" in
    200) echo "  ABORT — ${label} already present"; exit 1 ;;
    204|404) ;; # absent — desired
    *) echo "  WARN — unexpected HTTP ${code}" ;;
  esac
}

probe "vlan-997" "Cisco-IOS-XE-native:native/vlan/vlan-list=997"
probe "vrf-TEST-13-VRF" "Cisco-IOS-XE-native:native/vrf/definition=TEST-13-VRF"
probe "loopback-9993" "Cisco-IOS-XE-native:native/interface/Loopback=9993"
echo ""
echo "All three test entities absent — safe to apply 00-apply-establish.yaml"
