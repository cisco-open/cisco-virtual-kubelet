#!/usr/bin/env bash
# Test 08 pre-state. Captures the management-interface ACL binding,
# the controller's source IP (helps the operator fill in
# 00-apply.yaml's <CONTROLLER_SOURCE_IP> placeholder), and a
# baseline of the controller's RESTCONF reachability.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
MGMT_INTF_TYPE="${MGMT_INTF_TYPE:-GigabitEthernet}"
MGMT_INTF_NAME="${MGMT_INTF_NAME:-0/0}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

ENC_NAME="$(printf '%s' "${MGMT_INTF_NAME}" | python3 -c "import sys, urllib.parse as p; print(p.quote(sys.stdin.read(), safe=''))")"

echo "mgmt-intf: ${MGMT_INTF_TYPE}${MGMT_INTF_NAME}"
echo ""
echo "mgmt-acl-binding-pre:"
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${MGMT_INTF_TYPE}=${ENC_NAME}/ip/access-group" \
  --output /tmp/test-08-acl-binding.json \
  --write-out '%{http_code}'
echo ""
case "$(cat /tmp/test-08-acl-binding.json 2>/dev/null | head -c1)" in
  '{') jq -c . < /tmp/test-08-acl-binding.json; cp /tmp/test-08-acl-binding.json "$(dirname "$0")/acl-binding-pre.json" ;;
  *)   echo "<no ACL binding on mgmt interface pre-test>"; rm -f "$(dirname "$0")/acl-binding-pre.json" ;;
esac

echo ""
echo "test-acl-name-exists:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/ip/access-list/extended=TEST-08-MGMT-LOCKOUT")"
case "${status}" in
  200) echo "yes — TEST-08-MGMT-LOCKOUT ACL already exists; ABORT TEST 08" ;;
  204|404) echo "no" ;;
  *)   echo "unexpected HTTP ${status}" ;;
esac

echo ""
echo "controller-source-ip-hint:"
echo "  Run on the device console:  show users  | include vty"
echo "  Or from RESTCONF:  GET .../Cisco-IOS-XE-tcp-oper:tcp-connections"
echo "  Substitute that IP into 00-apply.yaml's <CONTROLLER_SOURCE_IP>"
echo ""
echo "===================================================================="
echo "OPERATOR ATTESTATION REQUIRED:"
echo "  ✓ Out-of-band console attached and verified working"
echo "  ✓ Maintenance window > 90 seconds"
echo "  ✓ Manual rollback procedure documented"
echo "  ✓ <CONTROLLER_SOURCE_IP> in 00-apply.yaml is the cluster's egress"
echo "    address (not 0.0.0.0, not the device's own IP)"
echo "===================================================================="
