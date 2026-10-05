#!/usr/bin/env bash
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Called only by the disposable kind qualification suite. Run the actual
# October, lab-baseline and candidate manager entrypoints, not copied validators.
# Partial contracts must fail before startup/ledger writes; the restored
# candidate must acquire leadership and restart with retained authority.
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
# This helper changes test admission bindings. Never accept a user cluster,
# devices or campaigns even if a caller gives its context a kind-like name.
test "$(docker inspect --format '{{ index .Config.Labels "io.x-k8s.kind.cluster" }}' "${context#kind-}-control-plane")" = "${context#kind-}"
test "$(kubectl --context "$context" get nodes -o jsonpath='{.items[0].metadata.name}')" = "${context#kind-}-control-plane"
for resource in ciscodevices.cisco.vk iosxesoftwareupgrades.ops.cisco.vk iosxesoftwarerollouts.ops.cisco.vk; do
  test -z "$(kubectl --context "$context" get "$resource" -A -o name)"
done
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
# Run with the rendered manager account and its projected namespace/token.
# A host process cannot exercise the unchanged in-cluster leader-election path.
run_probe() {
local name="$1" image="$2" expected="$3" denial="${4:-}"
local pod="manager-startup-${name}-$$" phase="" ready=""
kubectl --context "$context" -n "$namespace" get \
  "configmap/${prefix}-topology-policy" "configmap/${prefix}-topology-ledger" \
  -o json >"$work/${name}-authority-before.json"
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
        - --health-probe-bind-address=:8081
        - --topology-policy-namespace=${namespace}
        - --topology-policy-name=${prefix}-topology-policy
        - --app-hosting-service-account=${app_account}
        - --network-management-service-account=${network_account}
        - --network-management-access-mode=readWrite
      env:
        - {name: CONFIG_LEASE_NAMESPACE, value: "${lease_namespace}"}
        - {name: CISCO_VK_ENABLE_IOSXE_SOFTWARE_UPGRADE, value: "true"}
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: [ALL]}
      resources:
        requests: {cpu: 100m, memory: 128Mi}
        limits: {cpu: "1", memory: 512Mi}
      readinessProbe:
        httpGet: {path: /readyz, port: 8081}
        periodSeconds: 1
EOF
for _ in $(seq 1 90); do
  phase=$(kubectl --context "$context" -n "$namespace" get pod "$pod" -o jsonpath='{.status.phase}')
  if [ "$phase" = Failed ] || [ "$phase" = Succeeded ]; then break; fi
  if [ "$expected" = Running ]; then
    ready=$(kubectl --context "$context" -n "$namespace" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].ready}')
    kubectl --context "$context" -n "$namespace" logs "$pod" >"$work/${name}.log" 2>&1 || true
    if [ "$ready" = true ]; then
      kubectl --context "$context" -n "$namespace" get lease ciscodevice.cisco.vk -o json >"$work/${name}-leader.json"
      if jq -e --arg pod "$pod" '.spec.holderIdentity | startswith($pod + "_")' "$work/${name}-leader.json" >/dev/null; then break; fi
    fi
  fi
  sleep 1
done
kubectl --context "$context" -n "$namespace" get pod "$pod" -o json >"$work/${name}-pod.json"
kubectl --context "$context" -n "$namespace" logs "$pod" >"$work/${name}.log" 2>&1 || true
kubectl --context "$context" -n "$namespace" get \
  "configmap/${prefix}-topology-policy" "configmap/${prefix}-topology-ledger" \
  -o json >"$work/${name}-authority-after.json"
if [ "$expected" = Failed ]; then
  if [ "$phase" != Failed ] || ! grep -Fq 'managed topology native admission preflight' "$work/${name}.log" ||
     [ -z "$denial" ] || ! grep -Fq "$denial" "$work/${name}.log" ||
     grep -Fq 'starting manager' "$work/${name}.log"; then
    printf 'FAIL: %s did not stop at admission preflight; inspect %s\n' "$name" "$work/${name}.log" >&2
    return 1
  fi
  cmp "$work/${name}-authority-before.json" "$work/${name}-authority-after.json"
