#!/usr/bin/env bash
# Test 02 rollback. The engine should not have touched the device,
# so the only thing to undo is the test CR + template.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cisco-vk-smoke}"

kubectl delete -f 00-apply.yaml --ignore-not-found
echo "rollback complete"
