#!/usr/bin/env bash

# Exercise the managed-topology trust boundary through a real Kubernetes API
# server and prove the scheduling contract with the in-tree kube-scheduler.
# The caller supplies the disposable cluster; CI runs this against kind.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$chart_dir/../.." && pwd)"
release_name="cvk-topology-it"
admission_prefix="${release_name}-cisco-virtual-kubelet"
expected_policy_count=27
legacy_node_marker_policy="${admission_prefix}-legacy-node-marker"
legacy_node_marker_digest="sha256:02c0e65602ac0ebcc3d19b15bd7cbcd7c3840c081d72f7541efbafc394f2ee76"
system_namespace="cvk-topology-system"
device_namespace="cvk-topology-test"
manager_username="system:serviceaccount:${system_namespace}:cisco-virtual-kubelet-controller"
planner_service_account="rollout-planner"
planner_username="system:serviceaccount:${device_namespace}:${planner_service_account}"
approver_service_account="rollout-approver"
approver_username="system:serviceaccount:${device_namespace}:${approver_service_account}"
# Dotted names exercise the full max-63 DNS-subdomain identity contract, not
# only the simpler DNS-label subset.
managed_node="cvk-topology.managed"
legacy_node="cvk-topology.legacy"
api_proxy_pid=""
api_proxy_url=""

sha256_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

# Mirror internal/controller.shortHash exactly; its zero seed is part of the
# persisted ClusterRoleBinding naming contract.
cvk_short_hash() {
  local value="$1"
  local hash=0
  local octet

  for octet in $(LC_ALL=C printf '%s' "$value" | od -An -tu1 -v); do
    hash=$(( ((hash ^ octet) * 16777619) & 0xffffffff ))
  done
  printf '%08x' "$hash"
}

vk_access_clusterrolebinding_name() {
  local namespace="$1"
  local service_account="$2"
  local raw="${namespace}-${service_account}"

  printf 'cisco-vk-%s-%s' "$raw" "$(cvk_short_hash "$raw")"
}

# This suite intentionally creates cluster-scoped admission and RBAC objects.
# Refuse to touch a non-kind context unless the caller has made an explicit
# disposable-cluster assertion.
current_context="$(kubectl config current-context)"
if [[ "$current_context" != kind-* ]] && \
   [[ "${CVK_TOPOLOGY_TEST_ALLOW_DISPOSABLE_CONTEXT:-}" != "true" ]]; then
  printf 'refusing topology integration test on non-kind context %q\n' \
    "$current_context" >&2
  echo "set CVK_TOPOLOGY_TEST_ALLOW_DISPOSABLE_CONTEXT=true only for a disposable cluster" >&2
  exit 1
fi
scratch_dir="$(mktemp -d)"

clear_test_device_finalizers() {
  local as_user="${1:-}"
  local device
  local devices
  local failed=0

  devices="$(kubectl get ciscodevices --namespace "$device_namespace" \
    -o name 2>/dev/null)" || return 0
  for device in $devices; do
    if [ -n "$as_user" ]; then
      kubectl patch --as="$as_user" "$device" \
        --namespace "$device_namespace" --type=merge \
        -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || failed=1
    else
      kubectl patch "$device" --namespace "$device_namespace" --type=merge \
        -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || failed=1
    fi
  done
  return "$failed"
}

cleanup() {
  local test_status="${1:-0}"
  local cleanup_status=0
  local finalizers_cleared=false
  local heartbeat_deleted=false
  local retained_objects

  if [ -n "$api_proxy_pid" ] && kill -0 "$api_proxy_pid" >/dev/null 2>&1; then
    kill "$api_proxy_pid" >/dev/null 2>&1 || true
    wait "$api_proxy_pid" 2>/dev/null || true
  fi
  api_proxy_pid=""
  api_proxy_url=""

  # Remove fixture finalizers through the still-authorized manager identity.
  # This is the normal cleanup path and avoids an admission-cache race after
  # the retained policy binding is deleted.
  clear_test_device_finalizers "$manager_username" || true
  kubectl delete --as="$manager_username" lease "$managed_node" \
    --namespace kube-node-lease --ignore-not-found --wait=true --timeout=60s \
    >/dev/null 2>&1 || true

  # Bindings go first so an interrupted negative test cannot prevent cleanup.
  helm uninstall "$release_name" --namespace "$system_namespace" \
    --no-hooks >/dev/null 2>&1 || true
  kubectl delete validatingadmissionpolicybinding \
    -l "app.kubernetes.io/instance=${release_name}" \
    --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  kubectl delete validatingadmissionpolicy \
    -l "app.kubernetes.io/instance=${release_name}" \
    --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  # Recover interrupted runs whose manager identity or RBAC is already gone.
  # Admission objects are observed asynchronously, so retry until their cache
  # has converged instead of suppressing a one-shot finalizer-patch failure.
  for _ in $(seq 1 20); do
    if clear_test_device_finalizers; then
      finalizers_cleared=true
      break
    fi
    sleep 1
  done
  if [ "$finalizers_cleared" = false ]; then
    echo "failed to clear topology integration CiscoDevice finalizers" >&2
    cleanup_status=1
  fi
  kubectl delete clusterrolebinding \
    -l "app.kubernetes.io/instance=${release_name}" \
    --ignore-not-found --wait=false >/dev/null || cleanup_status=1
  kubectl delete clusterrolebinding \
    cvk-topology-it-worker cvk-topology-it-worker-pod-delete \
    cvk-topology-it-legacy-worker \
    cvk-topology-it-retirement-worker \
    --ignore-not-found --wait=true --timeout=60s \
    >/dev/null || cleanup_status=1
  if [ -n "${worker_cluster_binding:-}" ]; then
    kubectl delete clusterrolebinding "$worker_cluster_binding" \
      --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  fi
  if [ -n "${legacy_cluster_binding:-}" ]; then
    kubectl delete clusterrolebinding "$legacy_cluster_binding" \
      --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  fi
  if [ -n "${retirement_legacy_cluster_binding:-}" ]; then
    kubectl delete clusterrolebinding "$retirement_legacy_cluster_binding" \
      --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  fi
  if [ -n "${shared_compatibility_cluster_binding:-}" ]; then
    kubectl delete clusterrolebinding "$shared_compatibility_cluster_binding" \
      --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  fi
  kubectl delete clusterrole \
    -l "app.kubernetes.io/instance=${release_name}" \
    --ignore-not-found --wait=false >/dev/null || cleanup_status=1
  # A just-removed admission binding may remain briefly visible to the API
  # server's admission cache. Retry the admin recovery path for interrupted
  # runs; the normal manager-authorized deletion above is immediate.
  for _ in $(seq 1 20); do
    if kubectl delete lease "$managed_node" --namespace kube-node-lease \
        --ignore-not-found --wait=true --timeout=5s >/dev/null 2>&1; then
      heartbeat_deleted=true
      break
    fi
    sleep 1
  done
  if [ "$heartbeat_deleted" = false ]; then
    echo "failed to delete topology integration heartbeat Lease" >&2
    cleanup_status=1
  fi
  kubectl delete node \
    "$managed_node" "$legacy_node" cvk-topology-unmarked \
    cvk-scheduler-a cvk-scheduler-b cvk-scheduler-missing cvk-scheduler-guarded \
    --ignore-not-found --wait=true --timeout=60s >/dev/null || cleanup_status=1
  # Test Pods bound to synthetic virtual Nodes have no live kubelet, and the
  # manager fixture uses a deliberately unavailable image. Force Pods only in
  # these disposable namespaces so neither can strand teardown.
  for namespace in "$device_namespace" "$system_namespace"; do
    # Admission has already been removed from this disposable cluster. An
    # interrupted negative test can leave its synthetic Pod drain-protected;
    # there is deliberately no running manager to complete that fixture.
    for pod in $(kubectl get pods --namespace "$namespace" -o name 2>/dev/null); do
      kubectl patch "$pod" --namespace "$namespace" --type=merge \
        -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || cleanup_status=1
    done
    kubectl delete pods --all --namespace "$namespace" \
      --force --grace-period=0 --ignore-not-found --wait=false \
      >/dev/null 2>&1 || true
  done
  if ! kubectl delete namespace "$device_namespace" "$system_namespace" \
      --ignore-not-found --wait=true --timeout=60s >/dev/null 2>&1; then
    echo "topology integration namespaces did not terminate cleanly" >&2
    kubectl get namespace "$device_namespace" "$system_namespace" \
      -o yaml >&2 2>/dev/null || true
    kubectl get ciscodevices --all-namespaces -o yaml >&2 2>/dev/null || true
    cleanup_status=1
  fi

  retained_objects="$(kubectl get \
    clusterroles,clusterrolebindings,validatingadmissionpolicies,validatingadmissionpolicybindings \
    -l "app.kubernetes.io/instance=${release_name}" -o name 2>/dev/null)" || \
    cleanup_status=1
  if [ -n "$retained_objects" ]; then
    echo "topology integration retained cluster-scoped objects:" >&2
    echo "$retained_objects" >&2
    cleanup_status=1
  fi
  rm -rf -- "$scratch_dir"

  # Preserve the test's original failure. A cleanup defect must still fail a
  # successful run so it cannot contaminate the next test in a shared cluster.
  if [ "$test_status" -ne 0 ]; then
    return "$test_status"
  fi
  return "$cleanup_status"
}

on_exit() {
  local test_status=$?
  local final_status

  trap - EXIT
  set +e
  cleanup "$test_status"
  final_status=$?
  exit "$final_status"
}
trap on_exit EXIT

# A prior interrupted local run may have left retained Helm objects or a
# cluster-scoped Node. The context guard above guarantees this destructive
# reset runs only against an explicitly disposable cluster.
cleanup 0
scratch_dir="$(mktemp -d)"

kubectl version >/dev/null
# A previous run may have installed these CRDs through Helm's dedicated CRD
# phase, which records a different field manager. This suite is explicitly
# limited to a disposable cluster and must install the checked-in schema, so
# take ownership of the authoritative CRD Spec instead of failing on that
# repeat-run-only managedFields conflict.
kubectl apply --server-side --force-conflicts \
  --field-manager=cvk-topology-integration -f "$chart_dir/crds" >/dev/null
kubectl create namespace "$system_namespace" >/dev/null
kubectl create namespace "$device_namespace" >/dev/null

# Exercise the real Helm migration path first. An upgrade must preserve an
# existing shared worker CRB/RB long enough for the controller's Recreate
# handoff, mark them for UID-safe retirement, and a fresh enabled render must
# omit them. The deliberately unavailable image keeps the manager from racing
# this migration fixture.
image_values=(
  --set image.repository=cvk-topology-never
  --set image.tag=integration
  --set image.pullPolicy=Never
)
helm install "$release_name" "$chart_dir" \
  --namespace "$system_namespace" \
  "${image_values[@]}" >/dev/null
kubectl get clusterrolebinding cisco-virtual-kubelet >/dev/null
kubectl get rolebinding cisco-virtual-kubelet-device \
  --namespace "$system_namespace" >/dev/null
helm upgrade "$release_name" "$chart_dir" \
  --namespace "$system_namespace" \
  "${image_values[@]}" \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.networkManagement.accessMode=readWrite \
  --set gnoi.enableSoftwareUpgrade=true \
  --set topology.policy.workloadDrain.enabled=true \
  --set-json "topology.policy.workloadDrain.allowedNamespaces=[\"${device_namespace}\"]" \
  >/dev/null
bootstrap_topology_revision="$(helm status "$release_name" \
  --namespace "$system_namespace" -o json | \
  sed -n 's/.*"version":[[:space:]]*\([0-9][0-9]*\).*/\1/p' | head -1)"
test -n "$bootstrap_topology_revision"
if kubectl get clusterrole cisco-virtual-kubelet-controller -o yaml | \
   grep -Fq '  - replicasets'; then
  echo "base manager role retained topology-only ReplicaSet authority" >&2
  exit 1
fi
test "$(kubectl auth can-i delete replicasets.apps --all-namespaces \
  --as="$manager_username")" = "yes"
test "$(kubectl get clusterrolebinding cisco-virtual-kubelet \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/retire-shared-worker-access}')" = \
  "rollout-v1"
test "$(kubectl get rolebinding cisco-virtual-kubelet-device \
  --namespace "$system_namespace" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/retire-shared-worker-access}')" = \
  "rollout-v1"
test "$(kubectl get clusterrolebinding cisco-virtual-kubelet \
  -o jsonpath='{.metadata.annotations.helm\.sh/resource-policy}')" = "keep"
test "$(kubectl get rolebinding cisco-virtual-kubelet-device \
  --namespace "$system_namespace" \
  -o jsonpath='{.metadata.annotations.helm\.sh/resource-policy}')" = "keep"

# No worker has started in this fixture, so deleting the bridge here simulates
# the controller's proven-quiescent retirement and lets the remaining probes
# assert that the shared ServiceAccount has no authority.
kubectl delete clusterrolebinding cisco-virtual-kubelet >/dev/null
kubectl delete rolebinding cisco-virtual-kubelet-device \
  --namespace "$system_namespace" >/dev/null

# The API server must compile and observe every policy generation. A warning on
# either a built-in or CRD policy is a qualification failure in this test.
policy_count="$(kubectl get validatingadmissionpolicy \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}' | wc -w | tr -d ' ')"
test "$policy_count" -eq "$expected_policy_count"
for policy in $(kubectl get validatingadmissionpolicy \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}'); do
  observed=""
  for _ in $(seq 1 60); do
    generation="$(kubectl get validatingadmissionpolicy "$policy" \
      -o jsonpath='{.metadata.generation}')"
    observed="$(kubectl get validatingadmissionpolicy "$policy" \
      -o jsonpath='{.status.observedGeneration}')"
    if [ -n "$observed" ] && [ "$observed" = "$generation" ]; then
      break
    fi
    sleep 1
  done
  test "$observed" = "$generation"
  warnings="$(kubectl get validatingadmissionpolicy "$policy" \
    -o jsonpath='{range .status.typeChecking.expressionWarnings[*]}{.fieldRef}{": "}{.warning}{"\n"}{end}')"
  if [ -n "$warnings" ]; then
    echo "ValidatingAdmissionPolicy ${policy} has expression warnings:" >&2
    echo "$warnings" >&2
    exit 1
  fi
done

# A direct upgrade from a chart that never installed the retained Node marker
# guard must not disable topology: released legacy Nodes would otherwise keep
# an unprotected handoff marker. Remove that guard to reproduce the old-chart
# state, prove the disable is rejected with an actionable two-step migration,
# then restore it through a topology-enabled upgrade before continuing.
test "$(kubectl get validatingadmissionpolicy "$legacy_node_marker_policy" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/admission-contract-digest}')" = \
  "$legacy_node_marker_digest"
test "$(kubectl get validatingadmissionpolicybinding "$legacy_node_marker_policy" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/admission-contract-digest}')" = \
  "$legacy_node_marker_digest"
kubectl delete validatingadmissionpolicybinding \
  "$legacy_node_marker_policy" >/dev/null
kubectl delete validatingadmissionpolicy "$legacy_node_marker_policy" >/dev/null
if helm upgrade "$release_name" "$chart_dir" \
    --namespace "$system_namespace" \
    "${image_values[@]}" \
    --set topology.enabled=false \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.networkManagement.accessMode=readWrite \
    --set gnoi.enableSoftwareUpgrade=true \
    >"$scratch_dir/topology-disable-missing-node-marker-policy.txt" 2>&1; then
  echo "topology disable unexpectedly accepted a missing retained Node marker guard" >&2
  exit 1
fi
grep -Fq \
  'upgrade this release once with topology.enabled=true before disabling topology' \
  "$scratch_dir/topology-disable-missing-node-marker-policy.txt"

helm upgrade "$release_name" "$chart_dir" \
  --namespace "$system_namespace" \
  "${image_values[@]}" \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.networkManagement.accessMode=readWrite \
  --set gnoi.enableSoftwareUpgrade=true \
  --set topology.policy.workloadDrain.enabled=true \
  --set-json "topology.policy.workloadDrain.allowedNamespaces=[\"${device_namespace}\"]" >/dev/null
restored_observed=""
for _ in $(seq 1 60); do
  restored_generation="$(kubectl get validatingadmissionpolicy \
    "$legacy_node_marker_policy" -o jsonpath='{.metadata.generation}')"
  restored_observed="$(kubectl get validatingadmissionpolicy \
    "$legacy_node_marker_policy" -o jsonpath='{.status.observedGeneration}')"
  if [ -n "$restored_observed" ] && \
     [ "$restored_observed" = "$restored_generation" ]; then
    break
  fi
  sleep 1
done
test "$restored_observed" = "$restored_generation"
restored_warnings="$(kubectl get validatingadmissionpolicy \
  "$legacy_node_marker_policy" \
  -o jsonpath='{range .status.typeChecking.expressionWarnings[*]}{.fieldRef}{": "}{.warning}{"\n"}{end}')"
if [ -n "$restored_warnings" ]; then
  echo "restored legacy Node marker policy has expression warnings:" >&2
  echo "$restored_warnings" >&2
  exit 1
fi
test "$(kubectl get validatingadmissionpolicy "$legacy_node_marker_policy" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/admission-contract-digest}')" = \
  "$legacy_node_marker_digest"
test "$(kubectl get validatingadmissionpolicybinding "$legacy_node_marker_policy" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/admission-contract-digest}')" = \
  "$legacy_node_marker_digest"

# Re-read the server-stored Specs and prove API defaulting/canonicalization has
# not changed the complete contract the manager hashes at startup. Custom
# release names are included in this normalization check.
live_admission_manifest="$scratch_dir/live-admission.yaml"
first_policy=true
for policy in $(kubectl get validatingadmissionpolicy \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}'); do
  if [ "$first_policy" = false ]; then
    printf '%s\n' '---' >>"$live_admission_manifest"
  fi
  kubectl get validatingadmissionpolicy "$policy" \
    -o yaml >>"$live_admission_manifest"
  first_policy=false
done
for role in \
  cisco-virtual-kubelet-app-hosting-read-only \
  cisco-virtual-kubelet-app-hosting-read-write \
  cisco-virtual-kubelet-app-hosting-device-read \
  cisco-virtual-kubelet-network-management-global-read \
  cisco-virtual-kubelet-network-management-lease-read-only \
  cisco-virtual-kubelet-network-management-lease-read-write \
  cisco-virtual-kubelet-network-management-read-only \
  cisco-virtual-kubelet-network-management-read-write; do
  printf '%s\n' '---' >>"$live_admission_manifest"
  kubectl get clusterrole "$role" -o yaml >>"$live_admission_manifest"
