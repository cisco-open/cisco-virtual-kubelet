#!/usr/bin/env bash
# Test 08 rollback. If the test passed cleanly (auto-revert
# fired), there is nothing to do — the device is already at
# pre-test state. We delete the test CR + ConfigMap and exit.
#
# If the test FAILED (auto-revert did NOT fire — the most serious
# failure mode for this test), the device is still in the broken
# state and the controller cannot reach it. Manual rollback via
# out-of-band console is the operator's responsibility:
#
#   conf t
#     interface <MGMT_INTF>
#       no ip access-group TEST-08-MGMT-LOCKOUT in
#     no ip access-list extended TEST-08-MGMT-LOCKOUT
#   end
#
# This script CANNOT recover from auto-revert failure because its
# RESTCONF egress IS the same source IP the broken ACL is denying.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"

kubectl delete -f 00-apply.yaml --ignore-not-found

echo ""
echo "===================================================================="
echo "OPERATOR CONSOLE CHECK:"
echo "  Verify on the device:  show running-config | include access-group"
echo "  Expected: NO 'ip access-group TEST-08-MGMT-LOCKOUT in' line."
echo "  Expected: NO 'ip access-list extended TEST-08-MGMT-LOCKOUT' block."
echo ""
echo "  If either is still present, the auto-revert FAILED and"
echo "  manual rollback is required (see comments in rollback.sh)."
echo "===================================================================="
