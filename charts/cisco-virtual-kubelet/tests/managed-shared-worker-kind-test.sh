#!/usr/bin/env bash

# End-to-end qualification for the managed shared-worker admission boundary.
#
# The test owns a newly-created, disposable Kubernetes 1.35 kind cluster. Pass
# an explicit name with `--cluster-name NAME` (or set
# CVK_SHARED_WORKER_KIND_CLUSTER); the script refuses to reuse an existing
# cluster and its EXIT trap deletes only the exact cluster it created.
#
# SECURITY BOUNDARY: managed device namespaces are locked operator namespaces.
# Native ValidatingAdmissionPolicy cannot make a general `edit` grant safe for
# a reserved worker: `edit` includes Pod CONNECT and Deployment /scale access.
# It does not include impersonation, but any separately granted authority to
# impersonate the manager identity is also outside this contract. This suite
# deliberately asserts those facts; it does not treat CONNECT, /scale, or
# manager impersonation as admission-protected operations.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster_name="${CVK_SHARED_WORKER_KIND_CLUSTER:-cvk-shared-worker-${PPID:-0}-$$}"
kind_node_image="${CVK_SHARED_WORKER_KIND_IMAGE:-kindest/node:v1.35.0}"

if [ "$#" -gt 0 ]; then
  if [ "$#" -ne 2 ] || [ "$1" != "--cluster-name" ]; then
    echo "usage: $0 [--cluster-name NAME]" >&2
    exit 2
  fi
  cluster_name="$2"
fi

