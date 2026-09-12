#!/usr/bin/env bash

# Exercise the managed-topology trust boundary through a real Kubernetes API
# server and prove the scheduling contract with the in-tree kube-scheduler.
# The caller supplies the disposable cluster; CI runs this against kind.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$chart_dir/../.." && pwd)"
release_name="cvk-topology-it"
admission_prefix="${release_name}-cisco-virtual-kubelet"
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

sha256_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
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
    cvk-topology-it-worker cvk-topology-it-legacy-worker \
    cvk-topology-it-retirement-worker \
    --ignore-not-found --wait=true --timeout=60s \
    >/dev/null || cleanup_status=1
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
  --set gnoi.enableSoftwareUpgrade=true >/dev/null
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
test "$policy_count" -eq 8
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

# Re-read the server-stored Specs and prove API defaulting/canonicalization has
# not changed the complete contract the manager hashes at startup. Custom
# release names are included in this normalization check.
live_admission_manifest="$scratch_dir/live-admission.yaml"
first_policy=true
for suffix in \
  managed-node managed-pod-status managed-device managed-rollout managed-upgrade-leaf \
  topology-policy topology-ledger managed-maintenance-lease; do
  if [ "$first_policy" = false ]; then
    printf '%s\n' '---' >>"$live_admission_manifest"
  fi
  kubectl get validatingadmissionpolicy \
    "${admission_prefix}-${suffix}" -o yaml >>"$live_admission_manifest"
  first_policy=false
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
      -run '^TestRenderedManagedAdmissionContract$' -count=1
)

# Persist the CiscoDevice before deriving its incarnation-bound worker name.
# Production uses the resolved virtual Node name plus the device UID hash; the
# test must exercise that exact identity contract rather than a synthetic alias.
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
worker_revision="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
legacy_device_uid="$(kubectl get ciscodevice device-legacy --namespace "$device_namespace" \
  -o jsonpath='{.metadata.uid}')"
legacy_uid_hash="$(printf '%s' "$legacy_device_uid" | sha256_stdin | cut -c1-8)"
legacy_service_account="cisco-vk-legacy-${legacy_node}-${legacy_uid_hash}"
legacy_username="system:serviceaccount:${device_namespace}:${legacy_service_account}"
device_key="device-$(printf '%s\0%s' "$device_namespace" device-a | sha256_stdin | cut -c1-16)"

# Create the exact per-device worker identity and bind only the fixed managed
# worker role. The manager role is already bound by the rendered chart output.
kubectl create serviceaccount "$worker_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl create clusterrolebinding cvk-topology-it-worker \
  --clusterrole=cisco-virtual-kubelet-managed-worker \
  --serviceaccount="${device_namespace}:${worker_service_account}" >/dev/null
kubectl create rolebinding cvk-topology-it-worker-device \
  --namespace "$device_namespace" \
  --clusterrole=cisco-virtual-kubelet-device \
  --serviceaccount="${device_namespace}:${worker_service_account}" >/dev/null

# An unselected topology-aware device gets a distinct UID-derived identity and
# keeps the legacy runtime/role. Fresh installs deliberately leave the old
# release-wide ServiceAccount unbound.
kubectl create serviceaccount "$legacy_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl create clusterrolebinding cvk-topology-it-legacy-worker \
  --clusterrole=cisco-virtual-kubelet \
  --serviceaccount="${device_namespace}:${legacy_service_account}" >/dev/null
kubectl create rolebinding cvk-topology-it-legacy-worker-device \
  --namespace "$device_namespace" \
  --clusterrole=cisco-virtual-kubelet-device \
  --serviceaccount="${device_namespace}:${legacy_service_account}" >/dev/null
test "$(kubectl auth can-i patch pods --subresource=status \
  --namespace "$device_namespace" \
  --as="system:serviceaccount:${system_namespace}:cisco-virtual-kubelet")" = "no"

# Upgrade enablement restores leaf writes only through the device-namespace
# RoleBinding; the cluster-wide managed-worker role carries no leaf rule.
test "$(kubectl auth can-i patch iosxesoftwareupgrades.ops.cisco.vk \
  --namespace "$device_namespace" --as="$worker_username")" = "yes"
test "$(kubectl auth can-i patch iosxesoftwareupgrades.ops.cisco.vk \
  --namespace "$system_namespace" --as="$worker_username")" = "no"

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
  labels:
    topology.kubernetes.io/region: test-region
    topology.kubernetes.io/zone: test-zone-a
