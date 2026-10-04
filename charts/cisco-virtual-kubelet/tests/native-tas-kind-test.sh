#!/usr/bin/env bash

# Exercise the optional Kubernetes 1.37 Workload/PodGroup topology-aware
# scheduler against the exact checked-in CVK example. The caller owns the
# disposable kind cluster. The script also executes CVK's drain guard against
# a served scheduling-group Pod; it does not install a CVK runtime component.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../../.." && pwd)"
example="$repo_root/examples/topology/experimental-native-tas-v1.37.yaml"
namespace="edge-workloads"
node_a="cvk-native-tas-a"
node_b="cvk-native-tas-b"
expected_context="${CVK_NATIVE_TAS_TEST_CONTEXT:-kind-cvk-native-tas}"
heartbeat_pid=""

fail() {
  echo "native TAS conformance: $*" >&2
  exit 1
}

current_context="$(kubectl config current-context 2>/dev/null || true)"
if [[ "$current_context" != kind-* ]]; then
  fail "refusing to run on non-kind context ${current_context:-<unset>}"
fi
if [[ "$current_context" != "$expected_context" ]] && \
   [[ "${CVK_NATIVE_TAS_TEST_ALLOW_DISPOSABLE_CONTEXT:-}" != "true" ]]; then
  fail "refusing unexpected kind context $current_context; expected $expected_context"
fi

# Never adopt or delete a pre-existing namespace or Node. A prior interrupted
# run must be discarded with its disposable cluster rather than guessed at.
if kubectl get namespace "$namespace" >/dev/null 2>&1; then
  fail "namespace $namespace already exists; use a fresh disposable cluster"
fi
for node in "$node_a" "$node_b"; do
  if kubectl get node "$node" >/dev/null 2>&1; then
    fail "Node $node already exists; use a fresh disposable cluster"
  fi
done

cleanup() {
  local cleanup_status=0

  if [[ -n "$heartbeat_pid" ]]; then
    kill "$heartbeat_pid" >/dev/null 2>&1 || true
    wait "$heartbeat_pid" >/dev/null 2>&1 || true
    heartbeat_pid=""
  fi

  kubectl delete pod --all \
    --namespace "$namespace" --force --grace-period=0 \
    --ignore-not-found --wait=false >/dev/null 2>&1 || cleanup_status=1
  kubectl delete podgroup --all \
    --namespace "$namespace" --ignore-not-found \
    --wait=true --timeout=30s >/dev/null 2>&1 || cleanup_status=1
  kubectl delete workload --all \
    --namespace "$namespace" --ignore-not-found \
    --wait=true --timeout=30s >/dev/null 2>&1 || cleanup_status=1
  kubectl delete namespace "$namespace" --ignore-not-found \
    --wait=true --timeout=30s >/dev/null 2>&1 || cleanup_status=1
  kubectl delete node "$node_a" "$node_b" --ignore-not-found \
    --wait=true --timeout=30s >/dev/null 2>&1 || cleanup_status=1

  if kubectl get namespace "$namespace" >/dev/null 2>&1; then
    echo "native TAS conformance namespace remains after cleanup" >&2
    cleanup_status=1
  fi
  for node in "$node_a" "$node_b"; do
    if kubectl get node "$node" >/dev/null 2>&1; then
      echo "native TAS conformance Node $node remains after cleanup" >&2
      cleanup_status=1
    fi
  done
  return "$cleanup_status"
}

on_exit() {
  local test_status=$?
  local cleanup_status

  trap - EXIT
  set +e
  if [[ "$test_status" -ne 0 ]]; then
    kubectl get workload,podgroup,pod --namespace "$namespace" -o wide >&2 2>/dev/null || true
    kubectl get node "$node_a" "$node_b" -o wide >&2 2>/dev/null || true
  fi
  cleanup
  cleanup_status=$?
  if [[ "$test_status" -ne 0 ]]; then
    exit "$test_status"
  fi
  exit "$cleanup_status"
}
trap on_exit EXIT

kubectl version >/dev/null

discovery="$(kubectl get --raw /apis/scheduling.k8s.io/v1beta1)"
grep -Eq '"name"[[:space:]]*:[[:space:]]*"workloads"' <<<"$discovery" || \
  fail "scheduling.k8s.io/v1beta1 Workload API is unavailable"
grep -Eq '"name"[[:space:]]*:[[:space:]]*"podgroups"' <<<"$discovery" || \
  fail "scheduling.k8s.io/v1beta1 PodGroup API is unavailable"