if [[ ! "$cluster_name" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] ||
   [ "${#cluster_name}" -gt 63 ]; then
  printf 'invalid kind cluster name %q\n' "$cluster_name" >&2
  exit 2
fi

for required_command in docker helm kind kubectl; do
  if ! command -v "$required_command" >/dev/null 2>&1; then
    printf 'required command not found: %s\n' "$required_command" >&2
    exit 1
  fi
done

if kind get clusters 2>/dev/null | grep -Fxq "$cluster_name"; then
  printf 'refusing to reuse existing kind cluster %q\n' "$cluster_name" >&2
  exit 1
fi

scratch_dir="$(mktemp -d)"
cluster_owned=false
original_context="$(kubectl config current-context 2>/dev/null || true)"

cleanup() {
  local status=$?

  trap - EXIT INT TERM
  if [ "$cluster_owned" = true ]; then
    kind delete cluster --name "$cluster_name" >/dev/null 2>&1 || true
  fi
  if [ -n "$original_context" ] &&
     kubectl config get-contexts "$original_context" --no-headers >/dev/null 2>&1; then
    kubectl config use-context "$original_context" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$scratch_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

# Mark ownership before creation so a partially-created cluster is still
# removed. The pre-existence check above ensures this name cannot identify a
# caller-owned cluster.
cluster_owned=true
kind create cluster --name "$cluster_name" --image "$kind_node_image" --wait 120s

context="kind-${cluster_name}"
server_version="$(kubectl --context "$context" get --raw=/version | \
  sed -n 's/.*"gitVersion"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
if [[ "$server_version" != v1.35.* ]]; then
  printf 'expected Kubernetes v1.35.x, got %s\n' "$server_version" >&2
  exit 1
fi

release_name="cvk-shared-worker-it"
system_namespace="cvk-shared-worker-system"
worker_namespace="cvk-shared-worker-test"
controller_service_account="cvk-shared-worker-manager"
app_service_account="cvk-app-hosting"
network_service_account="cvk-network-management"
manager_username="system:serviceaccount:${system_namespace}:${controller_service_account}"
tenant_service_account="admission-probe"
tenant_username="system:serviceaccount:${worker_namespace}:${tenant_service_account}"
edit_service_account="edit-boundary-probe"
edit_username="system:serviceaccount:${worker_namespace}:${edit_service_account}"
cached_image="registry.k8s.io/pause:3.10"

kubectl --context "$context" apply -f "$chart_dir/crds" >/dev/null
kubectl --context "$context" create namespace "$system_namespace" >/dev/null
kubectl --context "$context" create namespace "$worker_namespace" >/dev/null

helm_args=(
  template "$release_name" "$chart_dir"
  --namespace "$system_namespace"
  --kube-version 1.35.0
  --set topology.enabled=true
  --set controller.leaderElect=true
  --set rbac.profile=strict
  --set "serviceAccount.controllerName=${controller_service_account}"
  --set "topology.workerAccounts.appHosting.serviceAccountName=${app_service_account}"
  --set "topology.workerAccounts.networkManagement.serviceAccountName=${network_service_account}"
)

rbac_manifest="$scratch_dir/rbac.yaml"
config_manifest="$scratch_dir/config.yaml"
admission_manifest="$scratch_dir/admission.yaml"

helm "${helm_args[@]}" \
  --show-only templates/role.yaml \
  --show-only templates/controller-rbac.yaml \
  --show-only templates/topology-rbac.yaml >"$rbac_manifest"
helm "${helm_args[@]}" \
  --show-only templates/topology-configmaps.yaml >"$config_manifest"
helm "${helm_args[@]}" \
  --show-only templates/topology-admission.yaml \
  --show-only templates/topology-worker-admission.yaml >"$admission_manifest"

kubectl --context "$context" apply -f "$rbac_manifest" >/dev/null
kubectl --context "$context" apply -f "$config_manifest" >/dev/null
kubectl --context "$context" apply -f "$admission_manifest" >/dev/null

expected_policy_count="$(awk '$0 == "kind: ValidatingAdmissionPolicy" { count++ } END { print count + 0 }' \
  "$admission_manifest")"
expected_binding_count="$(awk '$0 == "kind: ValidatingAdmissionPolicyBinding" { count++ } END { print count + 0 }' \
  "$admission_manifest")"
actual_policy_count="$(kubectl --context "$context" get validatingadmissionpolicies \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}' | wc -w | tr -d ' ')"
actual_binding_count="$(kubectl --context "$context" get validatingadmissionpolicybindings \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}' | wc -w | tr -d ' ')"
test "$expected_policy_count" -gt 0
test "$actual_policy_count" -eq "$expected_policy_count"
test "$actual_binding_count" -eq "$expected_binding_count"

# A policy is usable only after the API server has compiled the current
# generation. Any type-check warning is a hard test failure.
for policy in $(kubectl --context "$context" get validatingadmissionpolicies \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}'); do
  observed=""
  for _ in $(seq 1 60); do
    generation="$(kubectl --context "$context" get validatingadmissionpolicy "$policy" \
      -o jsonpath='{.metadata.generation}')"
    observed="$(kubectl --context "$context" get validatingadmissionpolicy "$policy" \
      -o jsonpath='{.status.observedGeneration}')"
    if [ -n "$observed" ] && [ "$observed" = "$generation" ]; then
      break
    fi
    sleep 1
  done
  if [ -z "$observed" ] || [ "$observed" != "$generation" ]; then
    printf 'policy %s was not observed at generation %s\n' \
      "$policy" "$generation" >&2
    exit 1
  fi
  warnings="$(kubectl --context "$context" get validatingadmissionpolicy "$policy" \
    -o jsonpath='{range .status.typeChecking.expressionWarnings[*]}{.fieldRef}{": "}{.warning}{"\n"}{end}')"
  if [ -n "$warnings" ]; then
    printf 'policy %s has type-check warnings:\n%s\n' "$policy" "$warnings" >&2
    exit 1
  fi
done

# Give the non-manager probe exactly the verbs needed to reach admission. A
# bare RBAC rejection would not prove the fail-closed CEL rules.
kubectl --context "$context" create serviceaccount "$tenant_service_account" \
  --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" create role admission-probe \
  --namespace "$worker_namespace" \
  --verb=get,list,watch,create,update,patch,delete \
  --resource=serviceaccounts,serviceaccounts/token,secrets,pods,pods/status,deployments.apps >/dev/null
kubectl --context "$context" create rolebinding admission-probe \
  --namespace "$worker_namespace" \
  --role=admission-probe \
  --serviceaccount="${worker_namespace}:${tenant_service_account}" >/dev/null

expect_denied() {
  local description="$1"
  local expected_message="$2"
  local output
  local status

  shift 2
  set +e
  output="$("$@" 2>&1)"
  status=$?
  set -e
  if [ "$status" -eq 0 ]; then
    printf 'expected denial: %s\n' "$description" >&2
    exit 1
  fi
  if [[ "$output" != *"$expected_message"* ]]; then
    printf 'wrong denial for %s; expected %q, got:\n%s\n' \
      "$description" "$expected_message" "$output" >&2
    exit 1
  fi
}

# Wait for admission binding activation by requiring the exact policy message;
# RBAC already authorizes this request.
activation_output=""
for _ in $(seq 1 30); do
  set +e
  activation_output="$(kubectl --context "$context" --as="$tenant_username" \
    create serviceaccount "$app_service_account" \
    --namespace "$worker_namespace" 2>&1)"
  activation_status=$?
  set -e
  if [ "$activation_status" -ne 0 ] &&
     [[ "$activation_output" == *"only the topology manager may create, update, or delete a reserved worker ServiceAccount"* ]]; then
    break
  fi
  sleep 1
done
if [ "$activation_status" -eq 0 ] ||
   [[ "$activation_output" != *"only the topology manager may create, update, or delete a reserved worker ServiceAccount"* ]]; then
  printf 'reserved ServiceAccount admission did not become active:\n%s\n' \
    "$activation_output" >&2
  exit 1
fi

# An ordinary workload that omits serviceAccountName remains unaffected.
kubectl --context "$context" --as="$tenant_username" create deployment ordinary \
  --namespace "$worker_namespace" --image="$cached_image" >/dev/null
kubectl --context "$context" --as="$tenant_username" patch deployment ordinary \
  --namespace "$worker_namespace" --type=merge \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"pause","image":"registry.k8s.io/pause:3.10","imagePullPolicy":"Never"}]}}}}' >/dev/null
