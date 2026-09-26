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
generated_service_account=""
manager_username="system:serviceaccount:${system_namespace}:${controller_service_account}"
app_username="system:serviceaccount:${worker_namespace}:${app_service_account}"
network_username="system:serviceaccount:${worker_namespace}:${network_service_account}"
generated_username=""
tenant_service_account="admission-probe"
tenant_username="system:serviceaccount:${worker_namespace}:${tenant_service_account}"
edit_service_account="edit-boundary-probe"
edit_username="system:serviceaccount:${worker_namespace}:${edit_service_account}"
cached_image="registry.k8s.io/pause:3.10"

kubectl --context "$context" apply -f "$chart_dir/crds" >/dev/null
kubectl --context "$context" create namespace "$system_namespace" >/dev/null
kubectl --context "$context" create namespace "$worker_namespace" >/dev/null

# Preserve one malformed pre-policy object to prove that the exact native
# namespace-controller collection-delete path cannot strand historical data.
# New partial bindings are rejected once admission is installed.
kubectl --context "$context" create -f - >/dev/null <<EOF
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: namespace-cleanup-partial-binding
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
spec:
  deviceRef:
    name: historical-device
  commands:
    - show version
---
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: root-kcm-cleanup-partial-binding
  namespace: ${worker_namespace}
  labels:
    cvk-test-scope: root-kcm-namespace-cleanup
  annotations:
    topology.cisco.vk/managed: "true"
spec:
  deviceRef:
    name: historical-device
  commands:
    - show version
EOF