spec: {}
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
kubectl create --as="$manager_username" -f "$scratch_dir/mutation-lease.yaml" >/dev/null
kubectl patch --as="$worker_username" lease "$mutation_lease" \
  --namespace "$device_namespace" --type=merge -p '{"spec":{
    "holderIdentity":"software-upgrade/44444444-4444-4444-8444-444444444444",
    "leaseDurationSeconds":3600,"acquireTime":"2026-01-01T00:00:00.000000Z",
    "renewTime":"2026-01-01T00:00:00.000000Z","leaseTransitions":1}}' >/dev/null

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
kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
  -p '{"status":{"phase":"Ready"}}' >/dev/null
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p "{\"status\":{\"legacyHandoff\":{\"phase\":\"Complete\",\"deviceUID\":\"${legacy_device_uid}\",\"nodeName\":\"${legacy_node}\",\"nodeUID\":\"11111111-1111-4111-8111-111111111111\",\"projectionHash\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"legacyWorkerUsername\":\"${legacy_username}\",\"requestedAt\":\"2026-01-01T00:00:00Z\",\"nodeReleasedAt\":\"2026-01-01T00:00:01Z\",\"completedAt\":\"2026-01-01T00:00:02Z\"}}}" \
    >"$scratch_dir/device-legacy-handoff-forgery.txt" 2>&1; then
  echo "legacy status writer forged a completed manager handoff" >&2
  exit 1
fi
grep -Eq 'manager-owned CiscoDevice identity|denied the request|failed expression' \
  "$scratch_dir/device-legacy-handoff-forgery.txt"
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --subresource=status --type=merge --dry-run=server \
    -p "{\"status\":{\"workerRevision\":{\"desiredRevision\":\"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\",\"deploymentUID\":\"22222222-2222-4222-8222-222222222222\",\"deploymentGeneration\":1,\"observedAt\":\"2026-01-01T00:00:00Z\"},\"healthObservation\":{\"observedAt\":\"2026-01-01T00:00:00Z\",\"nodeReadyHeartbeatTime\":\"2026-01-01T00:00:00Z\",\"deviceConditionsHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"},\"topologyLock\":{\"state\":\"Active\",\"policyEpoch\":1,\"acquisitionID\":\"cccccccccccccccccccccccccccccccc\",\"campaignNamespace\":\"${device_namespace}\",\"campaignName\":\"forged\",\"campaignUID\":\"22222222-2222-4222-8222-222222222222\",\"planHash\":\"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\",\"reservationID\":\"forged-reservation\",\"deviceUID\":\"${legacy_device_uid}\",\"deviceGeneration\":1,\"nodeUID\":\"11111111-1111-4111-8111-111111111111\",\"projectionHash\":\"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\",\"acquiredAt\":\"2026-01-01T00:00:00Z\"}}}" \
    >"$scratch_dir/device-manager-status-forgery.txt" 2>&1; then
  echo "legacy status writer forged manager worker, health, or topology-lock authority" >&2
  exit 1
fi
grep -Eq 'manager-owned CiscoDevice identity|denied the request|failed expression' \
  "$scratch_dir/device-manager-status-forgery.txt"

# The isolated legacy identity marker itself selects UID-derived broad worker
# RBAC. It must be absent at user creation, manager-created with the exact live
# CiscoDevice UID, and immutable thereafter.
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
kubectl patch --as="$manager_username" ciscodevice device-legacy \
  --namespace "$device_namespace" --type=merge \
  -p "{\"metadata\":{\"annotations\":{\"topology.cisco.vk/isolated-legacy-worker\":\"${legacy_device_uid}\"}}}" >/dev/null
if kubectl patch --as="$device_editor_username" ciscodevice device-legacy \
    --namespace "$device_namespace" --type=json --dry-run=server \
    -p '[{"op":"remove","path":"/metadata/annotations/topology.cisco.vk~1isolated-legacy-worker"}]' \
    >"$scratch_dir/device-isolated-marker-remove.txt" 2>&1; then
  echo "ordinary device editor removed isolated legacy worker authority" >&2
  exit 1
fi
grep -Eq 'isolated legacy worker marker is manager-created|denied the request|failed expression' \
  "$scratch_dir/device-isolated-marker-remove.txt"
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
grep -Eq 'in-flight legacy handoff retains managed binding state|denied the request|failed expression' \
  "$scratch_dir/device-handoff-missing-binding.txt"
if kubectl patch --as="$manager_username" ciscodevice device-a \
    --namespace "$device_namespace" --subresource=status --type=merge \
    --dry-run=server \
    -p "{\"status\":{\"legacyHandoff\":{\"phase\":\"Complete\",\"deviceUID\":\"${device_uid}\",\"nodeName\":\"${managed_node}\",\"nodeUID\":\"${managed_node_uid}\",\"projectionHash\":\"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"legacyWorkerUsername\":\"${legacy_username}\",\"requestedAt\":\"2026-01-01T00:00:00Z\",\"nodeReleasedAt\":\"2026-01-01T00:00:01Z\",\"completedAt\":\"2026-01-01T00:00:02Z\"}}}" \
    >"$scratch_dir/device-handoff-dual-writer.txt" 2>&1; then
  echo "manager published Complete while managed binding state remained" >&2
  exit 1
