#!/usr/bin/env bash
# Sourced by the owned-cluster suite; exercise real native foreground GC.
foreground_deployment=foreground-worker-cleanup
create_reserved_deployment "$manager_username" "$foreground_deployment" >/dev/null
kubectl --context "$context" rollout status deployment/"$foreground_deployment" \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
foreground_pod="$(kubectl --context "$context" get pod -n "$worker_namespace" \
  -l "app=$foreground_deployment" -o jsonpath='{.items[0].metadata.name}')"
gc_patch() {
  kubectl --context "$context" \
    --as=system:serviceaccount:kube-system:generic-garbage-collector \
    --as-group=system:serviceaccounts --as-group=system:serviceaccounts:kube-system \
    --as-group=system:authenticated patch "$@" --namespace "$worker_namespace" \
    --type=merge --dry-run=server
}
expect_denied 'garbage collector cannot edit a live worker Pod' 'reserved worker Pod is immutable' \
  gc_patch pod "$foreground_pod" -p '{"metadata":{"labels":{"foreign":"changed"}}}'
expect_denied 'garbage collector cannot scale a worker Deployment' 'only the topology manager' \
  gc_patch deployment "$foreground_deployment" -p '{"spec":{"replicas":0}}'
# Keep one owned fixture Pod observable while native GC processes the cascade.
kubectl --context "$context" --as="$manager_username" patch pod "$foreground_pod" \
  -n "$worker_namespace" --type=merge \
  -p '{"metadata":{"finalizers":["test.cisco.vk/hold"]}}' >/dev/null
foreground_uid="$(kubectl --context "$context" get deployment "$foreground_deployment" \
  -n "$worker_namespace" -o jsonpath='{.metadata.uid}')"
kubectl --context "$context" --as="$manager_username" delete \
  --raw="/apis/apps/v1/namespaces/${worker_namespace}/deployments/${foreground_deployment}" -f - >/dev/null <<EOF
{"apiVersion":"meta.k8s.io/v1","kind":"DeleteOptions","propagationPolicy":"Foreground","preconditions":{"uid":"${foreground_uid}"}}
EOF
kubectl --context "$context" wait --for=jsonpath='{.metadata.deletionTimestamp}' \
  pod/"$foreground_pod" -n "$worker_namespace" --timeout=60s >/dev/null
expect_denied 'garbage collector cannot remove a foreign finalizer' 'reserved worker Pod is immutable' \
  gc_patch pod "$foreground_pod" -p '{"metadata":{"finalizers":[]}}'
expect_denied 'garbage collector cannot edit a terminating worker Pod' 'reserved worker Pod is immutable' \
  gc_patch pod "$foreground_pod" -p '{"metadata":{"labels":{"foreign":"changed"}}}'
# Release only the test-owned hold; native GC must remove foregroundDeletion.
kubectl --context "$context" --as="$manager_username" patch pod "$foreground_pod" \
  -n "$worker_namespace" --type=strategic \
  -p '{"metadata":{"$deleteFromPrimitiveList/finalizers":["test.cisco.vk/hold"]}}' >/dev/null
kubectl --context "$context" wait --for=delete deployment/"$foreground_deployment" \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
test -z "$(kubectl --context "$context" get pods,replicasets \
  -n "$worker_namespace" -l "app=$foreground_deployment" -o name)"
echo 'native foreground deletion of reserved Deployment, ReplicaSet and Pod passed'