helm_args=(
  template "$release_name" "$chart_dir"
  --namespace "$system_namespace"
  --kube-version 1.35.0
  --set topology.enabled=true
  --set gnoi.enableSoftwareUpgrade=true
  --set topology.workerAccounts.networkManagement.accessMode=readWrite
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

# Keep both upstream kube-controller-manager credential modes in the rendered
# contract: the dedicated GC ServiceAccount used with per-controller
# credentials, and the exact authenticated root controller-manager identity
# used when --use-service-account-credentials=false. The latter intentionally
# does not trust a broad controller group.
grep -Fq "system:serviceaccount:kube-system:generic-garbage-collector" \
  "$admission_manifest"
grep -Fq "request.userInfo.username == 'system:kube-controller-manager'" \
  "$admission_manifest"
grep -Fq "request.userInfo.groups.exists(g, g == 'system:authenticated')" \
  "$admission_manifest"
grep -Fq "has(request.options.preconditions.uid)" "$admission_manifest"

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

# Shared and generated ServiceAccount reservation deliberately have distinct
# API-server identities. Their immutable UIDs and spec generations jointly
# define the first-upgrade identity epoch used to invalidate older tokens.
admission_prefix="${release_name}-cisco-virtual-kubelet"
shared_account_policy="${admission_prefix}-shared-worker-serviceaccount"
generated_account_policy="${admission_prefix}-generated-worker-serviceaccount"
shared_account_policy_uid="$(kubectl --context "$context" get \
  validatingadmissionpolicy "$shared_account_policy" -o jsonpath='{.metadata.uid}')"
generated_account_policy_uid="$(kubectl --context "$context" get \
  validatingadmissionpolicy "$generated_account_policy" -o jsonpath='{.metadata.uid}')"
shared_account_binding_uid="$(kubectl --context "$context" get \
  validatingadmissionpolicybinding "$shared_account_policy" -o jsonpath='{.metadata.uid}')"
generated_account_binding_uid="$(kubectl --context "$context" get \
  validatingadmissionpolicybinding "$generated_account_policy" -o jsonpath='{.metadata.uid}')"
test -n "$shared_account_policy_uid"
test -n "$generated_account_policy_uid"
test -n "$shared_account_binding_uid"
test -n "$generated_account_binding_uid"
test "$shared_account_policy_uid" != "$generated_account_policy_uid"
test "$shared_account_binding_uid" != "$generated_account_binding_uid"

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
  --verb=get,list,watch,create,update,patch,delete,deletecollection \
  --resource=configmaps,serviceaccounts,serviceaccounts/token,secrets,pods,pods/status,deployments.apps,deployments.apps/status,replicasets.apps,replicasets.apps/status,deviceoperations.ops.cisco.vk,iosxediagnostics.config.cisco.vk,iosxeconfigrevisions.config.cisco.vk >/dev/null
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

delete_collection_raw_as() {
  local username="$1"
  local path="$2"

  kubectl --context "$context" --as="$username" delete --raw="$path"
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

# DELETECOLLECTION carries each old object but no request.name. Prove the two
# retained topology safety ConfigMaps resolve identity from oldObject and that
# a broad namespaced deleter cannot remove either one in bulk.
kubectl --context "$context" create role topology-configmap-delete-probe \
  --namespace "$system_namespace" --verb=get,list,delete,deletecollection \
  --resource=configmaps >/dev/null
kubectl --context "$context" create rolebinding topology-configmap-delete-probe \
  --namespace "$system_namespace" --role=topology-configmap-delete-probe \
  --serviceaccount="${worker_namespace}:${tenant_service_account}" >/dev/null
topology_policy_configmap="$(kubectl --context "$context" get configmap \
  --namespace "$system_namespace" --selector=app.kubernetes.io/component=topology-policy \
  -o jsonpath='{.items[0].metadata.name}')"
topology_ledger_configmap="$(kubectl --context "$context" get configmap \
  --namespace "$system_namespace" --selector=app.kubernetes.io/component=topology-ledger \
  -o jsonpath='{.items[0].metadata.name}')"
test -n "$topology_policy_configmap"
test -n "$topology_ledger_configmap"
expect_denied "tenant topology policy collection delete" \
  "policy content changes require the custom topology permission; the manager may only bind the ledger UID" \
  delete_collection_raw_as "$tenant_username" \
  "/api/v1/namespaces/${system_namespace}/configmaps?labelSelector=app.kubernetes.io%2Fcomponent%3Dtopology-policy"
expect_denied "tenant topology ledger collection delete" \
  "ledger writes are manager-only; break-glass mutation requires manage-ledger permission" \
  delete_collection_raw_as "$tenant_username" \
  "/api/v1/namespaces/${system_namespace}/configmaps?labelSelector=app.kubernetes.io%2Fcomponent%3Dtopology-ledger"
kubectl --context "$context" get configmap "$topology_policy_configmap" \
  --namespace "$system_namespace" >/dev/null
kubectl --context "$context" get configmap "$topology_ledger_configmap" \
  --namespace "$system_namespace" >/dev/null

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
kubectl --context "$context" --as="$manager_username" label serviceaccount \
  "$app_service_account" --namespace "$worker_namespace" \
  cvk-test-scope=shared-reserved >/dev/null
expect_denied "tenant shared ServiceAccount collection delete" \
  "only the topology manager may create, update, or delete a reserved worker ServiceAccount" \
  delete_collection_raw_as "$tenant_username" \
  "/api/v1/namespaces/${worker_namespace}/serviceaccounts?labelSelector=cvk-test-scope%3Dshared-reserved"
kubectl --context "$context" get serviceaccount "$app_service_account" \
  --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" create clusterrolebinding app-profile-probe \
  --clusterrole=cisco-virtual-kubelet-app-hosting-read-write \
  --serviceaccount="${worker_namespace}:${app_service_account}" >/dev/null
test "$(kubectl --context "$context" auth can-i patch pods --subresource=status \
  --namespace "$worker_namespace" --as="$app_username")" = "yes"

# The result policy must not capture ordinary ConfigMaps merely because the
# topology manager is the caller. Manager-created result bindings are still
# selected by their protected annotations, while unrelated controller state
# remains governed by its own admission contract.
kubectl --context "$context" --as="$manager_username" create configmap \
  unrelated-manager-state --namespace "$system_namespace" \
  --from-literal=state=initial >/dev/null
kubectl --context "$context" --as="$manager_username" patch configmap \
  unrelated-manager-state --namespace "$system_namespace" --type=merge \
  -p '{"data":{"state":"updated"}}' >/dev/null
test "$(kubectl --context "$context" get configmap unrelated-manager-state \
  --namespace "$system_namespace" -o jsonpath='{.data.state}')" = "updated"

# Bind the production read-write network profile so positive result-sink
# probes exercise the same namespaced authority used by a real worker. Its
# username must select the policy even when protected annotations are absent;
# admission still requires Pod-bound claims and the exact result shape.
kubectl --context "$context" create rolebinding network-profile-probe \
  --namespace "$worker_namespace" \
  --clusterrole=cisco-virtual-kubelet-network-management-read-write \
  --serviceaccount="${worker_namespace}:${network_service_account}" >/dev/null
test "$(kubectl --context "$context" auth can-i create configmaps \
  --namespace "$worker_namespace" --as="$network_username")" = "yes"

create_reserved_deployment() {
  local as_user="$1"
  local deployment_name="$2"
  local service_account="${3:-$app_service_account}"

  kubectl --context "$context" --as="$as_user" create -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${deployment_name}
  namespace: ${worker_namespace}
  labels:
    app: ${deployment_name}
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
      serviceAccountName: ${service_account}
      containers:
        - name: pause
          image: ${cached_image}
          imagePullPolicy: Never
EOF
}

create_legacy_token_secret() {
  local service_account="${1:-$app_service_account}"
  local secret_name="${2:-forbidden-legacy-token}"

  kubectl --context "$context" --as="$tenant_username" create -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${secret_name}
  namespace: ${worker_namespace}
  annotations:
    kubernetes.io/service-account.name: ${service_account}
type: kubernetes.io/service-account-token
EOF
}

create_tenant_pod_with_account() {
  local pod_name="$1"
  local service_account="$2"

  kubectl --context "$context" --as="$tenant_username" create -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${pod_name}
  namespace: ${worker_namespace}
spec:
  serviceAccountName: ${service_account}
  containers:
    - name: pause
      image: ${cached_image}
      imagePullPolicy: Never
EOF
}

create_tenant_replicaset_with_account() {
  local replicaset_name="$1"
  local service_account="$2"

  kubectl --context "$context" --as="$tenant_username" create -f - <<EOF
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: ${replicaset_name}
  namespace: ${worker_namespace}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${replicaset_name}
  template:
    metadata:
      labels:
        app: ${replicaset_name}
    spec:
      serviceAccountName: ${service_account}
      containers:
        - name: pause
          image: ${cached_image}
          imagePullPolicy: Never
EOF
}

delete_raw_with_uid() {
  local kubeconfig="$1"
  local resource_path="$2"
  local uid="$3"

  kubectl --kubeconfig "$kubeconfig" delete --raw="$resource_path" -f - <<EOF
{"apiVersion":"meta.k8s.io/v1","kind":"DeleteOptions","preconditions":{"uid":"${uid}"}}
EOF
}

manager_pod_delete_dry_run() {
  local pod="$1"
  local preconditions="$2"
  kubectl --context "$context" --as="$manager_username" delete \
    --raw="/api/v1/namespaces/${worker_namespace}/pods/${pod}" -f - <<EOF
{"apiVersion":"meta.k8s.io/v1","kind":"DeleteOptions","dryRun":["All"],"preconditions":${preconditions}}
EOF
}

delete_raw_with_uid_as_gc() {
  local resource_path="$1"
  local uid="$2"

  kubectl --context "$context" \
    --as=system:serviceaccount:kube-system:generic-garbage-collector \
    --as-group=system:serviceaccounts \
    --as-group=system:serviceaccounts:kube-system \
    --as-group=system:authenticated \
    delete --raw="$resource_path" -f - <<EOF
{"apiVersion":"meta.k8s.io/v1","kind":"DeleteOptions","preconditions":{"uid":"${uid}"}}
EOF
}

sha256_prefix() {
  local value="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$value" | sha256sum | awk '{print substr($1, 1, 8)}'
    return
  fi
  printf '%s' "$value" | shasum -a 256 | awk '{print substr($1, 1, 8)}'
}

create_partially_bound_configmap() {
  kubectl --context "$context" --as="$tenant_username" create -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: partial-result-binding
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/device-uid: partial-binding
data:
  state: forbidden
EOF
}

expect_denied "non-manager reserved ServiceAccount update" \
  "only the topology manager may create, update, or delete a reserved worker ServiceAccount" \
  kubectl --context "$context" --as="$tenant_username" label serviceaccount \
  "$app_service_account" --namespace "$worker_namespace" attacker=true
expect_denied "non-manager reserved Deployment" \
  "only the topology manager, exact native Deployment controller, or namespace cleanup may mutate a Deployment that uses a reserved worker account" \
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
expect_denied "tenant reserved Deployment status forgery" \
  "only the topology manager, exact native Deployment controller, or namespace cleanup may mutate a Deployment that uses a reserved worker account" \
  kubectl --context "$context" --as="$tenant_username" patch deployment reserved-worker \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"availableReplicas":0}}'
expect_denied "tenant reserved ReplicaSet status forgery" \
  "only the topology manager or exact native Deployment, ReplicaSet status, namespace, or garbage-collection path may mutate a ReplicaSet that uses a reserved worker account" \
  kubectl --context "$context" --as="$tenant_username" patch replicaset "$reserved_replicaset" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"availableReplicas":0}}'