fi
grep -Eq 'completed handoff releases it|denied the request|failed expression' \
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
grep -Eq 'finalizers and owner references are manager-owned|denied the request|failed expression' \
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
# gate. The manager image is intentionally unavailable in this test, so the
# fixtures below reproduce only its exact API transactions; no device RPC is
# involved.
ledger_uid="$(kubectl get configmap "$ledger_name" --namespace "$system_namespace" \
  -o jsonpath='{.metadata.uid}')"
kubectl patch --as="$manager_username" configmap "$ledger_name" \
  --namespace "$system_namespace" --type=merge \
  -p "{\"data\":{\"ledger.json\":\"{\\\"version\\\":\\\"v1\\\",\\\"uid\\\":\\\"${ledger_uid}\\\",\\\"reservations\\\":{}}\"}}" >/dev/null
kubectl annotate --as="$manager_username" configmap "$policy_name" \
  --namespace "$system_namespace" \
  "topology.cisco.vk/ledger-uid=${ledger_uid}" --overwrite >/dev/null

# A live downgrade must reject a still-managed Node even though all retained
# policy/RBAC coordinates are present and valid.
if helm upgrade "$release_name" "$chart_dir" \
    --namespace "$system_namespace" \
    "${image_values[@]}" \
    --set topology.enabled=false \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set gnoi.enableSoftwareUpgrade=true \
    >"$scratch_dir/topology-disable-incomplete.txt" 2>&1; then
  echo "Helm disabled managed topology before reverse handoff completed" >&2
  exit 1
fi
grep -Eq 'still has managed status.nodeIdentity|handoff.*not Complete' \
  "$scratch_dir/topology-disable-incomplete.txt"

# Release the synthetic lock, authorize the exact Node UID, and persist the
# manager-only device marker before publishing durable handoff state.
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"topologyLock":null}}' >/dev/null
kubectl annotate --as="$topology_author_username" ciscodevice device-a \
  --namespace "$device_namespace" \
  "topology.cisco.vk/request-legacy-handoff=${managed_node_uid}" --overwrite >/dev/null
kubectl annotate --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" \
  "topology.cisco.vk/isolated-legacy-worker=${device_uid}" --overwrite >/dev/null

retirement_legacy_service_account="cisco-vk-legacy-${managed_node}-${worker_uid_hash}"
retirement_legacy_username="system:serviceaccount:${device_namespace}:${retirement_legacy_service_account}"
kubectl create serviceaccount "$retirement_legacy_service_account" \
  --namespace "$device_namespace" >/dev/null
kubectl create clusterrolebinding cvk-topology-it-retirement-worker \
  --clusterrole=cisco-virtual-kubelet \
  --serviceaccount="${device_namespace}:${retirement_legacy_service_account}" >/dev/null
kubectl create rolebinding cvk-topology-it-retirement-worker-device \
  --namespace "$device_namespace" \
  --clusterrole=cisco-virtual-kubelet-device \
  --serviceaccount="${device_namespace}:${retirement_legacy_service_account}" >/dev/null

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
grep -Eq 'preserve its handoff marker|manager/bound-worker owned|denied the request|failed expression' \
  "$scratch_dir/node-handoff-marker-remove.txt"

kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"legacyHandoff":{"phase":"LegacyWriterPending","nodeReleasedAt":"2026-01-01T00:11:00Z"}}}' >/dev/null
kubectl patch --as="$manager_username" ciscodevice device-a \
  --namespace "$device_namespace" --subresource=status --type=merge \
  -p '{"status":{"nodeIdentity":null,"topologyProjection":null,"healthObservation":null,"workerRevision":null,"legacyHandoff":{"phase":"Complete","completedAt":"2026-01-01T00:12:00Z"}}}' >/dev/null
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
# manager authority, no shared binding, and Complete device state. The chart
# must preserve admission and omit the historical shared identity after the
# feature flag is turned off.
helm upgrade "$release_name" "$chart_dir" \
  --namespace "$system_namespace" \
  "${image_values[@]}" \
  --set topology.enabled=false \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
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
  -o jsonpath='{.items[*].metadata.name}' | wc -w | tr -d ' ')" -eq 8
test -z "$(kubectl get deployment "${release_name}-cisco-virtual-kubelet-controller" \
  --namespace "$system_namespace" \
  -o jsonpath='{.spec.template.spec.containers[0].command}' | grep -o -- '--enable-managed-topology' || true)"

echo "managed topology real-API admission and default-scheduler contract passed"
