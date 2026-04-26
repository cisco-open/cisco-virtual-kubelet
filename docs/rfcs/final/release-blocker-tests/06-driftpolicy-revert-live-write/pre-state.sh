#!/usr/bin/env bash
# Test 06 pre-state. Captures the device's existing banner motd
# verbatim. The rollback feeds this back in.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

echo "banner-pre-json:"
status="$(curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  --output /tmp/test-06-banner-pre.json \
  --write-out '%{http_code}' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/banner/motd")"

case "${status}" in
  200)
    cat /tmp/test-06-banner-pre.json
    cp /tmp/test-06-banner-pre.json "$(dirname "$0")/banner-pre.json"
    ;;
  204|404)
    echo "<no banner motd configured>"
    rm -f "$(dirname "$0")/banner-pre.json"
    ;;
  *)
    echo "ERROR: unexpected HTTP ${status}" >&2
    exit 1
    ;;
esac
