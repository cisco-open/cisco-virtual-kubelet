#!/usr/bin/env bash
# Sourced only in the disposable shared-worker kind suite, with real Pod-bound tokens.
drain_leaf=split-drain-probe
app_revision="sha256:$(printf 'a%.0s' {1..64})"
network_revision="sha256:$(printf 'b%.0s' {1..64})"
drain_session=00000000-0000-4000-8000-000000000001
kubectl --context "$context" --as="$manager_username" create -f - >/dev/null <<EOF
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: ${drain_leaf}
  namespace: ${worker_namespace}
  annotations:
    topology.cisco.vk/managed: "true"
    topology.cisco.vk/device-namespace: ${worker_namespace}
    topology.cisco.vk/device-name: ${network_device_name}
    topology.cisco.vk/device-uid: ${network_device_uid}
    topology.cisco.vk/device-generation: "1"
    topology.cisco.vk/node-name: split-drain-node
    topology.cisco.vk/node-uid: split-drain-node-uid
    topology.cisco.vk/worker-username: ${network_username}
    topology.cisco.vk/worker-protocol: rollout-v1
    topology.cisco.vk/app-worker-username: ${app_username}
    topology.cisco.vk/app-worker-pod-name: ${reserved_pod}
    topology.cisco.vk/app-worker-pod-uid: ${reserved_pod_uid}
    topology.cisco.vk/app-worker-config-revision: ${app_revision}
    topology.cisco.vk/network-worker-username: ${network_username}
    topology.cisco.vk/network-worker-pod-name: ${network_pod}
    topology.cisco.vk/network-worker-pod-uid: ${network_pod_uid}
    topology.cisco.vk/campaign-namespace: ${worker_namespace}
    topology.cisco.vk/campaign-name: split-drain
    topology.cisco.vk/campaign-uid: campaign-uid
    topology.cisco.vk/plan-hash: ${network_revision}
    topology.cisco.vk/ledger-uid: ledger-uid
    topology.cisco.vk/reservation-id: reservation-1
spec:
  deviceRef:
    name: ${network_device_name}
  imageSource:
    url: https://images.example.test/cat9k.bin
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  targetVersion: 17.18.03
EOF
drain_leaf_uid="$(kubectl --context "$context" get iosxesoftwareupgrade "$drain_leaf" -n "$worker_namespace" -o jsonpath='{.metadata.uid}')"
kubectl --context "$context" --as="$manager_username" patch iosxesoftwareupgrade "$drain_leaf" -n "$worker_namespace" --subresource=status --type=merge -p "{
 \"status\": {
  \"managerAdmission\": {\"protocolVersion\":\"rollout-v1\",\"state\":\"Pending\",
   \"campaignUID\":\"campaign-uid\",\"planHash\":\"$network_revision\",
   \"policyUID\":\"policy-uid\",\"policyResourceVersion\":\"1\",\"policyEpoch\":1,
   \"ledgerUID\":\"ledger-uid\",\"reservationID\":\"reservation-1\",
   \"topologyLockID\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"leafUID\":\"$drain_leaf_uid\",
   \"deviceUID\":\"$network_device_uid\",\"deviceGeneration\":1,\"physicalIdentity\":\"serial-1\",
   \"nodeUID\":\"split-drain-node-uid\",\"controlRevision\":0,\"updatedAt\":\"2026-01-01T00:00:00Z\"},
  \"managerControl\":{\"revision\":0,\"updatedAt\":\"2026-01-01T00:00:00Z\"},
  \"managerDrain\":{\"protocolVersion\":\"pdb-drain-v1\",\"state\":\"Preparing\",
   \"sessionToken\":\"$drain_session\",\"reservationID\":\"reservation-1\",
   \"policyEpoch\":1,\"controlRevision\":0,\"nodeUID\":\"split-drain-node-uid\",
   \"nodeUnschedulableBefore\":false,\"maintenanceTaintPresentBefore\":false,
   \"startedAt\":\"2026-01-01T00:00:00Z\",\"drainDeadline\":\"2026-01-01T00:10:00Z\",
   \"updatedAt\":\"2026-01-01T00:00:00Z\"}
 }}" >/dev/null