done
(
  cd "$repo_root"
  CVK_ADMISSION_MANIFEST="$live_admission_manifest" \
    CVK_ADMISSION_PREFIX="$admission_prefix" \
    CVK_ADMISSION_MANAGER_USERNAME="$manager_username" \
    CVK_ADMISSION_POLICY_NAMESPACE="$system_namespace" \
    CVK_ADMISSION_POLICY_NAME="${admission_prefix}-topology-policy" \
    CVK_ADMISSION_LEDGER_NAME="${admission_prefix}-topology-ledger" \
    GOCACHE="${GOCACHE:-/tmp/cvk-topology-gocache}" \
    go test ./cmd/cisco-vk \
      -run '^TestRenderedManaged(AdmissionContract|WorkerClusterRoleContracts)$' -count=1
)

# Persist the CiscoDevice before deriving the retained PR #190-era per-device
# worker name. This compatibility fixture proves that an upgrade can constrain
# and retire that historical identity; steady-state production now uses the
# two namespace-shared functional accounts.
cat >"$scratch_dir/device.yaml" <<EOF
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: device-a
  namespace: ${device_namespace}
  finalizers:
    - cisco.vk/device-cleanup
  labels:
    topology.cisco.vk/managed: "true"
    topology.kubernetes.io/region: test-region
    topology.kubernetes.io/zone: test-zone-a
    operations.cisco.vk/image-family: cat9k
    operations.cisco.vk/qualification-cohort: c9300
spec:
  nodeName: ${managed_node}
  driver: XE
  physicalIdentity: integration-serial-managed
  address: 192.0.2.10
  port: 443
  username: integration
  credentialSecretRef:
    name: unused-integration-credential
  tls:
    enabled: true
    insecureSkipVerify: true
  maxPods: 4
  xe:
    networking:
      interface:
        type: Management
        management:
          dhcp: true
EOF
kubectl create -f "$scratch_dir/device.yaml" >/dev/null
cat >"$scratch_dir/legacy-device.yaml" <<EOF
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: device-legacy
  namespace: ${device_namespace}
  finalizers:
    - cisco.vk/device-cleanup
spec:
  nodeName: ${legacy_node}
  driver: XE
  physicalIdentity: integration-serial-legacy
  address: 192.0.2.11
  port: 443
  username: integration
  credentialSecretRef:
    name: unused-integration-credential
  tls:
    enabled: true
    insecureSkipVerify: true
  maxPods: 4
  xe:
    networking:
      interface:
        type: Management
        management:
          dhcp: true
EOF
kubectl create -f "$scratch_dir/legacy-device.yaml" >/dev/null
device_uid="$(kubectl get ciscodevice device-a --namespace "$device_namespace" \
  -o jsonpath='{.metadata.uid}')"
worker_uid_hash="$(printf '%s' "$device_uid" | sha256_stdin | cut -c1-8)"
worker_service_account="cisco-vk-managed-${managed_node}-${worker_uid_hash}"
worker_username="system:serviceaccount:${device_namespace}:${worker_service_account}"
worker_cluster_binding="$(vk_access_clusterrolebinding_name \
  "$device_namespace" "$worker_service_account")"
worker_revision="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
worker_revision_rotated="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
worker_revision_recovery="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
legacy_device_uid="$(kubectl get ciscodevice device-legacy --namespace "$device_namespace" \
  -o jsonpath='{.metadata.uid}')"
legacy_uid_hash="$(printf '%s' "$legacy_device_uid" | sha256_stdin | cut -c1-8)"
legacy_service_account="cisco-vk-legacy-${legacy_node}-${legacy_uid_hash}"
legacy_username="system:serviceaccount:${device_namespace}:${legacy_service_account}"
legacy_cluster_binding="$(vk_access_clusterrolebinding_name \
  "$device_namespace" "$legacy_service_account")"
device_key="device-$(printf '%s\0%s' "$device_namespace" device-a | sha256_stdin | cut -c1-16)"

# Reproduce the retained PR #190-era role that exists during a real live
# upgrade. A fresh chart no longer renders it, but this suite still exercises
# the compatibility admission path and proves that the old identity cannot
# escape its device while the manager retires it.
cat >"$scratch_dir/legacy-managed-worker-role.yaml" <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cisco-virtual-kubelet-managed-worker
  labels:
    app.kubernetes.io/instance: ${release_name}
rules:
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["nodes/status"]
    verbs: ["get", "update", "patch"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch", "delete"]
  - apiGroups: [""]
    resources: ["pods/status"]
    verbs: ["get", "update", "patch"]
  - apiGroups: [""]
    resources: ["configmaps", "secrets", "services"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: ["cisco.vk"]
    resources: ["ciscodevices"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["config.cisco.vk"]
    resources: ["iosxeconfigdefaults"]
    verbs: ["get", "list", "watch"]
EOF
kubectl create -f "$scratch_dir/legacy-managed-worker-role.yaml" >/dev/null

# Create the exact retained per-device identities: canonical names, reserved
# incarnation annotations, sole subjects, and controller ownership on the two
# namespaced objects. This is the shape the production audit/cleanup path must
# recognize after a PR #190-era live upgrade.
cat >"$scratch_dir/generated-worker-access.yaml" <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${worker_service_account}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/worker-protocol: rollout-v1
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: CiscoDevice
      name: device-a
      uid: ${device_uid}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ${worker_service_account}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/worker-protocol: rollout-v1
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: CiscoDevice
      name: device-a
      uid: ${device_uid}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cisco-virtual-kubelet-device
subjects:
  - kind: ServiceAccount
    name: ${worker_service_account}
    namespace: ${device_namespace}
EOF
kubectl create --as="$manager_username" \
  -f "$scratch_dir/generated-worker-access.yaml" >/dev/null

# The retired managed role is deliberately outside the current manager's bind
# allowlist. Reproduce only that historical cluster-scoped grant as the test
# administrator; reserved generated ServiceAccounts and current legacy access
# are still created through the production manager identity.
cat >"$scratch_dir/generated-managed-worker-cluster-binding.yaml" <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${worker_cluster_binding}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/worker-protocol: rollout-v1
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cisco-virtual-kubelet-managed-worker
subjects:
  - kind: ServiceAccount
    name: ${worker_service_account}
    namespace: ${device_namespace}
EOF
kubectl create \
  -f "$scratch_dir/generated-managed-worker-cluster-binding.yaml" >/dev/null

cat >"$scratch_dir/generated-legacy-worker-access.yaml" <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${legacy_service_account}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-legacy
    topology.cisco.vk/device-uid: ${legacy_device_uid}
    topology.cisco.vk/node-name: ${legacy_node}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: CiscoDevice
      name: device-legacy
      uid: ${legacy_device_uid}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ${legacy_service_account}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-legacy
    topology.cisco.vk/device-uid: ${legacy_device_uid}
    topology.cisco.vk/node-name: ${legacy_node}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: CiscoDevice
      name: device-legacy
      uid: ${legacy_device_uid}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cisco-virtual-kubelet-device
subjects:
  - kind: ServiceAccount
    name: ${legacy_service_account}
    namespace: ${device_namespace}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${legacy_cluster_binding}
  annotations:
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-legacy
    topology.cisco.vk/device-uid: ${legacy_device_uid}
    topology.cisco.vk/node-name: ${legacy_node}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cisco-virtual-kubelet
subjects:
  - kind: ServiceAccount
    name: ${legacy_service_account}
    namespace: ${device_namespace}
EOF
kubectl create --as="$manager_username" \
  -f "$scratch_dir/generated-legacy-worker-access.yaml" >/dev/null

# An unselected topology-aware device gets a distinct UID-derived identity and
# keeps the legacy runtime/role. Fresh installs deliberately leave the old
# release-wide ServiceAccount unbound.
test "$(kubectl auth can-i patch pods --subresource=status \
  --namespace "$device_namespace" \
  --as="system:serviceaccount:${system_namespace}:cisco-virtual-kubelet")" = "no"

# Upgrade enablement restores leaf writes only through the device-namespace
# RoleBinding; the cluster-wide managed-worker role carries no leaf rule.
test "$(kubectl auth can-i patch iosxesoftwareupgrades.ops.cisco.vk \
  --namespace "$device_namespace" --as="$worker_username")" = "yes"
test "$(kubectl auth can-i patch iosxesoftwareupgrades.ops.cisco.vk \
  --namespace "$system_namespace" --as="$worker_username")" = "no"
# Workload drain is namespace-allowlisted and uses the Eviction subresource.
# The manager cannot delete Pods. A generated worker may only finish deletion
# of its own already-terminating Pod through admission and receives none of the
# manager's drain-metadata authority.
test "$(kubectl auth can-i patch pods --namespace "$device_namespace" \
  --as="$manager_username")" = "yes"
test "$(kubectl auth can-i create pods --subresource=eviction \
  --namespace "$device_namespace" --as="$manager_username")" = "yes"
test "$(kubectl auth can-i delete pods --namespace "$device_namespace" \
  --as="$manager_username")" = "yes"
test "$(kubectl auth can-i create pods --namespace "$device_namespace" \
  --as="$manager_username")" = "no"
test "$(kubectl auth can-i delete pods --all-namespaces \
  --as="$worker_username")" = "yes"
test "$(kubectl auth can-i deletecollection pods --all-namespaces \
  --as="$worker_username")" = "no"
test "$(kubectl auth can-i patch pods --namespace "$device_namespace" \
  --as="$worker_username")" = "no"

cat >"$scratch_dir/managed-node.yaml" <<EOF
apiVersion: v1
kind: Node
metadata:
  name: ${managed_node}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/worker-username: ${worker_username}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/projected-keys: topology.kubernetes.io/region,topology.kubernetes.io/zone
    topology.cisco.vk/projection-hash: sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    topology.cisco.vk/managed-taints: "topology.cisco.vk/uninitialized|NoSchedule"
  labels:
    topology.kubernetes.io/region: test-region
    topology.kubernetes.io/zone: test-zone-a
spec:
  taints:
    - key: topology.cisco.vk/uninitialized
      value: "true"
      effect: NoSchedule
EOF
kubectl create --as="$manager_username" -f "$scratch_dir/managed-node.yaml" >/dev/null
managed_node_uid="$(kubectl get node "$managed_node" -o jsonpath='{.metadata.uid}')"
kubectl patch --as="$manager_username" node "$managed_node" --type=merge \
  -p "{\"metadata\":{\"annotations\":{\"topology.cisco.vk/node-uid\":\"${managed_node_uid}\"}}}" >/dev/null

# RBAC permits status but not main-resource Node mutation. Admission must allow
# a harmless status write and reject label smuggling through /status.
test "$(kubectl auth can-i patch nodes --as="$worker_username")" = "no"
test "$(kubectl auth can-i patch nodes --subresource=status \
  --as="$worker_username")" = "yes"
kubectl patch --as="$worker_username" node "$managed_node" \
  --subresource=status --type=merge \
  -p "{\"metadata\":{\"annotations\":{\"topology.cisco.vk/worker-observed-revision\":\"${worker_revision}\"}},\"status\":{}}" >/dev/null
if kubectl patch --as="$worker_username" node "$managed_node" \
    --subresource=status --type=merge --dry-run=server \
    -p '{"metadata":{"annotations":{"topology.cisco.vk/worker-observed-revision":"not-a-content-address"}},"status":{}}' \
    >"$scratch_dir/node-worker-revision-negative.txt" 2>&1; then
  echo "managed worker published a malformed configuration revision" >&2
  exit 1
fi
grep -Eq 'manager/bound-worker owned|denied the request|failed expression' \
  "$scratch_dir/node-worker-revision-negative.txt"
if kubectl patch --as="$worker_username" node "$managed_node" \
    --subresource=status --type=merge --dry-run=server \
    -p "{\"metadata\":{\"annotations\":{\"topology.cisco.vk/worker-observed-revision\":\"${worker_revision}\"},\"labels\":{\"topology.cisco.vk/admission-probe\":\"must-be-denied\"}}}" \
    >"$scratch_dir/node-negative.txt" 2>&1; then
  echo "managed worker changed a Node label through the status subresource" >&2
  exit 1
fi
grep -Eq 'manager/bound-worker owned|manager-owned|only its bound worker|denied the request|failed expression' \
  "$scratch_dir/node-negative.txt"

cat <<'EOF' | kubectl create -f - >/dev/null
apiVersion: v1
kind: Node
metadata:
  name: cvk-topology-unmarked
spec: {}
EOF
if kubectl patch --as="$worker_username" node cvk-topology-unmarked \
    --subresource=status --type=merge --dry-run=server \
    -p '{"status":{}}' >"$scratch_dir/unmarked-negative.txt" 2>&1; then
  echo "managed worker wrote an unmarked peer Node" >&2
  exit 1
fi
grep -Eq 'manager/bound-worker owned|manager-owned|only its bound worker|denied the request|failed expression' \
  "$scratch_dir/unmarked-negative.txt"

# The topology-generated legacy identity retains historical Node CRUD only for
# the exact unmarked Node encoded in its ServiceAccount. It cannot cross into a
# peer legacy Node or a manager-bound Node.
cat <<EOF | kubectl create --as="$legacy_username" -f - >/dev/null
apiVersion: v1
kind: Node
metadata:
  name: ${legacy_node}
spec: {}
EOF
test "$(kubectl auth can-i patch nodes --as="$legacy_username")" = "yes"
kubectl patch --as="$legacy_username" node "$legacy_node" \
  --type=merge --dry-run=server \
  -p '{"metadata":{"labels":{"legacy-probe":"allowed"}}}' >/dev/null
kubectl patch --as="$legacy_username" node "$legacy_node" \
  --subresource=status --type=merge --dry-run=server -p '{"status":{}}' >/dev/null
kubectl delete --as="$legacy_username" node "$legacy_node" \
  --dry-run=server >/dev/null
legacy_node_uid="$(kubectl get node "$legacy_node" -o jsonpath='{.metadata.uid}')"
if kubectl patch --as="$legacy_username" node cvk-topology-unmarked \
    --subresource=status --type=merge --dry-run=server -p '{"status":{}}' \
    >"$scratch_dir/legacy-node-peer-negative.txt" 2>&1; then
  echo "legacy worker wrote a different unmarked Node" >&2
  exit 1
fi
grep -Eq 'exact unmarked Node|manager/bound-worker owned|manager-owned|denied the request|failed expression' \
  "$scratch_dir/legacy-node-peer-negative.txt"
if kubectl patch --as="$legacy_username" node "$managed_node" \
    --subresource=status --type=merge --dry-run=server -p '{"status":{}}' \
    >"$scratch_dir/legacy-node-managed-negative.txt" 2>&1; then
  echo "legacy worker wrote a manager-bound Node" >&2
  exit 1
fi
grep -Eq 'exact unmarked Node|manager/bound-worker owned|manager-owned|denied the request|failed expression' \
  "$scratch_dir/legacy-node-managed-negative.txt"

# Pod status remains cluster-scoped in RBAC because Pods can be scheduled from
# any namespace. Admission narrows that grant to the virtual Node encoded in
# the generated worker identity and preserves all Pod metadata/spec.
for pod_and_node in \
  "cvk-worker-own:${managed_node}" \
  "cvk-worker-peer:cvk-topology-unmarked" \
  "cvk-legacy-own:${legacy_node}" \
  "cvk-legacy-peer:${managed_node}"; do
  pod="${pod_and_node%%:*}"
  node="${pod_and_node#*:}"
  cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${pod}
  namespace: ${device_namespace}
spec:
  nodeName: ${node}
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
done
test "$(kubectl auth can-i patch pods --subresource=status \
  --namespace "$device_namespace" \
  --as="$worker_username")" = "yes"
kubectl patch --as="$worker_username" pod cvk-worker-own \
  --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"phase":"Pending"}}' >/dev/null
if kubectl patch --as="$worker_username" pod cvk-worker-peer \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"phase":"Pending"}}' \
    >"$scratch_dir/pod-peer-negative.txt" 2>&1; then
  echo "managed worker updated Pod status for a different virtual Node" >&2
  exit 1
fi
grep -Eq 'exact bound virtual Node|generated worker identity|denied the request|failed expression' \
  "$scratch_dir/pod-peer-negative.txt"
if kubectl patch --as="$worker_username" pod cvk-worker-own \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"metadata":{"labels":{"forged":"true"}},"status":{"phase":"Pending"}}' \
    >"$scratch_dir/pod-metadata-negative.txt" 2>&1; then
  echo "managed worker smuggled Pod metadata through status" >&2
  exit 1
fi
grep -Eq 'may not change Pod metadata or spec|denied the request|failed expression' \
  "$scratch_dir/pod-metadata-negative.txt"
kubectl patch --as="$legacy_username" pod cvk-legacy-own \
  --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"phase":"Pending"}}' >/dev/null
if kubectl patch --as="$legacy_username" pod cvk-legacy-peer \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"phase":"Pending"}}' \
    >"$scratch_dir/legacy-pod-peer-negative.txt" 2>&1; then
  echo "legacy worker updated Pod status for a different virtual Node" >&2
  exit 1
fi
grep -Eq 'exact bound virtual Node|generated worker identity|denied the request|failed expression' \
  "$scratch_dir/legacy-pod-peer-negative.txt"

# Virtual Kubelet completes Pod removal with a final UID-preconditioned,
# zero-grace API DELETE after provider teardown or after provider status has
# observed the Pod non-running. Exercise that exact request for both generated
# identity formats: neither worker may start deletion, each may complete its
# own already-started deletion, and neither may complete a terminating peer
# Pod. A neutral test finalizer holds terminating fixtures in the API long
# enough for the second DELETE; it is unrelated to the drain finalizer below.
for pod_and_node in \
  "cvk-delete-managed-live:${managed_node}" \
  "cvk-delete-managed-own:${managed_node}" \
  "cvk-delete-managed-peer:${legacy_node}" \
  "cvk-delete-legacy-live:${legacy_node}" \
  "cvk-delete-legacy-own:${legacy_node}" \
  "cvk-delete-legacy-peer:${managed_node}"; do
  pod="${pod_and_node%%:*}"
  node="${pod_and_node#*:}"
  cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${pod}
  namespace: ${device_namespace}
  finalizers:
    - cvk-topology-test/hold
spec:
  nodeName: ${node}
  terminationGracePeriodSeconds: 0
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
done
test "$(kubectl auth can-i delete pods --all-namespaces \
  --as="$legacy_username")" = "yes"
test "$(kubectl auth can-i deletecollection pods --all-namespaces \
  --as="$legacy_username")" = "no"

# kubectl's named delete command intentionally omits UID preconditions. Use its
# authenticated local proxy so these probes can send the exact DeleteOptions
# body while still exercising API-server impersonation and native admission.
command -v curl >/dev/null
api_proxy_log="$scratch_dir/kubectl-proxy.log"
kubectl proxy --port=0 >"$api_proxy_log" 2>&1 &
api_proxy_pid=$!
for _ in $(seq 1 100); do
  api_proxy_url="$(sed -n 's/^Starting to serve on \(.*\)$/http:\/\/\1/p' \
    "$api_proxy_log" | tail -1)"
  if [ -n "$api_proxy_url" ] && curl -fsS "$api_proxy_url/version" >/dev/null; then
    break
  fi
  if ! kill -0 "$api_proxy_pid" >/dev/null 2>&1; then
    cat "$api_proxy_log" >&2
    echo "kubectl proxy exited before becoming ready" >&2
    exit 1
  fi
  sleep 0.1