kubectl --context "$context" rollout status deployment/ordinary \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
test -z "$(kubectl --context "$context" get deployment ordinary \
  --namespace "$worker_namespace" \
  -o jsonpath='{.spec.template.spec.serviceAccountName}')"

# Only the manager identity can establish either reserved account.
kubectl --context "$context" --as="$manager_username" create serviceaccount \
  "$app_service_account" --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" --as="$manager_username" create serviceaccount \
  "$network_service_account" --namespace "$worker_namespace" >/dev/null

create_reserved_deployment() {
  local as_user="$1"
  local deployment_name="$2"

  kubectl --context "$context" --as="$as_user" create -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${deployment_name}
  namespace: ${worker_namespace}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${deployment_name}
  template:
    metadata:
      labels:
        app: ${deployment_name}
    spec:
      serviceAccountName: ${app_service_account}
      containers:
        - name: pause
          image: ${cached_image}
          imagePullPolicy: Never
EOF
}

create_legacy_token_secret() {
  kubectl --context "$context" --as="$tenant_username" create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: forbidden-legacy-token
  namespace: ${worker_namespace}
  annotations:
    kubernetes.io/service-account.name: ${app_service_account}
type: kubernetes.io/service-account-token
EOF
}

expect_denied "non-manager reserved ServiceAccount update" \
  "only the topology manager may create, update, or delete a reserved worker ServiceAccount" \
  kubectl --context "$context" --as="$tenant_username" label serviceaccount \
  "$app_service_account" --namespace "$worker_namespace" attacker=true
expect_denied "non-manager reserved Deployment" \
  "only the topology manager may create or update a Deployment that uses a reserved worker account" \
  create_reserved_deployment "$tenant_username" forbidden-worker

create_reserved_deployment "$manager_username" reserved-worker >/dev/null
kubectl --context "$context" rollout status deployment/reserved-worker \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
kubectl --context "$context" wait pod \
  --namespace "$worker_namespace" --selector=app=reserved-worker \
  --for=condition=Ready --timeout=90s >/dev/null

reserved_replicaset="$(kubectl --context "$context" get replicaset \
  --namespace "$worker_namespace" --selector=app=reserved-worker \
  -o jsonpath='{.items[0].metadata.name}')"
reserved_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector=app=reserved-worker \
  -o jsonpath='{.items[0].metadata.name}')"
reserved_pod_uid="$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"

