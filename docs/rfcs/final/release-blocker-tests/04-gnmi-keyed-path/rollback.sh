#!/usr/bin/env bash
# Test 04 rollback. Deletes the test CR; with driftPolicy=revert + the
# ManagedFamilies machinery, deleting the CR triggers the engine's
# empty-intent path which removes the description it added. Confirm
# with pre-state.sh after this completes.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"

kubectl delete -f 00-apply.yaml --ignore-not-found

# Wait briefly for the deletion finalizer / next reconcile to run.
sleep 5

# If the description still shows the test string, manually issue a
# RESTCONF DELETE to remove it. This is the manual fallback when the
# CR's deletion doesn't drive the family-empty path (e.g. if the
# pruneOnRelinquish flag isn't set on the synthetic teardown CR for
# this family — it isn't here because the test uses a regular CR, not
# a configPrereqs CR).

DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_INTF_TYPE="${TEST_INTF_TYPE:-GigabitEthernet}"
TEST_INTF_NAME="${TEST_INTF_NAME:-0/0/0}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

ENC_NAME="$(printf '%s' "${TEST_INTF_NAME}" | python3 -c "import sys, urllib.parse as p; print(p.quote(sys.stdin.read(), safe=''))")"

current="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${TEST_INTF_TYPE}=${ENC_NAME}/description" \
  | jq -r '.["Cisco-IOS-XE-native:description"] // ""' )"

if [[ "${current}" == *"release-blocker test 04"* ]]; then
  echo "Test description still on device; issuing RESTCONF DELETE as fallback."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    --header 'Accept: application/yang-data+json' \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${TEST_INTF_TYPE}=${ENC_NAME}/description"
fi

echo "rollback complete"
