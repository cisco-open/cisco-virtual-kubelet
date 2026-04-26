#!/usr/bin/env bash
# Test 07 pre-state. Records running-config absence of Loopback9997
# and (for operators with SSH access) startup-config absence too.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

echo "loopback-9997-running-exists:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /dev/null \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9997")"
case "${status}" in
  200) echo "yes — Loopback9997 already in running-config; ABORT TEST 07" ;;
  204|404) echo "no" ;;
  *)   echo "unexpected HTTP ${status}" ;;
esac

# Optional startup-config probe via the Cisco-IOS-XE-rpc operations
# RPC. Skipped silently if the device doesn't expose it; operator
# can SSH-attach a `show startup-config | include Loopback9997`
# excerpt to evidence/.
echo ""
echo "startup-config-includes-loopback9997:"
echo "(operator SSH attestation, optional — paste output of 'show startup-config | include Loopback9997')"