done
if [ -z "$api_proxy_url" ] || ! curl -fsS "$api_proxy_url/version" >/dev/null; then
  cat "$api_proxy_log" >&2
  echo "kubectl proxy did not become ready" >&2
  exit 1
fi

raw_pod_delete() {
  local username="$1"
  local pod="$2"
  local options="$3"
  curl --fail-with-body -sS -X DELETE \
    -H 'Content-Type: application/json' \
    -H "Impersonate-User: ${username}" \
    -H 'Impersonate-Group: system:serviceaccounts' \
    -H "Impersonate-Group: system:serviceaccounts:${device_namespace}" \
    -H 'Impersonate-Group: system:authenticated' \
    --data-binary "$options" \
    "${api_proxy_url}/api/v1/namespaces/${device_namespace}/pods/${pod}"
}

for identity_case in managed legacy; do
  case "$identity_case" in
    managed)
      delete_username="$worker_username"
      delete_live_pod="cvk-delete-managed-live"
      delete_own_pod="cvk-delete-managed-own"
      delete_peer_pod="cvk-delete-managed-peer"
      ;;
    legacy)
      delete_username="$legacy_username"
      delete_live_pod="cvk-delete-legacy-live"
      delete_own_pod="cvk-delete-legacy-own"
      delete_peer_pod="cvk-delete-legacy-peer"
      ;;
  esac

  delete_live_uid="$(kubectl get pod "$delete_live_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.uid}')"
  delete_live_options="{\"apiVersion\":\"v1\",\"kind\":\"DeleteOptions\",\"gracePeriodSeconds\":0,\"preconditions\":{\"uid\":\"${delete_live_uid}\"}}"
  if raw_pod_delete "$delete_username" "$delete_live_pod" "$delete_live_options" \
      >"$scratch_dir/pod-delete-${identity_case}-live-negative.txt" 2>&1; then
    echo "${identity_case} worker initiated deletion of a live Pod" >&2
    exit 1
  fi
  grep -Eq 'already initiated through Kubernetes|denied the request|failed expression' \
    "$scratch_dir/pod-delete-${identity_case}-live-negative.txt"
  test -z "$(kubectl get pod "$delete_live_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"

  kubectl delete pod "$delete_own_pod" "$delete_peer_pod" \
    --namespace "$device_namespace" --wait=false >/dev/null
  test -n "$(kubectl get pod "$delete_own_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"
  test -n "$(kubectl get pod "$delete_peer_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"

  delete_own_uid="$(kubectl get pod "$delete_own_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.uid}')"
  delete_peer_uid="$(kubectl get pod "$delete_peer_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.uid}')"
  delete_missing_uid_options='{"apiVersion":"v1","kind":"DeleteOptions","gracePeriodSeconds":0}'
  delete_wrong_uid_options='{"apiVersion":"v1","kind":"DeleteOptions","gracePeriodSeconds":0,"preconditions":{"uid":"00000000-0000-0000-0000-000000000000"}}'
  delete_own_options="{\"apiVersion\":\"v1\",\"kind\":\"DeleteOptions\",\"gracePeriodSeconds\":0,\"preconditions\":{\"uid\":\"${delete_own_uid}\"}}"
  delete_peer_options="{\"apiVersion\":\"v1\",\"kind\":\"DeleteOptions\",\"gracePeriodSeconds\":0,\"preconditions\":{\"uid\":\"${delete_peer_uid}\"}}"

  if raw_pod_delete "$delete_username" "$delete_own_pod" "$delete_missing_uid_options" \
      >"$scratch_dir/pod-delete-${identity_case}-missing-uid-negative.txt" 2>&1; then
    echo "${identity_case} worker completed deletion without a UID precondition" >&2
    exit 1
  fi
  grep -Eq 'current UID precondition and zero grace period|denied the request|failed expression' \
    "$scratch_dir/pod-delete-${identity_case}-missing-uid-negative.txt"
  if raw_pod_delete "$delete_username" "$delete_own_pod" "$delete_wrong_uid_options" \
      >"$scratch_dir/pod-delete-${identity_case}-wrong-uid-negative.txt" 2>&1; then
    echo "${identity_case} worker completed deletion with a stale UID precondition" >&2
    exit 1
  fi
  # Storage CAS may reject a stale UID before DELETE admission is invoked;
  # either path proves that a recreated Pod cannot be removed by this request.
  grep -Eq 'UID in the precondition.*does not match|Precondition failed|Conflict|current UID precondition and zero grace period|denied the request|failed expression' \
    "$scratch_dir/pod-delete-${identity_case}-wrong-uid-negative.txt"

  raw_pod_delete "$delete_username" "$delete_own_pod" "$delete_own_options" >/dev/null
  test -n "$(kubectl get pod "$delete_own_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"
  if raw_pod_delete "$delete_username" "$delete_peer_pod" "$delete_peer_options" \
      >"$scratch_dir/pod-delete-${identity_case}-peer-negative.txt" 2>&1; then
    echo "${identity_case} worker completed deletion of a terminating peer Pod" >&2
    exit 1
  fi
  grep -Eq 'exact bound virtual Node|denied the request|failed expression' \
    "$scratch_dir/pod-delete-${identity_case}-peer-negative.txt"
  test -n "$(kubectl get pod "$delete_peer_pod" \
    --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"
done

for delete_pod in \
  cvk-delete-managed-live cvk-delete-managed-own cvk-delete-managed-peer \
  cvk-delete-legacy-live cvk-delete-legacy-own cvk-delete-legacy-peer; do
  kubectl patch pod "$delete_pod" --namespace "$device_namespace" \
    --type=merge -p '{"metadata":{"finalizers":[]}}' >/dev/null
done
kubectl delete pod \
  cvk-delete-managed-live cvk-delete-managed-own cvk-delete-managed-peer \
  cvk-delete-legacy-live cvk-delete-legacy-own cvk-delete-legacy-peer \
  --namespace "$device_namespace" --ignore-not-found --wait=true --timeout=30s \
  >/dev/null

# Grant a generated worker main-resource Pod patch only as an adversarial test.
# Admission must reserve the exact drain marker/finalizer to the manager, must
# reject live session replacement, and must not let the manager smuggle any
# unrelated Pod mutation through its narrowly scoped allowlist Role.
kubectl create role drain-pod-adversary --namespace "$device_namespace" \
  --verb=get,update,patch,delete --resource=pods >/dev/null
kubectl create rolebinding drain-pod-adversary --namespace "$device_namespace" \
  --role=drain-pod-adversary \
  --serviceaccount="${device_namespace}:${worker_service_account}" >/dev/null
# Prove admission still rejects the manager if a future RBAC expansion were to
# accidentally grant direct deletion; policy/v1 Eviction remains the only path.
kubectl create rolebinding drain-pod-manager-adversary --namespace "$device_namespace" \
  --role=drain-pod-adversary \
  --serviceaccount="${system_namespace}:cisco-virtual-kubelet-controller" >/dev/null
test "$(kubectl auth can-i delete pods --namespace "$device_namespace" \
  --as="$worker_username")" = "yes"
test "$(kubectl auth can-i delete pods --namespace "$device_namespace" \
  --as="$manager_username")" = "yes"
drain_session_a="55555555-5555-4555-8555-555555555555"
drain_session_b="66666666-6666-4666-8666-666666666666"
kubectl patch --as="$manager_username" pod cvk-worker-own \
  --namespace "$device_namespace" --type=merge \
  -p "{\"metadata\":{\"annotations\":{\"ops.cisco.vk/drain-session\":\"${drain_session_a}\"},\"finalizers\":[\"ops.cisco.vk/iosxe-rollout-drain\"]}}" \
  >/dev/null
if kubectl patch --as="$worker_username" pod cvk-worker-own \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"annotations":{"ops.cisco.vk/drain-session":null},"finalizers":[]}}' \
    >"$scratch_dir/drain-pod-worker-negative.txt" 2>&1; then
  echo "managed worker removed the manager-owned drain protection" >&2
  exit 1
fi
grep -Eq 'only the topology manager|denied the request|failed expression' \
  "$scratch_dir/drain-pod-worker-negative.txt"
if kubectl patch --as="$manager_username" pod cvk-worker-own \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p "{\"metadata\":{\"annotations\":{\"ops.cisco.vk/drain-session\":\"${drain_session_b}\"}}}" \
    >"$scratch_dir/drain-pod-session-replace-negative.txt" 2>&1; then
  echo "topology manager replaced a live Pod drain session" >&2
  exit 1
fi
grep -Eq 'live session cannot be replaced|denied the request|failed expression' \
  "$scratch_dir/drain-pod-session-replace-negative.txt"
if kubectl patch --as="$manager_username" pod cvk-worker-own \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"labels":{"manager-smuggled":"true"}}}' \
    >"$scratch_dir/drain-pod-manager-scope-negative.txt" 2>&1; then
  echo "topology manager changed unrelated protected Pod metadata" >&2
  exit 1
fi
grep -Eq 'may change only its exact drain protection|denied the request|failed expression' \
  "$scratch_dir/drain-pod-manager-scope-negative.txt"
kubectl patch --as="$manager_username" pod cvk-worker-own \
  --namespace "$device_namespace" --type=merge \
  -p '{"metadata":{"annotations":{"ops.cisco.vk/drain-session":null},"finalizers":[]}}' \
  >/dev/null

# Prove drain protection is an independent denial after the completion policy
# would otherwise allow the generated worker's final DELETE. A real Eviction
# starts deletion, while the reserved drain finalizer keeps the Pod observable.
cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: cvk-drain-direct-delete
  namespace: ${device_namespace}
spec:
  nodeName: ${managed_node}
  terminationGracePeriodSeconds: 0
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
kubectl patch --as="$manager_username" pod cvk-drain-direct-delete \
  --namespace "$device_namespace" --type=merge \
  -p "{\"metadata\":{\"annotations\":{\"ops.cisco.vk/drain-session\":\"${drain_session_a}\"},\"finalizers\":[\"ops.cisco.vk/iosxe-rollout-drain\"]}}" \
  >/dev/null
cat >"$scratch_dir/drain-direct-delete-eviction.yaml" <<EOF
{"apiVersion":"policy/v1","kind":"Eviction","metadata":{
  "name":"cvk-drain-direct-delete","namespace":"${device_namespace}"},
  "deleteOptions":{"gracePeriodSeconds":0}}
EOF
kubectl create --as="$manager_username" \
  --raw="/api/v1/namespaces/${device_namespace}/pods/cvk-drain-direct-delete/eviction" \
  -f "$scratch_dir/drain-direct-delete-eviction.yaml" >/dev/null
test -n "$(kubectl get pod cvk-drain-direct-delete \
  --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"
drain_direct_delete_uid="$(kubectl get pod cvk-drain-direct-delete \
  --namespace "$device_namespace" -o jsonpath='{.metadata.uid}')"
drain_direct_delete_options="{\"apiVersion\":\"v1\",\"kind\":\"DeleteOptions\",\"gracePeriodSeconds\":0,\"preconditions\":{\"uid\":\"${drain_direct_delete_uid}\"}}"

if raw_pod_delete "$worker_username" cvk-drain-direct-delete "$drain_direct_delete_options" \
    >"$scratch_dir/drain-pod-direct-delete-negative.txt" 2>&1; then
  echo "non-manager directly deleted a terminating drain-protected Pod" >&2
  exit 1
fi
grep -Eq 'direct deletion of a protected Pod|denied the request|failed expression' \
  "$scratch_dir/drain-pod-direct-delete-negative.txt"
if raw_pod_delete "$manager_username" cvk-drain-direct-delete "$drain_direct_delete_options" \
    >"$scratch_dir/drain-pod-manager-delete-negative.txt" 2>&1; then
  echo "topology manager directly deleted a terminating drain-protected Pod" >&2
  exit 1
fi
grep -Eq 'direct deletion of a protected Pod|only the topology manager or exact native|denied the request|failed expression' \
  "$scratch_dir/drain-pod-manager-delete-negative.txt" || {
  cat "$scratch_dir/drain-pod-manager-delete-negative.txt" >&2
  exit 1
}
test -n "$(kubectl get pod cvk-drain-direct-delete \
  --namespace "$device_namespace" -o jsonpath='{.metadata.deletionTimestamp}')"
kubectl patch --as="$manager_username" pod cvk-drain-direct-delete \
  --namespace "$device_namespace" --type=merge \
  -p '{"metadata":{"annotations":{"ops.cisco.vk/drain-session":null},"finalizers":[]}}' \
  >/dev/null
kubectl wait --for=delete pod/cvk-drain-direct-delete \
  --namespace "$device_namespace" --timeout=30s >/dev/null

# Exercise the only supported disruptive path through the real policy/v1
# Eviction endpoint. One healthy Pod is PDB-permitted; a second is protected
# by minAvailable. Successful eviction leaves the manager finalizer in place
# until exact cleanup, while a 429 must leave the blocked Pod untouched.
for drain_case in permitted blocked; do
  cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: cvk-drain-${drain_case}
  namespace: ${device_namespace}
  labels:
    cvk-topology-test/drain-case: ${drain_case}
spec:
  nodeName: ${managed_node}
  terminationGracePeriodSeconds: 1
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
  kubectl patch pod "cvk-drain-${drain_case}" \
    --namespace "$device_namespace" --subresource=status --type=merge \
    -p '{"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True","lastTransitionTime":"2026-01-01T00:00:00Z"}]}}' \
    >/dev/null
done
cat <<EOF | kubectl create -f - >/dev/null
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: cvk-drain-permitted
  namespace: ${device_namespace}
spec:
  minAvailable: 0
  selector:
    matchLabels:
      cvk-topology-test/drain-case: permitted
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: cvk-drain-blocked
  namespace: ${device_namespace}
spec:
  minAvailable: 1
  selector:
    matchLabels:
      cvk-topology-test/drain-case: blocked
EOF
for _ in $(seq 1 40); do
  permitted_disruptions="$(kubectl get pdb cvk-drain-permitted \
    --namespace "$device_namespace" -o jsonpath='{.status.disruptionsAllowed}')"
  blocked_disruptions="$(kubectl get pdb cvk-drain-blocked \
    --namespace "$device_namespace" -o jsonpath='{.status.disruptionsAllowed}')"
  if [ "$permitted_disruptions" = "1" ] && [ "$blocked_disruptions" = "0" ]; then
    break
  fi
  sleep 0.25
done
test "$permitted_disruptions" = "1"
test "$blocked_disruptions" = "0"
for drain_case in permitted blocked; do
  kubectl patch --as="$manager_username" pod "cvk-drain-${drain_case}" \
    --namespace "$device_namespace" --type=merge \
    -p "{\"metadata\":{\"annotations\":{\"ops.cisco.vk/drain-session\":\"${drain_session_a}\"},\"finalizers\":[\"ops.cisco.vk/iosxe-rollout-drain\"]}}" \
    >/dev/null
done
cat >"$scratch_dir/drain-eviction-permitted.yaml" <<EOF
{"apiVersion":"policy/v1","kind":"Eviction","metadata":{
  "name":"cvk-drain-permitted","namespace":"${device_namespace}"},
  "deleteOptions":{"gracePeriodSeconds":0}}
EOF
kubectl create --as="$manager_username" \
  --raw="/api/v1/namespaces/${device_namespace}/pods/cvk-drain-permitted/eviction" \
  -f "$scratch_dir/drain-eviction-permitted.yaml" >/dev/null
test -n "$(kubectl get pod cvk-drain-permitted --namespace "$device_namespace" \
  -o jsonpath='{.metadata.deletionTimestamp}')"
test "$(kubectl get pod cvk-drain-permitted --namespace "$device_namespace" \
  -o jsonpath='{.metadata.annotations.ops\.cisco\.vk/drain-session}')" = "$drain_session_a"
kubectl patch --as="$manager_username" pod cvk-drain-permitted \
  --namespace "$device_namespace" --type=merge \
  -p '{"metadata":{"annotations":{"ops.cisco.vk/drain-session":null},"finalizers":[]}}' \
  >/dev/null
kubectl wait --for=delete pod/cvk-drain-permitted \
  --namespace "$device_namespace" --timeout=30s >/dev/null

cat >"$scratch_dir/drain-eviction-blocked.yaml" <<EOF
{"apiVersion":"policy/v1","kind":"Eviction","metadata":{
  "name":"cvk-drain-blocked","namespace":"${device_namespace}"}}
EOF
if kubectl create --as="$manager_username" \
    --raw="/api/v1/namespaces/${device_namespace}/pods/cvk-drain-blocked/eviction" \
    -f "$scratch_dir/drain-eviction-blocked.yaml" \
    >"$scratch_dir/drain-eviction-blocked.txt" 2>&1; then
  echo "PDB-blocked Pod eviction unexpectedly succeeded" >&2
  exit 1
fi
grep -Eq 'Cannot evict pod|disruption budget|Too Many Requests|429' \
  "$scratch_dir/drain-eviction-blocked.txt"
test -z "$(kubectl get pod cvk-drain-blocked --namespace "$device_namespace" \
  -o jsonpath='{.metadata.deletionTimestamp}')"
test "$(kubectl get pod cvk-drain-blocked --namespace "$device_namespace" \
  -o jsonpath='{.metadata.annotations.ops\.cisco\.vk/drain-session}')" = "$drain_session_a"
test "$(kubectl get pod cvk-drain-blocked --namespace "$device_namespace" \
  -o jsonpath='{.metadata.finalizers[0]}')" = "ops.cisco.vk/iosxe-rollout-drain"
kubectl patch --as="$manager_username" pod cvk-drain-blocked \
  --namespace "$device_namespace" --type=merge \
  -p '{"metadata":{"annotations":{"ops.cisco.vk/drain-session":null},"finalizers":[]}}' \
  >/dev/null

# Exercise manager/worker ownership and the immutable drain ledger on an exact
# manager-created leaf. The complete candidate/PDB snapshot is published once;
# only progress may change afterward, and worker inventory may lag a newer
# recovery control revision while remaining bound to the exact session.
device_generation="$(kubectl get ciscodevice device-a --namespace "$device_namespace" \
  -o jsonpath='{.metadata.generation}')"
policy_uid_now="$(kubectl get configmap "${admission_prefix}-topology-policy" \
  --namespace "$system_namespace" -o jsonpath='{.metadata.uid}')"