kubectl explain workloads.scheduling.k8s.io.spec.podGroupTemplates.schedulingConstraints \
  >/dev/null
kubectl explain podgroups.scheduling.k8s.io.spec.workloadRef >/dev/null
kubectl explain podgroups.scheduling.k8s.io.spec.schedulingConstraints >/dev/null
kubectl explain pod.spec.schedulingGroup >/dev/null

kubectl create namespace "$namespace" >/dev/null

create_scheduler_node() {
  local name="$1"
  local fixture_id="$2"
  local site="$3"

  cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Node
metadata:
  name: ${name}
  labels:
    type: virtual-kubelet
    test.cisco.vk/native-tas: "true"
    test.cisco.vk/native-tas-node: "${fixture_id}"
    topology.cisco.vk/site: "${site}"
spec: {}
EOF
}

create_scheduler_node "$node_a" a site-a
create_scheduler_node "$node_b" b site-b

refresh_scheduler_nodes() {
  local heartbeat node
  heartbeat="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for node in "$node_a" "$node_b"; do
    kubectl patch node "$node" --subresource=status --type=merge -p "{
      \"status\":{
        \"capacity\":{\"cpu\":\"4\",\"memory\":\"8Gi\",\"pods\":\"16\"},
        \"allocatable\":{\"cpu\":\"4\",\"memory\":\"8Gi\",\"pods\":\"16\"},
        \"conditions\":[{
          \"type\":\"Ready\",\"status\":\"True\",\"reason\":\"ConformanceFixture\",
          \"message\":\"synthetic native TAS scheduler fixture\",
          \"lastHeartbeatTime\":\"${heartbeat}\",\"lastTransitionTime\":\"${heartbeat}\"
        }]
      }
    }" >/dev/null
  done
}

# Synthetic Nodes have no kubelet to renew their status. Keep their heartbeat
# current while uncached CI compiles the live integration guard; otherwise the
# node lifecycle controller correctly marks them NotReady and adds a taint,
# turning a scheduler assertion into a runner-speed race.
refresh_scheduler_nodes
(
  while true; do
    sleep 10
    refresh_scheduler_nodes >/dev/null 2>&1 || true
  done
) &
heartbeat_pid=$!
kubectl wait --for=condition=Ready node/"$node_a" node/"$node_b" \
  --timeout=15s >/dev/null

# Server-side dry-run is the compatibility boundary that the Kubernetes 1.35
# render test cannot provide. Apply the same file afterward so the scheduler
# proof covers the public example rather than a private copy of its schema.
kubectl apply --server-side --dry-run=server -f "$example" >/dev/null
kubectl apply -f "$example" >/dev/null

wait_for_binding() {
  local pod="$1"
  local node=""

  for _ in $(seq 1 45); do
    node="$(kubectl get pod "$pod" --namespace "$namespace" \
      -o jsonpath='{.spec.nodeName}')"
    if [[ -n "$node" ]]; then
      echo "$node"
      return 0
    fi
    sleep 1
  done
  kubectl describe pod "$pod" --namespace "$namespace" >&2
  return 1
}

wait_for_group_condition() {
  local group="$1"
  local expected_status="$2"
  local expected_reason="$3"
  local condition=""

  for _ in $(seq 1 45); do
    condition="$(kubectl get podgroup "$group" --namespace "$namespace" \
      -o jsonpath='{range .status.conditions[*]}{.type}{"="}{.status}{"/"}{.reason}{"\n"}{end}' \
      | awk -F= '$1 == "PodGroupInitiallyScheduled" {print $2}')"
    if [[ "$condition" == "${expected_status}/${expected_reason}" ]]; then
      return 0
    fi
    sleep 1
  done
  kubectl describe podgroup "$group" --namespace "$namespace" >&2
  return 1
}

assert_unbound() {
  local pod

  for pod in "$@"; do
    [[ -z "$(kubectl get pod "$pod" --namespace "$namespace" \
      -o jsonpath='{.spec.nodeName}')" ]] || \
      fail "Pod $pod was unexpectedly bound"
  done
}

first_node="$(wait_for_binding edge-worker-0)"
second_node="$(wait_for_binding edge-worker-1)"
[[ "$first_node" == "$second_node" ]] || \
  fail "TAS split the example Pods across $first_node and $second_node"
