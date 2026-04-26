#!/usr/bin/env bash
# Test 06 rollback. Two-phase:
#   1. Delete the test CR (which alone does NOT undo the banner write
#      since pruneOnRelinquish=false by default).
#   2. Restore the banner from pre-state.json (or RESTCONF DELETE if
#      no pre-state was captured — meaning the device had no banner
#      before the test).

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

# Phase 1: delete the CR.
kubectl delete -f 01-flip-to-revert.yaml --ignore-not-found
kubectl delete -f 00-apply-report.yaml --ignore-not-found

# Phase 2: restore the device banner.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

PRESTATE="$(dirname "$0")/banner-pre.json"
if [[ -f "${PRESTATE}" ]]; then
  echo "Restoring banner from pre-state JSON."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request PUT \
    --header 'Content-Type: application/yang-data+json' \
    --header 'Accept: application/yang-data+json' \
    --data @"${PRESTATE}" \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/banner/motd"
else
  echo "No pre-state banner — issuing RESTCONF DELETE on the motd subtree."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/banner/motd"
fi

echo "rollback complete; verify with pre-state.sh"