network_control="{\"status\":{\"workerControl\":{\"observedAdmissionState\":\"Pending\",\"observedPolicyEpoch\":1,\"observedControlRevision\":0,\"observedWorkerConfigRevision\":\"$network_revision\",\"effectiveState\":\"Ready\",\"updatedAt\":\"2026-01-01T00:00:01Z\"}}}"
kubectl --kubeconfig "$bound_network_kubeconfig" patch iosxesoftwareupgrade "$drain_leaf" --subresource=status --type=merge -p "$network_control" >/dev/null
app_inventory="{\"status\":{\"workerDrain\":{\"protocolVersion\":\"pdb-drain-v1\",\"observedSessionToken\":\"$drain_session\",\"observedPolicyEpoch\":1,\"observedControlRevision\":0,\"observedWorkerConfigRevision\":\"$app_revision\",\"observedWorkerPodUID\":\"$reserved_pod_uid\",\"updatedAt\":\"2026-01-01T00:00:02Z\",\"inventoryRevision\":1,\"inventoryComplete\":true,\"inventoryObservedAt\":\"2026-01-01T00:00:02Z\",\"unknownDeviceWorkloadCount\":0,\"foreignDeviceWorkloadCount\":0}}}"
expect_denied "network worker cannot forge app drain inventory" "split-worker drain inventory is writable only" \
  kubectl --kubeconfig "$bound_network_kubeconfig" patch iosxesoftwareupgrade "$drain_leaf" --subresource=status --type=merge -p "$app_inventory"
kubectl --kubeconfig "$bound_kubeconfig" patch iosxesoftwareupgrade "$drain_leaf" --subresource=status --type=merge -p "$app_inventory" >/dev/null
expect_denied "app worker cannot write gNOI phase" "managed upgrade leaves are manager-owned" \
  kubectl --kubeconfig "$bound_kubeconfig" patch iosxesoftwareupgrade "$drain_leaf" --subresource=status --type=merge -p '{"status":{"phase":"Succeeded"}}'
expect_denied "app worker cannot change network acknowledgement" "managed upgrade leaves are manager-owned" \
  kubectl --kubeconfig "$bound_kubeconfig" patch iosxesoftwareupgrade "$drain_leaf" --subresource=status --type=merge -p '{"status":{"workerControl":{"effectiveState":"Claimed"}}}'
kubectl --context "$context" --as="$manager_username" annotate iosxesoftwareupgrade "$drain_leaf" -n "$worker_namespace" topology.cisco.vk/app-worker-pod-uid=replacement --overwrite >/dev/null
expect_denied "old app Pod cannot publish inventory after rotation" "exact manager-bound worker Pod name and UID" \
  kubectl --kubeconfig "$bound_kubeconfig" patch iosxesoftwareupgrade "$drain_leaf" --subresource=status --type=merge -p '{"status":{"workerDrain":{"inventoryRevision":2,"inventoryObservedAt":"2026-01-01T00:00:03Z","updatedAt":"2026-01-01T00:00:03Z"}}}'
# A replacement app worker must be bound even while the workload is protected.
kubectl --context "$context" --as="$manager_username" patch pod "$ordinary_pod" -n "$worker_namespace" --type=merge \
  -p "{\"metadata\":{\"annotations\":{\"ops.cisco.vk/drain-session\":\"$drain_session\"},\"finalizers\":[\"ops.cisco.vk/iosxe-rollout-drain\"]}}" >/dev/null
kubectl --context "$context" --as="$manager_username" annotate pod "$ordinary_pod" -n "$worker_namespace" \
  topology.cisco.vk/app-worker-pod-uid=replacement --overwrite >/dev/null
expect_denied "protected Pod still rejects unrelated manager edits" "only its exact drain protection" \
  kubectl --context "$context" --as="$manager_username" label pod "$ordinary_pod" -n "$worker_namespace" unrelated=forbidden
kubectl --context "$context" --as="$manager_username" patch pod "$ordinary_pod" -n "$worker_namespace" --type=merge \
  -p '{"metadata":{"annotations":{"ops.cisco.vk/drain-session":null},"finalizers":[]}}' >/dev/null
echo "split app/network revisions, drain-only writes, protected rebinding and stale token denial passed"