policy_resource_version_now="$(kubectl get configmap "${admission_prefix}-topology-policy" \
  --namespace "$system_namespace" -o jsonpath='{.metadata.resourceVersion}')"
ledger_uid_now="$(kubectl get configmap "${admission_prefix}-topology-ledger" \
  --namespace "$system_namespace" -o jsonpath='{.metadata.uid}')"
drain_pod_uid="$(kubectl get pod cvk-worker-own --namespace "$device_namespace" \
  -o jsonpath='{.metadata.uid}')"
cat >"$scratch_dir/managed-drain-leaf.yaml" <<EOF
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: managed-drain-probe
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/device-generation: "${device_generation}"
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/node-uid: ${managed_node_uid}
    topology.cisco.vk/worker-username: ${worker_username}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/campaign-namespace: ${device_namespace}
    topology.cisco.vk/campaign-name: integration-rollout
    topology.cisco.vk/campaign-uid: 88888888-8888-4888-8888-888888888888
    topology.cisco.vk/plan-hash: sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    topology.cisco.vk/ledger-uid: ${ledger_uid_now}
    topology.cisco.vk/reservation-id: reservation-drain-probe
spec:
  deviceRef:
    name: device-a
  imageSource:
    url: https://images.example.test/cat9k.bin
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  targetVersion: 17.18.4
EOF
kubectl create --as="$manager_username" -f "$scratch_dir/managed-drain-leaf.yaml" >/dev/null
drain_leaf_uid="$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" -o jsonpath='{.metadata.uid}')"
cat >"$scratch_dir/manager-drain-status.json" <<EOF
{
  "status": {
    "managerAdmission": {
      "state": "Pending",
      "protocolVersion": "rollout-v1",
      "campaignUID": "88888888-8888-4888-8888-888888888888",
      "planHash": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "policyUID": "${policy_uid_now}",
      "policyResourceVersion": "${policy_resource_version_now}",
      "policyEpoch": 1,
      "ledgerUID": "${ledger_uid_now}",
      "reservationID": "reservation-drain-probe",
      "topologyLockID": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "leafUID": "${drain_leaf_uid}",
      "deviceUID": "${device_uid}",
      "deviceGeneration": ${device_generation},
      "physicalIdentity": "integration-serial-managed",
      "nodeUID": "${managed_node_uid}",
      "controlRevision": 0,
      "updatedAt": "2026-01-01T00:00:00Z"
    },
    "managerControl": {
      "revision": 0,
      "updatedAt": "2026-01-01T00:00:00Z"
    },
    "managerDrain": {
      "protocolVersion": "pdb-drain-v1",
      "state": "Preparing",
      "sessionToken": "${drain_session_a}",
      "reservationID": "reservation-drain-probe",
      "policyEpoch": 1,
      "controlRevision": 0,
      "nodeUID": "${managed_node_uid}",
      "nodeUnschedulableBefore": false,
      "maintenanceTaintPresentBefore": false,
      "startedAt": "2026-01-01T00:00:00Z",
      "drainDeadline": "2026-01-01T00:10:00Z",
      "updatedAt": "2026-01-01T00:00:00Z",
      "pods": [{
        "namespace": "${device_namespace}",
        "name": "cvk-worker-own",
        "uid": "${drain_pod_uid}",
        "eligibilityHash": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        "controller": {
          "apiVersion": "apps/v1",
          "kind": "ReplicaSet",
          "namespace": "${device_namespace}",
          "name": "integration-rs",
          "uid": "99999999-9999-4999-8999-999999999999",
          "generation": 1
        },
        "workloadController": {
          "apiVersion": "apps/v1",
          "kind": "Deployment",
          "namespace": "${device_namespace}",
          "name": "integration-deployment",
          "uid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
          "generation": 1
        },
        "pdbs": [{
          "apiVersion": "policy/v1",
          "kind": "PodDisruptionBudget",
          "namespace": "${device_namespace}",
          "name": "integration-pdb",
          "uid": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
          "generation": 1,
          "observedGeneration": 1,
          "disruptionsAllowed": 1,
          "currentHealthy": 1,
          "desiredHealthy": 0,
          "expectedPods": 1
        }],
        "terminationGracePeriodSeconds": 30,
        "phase": "Selected"
      }]
    }
  }
}
EOF
sed "s/${drain_session_a}/55555555-5555-1555-8555-555555555555/" \
  "$scratch_dir/manager-drain-status.json" >"$scratch_dir/manager-drain-v1-token.json"
if kubectl patch --as="$manager_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --patch-file "$scratch_dir/manager-drain-v1-token.json" --dry-run=server \
    >"$scratch_dir/manager-drain-v1-token-negative.txt" 2>&1; then
  echo "manager drain accepted a non-v4 session token" >&2
  exit 1
fi
grep -Eq 'sessionToken|Invalid value|denied (the )?request|failed rule' \
  "$scratch_dir/manager-drain-v1-token-negative.txt"
kubectl patch --as="$manager_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge \
  --patch-file "$scratch_dir/manager-drain-status.json" >/dev/null
if kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"managerDrain":{"state":"Guarded"}}}' \
    >"$scratch_dir/worker-manager-drain-negative.txt" 2>&1; then
  echo "managed worker changed managerDrain" >&2
  exit 1
fi
grep -Eq 'managerDrain status are manager-owned|denied the request|failed expression' \
  "$scratch_dir/worker-manager-drain-negative.txt"
if kubectl patch --as="$manager_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=json --dry-run=server \
    -p '[{"op":"replace","path":"/status/managerDrain/pods/0/eligibilityHash","value":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}]' \
    >"$scratch_dir/manager-drain-snapshot-negative.txt" 2>&1; then
  echo "manager changed the frozen drain eligibility digest" >&2
  exit 1
fi
grep -Eq 'eligibility snapshot is immutable|denied (the )?request|failed rule' \
  "$scratch_dir/manager-drain-snapshot-negative.txt"
if kubectl patch --as="$manager_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"managerDrain":{"pods":[]}}}' \
    >"$scratch_dir/manager-drain-selection-negative.txt" 2>&1; then
  echo "manager removed the immutable drain Pod selection" >&2
  exit 1
fi
grep -Eq 'Pod entries cannot be removed|denied (the )?request|failed rule' \
  "$scratch_dir/manager-drain-selection-negative.txt"
kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"workerControl\":{
      \"observedAdmissionState\":\"Pending\",
      \"observedPolicyEpoch\":1,
      \"observedControlRevision\":0,
      \"observedWorkerConfigRevision\":\"${worker_revision}\",
      \"effectiveState\":\"Denied\",
      \"updatedAt\":\"2026-01-01T00:01:00Z\"}}}" >/dev/null
kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"workerDrain\":{
      \"protocolVersion\":\"pdb-drain-v1\",
      \"observedSessionToken\":\"${drain_session_a}\",
      \"observedPolicyEpoch\":1,
      \"observedControlRevision\":0,
      \"observedWorkerConfigRevision\":\"${worker_revision}\",
      \"inventoryRevision\":1,
      \"inventoryObservedAt\":\"2026-01-01T00:02:00Z\",
      \"inventoryComplete\":true,
      \"remainingAuthorizedPodUIDs\":[\"${drain_pod_uid}\"],
      \"updatedAt\":\"2026-01-01T00:02:00Z\"}}}" >/dev/null
if kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"workerDrain":{"inventoryComplete":false,"unknownDeviceWorkloadCount":1}}}' \
    >"$scratch_dir/worker-drain-same-revision-evidence-negative.txt" 2>&1; then
  echo "workerDrain changed inventory evidence without advancing inventoryRevision" >&2
  exit 1
fi
grep -Eq 'strictly newer inventory revision|denied (the )?request|failed rule' \
  "$scratch_dir/worker-drain-same-revision-evidence-negative.txt"
if kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"workerDrain":{"remainingAuthorizedPodUIDs":["foreign-pod-uid"]}}}' \
    >"$scratch_dir/worker-drain-subset-negative.txt" 2>&1; then
  echo "workerDrain claimed a Pod outside the immutable manager snapshot" >&2
  exit 1
fi
grep -Eq 'subset of the frozen manager snapshot|managerDrain Pod|denied the request|failed expression' \
  "$scratch_dir/worker-drain-subset-negative.txt"
# A live worker revision can rotate while the previous inventory remains
# durable but stale. It is no longer proof until a fresh inventory observation
# advances both its revision and timestamps under the new WorkerControl.
kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"workerControl\":{
      \"observedAdmissionState\":\"Pending\",
      \"observedPolicyEpoch\":1,
      \"observedControlRevision\":0,
      \"observedWorkerConfigRevision\":\"${worker_revision_rotated}\",
      \"effectiveState\":\"Denied\",
      \"updatedAt\":\"2026-01-01T00:02:30Z\"}}}" >/dev/null
test "$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" \
  -o jsonpath='{.status.workerDrain.observedWorkerConfigRevision}')" = "$worker_revision"
if kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p "{\"status\":{\"workerDrain\":{
      \"observedWorkerConfigRevision\":\"${worker_revision_rotated}\"}}}" \
    >"$scratch_dir/worker-drain-rotation-without-inventory-negative.txt" 2>&1; then
  echo "workerDrain changed configuration revision without a fresh inventory" >&2
  exit 1
fi
grep -Eq 'strictly newer inventory observation|denied (the )?request|failed rule' \
  "$scratch_dir/worker-drain-rotation-without-inventory-negative.txt"
if kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p "{\"status\":{\"workerDrain\":{
      \"observedWorkerConfigRevision\":\"${worker_revision_rotated}\",
      \"inventoryRevision\":2}}}" \
    >"$scratch_dir/worker-drain-rotation-without-time-negative.txt" 2>&1; then
  echo "workerDrain changed configuration revision without newer observation times" >&2
  exit 1
fi
grep -Eq 'newer observation timestamps|strictly newer inventory observation|denied (the )?request|failed rule' \
  "$scratch_dir/worker-drain-rotation-without-time-negative.txt"
kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"workerDrain\":{
      \"observedWorkerConfigRevision\":\"${worker_revision_rotated}\",
      \"inventoryRevision\":2,
      \"inventoryObservedAt\":\"2026-01-01T00:03:00Z\",
      \"updatedAt\":\"2026-01-01T00:03:00Z\"}}}" >/dev/null
test "$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" -o jsonpath='{.status.workerDrain.inventoryRevision}')" = "2"
test "$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" \
  -o jsonpath='{.status.workerDrain.observedWorkerConfigRevision}')" = "$worker_revision_rotated"
# A manager cancellation advances control and enters bounded recovery while the
# last worker inventory observation legitimately remains at the older revision.
kubectl patch --as="$manager_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p '{
    "status":{
      "managerControl":{"revision":1,"cancel":true,"updatedAt":"2026-01-01T00:05:00Z"},
      "managerDrain":{"state":"Recovering","controlRevision":1,
        "recoveryDeadline":"2026-01-01T00:20:00Z","updatedAt":"2026-01-01T00:05:00Z"}
    }}' >/dev/null
kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"workerControl\":{
      \"observedAdmissionState\":\"Pending\",
      \"observedPolicyEpoch\":1,
      \"observedControlRevision\":1,
      \"observedWorkerConfigRevision\":\"${worker_revision_recovery}\",
      \"effectiveState\":\"Cancelled\",
      \"updatedAt\":\"2026-01-01T00:06:00Z\"}}}" >/dev/null
test "$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" \
  -o jsonpath='{.status.workerDrain.observedWorkerConfigRevision}')" = "$worker_revision_rotated"
kubectl patch --as="$worker_username" iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"workerDrain\":{
      \"observedControlRevision\":1,
      \"observedWorkerConfigRevision\":\"${worker_revision_recovery}\",
      \"inventoryRevision\":3,
      \"inventoryObservedAt\":\"2026-01-01T00:07:00Z\",
      \"updatedAt\":\"2026-01-01T00:07:00Z\"}}}" >/dev/null
test "$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" -o jsonpath='{.status.workerDrain.inventoryRevision}')" = "3"
test "$(kubectl get iosxesoftwareupgrade managed-drain-probe \
  --namespace "$device_namespace" \
  -o jsonpath='{.status.workerDrain.observedWorkerConfigRevision}')" = "$worker_revision_recovery"
if kubectl patch --as="$manager_username" iosxesoftwareupgrade managed-drain-probe \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"workerDrain":{"inventoryRevision":4,
      "inventoryObservedAt":"2026-01-01T00:08:00Z","updatedAt":"2026-01-01T00:08:00Z"}}}' \
    >"$scratch_dir/manager-worker-drain-negative.txt" 2>&1; then
  echo "topology manager changed workerDrain" >&2
  exit 1
fi
grep -Eq 'workerDrain.*worker-owned|denied the request|failed expression' \
  "$scratch_dir/manager-worker-drain-negative.txt"

# Every generated-worker Lease request is fenced even before annotations exist,
# so a worker cannot create or squat an arbitrary coordination object.
test "$(kubectl auth can-i create leases.coordination.k8s.io \
  --namespace "$device_namespace" --as="$worker_username")" = "no"
test "$(kubectl auth can-i delete leases.coordination.k8s.io \
  --namespace "$device_namespace" --as="$worker_username")" = "no"
test "$(kubectl auth can-i update leases.coordination.k8s.io \
  --namespace "$device_namespace" --as="$worker_username")" = "yes"

config_family="vlan"
config_hash="$(printf '%s/%s' "$device_key" "$config_family" | sha256_stdin | cut -c1-8)"
config_lease="cvk-${device_key}-${config_family}-${config_hash}"
cat >"$scratch_dir/config-lease.yaml" <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: ${config_lease}
  namespace: ${device_namespace}
  labels:
    cisco.vk/device: ${device_key}
    cisco.vk/family: ${config_family}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/retain-lease: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/node-uid: ${managed_node_uid}
    topology.cisco.vk/worker-username: ${worker_username}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/lease-purpose: config-family
spec: {}
EOF
kubectl create --as="$manager_username" -f "$scratch_dir/config-lease.yaml" >/dev/null
kubectl patch --as="$worker_username" lease "$config_lease" \
  --namespace "$device_namespace" --type=merge -p '{"spec":{
    "holderIdentity":"cvk-topology-test/integration-config#33333333-3333-4333-8333-333333333333",
    "leaseDurationSeconds":30,"acquireTime":"2026-01-01T00:00:00.000000Z",
    "renewTime":"2026-01-01T00:00:00.000000Z","leaseTransitions":1}}' >/dev/null
kubectl patch --as="$worker_username" lease "$config_lease" \
  --namespace "$device_namespace" --type=merge --dry-run=server \
  -p '{"spec":{"renewTime":"2026-01-01T00:00:01.000000Z"}}' >/dev/null
if kubectl patch --as="$worker_username" lease "$config_lease" \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"annotations":{"topology.cisco.vk/node-uid":"forged"}}}' \
    >"$scratch_dir/lease-binding-negative.txt" 2>&1; then
  echo "managed worker changed its Lease binding" >&2
  exit 1
fi
grep -Eq 'managed Lease|managed worker may not change|bound worker may only|denied the request|failed expression|forbidden' \
  "$scratch_dir/lease-binding-negative.txt" || {
    cat "$scratch_dir/lease-binding-negative.txt" >&2
    exit 1
  }

mutation_family="device-disruptive-mutation"
mutation_hash="$(printf '%s/%s' "$device_key" "$mutation_family" | sha256_stdin | cut -c1-8)"
mutation_lease="cvk-${device_key}-${mutation_family}-${mutation_hash}"
sed -e "s/name: ${config_lease}/name: ${mutation_lease}/" \
  -e "s/cisco.vk\/family: ${config_family}/cisco.vk\/family: ${mutation_family}/" \
  -e 's/lease-purpose: config-family/lease-purpose: device-mutation/' \
  "$scratch_dir/config-lease.yaml" >"$scratch_dir/mutation-lease.yaml"
# A preexisting unowned object carrying any maintenance protocol field is not a
# wholly idle legacy Lease and cannot be adopted into the managed trust domain.
cat <<EOF | kubectl create -f - >/dev/null
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: ${mutation_lease}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/maintenance-purpose: WorkloadDrain
spec: {}
EOF
if kubectl apply --server-side --force-conflicts \
    --field-manager=cvk-topology-adoption-negative --as="$manager_username" \
    --dry-run=server -f "$scratch_dir/mutation-lease.yaml" \
    >"$scratch_dir/lease-adopt-maintenance-negative.txt" 2>&1; then
  echo "topology manager adopted a Lease carrying stale maintenance purpose" >&2
  exit 1
fi
grep -Eq 'safely adopt|complete protocol/purpose|denied the request|failed expression' \
  "$scratch_dir/lease-adopt-maintenance-negative.txt"
kubectl delete lease "$mutation_lease" --namespace "$device_namespace" >/dev/null
kubectl create --as="$manager_username" -f "$scratch_dir/mutation-lease.yaml" >/dev/null
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"spec":{
    "holderIdentity":"software-upgrade/44444444-4444-4444-8444-444444444444",
    "leaseDurationSeconds":3600,"acquireTime":"2026-01-01T00:00:00.000000Z",
    "renewTime":"2026-01-01T00:00:00.000000Z","leaseTransitions":1}}' >/dev/null
# Existing rollout-v1 requests remain backward-compatible without a purpose.
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"metadata":{"annotations":{
    "topology.cisco.vk/maintenance-request-version":"rollout-v1",
    "topology.cisco.vk/maintenance-session-token":"77777777-7777-4777-8777-777777777777",
    "topology.cisco.vk/maintenance-requested-at":"2026-01-01T00:00:01Z",
    "topology.cisco.vk/maintenance-operation-namespace":"cvk-topology-test",
    "topology.cisco.vk/maintenance-operation-name":"integration-upgrade",
    "topology.cisco.vk/maintenance-operation-uid":"44444444-4444-4444-8444-444444444444",
    "topology.cisco.vk/maintenance-control-revision":"0"}}}' >/dev/null
# Clear the legacy request and release before acquiring the same canonical
# fence for a drain session. No conflicting Lease family is introduced.
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"metadata":{"annotations":{
    "topology.cisco.vk/maintenance-request-version":null,
    "topology.cisco.vk/maintenance-session-token":null,
    "topology.cisco.vk/maintenance-requested-at":null,
    "topology.cisco.vk/maintenance-operation-namespace":null,
    "topology.cisco.vk/maintenance-operation-name":null,
    "topology.cisco.vk/maintenance-operation-uid":null,
    "topology.cisco.vk/maintenance-control-revision":null,
    "topology.cisco.vk/maintenance-purpose":null}},"spec":{
    "holderIdentity":null,"leaseDurationSeconds":null,"acquireTime":null,
    "renewTime":null}}' >/dev/null
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"spec":{
    "holderIdentity":"software-drain/44444444-4444-4444-8444-444444444444",
    "leaseDurationSeconds":3600,"acquireTime":"2026-01-01T00:00:02.000000Z",
    "renewTime":"2026-01-01T00:00:02.000000Z","leaseTransitions":2}}' >/dev/null