case "$first_node" in
  "$node_a"|"$node_b") ;;
  *) fail "example Pod bound outside the synthetic CVK Nodes: $first_node" ;;
esac
site="$(kubectl get node "$first_node" \
  -o jsonpath='{.metadata.labels.topology\.cisco\.vk/site}')"
[[ -n "$site" ]] || fail "selected Node has no topology.cisco.vk/site label"
wait_for_group_condition edge-workers-0 True Scheduled

# Exercise the production CVK guard through a real Kubernetes 1.37 API read.
# This must happen before replacement tests mutate the group fixture.
CVK_NATIVE_TAS_TEST_CONTEXT="$expected_context" \
CVK_NATIVE_TAS_GUARD_NAMESPACE="$namespace" \
CVK_NATIVE_TAS_GUARD_POD="edge-worker-0" \
  go test -tags native_tas_integration -count=1 -v \
    ./internal/controller -run '^TestNativeTASServedSchedulingGroupGuard$'

# Recreate one member after the group initially schedules. The replacement
# must retain the group's selected topology rather than silently scheduling in
# another domain. This is a scheduler recovery assertion; a supported workload
# controller remains a separate physical-CVK qualification requirement.
kubectl delete pod edge-worker-1 --namespace "$namespace" \
  --force --grace-period=0 --wait=true >/dev/null
cat <<EOF | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: edge-worker-1
  namespace: ${namespace}
  labels:
    app: edge-gang
spec:
  schedulingGroup:
    podGroupName: edge-workers-0
  nodeSelector:
    type: virtual-kubelet
  containers:
    - name: edge-worker
      image: registry.k8s.io/pause:3.10
      resources:
        requests:
          cpu: 100m
          memory: 128Mi
EOF
replacement_node="$(wait_for_binding edge-worker-1)"
[[ "$replacement_node" == "$first_node" ]] || \
  fail "replacement group member moved from $first_node to $replacement_node"

# A second group proves the topology constraint is enforced rather than the
# successful pair merely landing together by chance. Each Pod is individually
# feasible, but their selectors force different site domains, so the gang must
# remain unbound and report Unschedulable.
cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: scheduling.k8s.io/v1beta1
kind: PodGroup
metadata:
  name: edge-conflict
  namespace: ${namespace}
spec:
  schedulingPolicy:
    gang:
      minCount: 2
  schedulingConstraints:
    topology:
      - key: topology.cisco.vk/site
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-conflict-a
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-conflict
  nodeSelector:
    test.cisco.vk/native-tas-node: "a"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-conflict-b
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-conflict
  nodeSelector:
    test.cisco.vk/native-tas-node: "b"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF

wait_for_group_condition edge-conflict False Unschedulable
assert_unbound edge-conflict-a edge-conflict-b

# A maintenance-tainted selected member must block the whole gang. This
# models a CVK Node that remains registered while maintenance makes it
# ineligible; the scheduler must not partially bind the other member.
kubectl taint node "$node_a" cisco.vk/maintenance=true:NoSchedule >/dev/null
cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: scheduling.k8s.io/v1beta1
kind: PodGroup
metadata:
  name: edge-maintenance
  namespace: ${namespace}
spec:
  schedulingPolicy:
    gang:
      minCount: 2
  schedulingConstraints:
    topology:
      - key: topology.cisco.vk/site
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-maintenance-0
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-maintenance
  nodeSelector:
    test.cisco.vk/native-tas-node: "a"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-maintenance-1
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-maintenance
  nodeSelector:
    test.cisco.vk/native-tas-node: "a"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
wait_for_group_condition edge-maintenance False Unschedulable
assert_unbound edge-maintenance-0 edge-maintenance-1
kubectl taint node "$node_a" cisco.vk/maintenance- >/dev/null
maintenance_node_0="$(wait_for_binding edge-maintenance-0)"
maintenance_node_1="$(wait_for_binding edge-maintenance-1)"
[[ "$maintenance_node_0" == "$node_a" ]] && \
  [[ "$maintenance_node_1" == "$node_a" ]] || \
  fail "maintenance group did not recover completely on $node_a"
wait_for_group_condition edge-maintenance True Scheduled
kubectl delete pod edge-maintenance-0 edge-maintenance-1 \
  --namespace "$namespace" --force --grace-period=0 --wait=true >/dev/null
kubectl delete podgroup edge-maintenance --namespace "$namespace" \
  --wait=true --timeout=30s >/dev/null

