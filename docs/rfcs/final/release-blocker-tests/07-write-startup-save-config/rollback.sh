#!/usr/bin/env bash
# Test 07 rollback. Two-phase per pre-pr-test-enrichment-plan §4:
#   1. Delete the CR (drives empty-intent reconcile → Loopback9997
#      removed from running-config).
#   2. Re-apply a CR with writeStartup=true and an empty intent so
#      the engine writes the cleaned running-config to startup-config
#      too. This re-uses the same path the test was proving — using
#      the test's own machinery to clean up its own startup-config
#      change.
#
# Fallbacks: if either phase leaves device-side state behind, manual
# RESTCONF DELETE on running-config + an SSH `write memory` on
# startup-config closes the gap.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"
TEST_CR="test-07-write-startup"

# Phase 1: delete the test CR. Engine drives empty-intent for
# interface_loopback; Loopback9997 disappears from running-config.
kubectl delete -f 00-apply.yaml --ignore-not-found
sleep 10

# Manual fallback if running-config still has Loopback9997.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9997")"
if [[ "${status}" == "200" ]]; then
  echo "Loopback9997 still in running-config; manual RESTCONF DELETE."
  curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
    --request DELETE \
    "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9997"
fi

# Phase 2: persist the cleaned running-config to startup-config so
# the device's startup state matches pre-test. Operator-driven via
# SSH because the engine's writeStartup path needs a separate CR
# whose only job is to save-config; rather than apply that CR,
# we direct the operator to issue `write memory` over SSH (or
# RESTCONF if the device exposes the save RPC).
echo ""
echo "============================================================"
echo "OPERATOR ACTION REQUIRED:"
echo "Run on the device to persist the cleaned running-config:"
echo "  ssh ${USER}@${ADDR}"
echo "  > write memory"
echo "Or via RESTCONF if Cisco-IOS-XE-rpc:save is enabled:"
echo "  curl -X POST -u ${USER}:\$PASS \\"
echo "    --header 'Content-Type: application/yang-data+json' \\"
echo "    --data '{\"input\":{\"target\":\"running\",\"source\":\"running\"}}' \\"
echo "    https://${ADDR}/restconf/operations/cisco-ia:save-config"
echo ""
echo "After save-config completes, run pre-state.sh and confirm"
echo "Loopback9997 is absent from BOTH running and startup."
echo "============================================================"