if kubectl patch --as="$worker_username" lease "$mutation_lease" \
    --namespace "$device_namespace" --type=merge --dry-run=server -p '{"metadata":{"annotations":{
      "topology.cisco.vk/maintenance-request-version":"pdb-drain-v1",
      "topology.cisco.vk/maintenance-session-token":"55555555-5555-1555-8555-555555555555",
      "topology.cisco.vk/maintenance-requested-at":"2026-01-01T00:00:03Z",
      "topology.cisco.vk/maintenance-operation-namespace":"cvk-topology-test",
      "topology.cisco.vk/maintenance-operation-name":"integration-upgrade",
      "topology.cisco.vk/maintenance-operation-uid":"44444444-4444-4444-8444-444444444444",
      "topology.cisco.vk/maintenance-control-revision":"0",
      "topology.cisco.vk/maintenance-purpose":"WorkloadDrain"}}}' \
    >"$scratch_dir/lease-drain-uuid-negative.txt" 2>&1; then
  echo "managed mutation Lease accepted a non-v4 drain session token" >&2
  exit 1
fi
grep -Eq 'complete protocol/purpose|denied the request|failed expression' \
  "$scratch_dir/lease-drain-uuid-negative.txt"
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p "{\"metadata\":{\"annotations\":{
    \"topology.cisco.vk/maintenance-request-version\":\"pdb-drain-v1\",
    \"topology.cisco.vk/maintenance-session-token\":\"${drain_session_a}\",
    \"topology.cisco.vk/maintenance-requested-at\":\"2026-01-01T00:00:03Z\",
    \"topology.cisco.vk/maintenance-operation-namespace\":\"${device_namespace}\",
    \"topology.cisco.vk/maintenance-operation-name\":\"integration-upgrade\",
    \"topology.cisco.vk/maintenance-operation-uid\":\"44444444-4444-4444-8444-444444444444\",
    \"topology.cisco.vk/maintenance-control-revision\":\"0\",
    \"topology.cisco.vk/maintenance-purpose\":\"WorkloadDrain\"}}}" >/dev/null
if kubectl patch --as="$worker_username" lease "$mutation_lease" \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"annotations":{"topology.cisco.vk/maintenance-purpose":"SoftwareMutation"}}}' \
    >"$scratch_dir/lease-drain-purpose-negative.txt" 2>&1; then
  echo "software-drain Lease accepted SoftwareMutation purpose" >&2
  exit 1
fi
grep -Eq 'complete protocol/purpose|identity is immutable|denied the request|failed expression' \
  "$scratch_dir/lease-drain-purpose-negative.txt"
if kubectl patch --as="$worker_username" lease "$mutation_lease" \
    --namespace "$device_namespace" --type=merge --dry-run=server -p '{
      "metadata":{"annotations":{"topology.cisco.vk/maintenance-purpose":"SoftwareMutation"}},
      "spec":{"holderIdentity":"software-upgrade/44444444-4444-4444-8444-444444444444"}}' \
    >"$scratch_dir/lease-direct-promotion-negative.txt" 2>&1; then
  echo "mutation Lease allowed direct held drain-to-software promotion" >&2
  exit 1
fi
grep -Eq 'bound worker may only|identity is immutable|denied the request|failed expression' \
  "$scratch_dir/lease-direct-promotion-negative.txt"
# Promotion releases then re-acquires the same mutation fence for the same
# operation/session before publishing SoftwareMutation purpose.
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"metadata":{"annotations":{
    "topology.cisco.vk/maintenance-request-version":null,
    "topology.cisco.vk/maintenance-session-token":null,
    "topology.cisco.vk/maintenance-requested-at":null,
    "topology.cisco.vk/maintenance-operation-namespace":null,
    "topology.cisco.vk/maintenance-operation-name":null,
    "topology.cisco.vk/maintenance-operation-uid":null,
    "topology.cisco.vk/maintenance-control-revision":null,
    "topology.cisco.vk/maintenance-purpose":null}},"spec":{
    "holderIdentity":null,"leaseDurationSeconds":null,"acquireTime":null,
    "renewTime":null}}' >/dev/null
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"spec":{
    "holderIdentity":"software-upgrade/44444444-4444-4444-8444-444444444444",
    "leaseDurationSeconds":3600,"acquireTime":"2026-01-01T00:00:04.000000Z",
    "renewTime":"2026-01-01T00:00:04.000000Z","leaseTransitions":3}}' >/dev/null
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p "{\"metadata\":{\"annotations\":{
    \"topology.cisco.vk/maintenance-request-version\":\"pdb-drain-v1\",
    \"topology.cisco.vk/maintenance-session-token\":\"${drain_session_a}\",
    \"topology.cisco.vk/maintenance-requested-at\":\"2026-01-01T00:00:05Z\",
    \"topology.cisco.vk/maintenance-operation-namespace\":\"${device_namespace}\",
    \"topology.cisco.vk/maintenance-operation-name\":\"integration-upgrade\",
    \"topology.cisco.vk/maintenance-operation-uid\":\"44444444-4444-4444-8444-444444444444\",
    \"topology.cisco.vk/maintenance-control-revision\":\"1\",
    \"topology.cisco.vk/maintenance-purpose\":\"SoftwareMutation\"}}}" >/dev/null

cat >"$scratch_dir/heartbeat-lease.yaml" <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: ${managed_node}
  namespace: kube-node-lease
  labels:
    cisco.vk/device: ${device_key}
    cisco.vk/family: node-heartbeat
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/retain-lease: "true"
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/node-uid: ${managed_node_uid}
    topology.cisco.vk/worker-username: ${worker_username}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/lease-purpose: node-heartbeat
  ownerReferences:
    - apiVersion: v1
      kind: Node
      name: ${managed_node}
      uid: ${managed_node_uid}
spec:
  holderIdentity: ${managed_node}
  leaseDurationSeconds: 40
EOF
# Exercise the manager's exact empty-heartbeat adoption path. This preserves
# the existing Lease UID while atomically adding the Node owner/bindings and
# initializing holderIdentity/duration; workers may only advance renewTime.
kubectl create -f - >/dev/null <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: ${managed_node}
  namespace: kube-node-lease
spec: {}
EOF
kubectl apply --server-side --field-manager=cvk-topology-adoption \
  --as="$manager_username" -f "$scratch_dir/heartbeat-lease.yaml" >/dev/null
kubectl patch --as="$worker_username" lease "$managed_node" \
  --namespace kube-node-lease --type=merge \
  -p '{"spec":{"renewTime":"2026-01-01T00:00:00.000000Z"}}' >/dev/null

# Temporarily grant create/delete only to prove admission still denies them;
# the production managed-worker role itself deliberately lacks both verbs.
kubectl create role lease-adversary --namespace "$device_namespace" \
  --verb=create,delete --resource=leases.coordination.k8s.io >/dev/null
kubectl create rolebinding lease-adversary --namespace "$device_namespace" \
  --role=lease-adversary \
  --serviceaccount="${device_namespace}:${worker_service_account}" >/dev/null
cat >"$scratch_dir/squat-lease.yaml" <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: arbitrary-worker-lease
  namespace: ${device_namespace}
spec: {}
EOF
if kubectl create --as="$worker_username" -f "$scratch_dir/squat-lease.yaml" \
    >"$scratch_dir/lease-negative.txt" 2>&1; then
  echo "managed worker created an arbitrary Lease" >&2
  exit 1
fi
grep -Eq 'only the manager may create|denied the request|failed expression' \
  "$scratch_dir/lease-negative.txt"
if kubectl delete --as="$worker_username" lease "$config_lease" \
    --namespace "$device_namespace" --dry-run=server \
    >"$scratch_dir/lease-delete-negative.txt" 2>&1; then
  echo "managed worker deleted a manager-bound Lease" >&2
  exit 1
fi
grep -Eq 'only the manager may create|denied the request|failed expression' \
  "$scratch_dir/lease-delete-negative.txt"
kubectl delete rolebinding lease-adversary --namespace "$device_namespace" >/dev/null
kubectl delete role lease-adversary --namespace "$device_namespace" >/dev/null
kubectl create -f - >/dev/null <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: unmarked-peer
  namespace: ${device_namespace}
spec: {}
EOF
if kubectl patch --as="$worker_username" lease unmarked-peer \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"spec":{"holderIdentity":"forged"}}' \
    >"$scratch_dir/lease-peer-negative.txt" 2>&1; then
  echo "managed worker updated an unbound Lease" >&2
  exit 1
fi
grep -Eq 'only the manager may create|denied the request|failed expression' \
  "$scratch_dir/lease-peer-negative.txt"

# Prove that ordinary CiscoDevice editors may change unrelated labels but not
# the administrator-owned topology/risk contract.
kubectl create serviceaccount device-editor --namespace "$device_namespace" >/dev/null
kubectl create role device-editor --namespace "$device_namespace" \
  --verb=create,get,list,watch,update,patch,delete \
  --resource=ciscodevices.cisco.vk,ciscodevices.cisco.vk/status >/dev/null
kubectl create rolebinding device-editor --namespace "$device_namespace" \
  --role=device-editor --serviceaccount="${device_namespace}:device-editor" >/dev/null
kubectl create role managed-status-writer --namespace "$device_namespace" \
  --verb=get,update,patch --resource=ciscodevices.cisco.vk,ciscodevices.cisco.vk/status >/dev/null
kubectl create rolebinding managed-status-writer --namespace "$device_namespace" \
  --role=managed-status-writer \
  --serviceaccount="${system_namespace}:cisco-virtual-kubelet-controller" >/dev/null
device_editor_username="system:serviceaccount:${device_namespace}:device-editor"

# This is the access-before-marker crash window: the generated ownerless CRB
# already grants cluster-wide legacy worker authority, while neither status nor
# the isolated-worker marker is durable yet. The cleanup finalizer must remain
# manager-owned even though the later lifecycle coordinates are all absent.
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"finalizers":[]}}' \
    >"$scratch_dir/device-phase-zero-finalizer-negative.txt" 2>&1; then
  echo "ordinary device editor removed the cleanup finalizer during access-before-marker recovery" >&2
  exit 1
fi
grep -Eq 'only the manager may remove the CiscoDevice cleanup finalizer|denied the request|failed expression' \
  "$scratch_dir/device-phase-zero-finalizer-negative.txt"

# Existing pre-feature objects may populate a previously absent physical
# identity exactly once before enrollment. This migration path must stay open
# even though replacement/removal is rejected after the first write.
cat >"$scratch_dir/device-identity-bootstrap.yaml" <<EOF
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: device-identity-bootstrap
  namespace: ${device_namespace}
spec:
  nodeName: device-identity-bootstrap
  driver: XE
  address: 192.0.2.91
  port: 443
  username: integration
  credentialSecretRef:
    name: unused-integration-credential
  tls:
    enabled: true
    insecureSkipVerify: true
  maxPods: 0
  xe:
    networking:
      interface:
        type: Management
        management:
          dhcp: true
EOF
kubectl create --as="$device_editor_username" \
  -f "$scratch_dir/device-identity-bootstrap.yaml" >/dev/null
kubectl patch --as="$device_editor_username" ciscodevice device-identity-bootstrap \
  --namespace "$device_namespace" --type=merge \
  -p '{"spec":{"physicalIdentity":"Bootstrap-Serial-01"}}' >/dev/null
test "$(kubectl get ciscodevice device-identity-bootstrap \
  --namespace "$device_namespace" -o jsonpath='{.spec.physicalIdentity}')" = \
  "Bootstrap-Serial-01"
if kubectl patch --as="$device_editor_username" \
    ciscodevice device-identity-bootstrap --namespace "$device_namespace" \
    --type=merge --dry-run=server -p '{"spec":{"physicalIdentity":null}}' \
    >"$scratch_dir/device-physical-identity-remove-negative.txt" 2>&1; then
  echo "ordinary device editor removed a write-once physical identity" >&2
  exit 1
fi
grep -Eq 'physicalIdentity is write-once|denied the request|failed expression' \
  "$scratch_dir/device-physical-identity-remove-negative.txt"

# Preserve legacy status compatibility before enrollment, but never let that
# compatibility path manufacture manager-owned authority. Each forged object
# below is schema-valid so denial is attributable to native admission.
bootstrap_device_uid="$(kubectl get ciscodevice device-identity-bootstrap \
  --namespace "$device_namespace" -o jsonpath='{.metadata.uid}')"
if kubectl patch --as="$manager_username" ciscodevice device-identity-bootstrap \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p "{\"status\":{\"legacyHandoff\":{\"phase\":\"Complete\",\"deviceUID\":\"${bootstrap_device_uid}\",\"nodeName\":\"${legacy_node}\",\"nodeUID\":\"${legacy_node_uid}\",\"projectionHash\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"legacyWorkerUsername\":\"${legacy_username}\",\"requestedAt\":\"2026-01-01T00:00:00Z\",\"nodeReleasedAt\":\"2026-01-01T00:00:01Z\",\"isolatedReadyAt\":\"2026-01-01T00:00:02Z\",\"completedAt\":\"2026-01-01T00:00:03Z\"}}}" \
    >"$scratch_dir/device-handoff-first-status-phase-negative.txt" 2>&1; then
  echo "manager inserted a first legacy handoff status beyond Preparing" >&2
  exit 1
fi
grep -Fq 'a legacy handoff must begin in Preparing phase' \
  "$scratch_dir/device-handoff-first-status-phase-negative.txt"
kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"phase":"Ready"}}' >/dev/null
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p "{\"status\":{\"legacyHandoff\":{\"phase\":\"Complete\",\"deviceUID\":\"${legacy_device_uid}\",\"nodeName\":\"${legacy_node}\",\"nodeUID\":\"11111111-1111-4111-8111-111111111111\",\"projectionHash\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"legacyWorkerUsername\":\"${legacy_username}\",\"requestedAt\":\"2026-01-01T00:00:00Z\",\"nodeReleasedAt\":\"2026-01-01T00:00:01Z\",\"isolatedReadyAt\":\"2026-01-01T00:00:02Z\",\"completedAt\":\"2026-01-01T00:00:03Z\"}}}" \
    >"$scratch_dir/device-legacy-handoff-forgery.txt" 2>&1; then
  echo "legacy status writer forged a completed manager handoff" >&2
  exit 1
fi
grep -Eq 'a legacy handoff must begin in Preparing phase|manager-owned CiscoDevice identity|denied the request|failed expression' \
  "$scratch_dir/device-legacy-handoff-forgery.txt"
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p "{\"status\":{\"workerRevision\":{\"desiredRevision\":\"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\",\"deploymentUID\":\"22222222-2222-4222-8222-222222222222\",\"deploymentGeneration\":1,\"observedAt\":\"2026-01-01T00:00:00Z\"},\"networkWorkerRevision\":{\"desiredRevision\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"deploymentUID\":\"33333333-3333-4333-8333-333333333333\",\"deploymentGeneration\":1,\"observedAt\":\"2026-01-01T00:00:00Z\"},\"healthObservation\":{\"observedAt\":\"2026-01-01T00:00:00Z\",\"nodeReadyHeartbeatTime\":\"2026-01-01T00:00:00Z\",\"deviceConditionsHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"},\"topologyLock\":{\"state\":\"Active\",\"policyEpoch\":1,\"acquisitionID\":\"cccccccccccccccccccccccccccccccc\",\"campaignNamespace\":\"${device_namespace}\",\"campaignName\":\"forged\",\"campaignUID\":\"22222222-2222-4222-8222-222222222222\",\"planHash\":\"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\",\"reservationID\":\"forged-reservation\",\"deviceUID\":\"${legacy_device_uid}\",\"deviceGeneration\":1,\"nodeUID\":\"11111111-1111-4111-8111-111111111111\",\"projectionHash\":\"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\",\"acquiredAt\":\"2026-01-01T00:00:00Z\"}}}" \
    >"$scratch_dir/device-manager-status-forgery.txt" 2>&1; then
  echo "legacy status writer forged manager app/network worker, health, or topology-lock authority" >&2
  exit 1
fi
grep -Eq 'manager-owned CiscoDevice identity|denied the request|failed expression' \
  "$scratch_dir/device-manager-status-forgery.txt"

# The isolated legacy identity marker selects UID-derived broad worker RBAC.
# It must be absent at user creation, and an ordinary editor cannot add it.
if kubectl create --as="$device_editor_username" --dry-run=server -f - \
    >"$scratch_dir/device-isolated-marker-create.txt" 2>&1 <<EOF; then
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: forged-isolated-device
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/isolated-legacy-worker: forged
spec:
  nodeName: forged-isolated-device
  driver: XE
  address: 192.0.2.90
  port: 443
  username: integration
  credentialSecretRef:
    name: unused-integration-credential
  tls:
    enabled: true
    insecureSkipVerify: true
  maxPods: 1
  xe:
    networking:
      interface:
        type: Management
        management:
          dhcp: true
EOF
  echo "ordinary device creator forged isolated legacy worker authority" >&2
  exit 1
fi
grep -Eq 'isolated legacy worker marker is manager-created|denied the request|failed expression' \
  "$scratch_dir/device-isolated-marker-create.txt"
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p "{\"metadata\":{\"annotations\":{\"topology.cisco.vk/isolated-legacy-worker\":\"${legacy_device_uid}\"}}}" \
    >"$scratch_dir/device-isolated-marker-update.txt" 2>&1; then
  echo "ordinary device editor enabled isolated legacy worker authority" >&2
  exit 1
fi
grep -Eq 'isolated legacy worker marker is manager-created|denied the request|failed expression' \
  "$scratch_dir/device-isolated-marker-update.txt"
# Phase-zero recovery is manager-only and UID-bound. The exact generated
# legacy ServiceAccount, RoleBinding, and ClusterRoleBinding fixture above is
# the controller's independent proof; native admission deliberately verifies
# only the durable object-local preconditions.
kubectl annotate --as="$manager_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --dry-run=server \
  "topology.cisco.vk/isolated-legacy-worker=${legacy_device_uid}" >/dev/null
device_resource_version="$(kubectl get ciscodevice device-a \
  --namespace "$device_namespace" -o jsonpath='{.metadata.resourceVersion}')"
