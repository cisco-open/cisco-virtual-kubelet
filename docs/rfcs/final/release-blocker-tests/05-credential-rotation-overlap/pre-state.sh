#!/usr/bin/env bash
# Test 05 pre-state. Records the pre-rotation pod identity, lease
# holders, and Deployment annotation. The post-test verify diffs
# against these to confirm a clean rollover happened.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"
DEVICE_NAME="${DEVICE_NAME:-cat9k-smoke}"

echo "secret-resource-version:"
kubectl get secret cat9k-creds -n "${NAMESPACE}" \
  -o jsonpath='{.metadata.resourceVersion}'
echo

echo "deployment-credential-annotation:"
kubectl get deployment "${DEVICE_NAME}-vk" -n "${NAMESPACE}" \
  -o jsonpath='{.spec.template.metadata.annotations.cisco\.vk/credential-resource-version}' || true
echo

echo "current-pod:"
kubectl get pod -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${DEVICE_NAME}" \
  -o jsonpath='{.items[0].metadata.name}'
echo

echo "current-pod-uid:"
kubectl get pod -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${DEVICE_NAME}" \
  -o jsonpath='{.items[0].metadata.uid}'
echo

echo "lease-holders:"
kubectl get lease -n "${NAMESPACE}" -o json 2>/dev/null \
  | jq -r '.items[] | select(.metadata.name | startswith("cvk-")) | "\(.metadata.name)\t\(.spec.holderIdentity)"' || echo "(none)"

echo "iosxeconfig-phase:"
kubectl get iosxeconfig "${DEVICE_NAME}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.phase}'
echo
