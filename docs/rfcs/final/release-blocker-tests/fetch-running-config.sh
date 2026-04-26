#!/usr/bin/env bash
# fetch-running-config.sh
#
# Capture a per-family snapshot of the live running configuration on
# the Cat9K via the project's own cisco-vk-config-lint tool, running
# in offline mode against a captured device fetch. Operator-runnable;
# the agent does not contact the device. Output is yours alone.
#
# Why this exists:
#
#   The release-blocker test packages under this directory each
#   require a "pre-state" snapshot so the operator can verify that
#   their per-test changes were the only changes, and that the device
#   ends each test back at the pre-state. This script captures that
#   baseline once at the start of the maintenance window. Re-run it
#   between tests if you want a fresh baseline.
#
# Usage:
#
#   ./fetch-running-config.sh <output-dir> [device-name] [namespace]
#
# Examples:
#
#   # Default: write into ./snapshot, device cat9k-smoke, namespace cisco-vk-smoke
#   ./fetch-running-config.sh ./snapshot
#
#   # Custom device + namespace
#   ./fetch-running-config.sh /tmp/cat9k-pre cat9k-smoke cisco-vk-smoke
#
# Requirements:
#
#   - kubectl configured against the cluster running the cisco-vk pod
#   - cisco-vk-config-lint binary on PATH (or use `go run ./tools/cisco-vk-config-lint`)
#   - The device's RESTCONF password set in $CVK_CONFIG_LINT_PASSWORD,
#     or pass --password-env directly via $LINT_EXTRA_FLAGS.
#
# Safety: this is a READ-ONLY operation. The cisco-vk-config-lint tool
# performs RESTCONF GETs only; it never writes to the device. The
# resulting JSON snapshot is the per-family observed state at the
# moment of the call.

set -euo pipefail

OUTPUT_DIR="${1:-./snapshot}"
DEVICE_NAME="${2:-cat9k-smoke}"
NAMESPACE="${3:-cisco-vk-smoke}"

if [[ -z "${CVK_CONFIG_LINT_PASSWORD:-}" ]]; then
  echo "ERROR: CVK_CONFIG_LINT_PASSWORD is not set." >&2
  echo "       Export the device RESTCONF password before running:" >&2
  echo "       export CVK_CONFIG_LINT_PASSWORD='<password>'" >&2
  exit 1
fi

mkdir -p "${OUTPUT_DIR}"

# Resolve device address from the CiscoDevice CR.
ADDR="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.address}' 2>/dev/null)"
if [[ -z "${ADDR}" ]]; then
  echo "ERROR: could not resolve spec.address for ciscodevice/${DEVICE_NAME} in ${NAMESPACE}" >&2
  exit 1
fi

# Resolve username (lint needs it explicitly).
USER="$(kubectl get ciscodevice "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.username}' 2>/dev/null || echo admin)"

echo "Capturing snapshot of ${DEVICE_NAME} (${ADDR}) under user ${USER} → ${OUTPUT_DIR}"

# Cluster-mode fetch: the tool reads the IOSXEConfig CRs from the
# cluster, performs RESTCONF GETs against the device for the families
# they manage, and emits the per-family observed state alongside any
# drift the resolver finds vs. intent. JSON output is mechanically
# diffable between runs.
cisco-vk-config-lint \
  --address "${ADDR}" \
  --device-name "${DEVICE_NAME}" \
  --username "${USER}" \
  --password-env CVK_CONFIG_LINT_PASSWORD \
  --insecure \
  --from-cluster \
  --namespace "${NAMESPACE}" \
  --mode full \
  --output json \
  ${LINT_EXTRA_FLAGS:-} \
  > "${OUTPUT_DIR}/lint.json"

# A more direct fetch via raw RESTCONF for the entire native tree —
# useful when you need ground-truth bytes the tool didn't ask for.
# Operators sometimes prefer this over the lint output for diff baselines.
echo "Capturing raw RESTCONF native tree → ${OUTPUT_DIR}/native.json"
curl --silent --insecure \
  --user "${USER}:${CVK_CONFIG_LINT_PASSWORD}" \
  --header "Accept: application/yang-data+json" \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native" \
  -o "${OUTPUT_DIR}/native.json"

# Quick summary so the operator can eyeball the result.
echo ""
echo "Snapshot complete:"
echo "  - ${OUTPUT_DIR}/lint.json   (per-family analysis + drift)"
echo "  - ${OUTPUT_DIR}/native.json (raw RESTCONF native tree)"
echo ""
echo "Hostname: $(jq -r '.["Cisco-IOS-XE-native:native"].hostname // "<unknown>"' < "${OUTPUT_DIR}/native.json" 2>/dev/null || echo unknown)"
echo "VLANs:    $(jq -r '.["Cisco-IOS-XE-native:native"].vlan["vlan-list"] | length' < "${OUTPUT_DIR}/native.json" 2>/dev/null || echo unknown)"
echo "Loopbacks:$(jq -r '.["Cisco-IOS-XE-native:native"].interface.Loopback | length' < "${OUTPUT_DIR}/native.json" 2>/dev/null || echo unknown)"