kubectl --context "$context" create rolebinding native-deployment-status-probe \
  --namespace "$worker_namespace" --role=admission-probe \
  --serviceaccount=kube-system:deployment-controller >/dev/null
kubectl --context "$context" create rolebinding native-replicaset-status-probe \
  --namespace "$worker_namespace" --role=admission-probe \
  --serviceaccount=kube-system:replicaset-controller >/dev/null
kubectl --context "$context" create rolebinding root-kcm-workload-status-probe \
  --namespace "$worker_namespace" --role=admission-probe \
  --user=system:kube-controller-manager >/dev/null
kubectl --context "$context" \
  --as=system:serviceaccount:kube-system:deployment-controller \
  --as-group=system:serviceaccounts \
  --as-group=system:serviceaccounts:kube-system \
  --as-group=system:authenticated patch deployment reserved-worker \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"availableReplicas":0}}' >/dev/null
kubectl --context "$context" \
  --as=system:serviceaccount:kube-system:replicaset-controller \
  --as-group=system:serviceaccounts \
  --as-group=system:serviceaccounts:kube-system \
  --as-group=system:authenticated patch replicaset "$reserved_replicaset" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"availableReplicas":0}}' >/dev/null
kubectl --context "$context" --as=system:kube-controller-manager \
  --as-group=system:authenticated patch deployment reserved-worker \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"availableReplicas":0}}' >/dev/null
kubectl --context "$context" --as=system:kube-controller-manager \
  --as-group=system:authenticated patch replicaset "$reserved_replicaset" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"availableReplicas":0}}' >/dev/null
expect_denied "tenant reserved Deployment collection delete" \
  "only the topology manager, exact native Deployment controller, or namespace cleanup may mutate a Deployment that uses a reserved worker account" \
  delete_collection_raw_as "$tenant_username" \
  "/apis/apps/v1/namespaces/${worker_namespace}/deployments?labelSelector=app%3Dreserved-worker"
expect_denied "tenant reserved ReplicaSet collection delete" \
  "only the topology manager or exact native Deployment, ReplicaSet status, namespace, or garbage-collection path may mutate a ReplicaSet that uses a reserved worker account" \
  delete_collection_raw_as "$tenant_username" \
  "/apis/apps/v1/namespaces/${worker_namespace}/replicasets?labelSelector=app%3Dreserved-worker"
expect_denied "tenant reserved Pod collection delete" \
  "only the topology manager or exact native ReplicaSet/kubelet/garbage-collection path may mutate a Pod that uses a reserved worker account" \
  delete_collection_raw_as "$tenant_username" \
  "/api/v1/namespaces/${worker_namespace}/pods?labelSelector=app%3Dreserved-worker"
kubectl --context "$context" get deployment reserved-worker \
  --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" get replicaset "$reserved_replicaset" \
  --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" >/dev/null
projected_token_paths="$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" \
  -o jsonpath='{range .spec.volumes[*].projected.sources[*]}{.serviceAccountToken.path}{"\n"}{end}')"
if [[ "$projected_token_paths" != *"token"* ]]; then
  echo "reserved worker Pod has no projected ServiceAccount token source" >&2
  exit 1
fi

# Exercise the positive half of the shared-app admission contract with an
# actual API-server-issued Pod-bound token. Impersonating the Pod's assigned
# kubelet reaches both the TokenRequest policy and the Node authorizer; the
# resulting token carries the pod-name/pod-uid extras that worker admission
# relies on. A separate kubeconfig prevents the cluster-admin client
# certificate from masking bearer-token authentication.
kubectl --context "$context" --as="$manager_username" annotate pod "$reserved_pod" \
  --namespace "$worker_namespace" \
  "topology.cisco.vk/app-worker-username=${app_username}" \
  "topology.cisco.vk/app-worker-pod-name=${reserved_pod}" \
  "topology.cisco.vk/app-worker-pod-uid=${reserved_pod_uid}" --overwrite >/dev/null
reserved_pod_node="$(kubectl --context "$context" get pod "$reserved_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.spec.nodeName}')"
test -n "$reserved_pod_node"
expect_denied "tenant reserved Pod status forgery" \
  "a reserved worker Pod is immutable except for status written by its exact authenticated node" \
  kubectl --context "$context" --as="$tenant_username" patch pod "$reserved_pod" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"reason":"TenantForgedWorkerReadiness"}}'
kubectl --context "$context" create rolebinding spoofed-node-token-probe \
  --namespace "$worker_namespace" --role=admission-probe \
  --user="system:node:${reserved_pod_node}" >/dev/null
expect_denied "node-shaped TokenRequest without the kubelet group" \
  "a reserved worker token must be requested by a kubelet and bound to one exact Pod UID" \
  kubectl --context "$context" --as="system:node:${reserved_pod_node}" \
  --as-group=system:authenticated create token "$app_service_account" \
  --namespace "$worker_namespace" --duration=10m \
  --bound-object-kind=Pod --bound-object-name="$reserved_pod" \
  --bound-object-uid="$reserved_pod_uid"
expect_denied "node-shaped Pod status without the kubelet group" \
  "a reserved worker Pod is immutable except for status written by its exact authenticated node" \
  kubectl --context "$context" --as="system:node:${reserved_pod_node}" \
  --as-group=system:authenticated patch pod "$reserved_pod" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"reason":"UngroupedNodeStatus"}}'
kubectl --context "$context" --as="system:node:${reserved_pod_node}" \
  --as-group=system:nodes --as-group=system:authenticated \
  patch pod "$reserved_pod" --namespace "$worker_namespace" \
  --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"reason":"NativeKubeletStatusQualified"}}' >/dev/null
bound_app_token="$(kubectl --context "$context" \
  --as="system:node:${reserved_pod_node}" --as-group=system:nodes \
  --as-group=system:authenticated \
  create token "$app_service_account" --namespace "$worker_namespace" \
  --duration=10m --bound-object-kind=Pod --bound-object-name="$reserved_pod" \
  --bound-object-uid="$reserved_pod_uid")"
test -n "$bound_app_token"
bound_kubeconfig="$scratch_dir/bound-app-worker.kubeconfig"
kind export kubeconfig --name "$cluster_name" --kubeconfig "$bound_kubeconfig" >/dev/null
kubectl config --kubeconfig "$bound_kubeconfig" set-credentials bound-app-worker \
  --token="$bound_app_token" >/dev/null
kubectl config --kubeconfig "$bound_kubeconfig" set-context bound-app-worker \
  --cluster="$context" --user=bound-app-worker --namespace="$worker_namespace" >/dev/null
