#!/usr/bin/env bash
# Test 03 rollback. Only invoked if the test FAILED partway through —
# e.g. the deletion finalizer hangs and leaves VLAN 999 or Loopback
# 9998 on the device. Issues manual RESTCONF DELETEs as the explicit
# fallback.
#
# If the test succeeded cleanly, rollback is unnecessary (the cleanup
# IS the final state).

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

# Force-remove the synthetic CiscoDevice if still present.
if kubectl get ciscodevice test-03-prereq-device -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "Removing finalizer from CiscoDevice test-03-prereq-device (force)."
  kubectl patch ciscodevice test-03-prereq-device -n "${NAMESPACE}" \
    --type=merge --patch '{"metadata":{"finalizers":[]}}' || true
  kubectl delete ciscodevice test-03-prereq-device -n "${NAMESPACE}" --ignore-not-found
fi

# Force-remove any synthetic IOSXEConfig children.
for name in $(kubectl get iosxeconfig -n "${NAMESPACE}" -o json \
  | jq -r '.items[] | select(.metadata.name | test("test-03-prereq-device")) | .metadata.name'); do
  echo "Force-removing synthetic IOSXEConfig ${name}"
  kubectl patch iosxeconfig "${name}" -n "${NAMESPACE}" \
    --type=merge --patch '{"metadata":{"finalizers":[]}}' || true
  kubectl delete iosxeconfig "${name}" -n "${NAMESPACE}" --ignore-not-found
done

# Direct device cleanup.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

echo "Direct RESTCONF DELETE on VLAN 999."
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --request DELETE \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/vlan/vlan-list=999" || true

echo "Direct RESTCONF DELETE on Loopback 9998."
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --request DELETE \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9998" || true

echo "rollback complete; verify with pre-state.sh"
