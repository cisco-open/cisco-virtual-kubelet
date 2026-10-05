#!/usr/bin/env bash
# Sourced by the owned-cluster suite; exercise real native foreground GC.
foreground_deployment=foreground-worker-cleanup
create_reserved_deployment "$manager_username" "$foreground_deployment" >/dev/null
# Model a stored legacy direct-Secret projection before upgrading the policy.
# This temporary historical-policy setup is confined to this owned kind cluster;
# no production policy or finalizer is bypassed by the migration procedure.
legacy_ca_policy="$(kubectl --context "$context" get validatingadmissionpolicy -o name | grep -- '-shared-worker-deployment$')"
kubectl --context "$context" get "$legacy_ca_policy" -o json > "$scratch_dir/ca-policy-before.json"
legacy_ca_expression="request.operation == 'DELETE' || !has(object.spec.template.spec.volumes) || object.spec.template.spec.volumes.all(v, v.name != 'device-tls-ca' || has(v.emptyDir) || (has(v.projected) && v.projected.sources.size() == 1 && has(v.projected.sources[0].secret) && has(v.projected.sources[0].secret.items) && v.projected.sources[0].secret.items.size() == 1 && v.projected.sources[0].secret.items[0].key == 'ca.crt' && v.projected.sources[0].secret.items[0].path == 'ca.crt'))"
kubectl --context "$context" patch "$legacy_ca_policy" --type=json \
  -p "$(jq -nc --arg expression "$legacy_ca_expression" '[{op:"replace",path:"/spec/validations/1/expression",value:$expression}]')" >/dev/null
legacy_ca_patch='{"spec":{"template":{"spec":{"volumes":[{"name":"device-tls-ca","projected":{"sources":[{"secret":{"name":"historical-public-ca","optional":true,"items":[{"key":"ca.crt","path":"ca.crt"}]}}]}}]}}}}'
for attempt in $(seq 1 30); do
  if kubectl --context "$context" --as="$manager_username" patch deployment "$foreground_deployment" \
    -n "$worker_namespace" --type=merge --dry-run=server -p "$legacy_ca_patch" \
    >"$scratch_dir/legacy-ca-policy-ready.log" 2>&1; then break; fi
  if [ "$attempt" = 30 ]; then cat "$scratch_dir/legacy-ca-policy-ready.log" >&2; exit 1; fi
  sleep 1
done
kubectl --context "$context" --as="$manager_username" patch deployment "$foreground_deployment" \
  -n "$worker_namespace" --type=merge \
  -p "$legacy_ca_patch" >/dev/null
kubectl --context "$context" rollout status deployment/"$foreground_deployment" \
  --namespace "$worker_namespace" --timeout=90s >/dev/null
kubectl --context "$context" patch "$legacy_ca_policy" --type=json \
  -p "$(jq -c '[{op:"replace",path:"/spec/validations/1",value:.spec.validations[1]}]' "$scratch_dir/ca-policy-before.json")" >/dev/null
for attempt in $(seq 1 30); do
  if ! kubectl --context "$context" --as="$manager_username" patch deployment "$foreground_deployment" \
    -n "$worker_namespace" --type=merge --dry-run=server -p "$legacy_ca_patch" \
    >"$scratch_dir/current-ca-policy-ready.log" 2>&1 && \
    grep -Fq 'device TLS CA projection requires a validated public ConfigMap snapshot' "$scratch_dir/current-ca-policy-ready.log"; then break; fi
  if [ "$attempt" = 30 ]; then cat "$scratch_dir/current-ca-policy-ready.log" >&2; exit 1; fi
  sleep 1
done
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
echo 'native foreground deletion of legacy direct-CA Deployment, ReplicaSet and Pod passed under current policy'