kubectl config --kubeconfig "$bound_kubeconfig" use-context bound-app-worker >/dev/null
test "$(kubectl --kubeconfig "$bound_kubeconfig" auth whoami \
  -o jsonpath='{.status.userInfo.username}')" = "$app_username"
ordinary_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector=app=ordinary \
  -o jsonpath='{.items[0].metadata.name}')"
test -n "$ordinary_pod"
manager_pod_delete_dry_run "$reserved_pod" "{\"uid\":\"${reserved_pod_uid}\"}" >/dev/null
expect_denied 'manager quarantine delete requires a UID' 'shared-worker-pod' \
  manager_pod_delete_dry_run "$reserved_pod" '{}'
# The API server rejects a stale UID before evaluating admission.
expect_denied 'manager quarantine delete rejects a stale UID' 'UID in the precondition' \
  manager_pod_delete_dry_run "$reserved_pod" '{"uid":"stale-worker-uid"}'
ordinary_pod_uid="$(kubectl --context "$context" get pod "$ordinary_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
expect_denied 'manager cannot directly delete an ordinary workload Pod' 'shared-worker-pod' \
  manager_pod_delete_dry_run "$ordinary_pod" "{\"uid\":\"${ordinary_pod_uid}\"}"
ordinary_replicaset="$(kubectl --context "$context" get replicaset \
  --namespace "$worker_namespace" --selector=app=ordinary \
  -o jsonpath='{.items[0].metadata.name}')"
test -n "$ordinary_replicaset"
kubectl --context "$context" --as="$tenant_username" patch deployment ordinary \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{}}' >/dev/null
kubectl --context "$context" --as="$tenant_username" patch replicaset "$ordinary_replicaset" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{}}' >/dev/null
kubectl --context "$context" --as="$tenant_username" patch pod "$ordinary_pod" \
  --namespace "$worker_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{}}' >/dev/null
kubectl --context "$context" --as="$manager_username" annotate pod "$ordinary_pod" \
  --namespace "$worker_namespace" \
  "topology.cisco.vk/app-worker-username=${app_username}" \
  "topology.cisco.vk/app-worker-pod-name=${reserved_pod}" \
  "topology.cisco.vk/app-worker-pod-uid=${reserved_pod_uid}" --overwrite >/dev/null
kubectl --kubeconfig "$bound_kubeconfig" patch pod "$ordinary_pod" \
  --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"reason":"BoundTokenAdmissionQualified"}}' >/dev/null

# UID-derived phase-zero and managed identities retain broad name-based RBAC
# while their generated bindings exist. Create one through the real manager
# path, prove tenant operations cannot seize or use it, and prove the native
# Deployment/ReplicaSet/kubelet chain can still start a worker and mint its
# Pod-bound projected token.
kubectl --context "$context" create -f - >/dev/null <<EOF
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: generated-account-owner
  namespace: ${worker_namespace}
spec:
  driver: FAKE
  address: 192.0.2.1
  username: probe
  maxPods: 1
EOF
generated_device_uid="$(kubectl --context "$context" get ciscodevice \
  generated-account-owner --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$generated_device_uid"
generated_service_account="cisco-vk-legacy-generated-account-owner-$(sha256_prefix "$generated_device_uid")"
generated_username="system:serviceaccount:${worker_namespace}:${generated_service_account}"
kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${generated_service_account}
  namespace: ${worker_namespace}
  labels:
    cvk-test-scope: generated-reserved
  annotations:
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: generated-account-owner
    topology.cisco.vk/device-uid: ${generated_device_uid}
    topology.cisco.vk/node-name: generated-account-owner
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      kind: CiscoDevice
      name: generated-account-owner
      uid: ${generated_device_uid}
      controller: true
      blockOwnerDeletion: true
EOF
expect_denied "tenant generated ServiceAccount delete" \
  "only the topology manager may create, update, or delete a reserved worker ServiceAccount" \
  kubectl --context "$context" --as="$tenant_username" delete serviceaccount \
  "$generated_service_account" --namespace "$worker_namespace"
expect_denied "tenant generated ServiceAccount collection delete" \
  "only the topology manager may create, update, or delete a reserved worker ServiceAccount" \
  delete_collection_raw_as "$tenant_username" \
  "/api/v1/namespaces/${worker_namespace}/serviceaccounts?labelSelector=cvk-test-scope%3Dgenerated-reserved"
kubectl --context "$context" get serviceaccount "$generated_service_account" \
  --namespace "$worker_namespace" >/dev/null
expect_denied "tenant generated-account TokenRequest" \
  "a reserved worker token must be requested by a kubelet and bound to one exact Pod UID" \
  kubectl --context "$context" --as="$tenant_username" create token \
  "$generated_service_account" --namespace "$worker_namespace" --duration=10m
expect_denied "tenant Pod using generated account" \
  "only the topology manager or exact native ReplicaSet/kubelet/garbage-collection path may mutate a Pod that uses a reserved worker account" \
  create_tenant_pod_with_account forbidden-generated-pod "$generated_service_account"
expect_denied "tenant Deployment using generated account" \
  "only the topology manager, exact native Deployment controller, or namespace cleanup may mutate a Deployment that uses a reserved worker account" \
  create_reserved_deployment "$tenant_username" forbidden-generated-deployment \
  "$generated_service_account"
expect_denied "tenant ReplicaSet using generated account" \
  "only the topology manager or exact native Deployment, ReplicaSet status, namespace, or garbage-collection path may mutate a ReplicaSet that uses a reserved worker account" \
  create_tenant_replicaset_with_account forbidden-generated-replicaset \
  "$generated_service_account"
expect_denied "generated-account legacy token Secret" \
  "legacy token Secrets are forbidden for reserved worker ServiceAccounts" \
  create_legacy_token_secret "$generated_service_account" \
  forbidden-generated-legacy-token

generated_deployment="generated-worker"
create_reserved_deployment "$manager_username" "$generated_deployment" \
  "$generated_service_account" >/dev/null
kubectl --context "$context" rollout status "deployment/${generated_deployment}" \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
generated_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector="app=${generated_deployment}" \
  -o jsonpath='{.items[0].metadata.name}')"
generated_replicaset="$(kubectl --context "$context" get replicaset \
  --namespace "$worker_namespace" --selector="app=${generated_deployment}" \
  -o jsonpath='{.items[0].metadata.name}')"
generated_pod_uid="$(kubectl --context "$context" get pod "$generated_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
generated_pod_node="$(kubectl --context "$context" get pod "$generated_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.spec.nodeName}')"
test -n "$generated_pod"
test -n "$generated_replicaset"
test -n "$generated_pod_uid"
test -n "$generated_pod_node"
test "$(kubectl --context "$context" get pod "$generated_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.spec.serviceAccountName}')" = \
  "$generated_service_account"
