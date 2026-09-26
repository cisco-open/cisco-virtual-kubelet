#!/usr/bin/env bash
# Sourced by the disposable bound-token suite; native health writes must not
# acquire CVK topology, identity, cordon, or healthy-status authority.
native_node=native-lifecycle-probe
# kubectl patch reads the status subresource first; the real controller reads
# the Node endpoint instead. Grant only this fixture read, not extra mutation.
kubectl --context "$context" create clusterrole native-node-fixture-status-read \
  --verb=get --resource=nodes/status --resource-name="$native_node" >/dev/null
kubectl --context "$context" create clusterrolebinding native-node-fixture-status-read \
  --clusterrole=native-node-fixture-status-read \
  --serviceaccount=kube-system:node-controller >/dev/null
kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: v1
kind: Node
metadata:
  name: ${native_node}
  labels:
    kubernetes.io/os: linux
    kubernetes.io/arch: amd64
    topology.kubernetes.io/zone: protected-zone
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/worker-username: ${app_username}
    topology.cisco.vk/app-worker-username: ${app_username}
    topology.cisco.vk/app-worker-pod-name: ${reserved_pod}
    topology.cisco.vk/app-worker-pod-uid: ${reserved_pod_uid}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/worker-protocol: rollout-v1
spec:
  unschedulable: true
  taints:
    - key: cisco.vk/device-maintenance
      value: "true"
      effect: NoSchedule
EOF
native_node_uid="$(kubectl --context "$context" get node "$native_node" -o jsonpath='{.metadata.uid}')"
kubectl --context "$context" --as="$manager_username" annotate node "$native_node" \
  "topology.cisco.vk/node-uid=$native_node_uid" >/dev/null
native_node_patch() {
  local patch_type=merge arg
  for arg in "$@"; do
    if [ "$arg" = --subresource=status ]; then patch_type=strategic; fi
  done
  kubectl --context "$context" \
    --as=system:serviceaccount:kube-system:node-controller \
    --as-group=system:serviceaccounts --as-group=system:serviceaccounts:kube-system \
    --as-group=system:authenticated patch node "$native_node" --type="$patch_type" "$@"
}
native_node_patch --subresource=status \
  -p '{"status":{"conditions":[{"type":"Ready","status":"Unknown","reason":"NodeStatusNeverUpdated"}]}}' >/dev/null
expect_denied 'native controller cannot fabricate healthy readiness' 'managed Nodes are' \
  native_node_patch --subresource=status --dry-run=server \
  -p '{"status":{"conditions":[{"type":"Ready","status":"True","reason":"Forged"}]}}'
expect_denied 'native controller cannot inflate capacity' 'managed Nodes are' \
  native_node_patch --subresource=status --dry-run=server -p '{"status":{"capacity":{"pods":"999"}}}'
native_node_patch -p '{"spec":{"taints":[{"key":"cisco.vk/device-maintenance","value":"true","effect":"NoSchedule"},{"key":"node.kubernetes.io/unreachable","effect":"NoSchedule"},{"key":"node.kubernetes.io/unreachable","effect":"NoExecute"}]}}' >/dev/null
expect_denied 'native controller cannot remove the CVK maintenance guard' 'managed Nodes are' \
  native_node_patch --dry-run=server -p '{"spec":{"taints":[]}}'
expect_denied 'native controller cannot clear the operator cordon' 'managed Nodes are' \
  native_node_patch --dry-run=server -p '{"spec":{"unschedulable":false}}'
expect_denied 'native controller cannot rewrite projected topology' 'managed Nodes are' \
  native_node_patch --dry-run=server -p '{"metadata":{"labels":{"topology.kubernetes.io/zone":"foreign"}}}'
native_node_patch -p '{"metadata":{"labels":{"beta.kubernetes.io/os":"linux","beta.kubernetes.io/arch":"amd64"}}}' >/dev/null
expect_denied 'native beta label must agree with the stable label' 'managed Nodes are' \
  native_node_patch --dry-run=server -p '{"metadata":{"labels":{"beta.kubernetes.io/os":"foreign"}}}'
native_node_patch -p '{"spec":{"taints":[{"key":"cisco.vk/device-maintenance","value":"true","effect":"NoSchedule"}]}}' >/dev/null
kubectl --context "$context" --as="$manager_username" delete node "$native_node" >/dev/null
echo 'native unhealthy-node status and taints preserve topology, identity and maintenance guards'