else
  if [ "$phase" != Running ] || [ "$ready" != true ] ||
     ! jq -e --arg pod "$pod" '.spec.holderIdentity | startswith($pod + "_")' "$work/${name}-leader.json" >/dev/null ||
     ! grep -Fq 'starting manager' "$work/${name}.log"; then
    printf 'FAIL: %s did not start and acquire leadership; inspect %s\n' "$name" "$work/${name}.log" >&2
    return 1
  fi
  # Observe a real manager with no device authority, then stop it gracefully.
  # Retain its lease/history; the next Pod must obtain leadership normally.
  test "$(jq -r '.status.containerStatuses[0].restartCount' "$work/${name}-pod.json")" = 0
  kubectl --context "$context" -n "$namespace" delete pod "$pod" --wait=true --timeout=60s >/dev/null
fi
printf 'PASS: manager startup boundary %s expected=%s; evidence %s\n' "$name" "$expected" "$work"
}

run_probe october-new-contract "$image" Failed 'variables differ from the compiled contract'

# Qualify the exact currently deployed lab runtime as well as the release.
# It already knows staged activation, but predates the new recovery contract.
lab_baseline=6f3686e98dad0ca4f30f8f21453d57683170537e
git -C "$root" cat-file -e "$lab_baseline^{commit}"
mkdir "$work/lab-source"
git -C "$root" archive "$lab_baseline" | tar -x -C "$work/lab-source"
(cd "$work/lab-source" && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" \
  go build -mod=readonly -trimpath -buildvcs=false -o "$work/image/october-manager" ./cmd/cisco-vk)
lab_image="cvk-lab-baseline-startup:probe-$$"
docker build --platform "linux/$architecture" -t "$lab_image" \
  -f "$root/scripts/testdata/released-manager.Dockerfile" "$work/image" >"$work/lab-image-build.log" 2>&1
kind load docker-image --name "${context#kind-}" "$lab_image" >"$work/lab-image-load.log" 2>&1
run_probe lab-new-contract "$lab_image" Failed 'has 9 validations, want exactly 7'

# Older staged managers either drop caSecretRef or project its mutable Secret
# directly. Both must stop at the exact current native public-CA contract.
pre_ca_baseline=dfe02ae43bd9ce721ab481bde05d970fc6fe5100
direct_ca_baseline=dbdbb4e7cbc4ce258fa3275b620ac01a2c409556
for stage in pre-ca direct-ca; do
  if [ "$stage" = pre-ca ]; then
    revision="$pre_ca_baseline"
    denial='has 2 validations, want exactly 1'
  else
    revision="$direct_ca_baseline"
    denial='want sha256:5d1f9e89b84c9eda661a652fe78f9beda28e07f7f2e1a4952c212443f34f7270'
  fi
  git -C "$root" cat-file -e "$revision^{commit}"
  mkdir "$work/$stage-source" "$work/$stage-image"
  git -C "$root" archive "$revision" | tar -x -C "$work/$stage-source"
  (cd "$work/$stage-source" && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" \
    go build -mod=readonly -trimpath -buildvcs=false -o "$work/$stage-image/october-manager" ./cmd/cisco-vk)
  historical_image="cvk-$stage-startup:probe-$$"
  docker build --platform "linux/$architecture" -t "$historical_image" \
    -f "$root/scripts/testdata/released-manager.Dockerfile" "$work/$stage-image" >"$work/$stage-image-build.log" 2>&1
  kind load docker-image --name "${context#kind-}" "$historical_image" >"$work/$stage-image-load.log" 2>&1
  run_probe "$stage-new-contract" "$historical_image" Failed "$denial"
done

# Reuse the same isolated binary container layout for the exact current source.
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" \
  go build -mod=readonly -trimpath -buildvcs=false -o "$work/image/october-manager" ./cmd/cisco-vk)
current_image="cvk-candidate-startup:probe-$$"
docker build --platform "linux/$architecture" -t "$current_image" \
  -f "$root/scripts/testdata/released-manager.Dockerfile" "$work/image" >"$work/current-image-build.log" 2>&1
kind load docker-image --name "${context#kind-}" "$current_image" >"$work/current-image-load.log" 2>&1

binding="${prefix}-managed-upgrade-leaf"
kubectl --context "$context" get validatingadmissionpolicybinding "$binding" -o json | \
  jq 'del(.metadata.uid,.metadata.resourceVersion,.metadata.creationTimestamp,.metadata.managedFields)' >"$work/binding.json"
kubectl --context "$context" patch validatingadmissionpolicybinding "$binding" --type=merge \
  -p '{"spec":{"validationActions":["Warn"]}}' >/dev/null
run_probe candidate-warn-only "$current_image" Failed 'must enforce only Deny'
kubectl --context "$context" delete validatingadmissionpolicybinding "$binding" --wait=true >/dev/null
run_probe candidate-missing-binding "$current_image" Failed 'get required ValidatingAdmissionPolicyBinding'
kubectl --context "$context" create -f "$work/binding.json" >/dev/null

wait_policy_compiled() {
  for _ in $(seq 1 60); do
    kubectl --context "$context" get validatingadmissionpolicy "$policy" -o json >"$work/current-policy.json"
    if jq -e '.metadata.generation == .status.observedGeneration and ((.status.typeChecking.expressionWarnings // []) | length == 0)' "$work/current-policy.json" >/dev/null; then return; fi
    sleep 1
  done
  printf 'FAIL: policy generation did not compile cleanly: %s\n' "$policy" >&2
  return 1
}

policy="${prefix}-managed-upgrade-leaf"
kubectl --context "$context" get validatingadmissionpolicy "$policy" -o json | jq '{spec:.spec}' >"$work/policy-spec.json"
kubectl --context "$context" patch validatingadmissionpolicy "$policy" --type=json \
  -p '[{"op":"replace","path":"/spec/validations/0/message","value":"incomplete migration test contract"}]' >/dev/null
wait_policy_compiled
run_probe candidate-partial-policy "$current_image" Failed 'compiled contract digest is'
kubectl --context "$context" patch validatingadmissionpolicy "$policy" --type=merge --patch-file "$work/policy-spec.json" >/dev/null
wait_policy_compiled

# Reproduce the durable midpoint of bootstrap: ledger initialized, policy not
# yet bound. Use the exact manager identity through the real native policies;
# this fixture is not an authorization test for impersonation itself.
kubectl --context "$context" -n "$namespace" get configmap "${prefix}-topology-policy" -o json | \
  jq -e '.metadata.annotations["topology.cisco.vk/ledger-uid"] == null' >/dev/null
kubectl --context "$context" -n "$namespace" get configmap "${prefix}-topology-ledger" -o json | \
  jq '{metadata:{resourceVersion:.metadata.resourceVersion}, data:{"ledger.json":({version:"v1", uid:.metadata.uid, reservations:{}} | tojson)}}' >"$work/interrupted-ledger.json"
kubectl --context "$context" -n "$namespace" --as="system:serviceaccount:${namespace}:${manager_account}" \
  patch configmap "${prefix}-topology-ledger" --type=merge --patch-file "$work/interrupted-ledger.json" >/dev/null
kubectl --context "$context" -n "$namespace" get configmap "${prefix}-topology-ledger" -o json >"$work/initialized-ledger.json"
run_probe candidate-restored "$current_image" Running
kubectl --context "$context" -n "$namespace" get configmap "${prefix}-topology-ledger" -o json >"$work/resumed-ledger.json"
cmp "$work/initialized-ledger.json" "$work/resumed-ledger.json"
run_probe candidate-restart "$current_image" Running
# Bootstrap can bind the empty ledger once. Restart may not replace its identity,
# policy binding or data. There are no targets and no reservation may appear.
cmp "$work/candidate-restart-authority-before.json" "$work/candidate-restart-authority-after.json"
jq -e '[.items[] | select(.data["ledger.json"] != null) | .data["ledger.json"] | fromjson | .reservations | length] == [0]' \
  "$work/candidate-restart-authority-after.json" >/dev/null
printf 'PASS: released=%s lab=%s pre-ca=%s direct-ca=%s candidate=%s startup/interrupted-policy/restart matrix; evidence %s\n' \
  "$baseline" "$lab_baseline" "$pre_ca_baseline" "$direct_ca_baseline" "$(git -C "$root" rev-parse HEAD)" "$work"
# The parent suite owns and removes the exact disposable cluster and this Pod.
