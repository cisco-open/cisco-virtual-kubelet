#!/usr/bin/env bash
# Test 05 rollback. Removes the test annotation from the Secret,
# which causes one more Deployment roll to settle the pod template
# back to its pre-test shape.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"

kubectl annotate secret cat9k-creds -n "${NAMESPACE}" \
  cisco.vk/release-blocker-test-05- || true

echo "Annotation removed; the Deployment will roll once more to converge."
echo "Watch with: kubectl get pod -n ${NAMESPACE} -l app=cat9k-smoke -w"
echo "(stop with Ctrl+C once the new pod is Ready and old one is gone)"
