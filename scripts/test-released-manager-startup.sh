#!/usr/bin/env bash
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Called only by the disposable kind qualification suite. Run the actual
# October manager entrypoint against the new native policies, not a copied
# validation function. It must exit before controller startup or ledger writes.
if [ "$#" -ne 7 ] || [[ "$1" != kind-* ]]; then
  echo "usage: $0 kind-CONTEXT NAMESPACE ADMISSION_PREFIX APP_ACCOUNT NETWORK_ACCOUNT LEASE_NAMESPACE MANAGER_ACCOUNT" >&2
  exit 2
fi
context="$1"
namespace="$2"
prefix="$3"
app_account="$4"
network_account="$5"
lease_namespace="$6"
manager_account="$7"
baseline=cf33e51c8ffc6d47acb313857665366d74eefe6c
root=$(git rev-parse --show-toplevel)
git -C "$root" cat-file -e "$baseline^{commit}"
umask 077
work=$(mktemp -d "${TMPDIR:-/tmp}/cvk-manager-startup.XXXXXX")
git -C "$root" archive "$baseline" | tar -x -C "$work"
mkdir "$work/image"
architecture=$(kubectl --context "$context" get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')
case "$architecture" in amd64|arm64) ;; *) echo "unsupported test architecture" >&2; exit 1;; esac
(cd "$work" && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" \
  go build -mod=readonly -trimpath -buildvcs=false -o "$work/image/october-manager" ./cmd/cisco-vk)
image="cvk-october-startup:probe-$$"
docker build --platform "linux/$architecture" -t "$image" \
  -f "$root/scripts/testdata/released-manager.Dockerfile" "$work/image" >"$work/image-build.log" 2>&1
kind load docker-image --name "${context#kind-}" "$image" >"$work/image-load.log" 2>&1
kubectl --context "$context" -n "$namespace" get \
  "configmap/${prefix}-topology-policy" "configmap/${prefix}-topology-ledger" \
  -o json >"$work/authority-before.json"

# Run with the rendered manager account and its projected namespace/token.
# A host process cannot exercise the unchanged in-cluster leader-election path.
pod="released-manager-startup-$$"
kubectl --context "$context" create -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${pod}
  namespace: ${namespace}
spec:
  serviceAccountName: ${manager_account}
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: manager
      image: ${image}
      imagePullPolicy: Never
      args:
        - manager
        - --enable-managed-topology
        - --leader-elect
        - --metrics-bind-address=0
        - --health-probe-bind-address=0
        - --topology-policy-namespace=${namespace}
        - --topology-policy-name=${prefix}-topology-policy
        - --app-hosting-service-account=${app_account}
        - --network-management-service-account=${network_account}
        - --network-management-access-mode=readWrite
      env:
        - {name: CONFIG_LEASE_NAMESPACE, value: "${lease_namespace}"}
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: [ALL]}
      resources:
        requests: {cpu: 100m, memory: 128Mi}
        limits: {cpu: "1", memory: 512Mi}
EOF
for _ in $(seq 1 60); do
  phase=$(kubectl --context "$context" -n "$namespace" get pod "$pod" -o jsonpath='{.status.phase}')
  if [ "$phase" = Failed ] || [ "$phase" = Succeeded ]; then break; fi
  sleep 1
done
kubectl --context "$context" -n "$namespace" get pod "$pod" -o json >"$work/manager-pod.json"
kubectl --context "$context" -n "$namespace" logs "$pod" >"$work/manager-startup.log" 2>&1 || true
if [ "$phase" != Failed ] || \
   ! grep -Fq 'managed topology native admission preflight' "$work/manager-startup.log" || \
   ! grep -Eq 'digest|validations|contract' "$work/manager-startup.log"; then
  printf 'FAIL: released manager did not reject newer authority; inspect %s\n' "$work/manager-startup.log" >&2
  exit 1
fi

kubectl --context "$context" -n "$namespace" get \
  "configmap/${prefix}-topology-policy" "configmap/${prefix}-topology-ledger" \
  -o json >"$work/authority-after.json"
cmp "$work/authority-before.json" "$work/authority-after.json"
printf 'PASS: actual October manager rejected newer authority before startup; policy/ledger unchanged; commit %s; evidence %s\n' "$baseline" "$work"
# The parent suite owns and removes the exact disposable cluster and this Pod.
