#!/usr/bin/env bash
# Test 02 pre-state capture. Records the device fields the test
# manifest could (incorrectly) modify, so the post-test diff is
# trivial to inspect.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}')"
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}')"
: "${CVK_CONFIG_LINT_PASSWORD:?set the device password before running}"

# Hostname (changes if the engine accidentally takes the system path).
echo "hostname:"
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/hostname" \
  | jq -r '.["Cisco-IOS-XE-native:hostname"] // "<none>"'

# Banner motd (the field this test's CLI block would write). If the
# engine wrongly took the apply path, this would change.
echo ""
echo "banner-motd-bytes:"
curl --silent --insecure --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/banner" \
  | wc -c
