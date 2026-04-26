#!/usr/bin/env bash
# Test 04 pre-state. Captures the description on the targeted
# interface so the post-test rollback can be diff-verified.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
# To target a port other than 0/0/0, set TEST_INTF_NAME accordingly.
TEST_INTF_TYPE="${TEST_INTF_TYPE:-GigabitEthernet}"
TEST_INTF_NAME="${TEST_INTF_NAME:-0/0/0}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

# RESTCONF URL-encodes the slashes in the key.
ENC_NAME="$(printf '%s' "${TEST_INTF_NAME}" | python3 -c "import sys, urllib.parse as p; print(p.quote(sys.stdin.read(), safe=''))")"

echo "interface: ${TEST_INTF_TYPE}${TEST_INTF_NAME}"
echo "description-pre:"
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${TEST_INTF_TYPE}=${ENC_NAME}/description" \
  | jq -r '.["Cisco-IOS-XE-native:description"] // "<unset>"'