# The desired revision itself is a durable fail-closed fence while no new
# Deployment/Pod has proved readiness. Its pending shape intentionally omits
# the deployment, Pod, and observed-revision tuple.
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p "{\"status\":{\"workerRevision\":{\"desiredRevision\":\"${worker_revision}\",\"observedAt\":\"2026-01-01T00:00:00Z\"}}}" >/dev/null
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p "{\"status\":{\"nodeIdentity\":{\"nodeName\":\"${managed_node}\",\"nodeUID\":\"${managed_node_uid}\",\"deviceUID\":\"${device_uid}\",\"physicalIdentity\":\"integration-serial-managed\"},\"topologyProjection\":{\"effectiveLabelHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"sourceResourceVersion\":\"${device_resource_version}\",\"lastSuccessfulTime\":\"2026-01-01T00:00:00Z\"},\"workerRevision\":{\"desiredRevision\":\"${worker_revision}\",\"observedRevision\":\"${worker_revision}\",\"deploymentUID\":\"33333333-3333-4333-8333-333333333333\",\"deploymentGeneration\":1,\"podUID\":\"44444444-4444-4444-8444-444444444444\",\"podStartTime\":\"2026-01-01T00:00:00Z\",\"readyHeartbeatTime\":\"2026-01-01T00:00:01Z\",\"observedAt\":\"2026-01-01T00:00:02Z\"}}}" >/dev/null
if kubectl patch --as="$manager_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p "{\"status\":{\"legacyHandoff\":{\"phase\":\"Preparing\",\"deviceUID\":\"${legacy_device_uid}\",\"nodeName\":\"${legacy_node}\",\"nodeUID\":\"${legacy_node_uid}\",\"projectionHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"legacyWorkerUsername\":\"${legacy_username}\",\"requestedAt\":\"2026-01-01T00:00:00Z\"}}}" \
    >"$scratch_dir/device-handoff-missing-binding.txt" 2>&1; then
  echo "manager published an in-flight handoff without managed binding state" >&2
  exit 1
fi
grep -Eq 'legacy handoff retains managed binding state|denied the request|failed expression' \
  "$scratch_dir/device-handoff-missing-binding.txt"
if kubectl patch --as="$manager_username" ciscodevice device-a \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p "{\"status\":{\"legacyHandoff\":{\"phase\":\"Complete\",\"deviceUID\":\"${device_uid}\",\"nodeName\":\"${managed_node}\",\"nodeUID\":\"${managed_node_uid}\",\"projectionHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"legacyWorkerUsername\":\"${legacy_username}\",\"requestedAt\":\"2026-01-01T00:00:00Z\",\"nodeReleasedAt\":\"2026-01-01T00:00:01Z\",\"isolatedReadyAt\":\"2026-01-01T00:00:02Z\",\"completedAt\":\"2026-01-01T00:00:03Z\"}}}" \
    >"$scratch_dir/device-handoff-dual-writer.txt" 2>&1; then
  echo "manager published Complete while managed binding state remained" >&2
  exit 1
fi
grep -Eq 'legacy handoff retains managed binding state|denied the request|failed expression' \
  "$scratch_dir/device-handoff-dual-writer.txt"
# An unlocked device remains ordinarily editable and deletable. These dry-run
# probes ensure the lock/session fences below do not become a blanket lifecycle
# denial for standalone or settled devices.
kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --type=merge \
  -p '{"spec":{"address":"192.0.2.12"}}' >/dev/null
# Physical identity is the immutable, operator-declared chassis authority even
# before enrollment. Prove the CRD transition rule independently of the later
# topology-lock fence.
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"spec":{"physicalIdentity":"replacement-chassis"}}' \
    >"$scratch_dir/device-physical-identity-negative.txt" 2>&1; then
  echo "ordinary device editor replaced the immutable physical identity" >&2
  exit 1
fi
grep -Eq 'physicalIdentity is write-once|denied the request|failed expression' \
  "$scratch_dir/device-physical-identity-negative.txt"
kubectl delete --as="$device_editor_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --dry-run=server >/dev/null
# The retained production-shaped legacy identity has served its admission and
# recovery probes. Remove it so the later live retirement gate is testing only
# device-a's active handoff state, not an unrelated generated grant.
kubectl delete --as="$manager_username" clusterrolebinding \
  "$legacy_cluster_binding" >/dev/null
kubectl delete --as="$manager_username" rolebinding "$legacy_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl delete --as="$manager_username" serviceaccount "$legacy_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl label --as="$device_editor_username" ciscodevice device-a \
  --namespace "$device_namespace" test.cisco.vk/note=allowed >/dev/null
if kubectl label --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" topology.kubernetes.io/zone=test-zone-b \
    --overwrite >"$scratch_dir/device-negative.txt" 2>&1; then
  echo "ordinary device editor changed protected topology" >&2
  exit 1
fi
grep -Eq 'require(s)?( the)? custom topology permission|denied (the )?request|failed expression|forbidden' \
  "$scratch_dir/device-negative.txt" || {
    cat "$scratch_dir/device-negative.txt" >&2
    exit 1
  }
if kubectl label --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" distribution.cisco.vk/cache-domain=berlin \
    >"$scratch_dir/device-distribution-negative.txt" 2>&1; then
  echo "ordinary device editor changed protected distribution topology" >&2
  exit 1
fi
grep -Eq 'require(s)?( the)? custom topology permission|denied (the )?request|failed expression|forbidden' \
  "$scratch_dir/device-distribution-negative.txt" || {
    cat "$scratch_dir/device-distribution-negative.txt" >&2
    exit 1
  }
if kubectl patch --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"finalizers":[]}}' \
    >"$scratch_dir/device-finalizer-negative.txt" 2>&1; then
  echo "ordinary device editor removed the managed lifecycle finalizer" >&2
  exit 1
fi
grep -Eq 'only the manager may remove the CiscoDevice cleanup finalizer|finalizers and owner references are manager-owned|denied the request|failed expression' \
  "$scratch_dir/device-finalizer-negative.txt"
if kubectl patch --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p '{"metadata":{"ownerReferences":[{"apiVersion":"v1","kind":"ConfigMap","name":"forged-owner","uid":"22222222-2222-2222-2222-222222222222"}]}}' \
    >"$scratch_dir/device-owner-negative.txt" 2>&1; then
  echo "ordinary device editor rebound managed CiscoDevice ownership" >&2
  exit 1
fi
grep -Eq 'finalizers and owner references are manager-owned|denied the request|failed expression' \
  "$scratch_dir/device-owner-negative.txt"
if kubectl patch --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"phase":"Ready"}}' \
    >"$scratch_dir/device-status-negative.txt" 2>&1; then
  echo "ordinary device status writer spoofed managed readiness" >&2
  exit 1
fi
grep -Eq 'all CiscoDevice status is manager-owned|denied the request|failed expression' \
  "$scratch_dir/device-status-negative.txt"

# Managed scheduling capacity is explicit and bounded. The selected device
# accepts both inclusive limits and rejects values outside them; the bootstrap
# object above proves that legacy/unselected maxPods=0 remains API-compatible.
for valid_max_pods in 1 110; do
  kubectl patch --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p "{\"spec\":{\"maxPods\":${valid_max_pods}}}" >/dev/null
done
for invalid_max_pods in 0 111; do
  if kubectl patch --as="$device_editor_username" ciscodevice device-a \
      --namespace "$device_namespace" --type=merge --dry-run=server \
      -p "{\"spec\":{\"maxPods\":${invalid_max_pods}}}" \
      >"$scratch_dir/device-max-pods-${invalid_max_pods}-negative.txt" 2>&1; then
    echo "selected managed CiscoDevice accepted maxPods=${invalid_max_pods}" >&2
    exit 1
  fi
  grep -Eq 'requires spec.maxPods from 1 through 110|denied the request|failed expression' \
    "$scratch_dir/device-max-pods-${invalid_max_pods}-negative.txt"
done

# A delegated topology author can normally change protected inventory, but a
# live reservation freezes both its risk-domain labels and the explicit
# reclassification acknowledgement until the manager releases the exact lock.
kubectl create serviceaccount topology-author --namespace "$device_namespace" >/dev/null
kubectl create role topology-author --namespace "$device_namespace" \
  --verb=get,update,patch,topology --resource=ciscodevices.cisco.vk >/dev/null
kubectl create rolebinding topology-author --namespace "$device_namespace" \
  --role=topology-author --serviceaccount="${device_namespace}:topology-author" >/dev/null
topology_author_username="system:serviceaccount:${device_namespace}:topology-author"
kubectl label --as="$topology_author_username" ciscodevice device-a \
  --namespace "$device_namespace" operations.cisco.vk/ring=blue >/dev/null
device_generation="$(kubectl get ciscodevice device-a --namespace "$device_namespace" \
  -o jsonpath='{.metadata.generation}')"
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"topologyLock\":{
      \"state\":\"Active\",
      \"policyEpoch\":1,
      \"acquisitionID\":\"11111111111111111111111111111111\",
      \"campaignNamespace\":\"${device_namespace}\",
      \"campaignName\":\"integration-rollout\",
      \"campaignUID\":\"55555555-5555-4555-8555-555555555555\",
      \"planHash\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",
      \"reservationID\":\"integration-reservation\",
      \"deviceUID\":\"${device_uid}\",
      \"deviceGeneration\":${device_generation},
      \"nodeUID\":\"${managed_node_uid}\",
      \"projectionHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",
      \"acquiredAt\":\"2026-01-01T00:00:00Z\"
    }}}
  " >/dev/null
if kubectl label --as="$topology_author_username" ciscodevice device-a \
    --namespace "$device_namespace" operations.cisco.vk/ring=red --overwrite \
    >"$scratch_dir/device-lock-label-negative.txt" 2>&1; then
  echo "topology author changed a risk-domain label under an active reservation lock" >&2
  exit 1
fi
grep -Eq 'frozen by an active topology lock|denied the request|failed expression' \
  "$scratch_dir/device-lock-label-negative.txt"
if kubectl annotate --as="$topology_author_username" ciscodevice device-a \
    --namespace "$device_namespace" \
    topology.cisco.vk/approve-reclassification-from=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
    >"$scratch_dir/device-lock-reclassification-negative.txt" 2>&1; then
  echo "topology author changed reclassification approval under an active reservation lock" >&2
  exit 1
fi
grep -Eq 'frozen by an active topology lock|denied the request|failed expression' \
  "$scratch_dir/device-lock-reclassification-negative.txt"

# A topology lock freezes the entire executable CiscoDevice spec, not only the
# projected labels. Credential/trust redirection and ordinary runtime changes
# therefore cannot inherit a campaign approved for another target. The manager
# itself has no implicit bypass while an acquisition is live.
assert_locked_spec_patch_denied() {
  local actor="$1"
  local field="$2"
  local patch="$3"
  if kubectl patch --as="$actor" ciscodevice device-a \
      --namespace "$device_namespace" --type=merge --dry-run=server \
      -p "$patch" >"$scratch_dir/device-lock-${field}-negative.txt" 2>&1; then
    echo "${field} changed while the CiscoDevice topology lock was active" >&2
    exit 1
  fi
  grep -Eq 'spec is immutable while a topology lock|denied the request|failed expression' \
    "$scratch_dir/device-lock-${field}-negative.txt"
}

assert_locked_spec_patch_denied "$device_editor_username" address \
  '{"spec":{"address":"192.0.2.99"}}'
assert_locked_spec_patch_denied "$device_editor_username" credential-secret \
  '{"spec":{"credentialSecretRef":{"name":"forged-credential"}}}'
assert_locked_spec_patch_denied "$device_editor_username" gnoi-tls-secret \
  '{"spec":{"gnoi":{"transportSecurity":"tls","tls":{"secretRef":{"name":"forged-gnoi-tls"}}}}}'
assert_locked_spec_patch_denied "$topology_author_username" taints \
  '{"spec":{"taints":[{"key":"test.cisco.vk/locked","value":"true","effect":"NoSchedule"}]}}'
assert_locked_spec_patch_denied "$manager_username" max-pods \
  '{"spec":{"maxPods":5}}'

if kubectl delete --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --dry-run=server \
    >"$scratch_dir/device-lock-delete-negative.txt" 2>&1; then
  echo "CiscoDevice deletion succeeded while its topology lock was active" >&2
  exit 1
fi
grep -Eq 'deletion requires no topology lock|denied the request|failed expression' \
  "$scratch_dir/device-lock-delete-negative.txt"

# Deletion is independently fenced by an acknowledged/active maintenance
# session even when no topology lock exists. Settling the exact session restores
# the normal delete path.
kubectl patch --as="$manager_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"maintenanceSession\":{
      \"phase\":\"Active\",
      \"sessionToken\":\"integration-session-0001\",
      \"lease\":{
        \"namespace\":\"${device_namespace}\",
        \"name\":\"integration-mutation-lease\",
        \"uid\":\"66666666-6666-4666-8666-666666666666\",
        \"holder\":\"software-upgrade/77777777-7777-4777-8777-777777777777\"
      },
      \"operation\":{
        \"namespace\":\"${device_namespace}\",
        \"name\":\"integration-upgrade\",
        \"uid\":\"77777777-7777-4777-8777-777777777777\"
      },
      \"deviceUID\":\"${legacy_device_uid}\",
      \"nodeName\":\"${legacy_node}\",
      \"nodeUID\":\"${legacy_node_uid}\",
      \"requestedAt\":\"2026-01-01T00:00:00Z\",
      \"acknowledgedAt\":\"2026-01-01T00:00:01Z\",
      \"controlRevision\":1
    }}}
  " >/dev/null
if kubectl delete --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --dry-run=server \
    >"$scratch_dir/device-maintenance-delete-negative.txt" 2>&1; then
  echo "CiscoDevice deletion succeeded during an active maintenance session" >&2
  exit 1
fi
grep -Eq 'no unsettled maintenance session|denied the request|failed expression' \
  "$scratch_dir/device-maintenance-delete-negative.txt"
kubectl patch --as="$manager_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"maintenanceSession":{"phase":"Settled"}}}' >/dev/null
kubectl delete --as="$device_editor_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --dry-run=server >/dev/null

# Campaign authority is opt-in and identity-bound. Bind the otherwise-unbound
# planner and approver roles only for this disposable namespace.
kubectl create serviceaccount "$planner_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl create serviceaccount "$approver_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl create rolebinding rollout-planner --namespace "$device_namespace" \
  --clusterrole="${release_name}-cisco-virtual-kubelet-rollout-planner" \
  --serviceaccount="${device_namespace}:${planner_service_account}" >/dev/null
kubectl create rolebinding rollout-approver --namespace "$device_namespace" \
  --clusterrole="${release_name}-cisco-virtual-kubelet-rollout-approver" \
  --serviceaccount="${device_namespace}:${approver_service_account}" >/dev/null

# Exercise the CRD's optional-bool transition rules independently of managed
# leaf admission. Omitted pause/cancel keys are valid at revision zero and on
# resume; once cancellation is true, a later revision cannot remove it.
kubectl create serviceaccount upgrade-control-crd-probe \
  --namespace "$device_namespace" >/dev/null
kubectl create role upgrade-control-crd-probe --namespace "$device_namespace" \
  --verb=create,get,update,patch \
  --resource=iosxesoftwareupgrades.ops.cisco.vk,iosxesoftwareupgrades.ops.cisco.vk/status >/dev/null
kubectl create rolebinding upgrade-control-crd-probe --namespace "$device_namespace" \
  --role=upgrade-control-crd-probe \
  --serviceaccount="${device_namespace}:upgrade-control-crd-probe" >/dev/null
upgrade_control_probe_username="system:serviceaccount:${device_namespace}:upgrade-control-crd-probe"
cat >"$scratch_dir/upgrade-control-crd-probe.yaml" <<EOF
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: upgrade-control-crd-probe
  namespace: ${device_namespace}
spec:
  deviceRef:
    name: device-a
  imageSource:
    url: https://images.example.test/cat9k.bin
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  targetVersion: 17.18.4
EOF
kubectl create --as="$upgrade_control_probe_username" \
  -f "$scratch_dir/upgrade-control-crd-probe.yaml" >/dev/null
kubectl patch --as="$upgrade_control_probe_username" iosxesoftwareupgrade \
  upgrade-control-crd-probe --namespace "$device_namespace" \
  --subresource=status --type=merge \
  -p '{"status":{"managerControl":{"revision":0,"updatedAt":"2026-01-01T00:00:00Z"}}}' >/dev/null
kubectl patch --as="$upgrade_control_probe_username" iosxesoftwareupgrade \
  upgrade-control-crd-probe --namespace "$device_namespace" \
  --subresource=status --type=merge \
  -p '{"status":{"managerControl":{"revision":1,"pause":true,"updatedAt":"2026-01-01T00:01:00Z"}}}' >/dev/null
kubectl patch --as="$upgrade_control_probe_username" iosxesoftwareupgrade \
  upgrade-control-crd-probe --namespace "$device_namespace" \
  --subresource=status --type=merge \
  -p '{"status":{"managerControl":{"revision":2,"pause":null,"updatedAt":"2026-01-01T00:02:00Z"}}}' >/dev/null
kubectl patch --as="$upgrade_control_probe_username" iosxesoftwareupgrade \
  upgrade-control-crd-probe --namespace "$device_namespace" \
  --subresource=status --type=merge \
  -p '{"status":{"managerControl":{"revision":3,"cancel":true,"updatedAt":"2026-01-01T00:03:00Z"}}}' >/dev/null
if kubectl patch --as="$upgrade_control_probe_username" iosxesoftwareupgrade \
    upgrade-control-crd-probe --namespace "$device_namespace" \
    --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"managerControl":{"revision":4,"cancel":null,"updatedAt":"2026-01-01T00:04:00Z"}}}' \
    >"$scratch_dir/upgrade-control-cancel-negative.txt" 2>&1; then
  echo "managed leaf CRD allowed terminal cancellation to be removed" >&2
  exit 1
fi
grep -Eq 'managed leaf cancellation is terminal|denied (the )?request|failed expression' \
  "$scratch_dir/upgrade-control-cancel-negative.txt"

cat >"$scratch_dir/rollout.yaml" <<EOF
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareRollout
metadata:
  name: integration-rollout
  namespace: ${device_namespace}
spec:
  plan:
    requestedBy: ${planner_username}
    requestedAt: "2026-01-01T00:00:00Z"
    targets:
      selector:
        matchLabels:
          topology.cisco.vk/managed: "true"
      maxTargets: 1
    image:
      sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      imageFamily: cat9k
      sources:
        - name: global
          priority: 100
          url: https://images.example.test/cat9k.bin
    targetVersion: 17.18.4
    strategy: Reload
    rollbackOnFailure: true
    installTimeoutSeconds: 3600
    rebootTimeoutSeconds: 1800
    canaries:
      - name: c9300
        devices: [device-a]
    pauseAfterCanary: true
    budgets:
      maxConcurrentTransfers: 1
      maxUnavailable: 1
      domains:
        - topologyKey: topology.kubernetes.io/zone
          maxUnavailable: 1
    workloads:
      policy: BlockIfRunning
    health:
      maxObservationAgeSeconds: 300
      canarySoakSeconds: 0
      waveSoakSeconds: 0
  control:
    revision: 0
