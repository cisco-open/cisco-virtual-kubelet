#!/usr/bin/env bash
# Test 09 pre-state. Confirms the three test entities (VLAN 998,
# VRF TEST-09-VRF, Loopback 9996) are absent before the test
# starts.

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
    --output /dev/null \
    --write-out '%{http_code}' \
    "https://${ADDR}/restconf/data/${path}")"
  echo "${label}: HTTP ${code}"
  case "${code}" in
    200) echo "  ABORT — ${label} already present"; exit 1 ;;
    204|404) ;; # absent — the desired pre-test state
    *) echo "  WARN — unexpected HTTP ${code}" ;;
  esac
}

echo "Verifying test entities are absent pre-test..."
probe "vlan-998" "Cisco-IOS-XE-native:native/vlan/vlan-list=998"
probe "vrf-TEST-09-VRF" "Cisco-IOS-XE-native:native/vrf/definition=TEST-09-VRF"
probe "loopback-9996" "Cisco-IOS-XE-native:native/interface/Loopback=9996"
echo ""
echo "All three test entities absent — safe to apply 00-apply-establish.yaml"
