#!/usr/bin/env bash
# Sourced by managed-shared-worker-kind-test.sh after both bound-token clients
# exist. Never run against a lab/production cluster.

rebind_lease="cvk-device-0123456789abcdef-device-disruptive-mutation-01234567"
kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: ${rebind_lease}
  namespace: ${worker_namespace}
  labels:
    cisco.vk/device: device-0123456789abcdef
    cisco.vk/family: device-disruptive-mutation
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/retain-lease: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/node-name: lease-rebind-node
    topology.cisco.vk/node-uid: lease-rebind-node-uid
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/lease-purpose: device-mutation
    topology.cisco.vk/worker-username: ${network_username}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/app-worker-username: ${app_username}
spec: {}
EOF
rebind_uid="$(kubectl --context "$context" get lease "$rebind_lease" -n "$worker_namespace" -o jsonpath='{.metadata.uid}')"
acquire_patch='{"spec":{"holderIdentity":"software-upgrade/44444444-4444-4444-8444-444444444444","leaseDurationSeconds":3600,"acquireTime":"2026-01-01T00:00:00.000000Z","renewTime":"2026-01-01T00:00:00.000000Z","leaseTransitions":1}}'
expect_denied "username-only bootstrap cannot mutate a Lease" \
  "exact manager-bound worker Pod name and UID" \
  kubectl --kubeconfig "$bound_network_kubeconfig" patch lease "$rebind_lease" --type=merge -p "$acquire_patch"
expect_denied "manager cannot publish a half-bound Pod" \
  "canonical purpose and complete immutable" \
  kubectl --context "$context" --as="$manager_username" annotate lease "$rebind_lease" -n "$worker_namespace" \
  topology.cisco.vk/network-worker-pod-name="$network_pod"
kubectl --context "$context" --as="$manager_username" annotate lease "$rebind_lease" -n "$worker_namespace" \
  topology.cisco.vk/network-worker-pod-name="$network_pod" \
  topology.cisco.vk/network-worker-pod-uid="$network_pod_uid" >/dev/null
kubectl --kubeconfig "$bound_network_kubeconfig" patch lease "$rebind_lease" --type=merge -p "$acquire_patch" >/dev/null
rebind_spec="$(kubectl --context "$context" get lease "$rebind_lease" -n "$worker_namespace" -o jsonpath='{.spec}')"
expect_denied "manager rebind cannot release an active lock" \
  "only the manager may create" \
  kubectl --context "$context" --as="$manager_username" patch lease "$rebind_lease" -n "$worker_namespace" \
  --type=merge -p '{"spec":{"holderIdentity":null,"leaseDurationSeconds":null,"acquireTime":null,"renewTime":null}}'
expect_denied "manager rebind cannot change device ownership" \
  "only the manager may create" \
  kubectl --context "$context" --as="$manager_username" annotate lease "$rebind_lease" -n "$worker_namespace" \
  topology.cisco.vk/device-uid=foreign --overwrite
kubectl --context "$context" --as="$manager_username" annotate lease "$rebind_lease" -n "$worker_namespace" \
  topology.cisco.vk/network-worker-pod-uid=replacement-pod --overwrite >/dev/null
expect_denied "old bound token loses Lease mutation authority after rotation" \
  "exact manager-bound worker Pod name and UID" \
  kubectl --kubeconfig "$bound_network_kubeconfig" patch lease "$rebind_lease" --type=merge \
  -p '{"spec":{"renewTime":"2026-01-01T00:00:01.000000Z"}}'
expect_denied "app token cannot substitute for network binding" \
  "exact manager-bound worker Pod name and UID" \
  kubectl --kubeconfig "$bound_kubeconfig" patch lease "$rebind_lease" -n "$worker_namespace" --type=merge \
  -p '{"spec":{"renewTime":"2026-01-01T00:00:01.000000Z"}}'
test "$rebind_uid" = "$(kubectl --context "$context" get lease "$rebind_lease" -n "$worker_namespace" -o jsonpath='{.metadata.uid}')"
test "$rebind_spec" = "$(kubectl --context "$context" get lease "$rebind_lease" -n "$worker_namespace" -o jsonpath='{.spec}')"
kubectl --context "$context" --as="$manager_username" delete lease "$rebind_lease" -n "$worker_namespace" >/dev/null
echo "retained Lease bootstrap, metadata-only rotation, stale/cross-plane denial passed"