expect_denied "tenant generated worker Pod update" \
  "a reserved worker Pod is immutable except for status written by its exact authenticated node" \
  kubectl --context "$context" --as="$tenant_username" label pod "$generated_pod" \
  --namespace "$worker_namespace" attacker=true
generated_bound_token="$(kubectl --context "$context" \
  --as="system:node:${generated_pod_node}" --as-group=system:nodes \
  --as-group=system:authenticated \
  create token "$generated_service_account" --namespace "$worker_namespace" \
  --duration=10m --bound-object-kind=Pod --bound-object-name="$generated_pod" \
  --bound-object-uid="$generated_pod_uid")"
test -n "$generated_bound_token"
generated_kubeconfig="$scratch_dir/generated-worker.kubeconfig"
kind export kubeconfig --name "$cluster_name" --kubeconfig "$generated_kubeconfig" >/dev/null
kubectl config --kubeconfig "$generated_kubeconfig" set-credentials generated-worker \
  --token="$generated_bound_token" >/dev/null
kubectl config --kubeconfig "$generated_kubeconfig" set-context generated-worker \
  --cluster="$context" --user=generated-worker --namespace="$worker_namespace" >/dev/null
kubectl config --kubeconfig "$generated_kubeconfig" use-context generated-worker >/dev/null
test "$(kubectl --kubeconfig "$generated_kubeconfig" auth whoami \
  -o jsonpath='{.status.userInfo.username}')" = "$generated_username"
expect_denied "tenant generated worker Deployment delete" \
  "only the topology manager, exact native Deployment controller, or namespace cleanup may mutate a Deployment that uses a reserved worker account" \
  kubectl --context "$context" --as="$tenant_username" delete deployment \
  "$generated_deployment" --namespace "$worker_namespace"
expect_denied "tenant generated worker ReplicaSet delete" \
  "only the topology manager or exact native Deployment, ReplicaSet status, namespace, or garbage-collection path may mutate a ReplicaSet that uses a reserved worker account" \
  kubectl --context "$context" --as="$tenant_username" delete replicaset \
  "$generated_replicaset" --namespace "$worker_namespace"
expect_denied "tenant generated worker Pod delete" \
  "only the topology manager or exact native ReplicaSet/kubelet/garbage-collection path may mutate a Pod that uses a reserved worker account" \
  kubectl --context "$context" --as="$tenant_username" delete pod \
  "$generated_pod" --namespace "$worker_namespace"
# Scaling the manager-owned Deployment to zero exercises the native
# ReplicaSet controller's positive Pod DELETE path. Scale back up so deleting
# the Deployment then exercises manager authorization plus native GC of both
# the ReplicaSet and its live Pod.
kubectl --context "$context" --as="$manager_username" patch deployment \
  "$generated_deployment" --namespace "$worker_namespace" --type=merge \
  -p '{"spec":{"replicas":0}}' >/dev/null
for _ in $(seq 1 60); do
  if ! kubectl --context "$context" get pod "$generated_pod" \
      --namespace "$worker_namespace" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if kubectl --context "$context" get pod "$generated_pod" \
    --namespace "$worker_namespace" >/dev/null 2>&1; then
  echo "native ReplicaSet controller did not delete the generated worker Pod" >&2
  exit 1
fi
kubectl --context "$context" --as="$manager_username" patch deployment \
  "$generated_deployment" --namespace "$worker_namespace" --type=merge \
  -p '{"spec":{"replicas":1}}' >/dev/null
kubectl --context "$context" rollout status "deployment/${generated_deployment}" \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
generated_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector="app=${generated_deployment}" \
  -o jsonpath='{.items[0].metadata.name}')"
test -n "$generated_pod"
kubectl --context "$context" --as="$manager_username" delete deployment \
  "$generated_deployment" --namespace "$worker_namespace" --wait=true >/dev/null
for _ in $(seq 1 60); do
  if ! kubectl --context "$context" get replicaset "$generated_replicaset" \
      --namespace "$worker_namespace" >/dev/null 2>&1 &&
     ! kubectl --context "$context" get pod "$generated_pod" \
      --namespace "$worker_namespace" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if kubectl --context "$context" get replicaset "$generated_replicaset" \
    --namespace "$worker_namespace" >/dev/null 2>&1 ||
   kubectl --context "$context" get pod "$generated_pod" \
    --namespace "$worker_namespace" >/dev/null 2>&1; then
  echo "native cleanup did not remove the generated worker ReplicaSet and Pod" >&2
  exit 1
fi
kubectl --context "$context" --as="$manager_username" delete serviceaccount \
  "$generated_service_account" --namespace "$worker_namespace" --wait=true >/dev/null
expect_denied "tenant generated ServiceAccount recreate" \
  "only the topology manager may create, update, or delete a reserved worker ServiceAccount" \
  kubectl --context "$context" --as="$tenant_username" create serviceaccount \
  "$generated_service_account" --namespace "$worker_namespace"

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
expect_denied "partial result binding using only device UID" \
  "only the topology manager may add, rotate, or remove a result binding" \
  create_partially_bound_configmap
expect_denied "shared network worker unbound result create" \
  "a shared network result requires its exact device-derived Pod and supported controller owner" \
  kubectl --context "$context" --as="$network_username" create configmap \
  unbound-network-result --namespace "$worker_namespace" --from-literal=state=forbidden
kubectl --context "$context" --as="$tenant_username" create configmap \
  ordinary-network-probe --namespace "$worker_namespace" \
  --from-literal=state=initial >/dev/null
expect_denied "shared network worker unbound result update" \
  "a shared network result requires its exact device-derived Pod and supported controller owner" \
  kubectl --context "$context" --as="$network_username" patch configmap \
  ordinary-network-probe --namespace "$worker_namespace" --type=merge \
  -p '{"data":{"state":"forbidden"}}'

# Qualify result CREATE/UPDATE/DELETE with an API-server-issued token bound to
# an exact manager-created network worker Pod. The name encodes the device name
# and UID exactly as the production controller does.
network_device_name="switch-with-a-lab-device-name-that-must-not-truncate-worker-uid"
network_device_uid="11111111-1111-4111-8111-111111111111"
network_deployment="u${network_device_uid}-network"
create_reserved_deployment "$manager_username" "$network_deployment" \
  "$network_service_account" >/dev/null
kubectl --context "$context" rollout status "deployment/${network_deployment}" \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
kubectl --context "$context" wait pod --namespace "$worker_namespace" \
  --selector="app=${network_deployment}" --for=condition=Ready --timeout=90s >/dev/null