EOF
if awk '
    { print }
    $0 == "          url: https://images.example.test/cat9k.bin" {
      print "        - name: second-global"
      print "          priority: 101"
      print "          url: https://backup-images.example.test/cat9k.bin"
    }
  ' "$scratch_dir/rollout.yaml" | kubectl create --as="$planner_username" \
    --dry-run=server -f - >"$scratch_dir/rollout-catch-all-negative.txt" 2>&1; then
  echo "API server accepted multiple unscoped rollout image sources" >&2
  exit 1
fi
grep -Eq 'exactly one image source must be an unscoped catch-all|Invalid value|failed rule' \
  "$scratch_dir/rollout-catch-all-negative.txt"
kubectl create --as="$planner_username" -f "$scratch_dir/rollout.yaml" >/dev/null
kubectl patch --as="$planner_username" iosxesoftwarerollout integration-rollout \
  --namespace "$device_namespace" --type=merge \
  -p "{\"spec\":{\"control\":{\"revision\":1,\"pause\":true,\"requestedBy\":\"${planner_username}\",\"requestedAt\":\"2026-01-01T00:01:00Z\"}}}" >/dev/null
if kubectl patch --as="$approver_username" iosxesoftwarerollout integration-rollout \
    --namespace "$device_namespace" --type=merge \
    -p "{\"spec\":{\"control\":{\"revision\":2,\"pause\":false,\"requestedBy\":\"${approver_username}\",\"requestedAt\":\"2026-01-01T00:02:00Z\"}}}" \
    >"$scratch_dir/control-negative.txt" 2>&1; then
  echo "rollout approver changed planner-only campaign control" >&2
  exit 1
fi
grep -Eq 'custom control permission|denied the request|failed expression' \
  "$scratch_dir/control-negative.txt"
kubectl patch --as="$planner_username" iosxesoftwarerollout integration-rollout \
  --namespace "$device_namespace" --type=merge \
  -p "{\"spec\":{\"control\":{\"revision\":2,\"pause\":null,\"requestedBy\":\"${planner_username}\",\"requestedAt\":\"2026-01-01T00:02:00Z\"}}}" >/dev/null
kubectl patch --as="$planner_username" iosxesoftwarerollout integration-rollout \
  --namespace "$device_namespace" --type=merge \
  -p "{\"spec\":{\"control\":{\"revision\":3,\"cancel\":true,\"requestedBy\":\"${planner_username}\",\"requestedAt\":\"2026-01-01T00:03:00Z\"}}}" >/dev/null
if kubectl patch --as="$planner_username" iosxesoftwarerollout integration-rollout \
    --namespace "$device_namespace" --type=merge --dry-run=server \
    -p "{\"spec\":{\"control\":{\"revision\":4,\"cancel\":null,\"requestedBy\":\"${planner_username}\",\"requestedAt\":\"2026-01-01T00:04:00Z\"}}}" \
    >"$scratch_dir/control-cancel-negative.txt" 2>&1; then
  echo "rollout CRD allowed terminal cancellation to be removed" >&2
  exit 1
fi
grep -Eq 'cancellation is terminal|denied (the )?request|failed expression' \
  "$scratch_dir/control-cancel-negative.txt"

# The CRD parent transition rule must make an optional frozenPlan truly
# append-only. Nested immutability alone does not run when an optional child is
# removed and later re-added, so prove that exact bypass against a real API
# server after publishing one structurally valid manager plan.
policy_name="${release_name}-cisco-virtual-kubelet-topology-policy"
ledger_name="${release_name}-cisco-virtual-kubelet-topology-ledger"
policy_uid="$(kubectl get configmap "$policy_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.uid}')"
policy_resource_version="$(kubectl get configmap "$policy_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.resourceVersion}')"
ledger_uid="$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.uid}')"
rollout_generation="$(kubectl get iosxesoftwarerollout integration-rollout \
  --namespace "$device_namespace" -o jsonpath='{.metadata.generation}')"
cat >"$scratch_dir/frozen-plan-status.json" <<EOF
{
  "status": {
    "frozenPlan": {
      "hash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "encodedSizeBytes": 1,
      "createdAt": "2026-01-01T00:03:00Z",
      "campaignGeneration": ${rollout_generation},
      "policy": {
        "name": "${policy_name}",
        "namespace": "${system_namespace}",
        "uid": "${policy_uid}",
        "resourceVersion": "${policy_resource_version}",
        "semanticHash": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "structuralHash": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        "maxTargets": 1,
        "maxConcurrentTransfers": 1,
        "maxUnavailable": 1,
        "healthFreshnessSeconds": 300,
        "ledgerNamespace": "${system_namespace}",
        "ledgerName": "${ledger_name}",
        "ledgerUID": "${ledger_uid}",
        "maxActiveReservations": 256,
        "maxLedgerSizeBytes": 262144
      },
      "targets": [{
        "deviceName": "device-a",
        "deviceUID": "${device_uid}",
        "deviceGeneration": ${device_generation},
        "physicalIdentity": "integration-serial-managed",
        "nodeName": "${managed_node}",
        "nodeUID": "${managed_node_uid}",
        "driver": "XE",
        "imageFamily": "cat9k",
        "source": {
          "name": "global",
          "priority": 100,
          "url": "https://images.example.test/cat9k.bin",
          "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        },
        "qualificationCohort": "c9300",
        "workerProtocolVersion": "rollout-v1",
        "projectionHash": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "topology": [{
          "key": "topology.kubernetes.io/zone",
          "value": "test-zone-a"
        }],
        "canaryCohort": "c9300",
        "wave": 0,
        "childName": "integration-rollout-device-a"
      }]
    }
  }
}
EOF
kubectl patch --as="$manager_username" iosxesoftwarerollout integration-rollout \
  --namespace "$device_namespace" --subresource=status --type=merge \
  --patch-file "$scratch_dir/frozen-plan-status.json" >/dev/null
if kubectl patch --as="$manager_username" iosxesoftwarerollout integration-rollout \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server -p '{"status":{"frozenPlan":null}}' \
    >"$scratch_dir/frozen-plan-remove-negative.txt" 2>&1; then
  echo "manager removed an already published frozen rollout plan" >&2
  exit 1
fi
grep -Eq 'frozen plan cannot be removed or replaced once published|denied the request|failed expression' \
  "$scratch_dir/frozen-plan-remove-negative.txt"
if kubectl patch --as="$manager_username" iosxesoftwarerollout integration-rollout \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p '{"status":{"frozenPlan":{"hash":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}}}' \
    >"$scratch_dir/frozen-plan-replace-negative.txt" 2>&1; then
  echo "manager replaced an already published frozen rollout plan" >&2
  exit 1
fi
grep -Eq 'frozen plan (is immutable|cannot be removed or replaced once published)|denied the request|failed expression' \
  "$scratch_dir/frozen-plan-replace-negative.txt"

# The ledger and policy are safety authority, not ordinary ConfigMaps.
kubectl create serviceaccount configmap-editor --namespace "$system_namespace" >/dev/null
kubectl create role configmap-editor --namespace "$system_namespace" \
  --verb=get,update,patch --resource=configmaps >/dev/null
kubectl create rolebinding configmap-editor --namespace "$system_namespace" \
  --role=configmap-editor --serviceaccount="${system_namespace}:configmap-editor" >/dev/null
configmap_editor_username="system:serviceaccount:${system_namespace}:configmap-editor"
if kubectl patch --as="$configmap_editor_username" configmap \
    "${release_name}-cisco-virtual-kubelet-topology-ledger" \
    --namespace "$system_namespace" --type=merge \
    -p '{"data":{"ledger.json":"{}"}}' \
    >"$scratch_dir/ledger-negative.txt" 2>&1; then
  echo "ordinary ConfigMap editor changed the topology ledger" >&2
  exit 1
fi
grep -Eq 'ledger writes are manager-only|denied the request|failed expression' \
  "$scratch_dir/ledger-negative.txt"
if kubectl patch --as="$configmap_editor_username" configmap \
    "${release_name}-cisco-virtual-kubelet-topology-policy" \
    --namespace "$system_namespace" --type=merge \
    -p '{"data":{"policy.json":"{}"}}' \
    >"$scratch_dir/policy-negative.txt" 2>&1; then
  echo "ordinary ConfigMap editor changed the topology policy" >&2
  exit 1
fi
grep -Eq 'policy content changes require|denied the request|failed expression' \
  "$scratch_dir/policy-negative.txt"

# Finally prove the scheduling layer with only native Node labels, taints,
# affinity, and topology spread. Fake ready Nodes are sufficient because the
# assertion is scheduler binding, not kubelet execution.
heartbeat="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
for node in cvk-scheduler-a cvk-scheduler-b cvk-scheduler-missing cvk-scheduler-guarded; do
  cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Node
metadata:
  name: ${node}
spec: {}
EOF
  kubectl label node "$node" test.cisco.vk/integration=true >/dev/null
done
kubectl label node cvk-scheduler-a \
  topology.kubernetes.io/region=test-region \
  topology.kubernetes.io/zone=test-zone-a >/dev/null
kubectl label node cvk-scheduler-b \
  topology.kubernetes.io/region=test-region \
  topology.kubernetes.io/zone=test-zone-b >/dev/null
kubectl label node cvk-scheduler-guarded test.cisco.vk/guarded=true >/dev/null
kubectl taint node cvk-scheduler-guarded \
  topology.cisco.vk/uninitialized=true:NoSchedule >/dev/null
for node in cvk-scheduler-a cvk-scheduler-b cvk-scheduler-missing cvk-scheduler-guarded; do
  kubectl patch node "$node" --subresource=status --type=merge -p "{
    \"status\":{
      \"capacity\":{\"cpu\":\"4\",\"memory\":\"8Gi\",\"pods\":\"16\"},
      \"allocatable\":{\"cpu\":\"4\",\"memory\":\"8Gi\",\"pods\":\"16\"},
      \"conditions\":[{
        \"type\":\"Ready\",\"status\":\"True\",\"reason\":\"IntegrationFixture\",
        \"message\":\"synthetic scheduler fixture\",
        \"lastHeartbeatTime\":\"${heartbeat}\",\"lastTransitionTime\":\"${heartbeat}\"
      }]
    }
  }" >/dev/null
done

create_spread_pod() {
  local name="$1"
  cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${name}
  namespace: ${device_namespace}
  labels:
    app: topology-spread-it
spec:
  nodeSelector:
    test.cisco.vk/integration: "true"
  topologySpreadConstraints:
    - maxSkew: 1
      minDomains: 2
      topologyKey: topology.kubernetes.io/zone
      whenUnsatisfiable: DoNotSchedule
      nodeTaintsPolicy: Honor
      labelSelector:
        matchLabels:
          app: topology-spread-it
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
      resources:
        requests:
          cpu: 10m
          memory: 8Mi
EOF
}

wait_for_binding() {
  local pod="$1"
  local node=""
  for _ in $(seq 1 45); do
    node="$(kubectl get pod "$pod" --namespace "$device_namespace" \
      -o jsonpath='{.spec.nodeName}')"
    if [ -n "$node" ]; then
      echo "$node"
      return 0
    fi
    sleep 1
  done
  kubectl describe pod "$pod" --namespace "$device_namespace" >&2
  return 1
}

create_spread_pod topology-spread-1
first_node="$(wait_for_binding topology-spread-1)"
create_spread_pod topology-spread-2
second_node="$(wait_for_binding topology-spread-2)"
test "$first_node" != "$second_node"
first_zone="$(kubectl get node "$first_node" -o jsonpath='{.metadata.labels.topology\.kubernetes\.io/zone}')"
second_zone="$(kubectl get node "$second_node" -o jsonpath='{.metadata.labels.topology\.kubernetes\.io/zone}')"
test -n "$first_zone"
test -n "$second_zone"
test "$first_zone" != "$second_zone"

cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: topology-guarded
  namespace: ${device_namespace}
spec:
  nodeSelector:
    test.cisco.vk/guarded: "true"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
sleep 5
test -z "$(kubectl get pod topology-guarded --namespace "$device_namespace" -o jsonpath='{.spec.nodeName}')"

# A direct nodeName assignment bypasses scheduler taints by Kubernetes design;
# the managed leaf's post-fence workload check is therefore a required second
# safety boundary, not an optional optimization.
cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: topology-direct-binding
  namespace: ${device_namespace}
spec:
  nodeName: cvk-scheduler-guarded
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
test "$(kubectl get pod topology-direct-binding --namespace "$device_namespace" \
  -o jsonpath='{.spec.nodeName}')" = "cvk-scheduler-guarded"

# Exercise the reverse writer handoff and the chart's live-only retirement
# gate. The manager image is intentionally unavailable in this test, so these
# fixtures reproduce its security-significant API state transitions; no device
# RPC is involved. Controller tests separately cover workload readiness timing.
ledger_uid="$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.uid}')"
kubectl patch --as="$manager_username" configmap "$ledger_name" \
  --namespace "$system_namespace" --type=merge \
  -p "{\"data\":{\"ledger.json\":\"{\\\"version\\\":\\\"v1\\\",\\\"uid\\\":\\\"${ledger_uid}\\\",\\\"reservations\\\":{}}\"}}" >/dev/null
kubectl annotate --as="$manager_username" configmap "$policy_name" \
  --namespace "$system_namespace" \
  "topology.cisco.vk/ledger-uid=${ledger_uid}" --overwrite >/dev/null

# Break-glass may repair a valid ledger but cannot erase established authority.
# The cluster-admin test identity has the wildcard permission that satisfies
# manage-ledger, so this specifically proves the unconditional non-empty fence.
if kubectl patch configmap "$ledger_name" --namespace "$system_namespace" \
    --type=merge --dry-run=server -p '{"data":{"ledger.json":""}}' \
    >"$scratch_dir/ledger-empty-breakglass-negative.txt" 2>&1; then
  echo "break-glass identity emptied an existing topology ledger" >&2
  exit 1
fi
grep -Fq 'an existing topology ledger cannot be emptied, including through break-glass' \
  "$scratch_dir/ledger-empty-breakglass-negative.txt"

# A normal live upgrade must carry the manager-owned immutable binding forward
# without submitting any write to the mutable reservation ledger. Copying the
# lookup value into an update would still have a read/apply race with a new
# reservation; dropping the kept ledger from the upgraded release manifest is
# the required ownership boundary.
ledger_json_before="$(kubectl get configmap "$ledger_name" \
  --namespace "$system_namespace" -o jsonpath='{.data.ledger\.json}')"
ledger_resource_version_before="$(kubectl get configmap "$ledger_name" \
  --namespace "$system_namespace" -o jsonpath='{.metadata.resourceVersion}')"
helm upgrade "$release_name" "$chart_dir" \
  --namespace "$system_namespace" \
  "${image_values[@]}" \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.networkManagement.accessMode=readWrite \
  --set gnoi.enableSoftwareUpgrade=true \
  --set topology.policy.workloadDrain.enabled=true \
  --set-json "topology.policy.workloadDrain.allowedNamespaces=[\"${device_namespace}\"]" \
  >/dev/null
test "$(kubectl get configmap "$policy_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/ledger-uid}')" = "$ledger_uid"
test "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.uid}')" = "$ledger_uid"
test "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.data.ledger\.json}')" = "$ledger_json_before"
test "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.resourceVersion}')" = "$ledger_resource_version_before"
helm get manifest "$release_name" --namespace "$system_namespace" \
  >"$scratch_dir/managed-upgrade-manifest.yaml"
if awk -v ledger_name="$ledger_name" '
    /^---$/ { kind=""; metadata=0; next }
    /^kind: / { kind=$2; metadata=0; next }
    /^metadata:$/ { metadata=1; next }
    metadata && /^[^ ]/ { metadata=0 }
    kind == "ConfigMap" && metadata && $1 == "name:" && $2 == ledger_name { found=1 }
    END { exit(found ? 0 : 1) }
  ' "$scratch_dir/managed-upgrade-manifest.yaml"; then
  echo "live Helm upgrade retained mutable ledger ownership" >&2
  exit 1
fi

# Changing both retained coordinates must not be mistaken for a fresh
# bootstrap. The release-owned admission contract is a cluster-scoped
# sentinel even if the operator also changes fullnameOverride.
renamed_policy="${policy_name}-renamed"
renamed_ledger="${ledger_name}-renamed"
if helm upgrade "$release_name" "$chart_dir" \
    --namespace "$system_namespace" \
    "${image_values[@]}" \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.networkManagement.accessMode=readWrite \
    --set gnoi.enableSoftwareUpgrade=true \
    --set "fullnameOverride=${release_name}-renamed" \
    --set topology.policy.workloadDrain.enabled=true \
    --set-json "topology.policy.workloadDrain.allowedNamespaces=[\"${device_namespace}\"]" \
    --set "topology.policy.name=${renamed_policy}" \
    --set "topology.ledger.name=${renamed_ledger}" \
    >"$scratch_dir/topology-coordinate-change-negative.txt" 2>&1; then
  echo "Helm accepted replacement topology policy/ledger coordinates" >&2
  exit 1
fi
grep -Eq 'refusing to bootstrap new topology policy|policy coordinates are immutable after bootstrap' \
  "$scratch_dir/topology-coordinate-change-negative.txt" || {
  cat "$scratch_dir/topology-coordinate-change-negative.txt" >&2
  exit 1
}
if kubectl get configmap "$renamed_policy" "$renamed_ledger" \
    --namespace "$system_namespace" >/dev/null 2>&1; then
  echo "rejected coordinate change created replacement topology state" >&2
  exit 1
fi
test "$(kubectl get configmap "$policy_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/ledger-uid}')" = "$ledger_uid"
test "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.uid}')" = "$ledger_uid"
test "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.data.ledger\.json}')" = "$ledger_json_before"

# A historical bootstrap revision contains the intentionally empty CREATE
# manifest. Once the manager has bound authority, an actual rollback must fail
# rather than replay that value. Helm normally refuses first because the kept
# ledger is no longer in its current release manifest; if it does submit an
# update, admission independently rejects emptying the live ledger.
if helm rollback "$release_name" "$bootstrap_topology_revision" \
    --namespace "$system_namespace" --server-side=false \
    >"$scratch_dir/bootstrap-rollback-negative.txt" 2>&1; then
  echo "Helm accepted rollback to an empty-ledger bootstrap revision" >&2
  exit 1
fi
if ! grep -Eq 'original object ConfigMap.*topology-ledger.*not found|ledger-uid must be absent at creation|existing topology ledger cannot be emptied|denied the request|failed expression' \
    "$scratch_dir/bootstrap-rollback-negative.txt"; then
  echo "Helm rollback failed for an unexpected reason:" >&2
  sed -n '1,120p' "$scratch_dir/bootstrap-rollback-negative.txt" >&2
  exit 1
