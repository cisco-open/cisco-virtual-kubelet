#!/usr/bin/env bash

# Exercise the optional Kubernetes 1.37 Workload/PodGroup topology-aware
# scheduler against the exact checked-in CVK example. The caller owns the
# disposable kind cluster; this script never installs a CVK runtime component.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../../.." && pwd)"
example="$repo_root/examples/topology/experimental-native-tas-v1.37.yaml"
namespace="edge-workloads"
node_a="cvk-native-tas-a"
node_b="cvk-native-tas-b"
expected_context="${CVK_NATIVE_TAS_TEST_CONTEXT:-kind-cvk-native-tas}"

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

  kubectl delete pod \
    edge-worker-0 edge-worker-1 edge-conflict-a edge-conflict-b \
    --namespace "$namespace" --force --grace-period=0 \
    --ignore-not-found --wait=false >/dev/null 2>&1 || cleanup_status=1
  kubectl delete podgroup edge-workers-0 edge-conflict \
    --namespace "$namespace" --ignore-not-found \
    --wait=true --timeout=30s >/dev/null 2>&1 || cleanup_status=1
  kubectl delete workload edge-gang-policy \
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
for pod in edge-conflict-a edge-conflict-b; do
  [[ -z "$(kubectl get pod "$pod" --namespace "$namespace" \
    -o jsonpath='{.spec.nodeName}')" ]] || \
    fail "conflicting topology Pod $pod was unexpectedly bound"
done

echo "native Kubernetes 1.37 TAS conformance passed: site=$site node=$first_node"