network_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector="app=${network_deployment}" \
  -o jsonpath='{.items[0].metadata.name}')"
network_pod_uid="$(kubectl --context "$context" get pod "$network_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
network_pod_node="$(kubectl --context "$context" get pod "$network_pod" \
  --namespace "$worker_namespace" -o jsonpath='{.spec.nodeName}')"
test -n "$network_pod"
test -n "$network_pod_uid"
test -n "$network_pod_node"
[[ "$network_pod" == "${network_deployment}-"* ]]

bound_network_token="$(kubectl --context "$context" \
  --as="system:node:${network_pod_node}" --as-group=system:nodes \
  --as-group=system:authenticated \
  create token "$network_service_account" --namespace "$worker_namespace" \
  --duration=10m --bound-object-kind=Pod --bound-object-name="$network_pod" \
  --bound-object-uid="$network_pod_uid")"
test -n "$bound_network_token"
bound_network_kubeconfig="$scratch_dir/bound-network-worker.kubeconfig"
kind export kubeconfig --name "$cluster_name" \
  --kubeconfig "$bound_network_kubeconfig" >/dev/null
kubectl config --kubeconfig "$bound_network_kubeconfig" \
  set-credentials bound-network-worker --token="$bound_network_token" >/dev/null
kubectl config --kubeconfig "$bound_network_kubeconfig" \
  set-context bound-network-worker --cluster="$context" \
  --user=bound-network-worker --namespace="$worker_namespace" >/dev/null
kubectl config --kubeconfig "$bound_network_kubeconfig" \
  use-context bound-network-worker >/dev/null
test "$(kubectl --kubeconfig "$bound_network_kubeconfig" auth whoami \
  -o jsonpath='{.status.userInfo.username}')" = "$network_username"

# A bound network object remains manager/native-controller owned even when a
# namespace principal has broad DELETE and DELETECOLLECTION RBAC. Native GC may
# remove the exact incarnation only with its normal UID precondition.
kubectl --context "$context" --as="$tenant_username" create -f - >/dev/null <<EOF
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: normal-delete-owner
  namespace: ${worker_namespace}
  labels:
    cvk-test-scope: bound-network-owner
spec:
  deviceRef:
    name: ${network_device_name}
  commands:
    - show version
EOF
kubectl --context "$context" --as="$manager_username" annotate iosxediagnostic \
  normal-delete-owner --namespace "$worker_namespace" \
  topology.cisco.vk/managed=true \
  topology.cisco.vk/device-namespace="$worker_namespace" \
  topology.cisco.vk/device-name="$network_device_name" \
  topology.cisco.vk/device-uid="$network_device_uid" \
  topology.cisco.vk/network-worker-username="$network_username" >/dev/null
normal_delete_owner_uid="$(kubectl --context "$context" get iosxediagnostic \
  normal-delete-owner --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$normal_delete_owner_uid"
expect_denied "tenant bound network-object delete" \
  "only the topology manager may add, rotate, or remove a network-object binding" \
  kubectl --context "$context" --as="$tenant_username" delete iosxediagnostic \
  normal-delete-owner --namespace "$worker_namespace"
expect_denied "tenant bound network-object collection delete" \
  "only the topology manager may add, rotate, or remove a network-object binding" \
  delete_collection_raw_as "$tenant_username" \
  "/apis/config.cisco.vk/v1alpha1/namespaces/${worker_namespace}/iosxediagnostics?labelSelector=cvk-test-scope%3Dbound-network-owner"
kubectl --context "$context" get iosxediagnostic normal-delete-owner \
  --namespace "$worker_namespace" >/dev/null
kubectl --context "$context" create role native-network-gc-probe \
  --namespace "$worker_namespace" --verb=delete \
  --resource=iosxediagnostics.config.cisco.vk,iosxeconfigs.config.cisco.vk >/dev/null
kubectl --context "$context" create rolebinding native-network-gc-probe \
  --namespace "$worker_namespace" --role=native-network-gc-probe \
  --serviceaccount=kube-system:generic-garbage-collector >/dev/null
expect_denied "native GC ownerless bound network-object delete" \
  "only the topology manager may add, rotate, or remove a network-object binding" \
  delete_raw_with_uid_as_gc \
  "/apis/config.cisco.vk/v1alpha1/namespaces/${worker_namespace}/iosxediagnostics/normal-delete-owner" \
  "$normal_delete_owner_uid"
kubectl --context "$context" --as="$manager_username" annotate iosxediagnostic \
  normal-delete-owner --namespace "$worker_namespace" \
  topology.cisco.vk/managed- \
  topology.cisco.vk/device-namespace- \
  topology.cisco.vk/device-name- \
  topology.cisco.vk/device-uid- \
  topology.cisco.vk/network-worker-username- >/dev/null
kubectl --context "$context" delete iosxediagnostic \
  normal-delete-owner --namespace "$worker_namespace" --wait=true >/dev/null

kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata:
  name: native-gc-owned-config
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: generated-account-owner
    topology.cisco.vk/device-uid: ${generated_device_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      kind: CiscoDevice
      name: generated-account-owner
      uid: ${generated_device_uid}
      controller: true
      blockOwnerDeletion: true
spec:
  deviceRef:
    name: generated-account-owner
  managedFamilies:
    - vlan
  source:
    inline:
      vlan:
        vlans: []
EOF
native_gc_config_uid="$(kubectl --context "$context" get iosxeconfig \
  native-gc-owned-config --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$native_gc_config_uid"
delete_raw_with_uid_as_gc \
  "/apis/config.cisco.vk/v1alpha1/namespaces/${worker_namespace}/iosxeconfigs/native-gc-owned-config" \
  "$native_gc_config_uid" >/dev/null

# The worker's two production cleanup resources require an incarnation-bound
# DELETE. Missing and wrong preconditions are rejected; the exact UID succeeds.
kubectl --kubeconfig "$bound_network_kubeconfig" create -f - >/dev/null <<EOF
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata:
  name: ttl-delete-probe
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/network-worker-pod-name: ${network_pod}
    topology.cisco.vk/network-worker-pod-uid: ${network_pod_uid}
spec:
  deviceRef:
    name: ${network_device_name}
  operation:
    kind: ShowCommand
    commands:
      - show version
  ttlSecondsAfterFinished: 60
EOF
ttl_operation_uid="$(kubectl --context "$context" get deviceoperation ttl-delete-probe \
  --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$ttl_operation_uid"
expect_denied "shared worker DeviceOperation delete without UID precondition" \
  "a shared network worker may delete only a supported owned object with its exact UID precondition" \
  kubectl --kubeconfig "$bound_network_kubeconfig" delete deviceoperation \
  ttl-delete-probe --namespace "$worker_namespace"
expect_denied "shared worker DeviceOperation delete with wrong UID precondition" \
  "UID in the precondition" \
  delete_raw_with_uid "$bound_network_kubeconfig" \
  "/apis/ops.cisco.vk/v1alpha1/namespaces/${worker_namespace}/deviceoperations/ttl-delete-probe" \
  "33333333-3333-4333-8333-333333333333"
delete_raw_with_uid "$bound_network_kubeconfig" \
  "/apis/ops.cisco.vk/v1alpha1/namespaces/${worker_namespace}/deviceoperations/ttl-delete-probe" \
  "$ttl_operation_uid" >/dev/null

kubectl --kubeconfig "$bound_network_kubeconfig" create -f - >/dev/null <<EOF
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfigRevision
metadata:
  name: revision-delete-probe
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/network-worker-pod-name: ${network_pod}
    topology.cisco.vk/network-worker-pod-uid: ${network_pod_uid}
spec:
  deviceRef:
    name: ${network_device_name}
  sourceRef: ${worker_namespace}/delete-probe
  sourceUID: source-delete-probe
  hash: sha256:delete-probe
  body: '{"v":1,"configuration":{}}'
EOF
revision_uid="$(kubectl --context "$context" get iosxeconfigrevision \
  revision-delete-probe --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$revision_uid"
delete_raw_with_uid "$bound_network_kubeconfig" \
  "/apis/config.cisco.vk/v1alpha1/namespaces/${worker_namespace}/iosxeconfigrevisions/revision-delete-probe" \
  "$revision_uid" >/dev/null

# Bind the result owner to the same exact device/Pod envelope that its result
# producer copies. The runtime producer separately dereferences this owner and
# rejects any cross-device child binding.
kubectl --context "$context" create -f - >/dev/null <<EOF
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: result-owner
  namespace: ${worker_namespace}
spec:
  deviceRef:
    name: ${network_device_name}
  commands:
    - show version
EOF
kubectl --context "$context" --as="$manager_username" annotate iosxediagnostic \
  result-owner --namespace "$worker_namespace" \
  topology.cisco.vk/managed=true \
  topology.cisco.vk/device-namespace="$worker_namespace" \
  topology.cisco.vk/device-name="$network_device_name" \
  topology.cisco.vk/device-uid="$network_device_uid" \
  topology.cisco.vk/network-worker-username="$network_username" \
  topology.cisco.vk/network-worker-pod-name="$network_pod" \
  topology.cisco.vk/network-worker-pod-uid="$network_pod_uid" >/dev/null
result_owner_uid="$(kubectl --context "$context" get iosxediagnostic result-owner \
  --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$result_owner_uid"

create_bound_diagnostic_result() {
  local result_name="$1"
  local captured_at="$2"
  local state="$3"

  kubectl --kubeconfig "$bound_network_kubeconfig" create -f - >/dev/null <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${result_name}
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/network-worker-pod-name: ${network_pod}
    topology.cisco.vk/network-worker-pod-uid: ${network_pod_uid}
  labels:
    cisco.vk/diagnostic: result-owner
    cisco.vk/diagnostic-uid: ${result_owner_uid}
    cisco.vk/diagnostic-capturedAt: ${captured_at}
  ownerReferences:
    - apiVersion: config.cisco.vk/v1alpha1
      kind: IOSXEDiagnostic
      name: result-owner
      uid: ${result_owner_uid}
      controller: true
data:
  state: ${state}
EOF
}

worker_result_captured_at="20260925-120000"
worker_result_name="u${network_device_uid}-result-u${result_owner_uid}-${worker_result_captured_at}"
create_bound_diagnostic_result "$worker_result_name" \
  "$worker_result_captured_at" initial
kubectl --kubeconfig "$bound_network_kubeconfig" patch configmap \
  "$worker_result_name" --namespace "$worker_namespace" --type=merge \
  -p '{"data":{"state":"updated"}}' >/dev/null
test "$(kubectl --context "$context" get configmap "$worker_result_name" \
  --namespace "$worker_namespace" -o jsonpath='{.data.state}')" = "updated"
expect_denied "tenant bound-result data update" \
  "only the topology manager, exact shared network worker, or native garbage collector may mutate a bound result" \
  kubectl --context "$context" --as="$tenant_username" patch configmap \
  "$worker_result_name" --namespace "$worker_namespace" --type=merge \
  -p '{"data":{"state":"forbidden"}}'
expect_denied "tenant bound-result delete" \
  "only the topology manager, exact shared network worker, or native garbage collector may mutate a bound result" \
  kubectl --context "$context" --as="$tenant_username" delete configmap \
  "$worker_result_name" --namespace "$worker_namespace"
expect_denied "unbound shared worker bound-result delete" \
  "a shared network result requires its exact device-derived Pod and supported controller owner" \
  kubectl --context "$context" --as="$network_username" delete configmap \
  "$worker_result_name" --namespace "$worker_namespace"
expect_denied "bound shared worker result delete without UID precondition" \
  "a shared network worker may delete only an exact result ConfigMap UID" \
  kubectl --kubeconfig "$bound_network_kubeconfig" delete configmap \
  "$worker_result_name" --namespace "$worker_namespace"
worker_result_uid="$(kubectl --context "$context" get configmap \
  "$worker_result_name" --namespace "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test -n "$worker_result_uid"
delete_raw_with_uid "$bound_network_kubeconfig" \
  "/api/v1/namespaces/${worker_namespace}/configmaps/${worker_result_name}" \
  "$worker_result_uid" >/dev/null

# The native generic garbage collector authenticates through its dedicated
# kube-system ServiceAccount when kube-controller-manager uses per-controller
# credentials (the kind/kubeadm default). Exact username plus ServiceAccount
# groups, UID precondition, and result shape are all required. An impersonated
# exact identity deliberately reaches the GC-specific rule, but an ordinary
# kubectl delete has no UID precondition and must fail there. Deleting the real
# owner must then remove the dependent through the actual GC request.
gc_result_captured_at="20260925-120001"
gc_result_name="u${network_device_uid}-result-u${result_owner_uid}-${gc_result_captured_at}"
create_bound_diagnostic_result "$gc_result_name" "$gc_result_captured_at" gc
expect_denied "garbage collector delete without UID precondition" \
  "native garbage collection requires a UID-preconditioned delete of an exact supported result sink" \
  kubectl --context "$context" \
  --as=system:serviceaccount:kube-system:generic-garbage-collector \
  delete configmap "$gc_result_name" --namespace "$worker_namespace"
# A cluster using --use-service-account-credentials=false must grant its root
# controller-manager credential the controller verbs it needs. The stock kind
# cluster uses per-controller credentials, so add only the namespaced delete
# permission needed to drive this admission-path probe.
kubectl --context "$context" create role legacy-controller-manager-gc-probe \
  --namespace "$worker_namespace" --verb=delete --resource=configmaps >/dev/null
kubectl --context "$context" create rolebinding legacy-controller-manager-gc-probe \
  --namespace "$worker_namespace" --role=legacy-controller-manager-gc-probe \
  --user=system:kube-controller-manager >/dev/null
expect_denied "root controller-manager delete without UID precondition" \
  "native garbage collection requires a UID-preconditioned delete of an exact supported result sink" \
  kubectl --context "$context" --as=system:kube-controller-manager \
  --as-group=system:authenticated delete configmap "$gc_result_name" \
  --namespace "$worker_namespace"
kubectl --context "$context" --as="$manager_username" annotate iosxediagnostic \
  result-owner --namespace "$worker_namespace" \
  topology.cisco.vk/managed- \
  topology.cisco.vk/device-namespace- \
  topology.cisco.vk/device-name- \
  topology.cisco.vk/device-uid- \
  topology.cisco.vk/network-worker-username- \
  topology.cisco.vk/network-worker-pod-name- \
  topology.cisco.vk/network-worker-pod-uid- >/dev/null
kubectl --context "$context" delete iosxediagnostic result-owner \
  --namespace "$worker_namespace" --wait=true >/dev/null
for _ in $(seq 1 60); do
  if ! kubectl --context "$context" get configmap "$gc_result_name" \
      --namespace "$worker_namespace" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if kubectl --context "$context" get configmap "$gc_result_name" \
    --namespace "$worker_namespace" >/dev/null 2>&1; then
  echo "native garbage collector did not remove the exact bound result" >&2
  exit 1
fi

ordinary_pod="$(kubectl --context "$context" get pod \
  --namespace "$worker_namespace" --selector=app=ordinary \
  -o jsonpath='{.items[0].metadata.name}')"
binding_patch="{\"metadata\":{\"annotations\":{\"topology.cisco.vk/app-worker-username\":\"system:serviceaccount:${worker_namespace}:${app_service_account}\",\"topology.cisco.vk/app-worker-pod-name\":\"${reserved_pod}\",\"topology.cisco.vk/app-worker-pod-uid\":\"forged-worker-pod-uid\"}}}"
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

# Clusters that run the controller manager without per-controller credentials
# use the exact authenticated root controller-manager identity for namespace
# collection deletion. Prove that path clears both malformed historical CRs
# and bound-but-orphaned result sinks without satisfying GC owner/UID rules.
kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: root-kcm-cleanup-orphan-result
  namespace: ${worker_namespace}
  labels:
    cvk-test-scope: root-kcm-namespace-cleanup
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/network-worker-pod-name: ${network_pod}
    topology.cisco.vk/network-worker-pod-uid: ${network_pod_uid}
data:
  state: orphaned-for-root-kcm-cleanup
EOF
kubectl --context "$context" create role root-kcm-namespace-cleanup-probe \
  --namespace "$worker_namespace" --verb=delete,deletecollection \
  --resource=configmaps,iosxediagnostics.config.cisco.vk >/dev/null
kubectl --context "$context" create rolebinding root-kcm-namespace-cleanup-probe \
  --namespace "$worker_namespace" --role=root-kcm-namespace-cleanup-probe \
  --user=system:kube-controller-manager >/dev/null
kubectl --context "$context" --as=system:kube-controller-manager \
  --as-group=system:authenticated delete \
  --raw="/apis/config.cisco.vk/v1alpha1/namespaces/${worker_namespace}/iosxediagnostics?labelSelector=cvk-test-scope%3Droot-kcm-namespace-cleanup" >/dev/null
kubectl --context "$context" --as=system:kube-controller-manager \
  --as-group=system:authenticated delete \
  --raw="/api/v1/namespaces/${worker_namespace}/configmaps?labelSelector=cvk-test-scope%3Droot-kcm-namespace-cleanup" >/dev/null
if kubectl --context "$context" get iosxediagnostic root-kcm-cleanup-partial-binding \
    --namespace "$worker_namespace" >/dev/null 2>&1 ||
   kubectl --context "$context" get configmap root-kcm-cleanup-orphan-result \
    --namespace "$worker_namespace" >/dev/null 2>&1; then
  echo "root kube-controller-manager collection cleanup left protected objects behind" >&2
  exit 1
fi

kubectl --context "$context" create rolebinding app-drain-device-probe \
  --namespace "$worker_namespace" --clusterrole=cisco-virtual-kubelet-app-hosting-device-read \
  --serviceaccount="${worker_namespace}:${app_service_account}" >/dev/null

# Exercise retained Lease bootstrap and rotation with actual bound tokens.
source "$chart_dir/tests/managed-lease-rebind-checks.sh"
source "$chart_dir/tests/managed-split-drain-checks.sh"
source "$chart_dir/tests/managed-native-node-checks.sh"
source "$chart_dir/tests/managed-foreground-cleanup-checks.sh"

# Namespace teardown exercises the DELETE-collection path while reserved
# worker objects, a malformed historical partial binding, a top-level bound
# network CR, and an orphaned bound result still exist. A missing request.name
# must not cause the fail-closed policies to strand namespace finalization.
kubectl --context "$context" --as="$tenant_username" create -f - >/dev/null <<EOF
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEDiagnostic
metadata:
  name: namespace-cleanup-network-object
  namespace: ${worker_namespace}
spec:
  deviceRef:
    name: ${network_device_name}
  commands:
    - show version
EOF
kubectl --context "$context" --as="$manager_username" annotate iosxediagnostic \
  namespace-cleanup-network-object --namespace "$worker_namespace" \
  topology.cisco.vk/managed=true \
  topology.cisco.vk/device-namespace="$worker_namespace" \
  topology.cisco.vk/device-name="$network_device_name" \
  topology.cisco.vk/device-uid="$network_device_uid" \
  topology.cisco.vk/network-worker-username="$network_username" >/dev/null
kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: namespace-cleanup-orphan-result
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/network-worker-pod-name: ${network_pod}
    topology.cisco.vk/network-worker-pod-uid: ${network_pod_uid}
data:
  state: orphaned-for-namespace-cleanup
EOF
kubectl --context "$context" delete namespace "$worker_namespace" \
  --wait=true --timeout=90s >/dev/null
if kubectl --context "$context" get namespace "$worker_namespace" >/dev/null 2>&1; then
  echo "worker namespace still exists after collection deletion" >&2
  exit 1
fi

echo "managed shared-worker admission integration test passed on ${server_version}"