test -n "$reserved_replicaset"
test -n "$reserved_pod"
test -n "$reserved_pod_uid"
test "$(kubectl --context "$context" get replicaset "$reserved_replicaset" \
  --namespace "$worker_namespace" \
  -o jsonpath='{.metadata.ownerReferences[0].kind}/{.metadata.ownerReferences[0].name}')" = \
  "Deployment/reserved-worker"
test "$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" \
  -o jsonpath='{.metadata.ownerReferences[0].kind}/{.metadata.ownerReferences[0].name}')" = \
  "ReplicaSet/${reserved_replicaset}"
test "$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.status.phase}')" = "Running"
test "$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.spec.serviceAccountName}')" = \
  "$app_service_account"
projected_token_paths="$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" \
  -o jsonpath='{range .spec.volumes[*].projected.sources[*]}{.serviceAccountToken.path}{"\n"}{end}')"
if [[ "$projected_token_paths" != *"token"* ]]; then
  echo "reserved worker Pod has no projected ServiceAccount token source" >&2
  exit 1
fi

expect_denied "manual unbound TokenRequest" \
  "a reserved worker token must be requested by a kubelet and bound to one exact Pod UID" \
  kubectl --context "$context" --as="$tenant_username" create token \
  "$app_service_account" --namespace "$worker_namespace" --duration=10m
expect_denied "manual Pod-bound TokenRequest" \
  "a reserved worker token must be requested by a kubelet and bound to one exact Pod UID" \
  kubectl --context "$context" --as="$tenant_username" create token \
  "$app_service_account" --namespace "$worker_namespace" --duration=10m \
  --bound-object-kind=Pod --bound-object-name="$reserved_pod" \
  --bound-object-uid="$reserved_pod_uid"
expect_denied "legacy ServiceAccount-token Secret" \
  "legacy token Secrets are forbidden for reserved worker ServiceAccounts" \
  create_legacy_token_secret

ordinary_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector=app=ordinary \
  -o jsonpath='{.items[0].metadata.name}')"
binding_patch="{\"metadata\":{\"annotations\":{\"topology.cisco.vk/app-worker-username\":\"system:serviceaccount:${worker_namespace}:${app_service_account}\",\"topology.cisco.vk/app-worker-pod-name\":\"${reserved_pod}\",\"topology.cisco.vk/app-worker-pod-uid\":\"${reserved_pod_uid}\"}}}"
expect_denied "non-manager Pod-status worker-binding mutation" \
  "only the topology manager may add, rotate, or remove a workload worker binding" \
  kubectl --context "$context" --as="$tenant_username" patch pod "$ordinary_pod" \
  --namespace "$worker_namespace" --subresource=status --type=merge \
  -p "$binding_patch"

# Assert the intentional locked-namespace boundary. These yes/no answers are
# documentation-as-test: edit cannot impersonate the manager by itself, but it
# can CONNECT to Pods and mutate Deployment scale. Operators must not bind edit
# (or equivalent CONNECT/scale privileges) in a managed device namespace.
kubectl --context "$context" create serviceaccount "$edit_service_account" \
  --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" create rolebinding edit-boundary-probe \
  --namespace "$worker_namespace" --clusterrole=edit \
  --serviceaccount="${worker_namespace}:${edit_service_account}" >/dev/null
test "$(kubectl --context "$context" auth can-i impersonate serviceaccounts \
  --as="$edit_username" --namespace="$system_namespace")" = "no"
test "$(kubectl --context "$context" auth can-i create pods/exec \
  --as="$edit_username" --namespace="$worker_namespace")" = "yes"
test "$(kubectl --context "$context" auth can-i update deployments.apps/scale \
  --as="$edit_username" --namespace="$worker_namespace")" = "yes"
echo "locked-namespace boundary confirmed: edit retains CONNECT and /scale; manager impersonation remains out of scope"

# Namespace teardown exercises the DELETE-collection path while reserved
# worker objects still exist. A missing request.name must not cause the
# fail-closed policies to strand namespace finalization.
kubectl --context "$context" delete namespace "$worker_namespace" \
  --wait=true --timeout=90s >/dev/null
if kubectl --context "$context" get namespace "$worker_namespace" >/dev/null 2>&1; then
  echo "worker namespace still exists after collection deletion" >&2
  exit 1
fi

echo "managed shared-worker admission integration test passed on ${server_version}"