# Each member is feasible in isolation, but the selected site lacks capacity
# for the complete gang. The group must stay entirely unbound with a visible
# Unschedulable condition.
cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: scheduling.k8s.io/v1beta1
kind: PodGroup
metadata:
  name: edge-capacity
  namespace: ${namespace}
spec:
  schedulingPolicy:
    gang:
      minCount: 2
  schedulingConstraints:
    topology:
      - key: topology.cisco.vk/site
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-capacity-0
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-capacity
  nodeSelector:
    test.cisco.vk/native-tas-node: "a"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
      resources:
        requests:
          cpu: "3"
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-capacity-1
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-capacity
  nodeSelector:
    test.cisco.vk/native-tas-node: "a"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
      resources:
        requests:
          cpu: "3"
EOF
wait_for_group_condition edge-capacity False Unschedulable
assert_unbound edge-capacity-0 edge-capacity-1

# Restart the native scheduler process and prove a new PodGroup is scheduled
# after kubelet recreates its static container. Deleting only the mirror Pod
# would not stop the underlying process, so this kind-only lane stops the CRI
# container inside the control-plane node and waits for a different container.
# This catches state that exists only in one scheduler process and verifies
# that API-backed group state survives the restart.
scheduler_pod="kube-scheduler-cvk-native-tas-control-plane"
kind_cluster="${expected_context#kind-}"
control_plane="${kind_cluster}-control-plane"
[[ "$(docker inspect --format '{{ index .Config.Labels "io.x-k8s.kind.cluster" }}' \
  "$control_plane")" == "$kind_cluster" ]] || \
  fail "container $control_plane is not part of kind cluster $kind_cluster"
scheduler_container_id="$(kubectl get pod "$scheduler_pod" \
  --namespace kube-system \
  -o jsonpath='{.status.containerStatuses[?(@.name=="kube-scheduler")].containerID}')"
runtime_scheduler_id="$(docker exec "$control_plane" \
  crictl ps --state Running --name kube-scheduler -q | head -1)"
[[ -n "$runtime_scheduler_id" ]] || \
  fail "could not resolve the running native scheduler container"
docker exec "$control_plane" crictl --timeout=30s stop --timeout=10 "$runtime_scheduler_id" >/dev/null
scheduler_restarted=false
for _ in $(seq 1 60); do
  new_scheduler_container_id="$(kubectl get pod "$scheduler_pod" \
    --namespace kube-system \
    -o jsonpath='{.status.containerStatuses[?(@.name=="kube-scheduler")].containerID}' \
    2>/dev/null || true)"
  ready="$(kubectl get pod "$scheduler_pod" --namespace kube-system \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' \
    2>/dev/null || true)"
  if [[ -n "$new_scheduler_container_id" ]] && \
     [[ "$new_scheduler_container_id" != "$scheduler_container_id" ]] && \
     [[ "$ready" == "True" ]]; then
    scheduler_restarted=true
    break
  fi
  sleep 1
done
[[ "$scheduler_restarted" == "true" ]] || \
  fail "native scheduler did not recover with a new container ID"

cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: scheduling.k8s.io/v1beta1
kind: PodGroup
metadata:
  name: edge-after-restart
  namespace: ${namespace}
spec:
  schedulingPolicy:
    gang:
      minCount: 2
  schedulingConstraints:
    topology:
      - key: topology.cisco.vk/site
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-after-restart-0
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-after-restart
  nodeSelector:
    test.cisco.vk/native-tas-node: "b"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
---
apiVersion: v1
kind: Pod
metadata:
  name: edge-after-restart-1
  namespace: ${namespace}
spec:
  schedulingGroup:
    podGroupName: edge-after-restart
  nodeSelector:
    test.cisco.vk/native-tas-node: "b"
  containers:
    - name: pause
      image: registry.k8s.io/pause:3.10
EOF
restart_node_0="$(wait_for_binding edge-after-restart-0)"
restart_node_1="$(wait_for_binding edge-after-restart-1)"
[[ "$restart_node_0" == "$node_b" ]] && \
  [[ "$restart_node_1" == "$node_b" ]] || \
  fail "post-restart group did not bind completely to $node_b"
wait_for_group_condition edge-after-restart True Scheduled

echo "native Kubernetes 1.37 TAS conformance passed: site=$site node=$first_node replacement=$replacement_node maintenance=blocked capacity=blocked scheduler-restart=recovered"