fi
if [ "$(kubectl get configmap "$policy_name" --namespace "$system_namespace" \
    -o jsonpath='{.metadata.annotations.topology\.cisco\.vk/ledger-uid}')" != "$ledger_uid" ] || \
   [ "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
    -o jsonpath='{.metadata.uid}')" != "$ledger_uid" ] || \
   [ "$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
    -o jsonpath='{.data.ledger\.json}')" != "$ledger_json_before" ]; then
  echo "rejected Helm rollback changed the retained topology identity or ledger" >&2
  exit 1
fi

# A live downgrade must reject a still-managed Node even though all retained
# policy/RBAC coordinates are present and valid.
if helm upgrade "$release_name" "$chart_dir" \
    --namespace "$system_namespace" \
    "${image_values[@]}" \
    --set topology.enabled=false \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.networkManagement.accessMode=readWrite \
    --set gnoi.enableSoftwareUpgrade=true \
    >"$scratch_dir/topology-disable-incomplete.txt" 2>&1; then
  echo "Helm disabled managed topology before reverse handoff completed" >&2
  exit 1
fi
grep -Eq 'still has managed status.nodeIdentity|handoff.*not Complete' \
  "$scratch_dir/topology-disable-incomplete.txt"

# Release the synthetic lock and authorize the exact Node UID. The manager
# publishes durable Preparing status before the marker so a crash cannot leave
# mutable request/selection state behind an apparently authoritative marker.
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"topologyLock":null}}' >/dev/null
kubectl annotate --as="$topology_author_username" ciscodevice device-a \
  --namespace "$device_namespace" \
  "topology.cisco.vk/request-legacy-handoff=${managed_node_uid}" --overwrite >/dev/null

retirement_legacy_service_account="cisco-vk-legacy-${managed_node}-${worker_uid_hash}"
retirement_legacy_username="system:serviceaccount:${device_namespace}:${retirement_legacy_service_account}"
retirement_legacy_cluster_binding="$(vk_access_clusterrolebinding_name \
  "$device_namespace" "$retirement_legacy_service_account")"
cat >"$scratch_dir/isolated-legacy-access.yaml" <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${retirement_legacy_service_account}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: CiscoDevice
      name: device-a
      uid: ${device_uid}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ${retirement_legacy_service_account}
  namespace: ${device_namespace}
  annotations:
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
  ownerReferences:
    - apiVersion: cisco.vk/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: CiscoDevice
      name: device-a
      uid: ${device_uid}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cisco-virtual-kubelet-device
subjects:
  - kind: ServiceAccount
    name: ${retirement_legacy_service_account}
    namespace: ${device_namespace}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${retirement_legacy_cluster_binding}
  annotations:
    topology.cisco.vk/device-namespace: ${device_namespace}
    topology.cisco.vk/device-name: device-a
    topology.cisco.vk/device-uid: ${device_uid}
    topology.cisco.vk/node-name: ${managed_node}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/worker-mode: legacy
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cisco-virtual-kubelet
subjects:
  - kind: ServiceAccount
    name: ${retirement_legacy_service_account}
    namespace: ${device_namespace}
EOF
kubectl create --as="$manager_username" \
  -f "$scratch_dir/isolated-legacy-access.yaml" >/dev/null

if kubectl annotate --as="$manager_username" ciscodevice device-a \
    --namespace "$device_namespace" \
    "topology.cisco.vk/isolated-legacy-worker=${device_uid}" --overwrite \
    --dry-run=server >"$scratch_dir/device-isolated-marker-before-status.txt" 2>&1; then
  echo "manager created an isolated worker marker before durable Preparing status" >&2
  exit 1
fi
grep -Eq 'isolated legacy worker marker is manager-created|denied the request|failed expression' \
  "$scratch_dir/device-isolated-marker-before-status.txt"

kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge -p "{
    \"status\":{\"legacyHandoff\":{
      \"phase\":\"Preparing\",
      \"deviceUID\":\"${device_uid}\",
      \"nodeName\":\"${managed_node}\",
      \"nodeUID\":\"${managed_node_uid}\",
      \"projectionHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",
      \"legacyWorkerUsername\":\"${retirement_legacy_username}\",
      \"requestedAt\":\"2026-01-01T00:10:00Z\"
    }}
  }" >/dev/null

# Preparing is the one intentional status-before-marker recovery point. A
# later phase may not be published until the exact UID marker is present.
if kubectl patch --as="$manager_username" ciscodevice device-a \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p '{"status":{"legacyHandoff":{"phase":"LegacyWriterPending","nodeReleasedAt":"2026-01-01T00:11:00Z"}}}' \
    >"$scratch_dir/device-handoff-phase-without-marker.txt" 2>&1; then
  echo "manager advanced handoff beyond Preparing without its UID marker" >&2
  exit 1
fi
grep -Eq 'marker must match the durable handoff phase|denied the request|failed expression' \
  "$scratch_dir/device-handoff-phase-without-marker.txt"

kubectl annotate --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" \
  "topology.cisco.vk/isolated-legacy-worker=${device_uid}" --overwrite >/dev/null

if kubectl annotate --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" \
    topology.cisco.vk/isolated-legacy-worker- --dry-run=server \
    >"$scratch_dir/device-isolated-marker-remove.txt" 2>&1; then
  echo "ordinary device editor removed isolated legacy worker authority" >&2
  exit 1
fi
grep -Eq 'isolated legacy worker marker is manager-created|denied the request|failed expression' \
  "$scratch_dir/device-isolated-marker-remove.txt"

if kubectl annotate --as="$manager_username" ciscodevice device-a \
    --namespace "$device_namespace" \
    topology.cisco.vk/isolated-legacy-worker- --dry-run=server \
    >"$scratch_dir/device-isolated-marker-early-remove.txt" 2>&1; then
  echo "manager removed the isolated worker marker before shared-writer transition" >&2
  exit 1
fi
grep -Fq 'removable only after an exact worker transition' \
  "$scratch_dir/device-isolated-marker-early-remove.txt"

if kubectl delete --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --dry-run=server \
    >"$scratch_dir/device-handoff-delete-negative.txt" 2>&1; then
  echo "CiscoDevice deletion succeeded during an in-progress legacy handoff" >&2
  exit 1
fi
grep -Eq 'no in-progress legacy handoff|denied the request|failed expression' \
  "$scratch_dir/device-handoff-delete-negative.txt"

if kubectl annotate --as="$topology_author_username" ciscodevice device-a \
    --namespace "$device_namespace" \
    topology.cisco.vk/request-legacy-handoff- --dry-run=server \
    >"$scratch_dir/device-handoff-request-remove.txt" 2>&1; then
  echo "topology author removed a consumed handoff request before completion" >&2
  exit 1
fi
grep -Eq 'accepted legacy handoff request is immutable|denied the request|failed expression' \
  "$scratch_dir/device-handoff-request-remove.txt"
if kubectl annotate --as="$topology_author_username" ciscodevice device-a \
    --namespace "$device_namespace" \
    topology.cisco.vk/request-legacy-handoff=22222222-2222-4222-8222-222222222222 \
    --overwrite --dry-run=server \
    >"$scratch_dir/device-handoff-request-change.txt" 2>&1; then
  echo "topology author changed a consumed handoff request before completion" >&2
  exit 1
fi
grep -Eq 'accepted legacy handoff request is immutable|bind the current managed Node UID|denied the request|failed expression' \
  "$scratch_dir/device-handoff-request-change.txt"

if kubectl patch --as="$manager_username" node "$managed_node" \
    --type=merge --dry-run=server \
    -p '{"metadata":{"annotations":{"topology.cisco.vk/managed":null}}}' \
    >"$scratch_dir/node-handoff-marker-negative.txt" 2>&1; then
  echo "manager removed managed Node ownership without the UID handoff marker" >&2
  exit 1
fi
grep -Eq 'exact UID-bound legacy handoff|managed Node bindings must be complete|denied the request|failed expression' \
  "$scratch_dir/node-handoff-marker-negative.txt"

# The historical managed identity must lose its topology mutation grants
# before the Node is released to the isolated compatibility worker. A retained
# projected token is then powerless during the writer transition.
kubectl delete --as="$manager_username" clusterrolebinding \
  "$worker_cluster_binding" >/dev/null
kubectl delete --as="$manager_username" rolebinding "$worker_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl delete --as="$manager_username" serviceaccount "$worker_service_account" \
  --namespace "$device_namespace" >/dev/null
test "$(kubectl auth can-i patch nodes --subresource=status \
  --as="$worker_username")" = "no"
test "$(kubectl auth can-i create leases.coordination.k8s.io \
  --namespace "$device_namespace" --as="$worker_username")" = "no"
test "$(kubectl auth can-i delete leases.coordination.k8s.io \
  --namespace "$device_namespace" --as="$worker_username")" = "no"

# Persist the release epoch before changing Node ownership. A crash here is
# safe: the old writer has no RBAC authority, and the next manager reconcile
# can idempotently finish the exact Node metadata transaction.
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"legacyHandoff":{"phase":"LegacyWriterPending","nodeReleasedAt":"2026-01-01T00:11:00Z"}}}' >/dev/null

kubectl patch --as="$manager_username" node "$managed_node" --type=merge -p "{
  \"metadata\":{\"annotations\":{
    \"topology.cisco.vk/managed\":null,
    \"topology.cisco.vk/device-namespace\":null,
    \"topology.cisco.vk/device-name\":null,
    \"topology.cisco.vk/device-uid\":null,
    \"topology.cisco.vk/node-uid\":null,
    \"topology.cisco.vk/worker-username\":null,
    \"topology.cisco.vk/worker-protocol\":null,
    \"topology.cisco.vk/worker-observed-revision\":null,
    \"topology.cisco.vk/projected-keys\":null,
    \"topology.cisco.vk/projection-hash\":null,
    \"topology.cisco.vk/managed-taints\":null,
    \"topology.cisco.vk/legacy-handoff\":\"${managed_node_uid}\"
  }}
}" >/dev/null
kubectl label --as="$retirement_legacy_username" node "$managed_node" \
  topology.cisco.vk/legacy-ready=true >/dev/null
if kubectl annotate --as="$retirement_legacy_username" node "$managed_node" \
    topology.cisco.vk/legacy-handoff- --dry-run=server \
    >"$scratch_dir/node-handoff-marker-remove.txt" 2>&1; then
  echo "legacy worker removed the manager's Node handoff audit marker" >&2
  exit 1
fi
grep -Eq 'preserve its handoff marker|manager/bound-worker owned|only the manager may remove or change a released Node handoff marker|denied the request|failed expression' \
  "$scratch_dir/node-handoff-marker-remove.txt"

test "$(kubectl get node "$managed_node" \
  -o jsonpath='{.spec.taints[?(@.key=="topology.cisco.vk/uninitialized")].effect}')" = "NoSchedule"
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"nodeIdentity":null,"topologyProjection":null,"healthObservation":null,"workerRevision":null,"networkWorkerRevision":null,"legacyHandoff":{"phase":"SharedWriterPending","isolatedReadyAt":"2026-01-01T00:12:00Z"}}}' >/dev/null
# The manager-only released-Node exception must not broaden even the exact
# generated legacy worker's Node authority. It may report readiness above, but
# it cannot clear the initialization fence before the manager has durably
# advanced the handoff.
if kubectl patch --as="$retirement_legacy_username" node "$managed_node" --type=json \
    --dry-run=server -p '[{"op":"remove","path":"/spec/taints/0"}]' \
    >"$scratch_dir/node-released-manager-exception-negative.txt" 2>&1; then
  echo "generated legacy worker used the released-Node manager exception" >&2
  exit 1
fi
grep -Eq 'only the manager may remove or change a released Node initialization fence|denied the request|failed expression' \
  "$scratch_dir/node-released-manager-exception-negative.txt"
kubectl patch --as="$manager_username" node "$managed_node" --type=json \
  -p '[{"op":"remove","path":"/spec/taints/0"}]' >/dev/null
test -z "$(kubectl get node "$managed_node" \
  -o jsonpath='{.spec.taints[?(@.key=="topology.cisco.vk/uninitialized")].effect}')"

# Even with managed status released, Helm must not retire the topology boundary
# while the temporary identity marker records an incomplete shared replacement.
if helm upgrade "$release_name" "$chart_dir" \
    --namespace "$system_namespace" \
    "${image_values[@]}" \
    --set topology.enabled=false \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.networkManagement.accessMode=readWrite \
    --set gnoi.enableSoftwareUpgrade=true \
    >"$scratch_dir/topology-disable-isolated-marker.txt" 2>&1; then
  echo "Helm disabled managed topology while an isolated worker marker remained" >&2
  exit 1
fi
grep -Fq 'legacy handoff is SharedWriterPending, not Complete' \
  "$scratch_dir/topology-disable-isolated-marker.txt"

# Replace the now-drained isolated identity with the exact namespace-shared
# compatibility identity, then revoke every temporary object before Complete.
shared_compatibility_service_account="cisco-virtual-kubelet"
shared_compatibility_username="system:serviceaccount:${device_namespace}:${shared_compatibility_service_account}"
shared_compatibility_cluster_binding="$(vk_access_clusterrolebinding_name \
  "$device_namespace" "$shared_compatibility_service_account")"
kubectl create --as="$manager_username" serviceaccount \
  "$shared_compatibility_service_account" --namespace "$device_namespace" >/dev/null
kubectl create --as="$manager_username" rolebinding \
  "$shared_compatibility_service_account" --namespace "$device_namespace" \
  --clusterrole=cisco-virtual-kubelet-device \
  --serviceaccount="${device_namespace}:${shared_compatibility_service_account}" >/dev/null
kubectl create --as="$manager_username" clusterrolebinding \
  "$shared_compatibility_cluster_binding" --clusterrole=cisco-virtual-kubelet \
  --serviceaccount="${device_namespace}:${shared_compatibility_service_account}" >/dev/null
kubectl label --as="$shared_compatibility_username" node "$managed_node" \
  topology.cisco.vk/legacy-ready=shared --overwrite >/dev/null
if kubectl annotate --as="$shared_compatibility_username" node "$managed_node" \
    topology.cisco.vk/legacy-handoff- --dry-run=server \
    >"$scratch_dir/node-shared-handoff-marker-remove.txt" 2>&1; then
  echo "shared compatibility worker removed the manager's Node handoff audit marker" >&2
  exit 1
fi
grep -Eq 'only the manager may remove or change a released Node handoff marker|denied the request|failed expression' \
  "$scratch_dir/node-shared-handoff-marker-remove.txt"

kubectl delete --as="$manager_username" rolebinding \
  "$retirement_legacy_service_account" --namespace "$device_namespace" >/dev/null
kubectl delete --as="$manager_username" clusterrolebinding \
  "$retirement_legacy_cluster_binding" >/dev/null
kubectl delete --as="$manager_username" serviceaccount \
  "$retirement_legacy_service_account" --namespace "$device_namespace" >/dev/null
if kubectl get serviceaccount "$retirement_legacy_service_account" \
    --namespace "$device_namespace" >/dev/null 2>&1 || \
   kubectl get rolebinding "$retirement_legacy_service_account" \
    --namespace "$device_namespace" >/dev/null 2>&1 || \
   kubectl get clusterrolebinding "$retirement_legacy_cluster_binding" \
    >/dev/null 2>&1; then
  echo "isolated legacy worker authority remained after shared replacement" >&2
  exit 1
fi
# Complete cannot become durable while its temporary device marker remains;
# cleanup followed by status publication is the only admitted ordering.
if kubectl patch --as="$manager_username" ciscodevice device-a \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p '{"status":{"legacyHandoff":{"phase":"Complete","completedAt":"2026-01-01T00:13:00Z"}}}' \
    >"$scratch_dir/device-handoff-complete-with-marker.txt" 2>&1; then
  echo "manager completed handoff while its isolated worker marker remained" >&2
  exit 1
fi
grep -Eq 'marker must match the durable handoff phase|denied the request|failed expression' \
  "$scratch_dir/device-handoff-complete-with-marker.txt"
kubectl annotate --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" \
  topology.cisco.vk/isolated-legacy-worker- >/dev/null
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"legacyHandoff":{"phase":"Complete","completedAt":"2026-01-01T00:13:00Z"}}}' >/dev/null
kubectl annotate --as="$device_editor_username" ciscodevice device-a \
  --namespace "$device_namespace" test.cisco.vk/post-handoff=allowed >/dev/null
kubectl annotate --as="$topology_author_username" ciscodevice device-a \
  --namespace "$device_namespace" \
  topology.cisco.vk/request-legacy-handoff- --dry-run=server >/dev/null
if kubectl patch --as="$device_editor_username" ciscodevice device-a \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p '{"status":{"legacyHandoff":null}}' \
    >"$scratch_dir/device-complete-handoff-remove.txt" 2>&1; then
  echo "ordinary status writer removed the durable completed handoff" >&2
  exit 1
fi
grep -Eq 'manager-owned CiscoDevice identity|all CiscoDevice status is manager-owned|legacy handoff may be cleared only by a new managed Node binding|denied the request|failed expression' \
  "$scratch_dir/device-complete-handoff-remove.txt"

# This live lookup now proves the exact policy/ledger identity, retained
# manager authority, no historical release-namespace shared binding, and
# Complete device state. The chart must preserve admission and omit that
# historical shared identity after the feature flag is turned off.
helm upgrade "$release_name" "$chart_dir" \
  --namespace "$system_namespace" \
  "${image_values[@]}" \
  --set topology.enabled=false \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.networkManagement.accessMode=readWrite \
  --set gnoi.enableSoftwareUpgrade=true >/dev/null
if kubectl get clusterrolebinding cisco-virtual-kubelet >/dev/null 2>&1 || \
   kubectl get rolebinding cisco-virtual-kubelet-device \
     --namespace "$system_namespace" >/dev/null 2>&1; then
  echo "topology retirement restored the release-wide shared worker identity" >&2
  exit 1
fi
kubectl get clusterrole "$admission_prefix-managed-topology-manager" >/dev/null
kubectl get clusterrolebinding "$admission_prefix-managed-topology-manager" >/dev/null
test "$(kubectl auth can-i delete replicasets.apps --all-namespaces \
  --as="$manager_username")" = "yes"
if kubectl get clusterrole cisco-virtual-kubelet-controller -o yaml | \
   grep -Fq '  - replicasets'; then
  echo "retirement leaked ReplicaSet authority into the base manager role" >&2
  exit 1
fi
test "$(kubectl get validatingadmissionpolicy \
  -l "app.kubernetes.io/instance=${release_name}" \
  -o jsonpath='{.items[*].metadata.name}' | wc -w | tr -d ' ')" -eq \
  "$expected_policy_count"
test -z "$(kubectl get deployment "${release_name}-cisco-virtual-kubelet-controller" \
  --namespace "$system_namespace" \
  -o jsonpath='{.spec.template.spec.containers[0].command}' | grep -o -- '--enable-managed-topology' || true)"

echo "managed topology real-API admission and default-scheduler contract passed"
