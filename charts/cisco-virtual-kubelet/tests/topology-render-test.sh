#!/usr/bin/env bash

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$chart_dir/../.." && pwd)"
scratch_dir="$(mktemp -d)"
trap 'rm -rf -- "$scratch_dir"' EXIT

default_render="$scratch_dir/default.yaml"
managed_render="$scratch_dir/managed.yaml"
managed_upgrade_render="$scratch_dir/managed-upgrade.yaml"
strict_render_bundle="$scratch_dir/managed-and-examples.yaml"
error_output="$scratch_dir/error.txt"

has_named_binding() {
  local manifest="$1"
  local expected_name="$2"
  awk -v expected="$expected_name" '
    /^---$/ { kind = ""; metadata = 0; next }
    /^kind: / { kind = $2; next }
    /^metadata:$/ { metadata = 1; next }
    metadata && /^  name: / {
      if ((kind == "ClusterRoleBinding" || kind == "RoleBinding") &&
          $2 == expected) found = 1
      metadata = 0
    }
    END { exit(found ? 0 : 1) }
  ' "$manifest"
}

helm lint "$chart_dir" --kube-version 1.28.0 >/dev/null
helm lint "$chart_dir" --kube-version 1.35.0 \
  --set topology.enabled=true --set controller.leaderElect=true \
  --set rbac.profile=strict >/dev/null
helm lint "$chart_dir" --kube-version 1.35.0 \
  --values "$repo_root/examples/topology/managed-topology-values.yaml" >/dev/null

# Keep the checked-in operator example executable as the API and chart evolve.
helm template cvk-example "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --values "$repo_root/examples/topology/managed-topology-values.yaml" >/dev/null

# Disabled-by-default compatibility: old supported render targets retain the
# legacy chart and receive no managed flags, authority objects, or admission.
helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.28.0 >"$default_render"
if grep -Eq -- '--enable-managed-topology|kind: ValidatingAdmissionPolicy|name: cisco-virtual-kubelet-managed-worker|name: cvk-cisco-virtual-kubelet-topology-(policy|ledger)|name: cvk-cisco-virtual-kubelet-managed-topology-manager' "$default_render"; then
  echo "managed topology resources leaked into the default render" >&2
  exit 1
fi
grep -Fq -- '- --topology-policy-namespace=cisco-vk-system' "$default_render"
grep -Fq -- '- --topology-policy-name=cvk-cisco-virtual-kubelet-topology-policy' "$default_render"
grep -Fq 'resources: ["nodes"]' "$default_render"
grep -Fq 'verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]' "$default_render"
has_named_binding "$default_render" cisco-virtual-kubelet
has_named_binding "$default_render" cisco-virtual-kubelet-device
default_controller_role="$scratch_dir/default-controller-role.yaml"
sed -n '/name: cisco-virtual-kubelet-controller/,/^---$/p' \
  "$default_render" >"$default_controller_role"
if grep -Fq '  - replicasets' "$default_controller_role"; then
  echo "topology-disabled base manager retained ReplicaSet retirement authority" >&2
  exit 1
fi

helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict >"$managed_render"

helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set gnoi.enableSoftwareUpgrade=true >"$managed_upgrade_render"

# The Go contract reader uses a duplicate-key-aware YAML decoder. Include the
# user-facing Kubernetes examples in the same strict pass; non-policy objects
# are intentionally ignored after syntax validation.
{
  cat "$managed_render"
  for example in \
    "$repo_root/examples/topology/devices-and-workload.yaml" \
    "$repo_root/examples/topology/iosxe-software-rollout.yaml" \
    "$repo_root/examples/topology/experimental-native-tas-v1.37.yaml"; do
    printf '%s\n' '---'
    cat "$example"
  done
} >"$strict_render_bundle"

# The manager hashes the complete policy Specs, not a handful of CEL
# substrings. Keep the compiled startup contract and this exact release render
# synchronized whenever admission changes.
(
  cd "$repo_root"
  CVK_ADMISSION_MANIFEST="$strict_render_bundle" \
    GOCACHE="${GOCACHE:-/tmp/cvk-topology-gocache}" \
    go test ./cmd/cisco-vk \
      -run '^TestRenderedManagedAdmissionContract$' -count=1
)

grep -Fq -- '- --enable-managed-topology' "$managed_render"
grep -Fq -- '- --leader-elect' "$managed_render"
grep -Fq -- '- --topology-policy-namespace=cisco-vk-system' "$managed_render"
grep -Fq -- '- --topology-policy-name=cvk-cisco-virtual-kubelet-topology-policy' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-topology-policy' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-topology-ledger' "$managed_render"
grep -Fq 'topology.cisco.vk/admission-policy-prefix: "cvk-cisco-virtual-kubelet"' "$managed_render"
test "$(grep -c '^    topology.cisco.vk/admission-contract-version: "v1"$' "$managed_render")" -eq 17
test "$(grep -c '^    helm.sh/resource-policy: keep$' "$managed_render")" -eq 21
grep -Fq '"globalMaxConcurrentTransfers":1' "$managed_render"
grep -Fq '"domainMaxConcurrentTransfers":{"topology.kubernetes.io/region":1}' "$managed_render"
grep -Fq 'ledger.json: ""' "$managed_render"
if grep -Eq '^[[:space:]]+topology\.cisco\.vk/ledger-uid:' "$managed_render"; then
  echo "render contains a fake ledger UID annotation" >&2
  exit 1
fi

test "$(grep -c '^kind: ValidatingAdmissionPolicy$' "$managed_render")" -eq 8
test "$(grep -c '^kind: ValidatingAdmissionPolicyBinding$' "$managed_render")" -eq 8

policy_section_count() {
  local manifest="$1"
  local section="$2"
  awk -v header="  ${section}:" '
    $0 == header { active = 1; next }
    active && /^  [[:alnum:]][[:alnum:]]*:/ { active = 0 }
    active && /^    - / { count++ }
    END { print count + 0 }
  ' "$manifest"
}

assert_policy_shape() {
  local suffix="$1"
  local expected_match_conditions="$2"
  local expected_variables="$3"
  local expected_validations="$4"
  local manifest="$scratch_dir/policy-${suffix}.yaml"

  sed -n "/name: cvk-cisco-virtual-kubelet-${suffix}/,/^---$/p" \
    "$managed_render" >"$manifest"
  test "$(policy_section_count "$manifest" matchConditions)" \
    -eq "$expected_match_conditions"
  test "$(policy_section_count "$manifest" variables)" \
    -eq "$expected_variables"
  test "$(policy_section_count "$manifest" validations)" \
    -eq "$expected_validations"
}

# Keep Helm's exact CEL contract shape synchronized with the manager startup
# preflight. Any new, removed, or reordered trust-boundary expression requires
# an explicit contract-version decision in both places.
assert_policy_shape managed-node 1 4 3
assert_policy_shape managed-pod-status 1 2 3
assert_policy_shape managed-device 0 5 11
assert_policy_shape managed-rollout 0 1 6
assert_policy_shape managed-upgrade-leaf 1 3 7
assert_policy_shape managed-maintenance-lease 1 10 8
assert_policy_shape topology-policy 1 2 3
assert_policy_shape topology-ledger 1 2 3

grep -Fq 'name: cvk-cisco-virtual-kubelet-managed-maintenance-lease' "$managed_render"
grep -Fq 'validationActions: [Deny]' "$managed_render"
grep -Fq "request.userInfo.username == \"system:serviceaccount:cisco-vk-system:cisco-virtual-kubelet-controller\"" "$managed_render"
grep -Fq "variables.managerCreate || variables.managerAdopt ||" "$managed_render"
grep -Fq "only the manager may create, safely adopt, or delete a managed Lease" "$managed_render"
grep -Fq "!has(oldObject.spec.leaseDurationSeconds)" "$managed_render"
grep -Fq "object.spec.leaseDurationSeconds <= 697200" "$managed_render"
grep -Fq "object.spec.leaseTransitions == variables.oldTransitions + 1" "$managed_render"
grep -Fq "object.spec.renewTime >= oldObject.spec.renewTime" "$managed_render"
grep -Fq "k != 'virtual-kubelet.io/last-applied-node-status'" "$managed_render"
grep -Fq "k != 'virtual-kubelet.io/last-applied-object-meta'" "$managed_render"
grep -Fq "k != 'topology.cisco.vk/worker-observed-revision'" "$managed_render"
grep -Fq "object.metadata.annotations['topology.cisco.vk/worker-observed-revision'].matches(" "$managed_render"
grep -Fq "f != 'ops.cisco.vk/iosxesoftwareupgrade-cleanup'" "$managed_render"
grep -Fq 'has(object.metadata.finalizers) ?' "$managed_render"
grep -Fq "c.stage == 'Staging'" "$managed_render"
grep -Fq "c.stage == 'RollbackActivation'" "$managed_render"
grep -Fq "c.reservationID == object.status.managerAdmission.reservationID" "$managed_render"
grep -Fq "c.policyEpoch == object.status.managerAdmission.policyEpoch" "$managed_render"
grep -Fq "object.status.workerControl.observedPolicyEpoch == object.status.managerAdmission.policyEpoch" "$managed_render"
grep -Fq "object.status.workerControl.observedWorkerConfigRevision.matches(" "$managed_render"
grep -Fq "has(object.status.managerAdmission.topologyLockID)" "$managed_render"
grep -Fq "object.status.managerAdmission.topologyLockID.matches('^[a-f0-9]{32}\$')" "$managed_render"
grep -Fq "c.controlRevision >= object.status.managerAdmission.controlRevision" "$managed_render"
grep -Fq "c.controlRevision <= object.status.managerControl.revision" "$managed_render"
grep -Fq "variables.oldClaims.all(oc" "$managed_render"
grep -Fq "object.status.managerAdmission.state == 'Granted'" "$managed_render"
grep -Fq "c.controlRevision == object.status.managerControl.revision" "$managed_render"
grep -Fq "object.spec.holderIdentity == 'software-upgrade/'" "$managed_render"
grep -Fq "l.metadata.labels['cisco.vk/family'] == 'device-disruptive-mutation'" "$managed_render"
grep -Fq "'topology.cisco.vk/retain-lease'" "$managed_render"
grep -Fq "'^cvk-' + l.metadata.labels['cisco.vk/device'] + '-device-disruptive-mutation-[0-9a-f]{8}\$'" "$managed_render"
grep -Fq "l.metadata.labels['cisco.vk/device'].matches('^device-[0-9a-f]{16}\$')" "$managed_render"
grep -Fq "variables.holderChanged" "$managed_render"
grep -Fq "object.spec.holderIdentity == object.metadata.annotations['topology.cisco.vk/node-name']" "$managed_render"
grep -Fq "object.spec == oldObject.spec" "$managed_render"
grep -Fq "object.spec.approval.planHash == oldObject.status.frozenPlan.hash" "$managed_render"
grep -Fq "object.spec.control.revision == 0" "$managed_render"
grep -Fq "check('approve').allowed()" "$managed_render"
grep -Fq "check('control').allowed()" "$managed_render"
grep -Fq "check('topology').allowed()" "$managed_render"
grep -Fq "changing the projection labels, taints, region, or zone of an established managed device" "$managed_render"
grep -Fq "CiscoDevice topology/risk labels and adoption/reclassification/handoff annotations are frozen by an active topology lock" "$managed_render"
grep -Fq "legacy handoff requests must bind the current managed Node UID" "$managed_render"
grep -Fq "an accepted legacy handoff request is immutable until the handoff is Complete" "$managed_render"
grep -Fq "the UID-bound isolated legacy worker marker is manager-created and immutable" "$managed_render"
grep -Fq "manager-owned CiscoDevice identity, topology, health, worker revision, handoff, lock, and maintenance status cannot be forged" "$managed_render"
grep -Fq "object.status.workerRevision == oldObject.status.workerRevision" "$managed_render"
grep -Fq "variables.managerLegacyHandoff" "$managed_render"
grep -Fq "topology.cisco.vk/legacy-handoff" "$managed_render"
grep -Fq "topology.cisco.vk/projected-keys" "$managed_render"
grep -Fq "topology.cisco.vk/managed-taints" "$managed_render"
grep -Fq "CiscoDevice spec is immutable while a topology lock is active or releasing" "$managed_render"
grep -Fq "a selected managed CiscoDevice requires spec.maxPods from 1 through 110" "$managed_render"
grep -Fq "object.spec.maxPods <= 110" "$managed_render"
grep -Fq "CiscoDevice deletion requires no topology lock, no in-progress legacy handoff, and no unsettled maintenance session" "$managed_render"
grep -Fq "object.spec == oldObject.spec" "$managed_render"
grep -Fq "oldObject.status.legacyHandoff.phase == 'Complete'" "$managed_render"
grep -Fq "oldObject.status.maintenanceSession.phase == 'Settled'" "$managed_render"
grep -Fq "CiscoDevice finalizers and owner references are manager-owned after managed Node identity or legacy handoff state is established" "$managed_render"
grep -Fq "all CiscoDevice status is manager-owned once managed Node identity or legacy handoff state exists" "$managed_render"
grep -Fq "the generated worker identity must encode the Pod's exact bound virtual Node" "$managed_render"
grep -Fq ':cisco-vk-legacy-[a-z0-9]([-a-z0-9.]{0,61}[a-z0-9])?-[a-f0-9]{8}$' "$managed_render"
grep -Fq "check('manage-ledger').allowed()" "$managed_render"

node_policy="$scratch_dir/managed-node-policy.yaml"
node_match="$scratch_dir/managed-node-match.txt"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-node/,/^---$/p' "$managed_render" >"$node_policy"
sed -n '/^  matchConditions:/,/^  variables:/p' "$node_policy" >"$node_match"
grep -Fq ':cisco-vk-managed-[a-z0-9]([-a-z0-9.]{0,61}[a-z0-9])?-[a-f0-9]{8}$' "$node_match"
grep -Fq ':cisco-vk-legacy-[a-z0-9]([-a-z0-9.]{0,61}[a-z0-9])?-[a-f0-9]{8}$' "$node_match"
grep -Fq "request.operation in ['CREATE', 'UPDATE']" "$node_match"
grep -Fq 'cisco-virtual-kubelet-controller' "$node_match"

leaf_policy="$scratch_dir/managed-leaf-policy.yaml"
leaf_match="$scratch_dir/managed-leaf-match.txt"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-upgrade-leaf/,/^---$/p' "$managed_render" >"$leaf_policy"
sed -n '/^  matchConditions:/,/^  variables:/p' "$leaf_policy" >"$leaf_match"
grep -Fq ':cisco-vk-managed-[a-z0-9]([-a-z0-9.]{0,61}[a-z0-9])?-[a-f0-9]{8}$' "$leaf_match"
grep -Fq 'cisco-virtual-kubelet-controller' "$leaf_match"
grep -Fq "object.metadata.annotations['topology.cisco.vk/managed'] == 'true'" "$leaf_policy"

managed_role="$scratch_dir/managed-role.yaml"
sed -n '/name: cisco-virtual-kubelet-managed-worker/,/^---$/p' "$managed_render" >"$managed_role"
grep -Fq 'resources: ["nodes"]' "$managed_role"
grep -Fq 'verbs: ["get"]' "$managed_role"
grep -Fq 'resources: ["nodes/status"]' "$managed_role"
grep -Fq 'verbs: ["get", "update", "patch"]' "$managed_role"
grep -Fq 'resources: ["pods"]' "$managed_role"
grep -Fq 'resources: ["pods/status"]' "$managed_role"
grep -A1 -F 'resources: ["pods"]' "$managed_role" | \
  grep -Fq 'verbs: ["get", "list", "watch"]'
grep -A1 -F 'resources: ["pods/status"]' "$managed_role" | \
  grep -Fq 'verbs: ["get", "update", "patch"]'
if grep -Eq 'resources: \["pods/(log|exec)"\]' "$managed_role"; then
  echo "managed worker retained pod log/exec permissions" >&2
  exit 1
fi
if grep -A1 -F 'resources: ["pods"]' "$managed_role" | grep -Eq 'create|update|patch|delete'; then
  echo "managed worker retained Pod main-resource mutation" >&2
  exit 1
fi
grep -Fq 'resources: ["secrets"]' "$managed_role"
grep -Fq 'resources: ["services"]' "$managed_role"
grep -Fq 'resources: ["events"]' "$managed_role"
grep -Fq 'resources: ["leases"]' "$managed_role"
grep -A1 -F 'resources: ["leases"]' "$managed_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
if grep -A1 -F 'resources: ["leases"]' "$managed_role" | grep -Eq 'create|delete'; then
  echo "managed worker retained Lease create/delete authority" >&2
  exit 1
fi
grep -Fq 'resources: ["ciscodevices"]' "$managed_role"
grep -Fq 'resources: ["iosxeconfigdefaults"]' "$managed_role"
if grep -Fq 'resources: ["iosxesoftwareupgrades"]' "$managed_role"; then
  echo "managed worker retained cluster-wide upgrade-leaf authority" >&2
  exit 1
fi
grep -Fq 'resources: ["configmaps"]' "$managed_role"
grep -A1 -F 'resources: ["configmaps"]' "$managed_role" | \
  grep -Fq 'verbs: ["get", "list", "watch"]'
if grep -A1 -F 'resources: ["configmaps"]' "$managed_role" | grep -Eq 'create|update|patch|delete'; then
  echo "managed worker retained cluster-wide ConfigMap mutation" >&2
  exit 1
fi
device_role="$scratch_dir/device-role.yaml"
sed -n '/name: cisco-virtual-kubelet-device/,/^---$/p' "$managed_render" >"$device_role"
grep -Fq 'resources: ["configmaps"]' "$device_role"
grep -Fq 'verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]' "$device_role"
grep -A1 -F 'resources: ["iosxesoftwareupgrades", "iosxeoperationalactions"]' "$device_role" | \
  grep -Fq 'verbs: ["list"]'
if grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$device_role" | \
   grep -Eq 'update|patch'; then
  echo "disabled software-upgrade controller retained leaf write authority" >&2
  exit 1
fi

upgrade_device_role="$scratch_dir/upgrade-device-role.yaml"
sed -n '/name: cisco-virtual-kubelet-device/,/^---$/p' \
  "$managed_upgrade_render" >"$upgrade_device_role"
grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$upgrade_device_role" | \
  grep -Fq 'verbs: ["get", "watch", "update", "patch"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades/status"]' "$upgrade_device_role" | \
  grep -Fq 'verbs: ["get", "update", "patch"]'
if grep -Fq 'resources: ["iosxesoftwareupgrades"]' "$managed_role"; then
  echo "software-upgrade enablement restored cross-namespace leaf authority" >&2
  exit 1
fi
if grep -Fq 'resources: ["nodes"]' "$managed_role" && \
   grep -A1 -F 'resources: ["nodes"]' "$managed_role" | grep -Eq 'create|update|patch|delete|list|watch'; then
  echo "managed worker regained Node metadata authority" >&2
  exit 1
fi

manager_role="$scratch_dir/manager-role.yaml"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-topology-manager/,/^---$/p' "$managed_render" >"$manager_role"
grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch"]'
if grep -Fq 'resources: ["iosxesoftwarerollouts"]' "$manager_role" || \
   grep -Fq 'resources: ["iosxesoftwareupgrades/status"]' "$manager_role" || \
   grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$manager_role" | \
     grep -Fq '"create"'; then
  echo "disabled software-upgrade controller retained manager mutation authority" >&2
  exit 1
fi
grep -A3 -F 'validatingadmissionpolicies' "$manager_role" | \
  grep -Fq 'verbs: ["get"]'
grep -A1 -F 'resources: ["leases"]' "$manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]'
grep -A1 -F 'resources: ["replicasets"]' "$manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "delete"]'
test "$(grep -c '^    helm.sh/resource-policy: keep$' "$manager_role")" -eq 2

# Safe retirement of the pre-topology shared identity is a manager operation.
# ReplicaSet inventory/deletion belongs only to the supplemental topology role;
# the base controller role retains the narrower topology-disabled contract.
controller_role="$scratch_dir/controller-role.yaml"
sed -n '/name: cisco-virtual-kubelet-controller/,/^---$/p' \
  "$managed_render" >"$controller_role"
grep -A12 -F '  - serviceaccounts' "$controller_role" | grep -Fq '  - delete'
if grep -Fq '  - replicasets' "$controller_role"; then
  echo "generated base manager role retained ReplicaSet retirement authority" >&2
  exit 1
fi
grep -Fq '  - rolebindings' "$controller_role"
grep -Fq '  - clusterrolebindings' "$controller_role"

upgrade_manager_role="$scratch_dir/upgrade-manager-role.yaml"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-topology-manager/,/^---$/p' \
  "$managed_upgrade_render" >"$upgrade_manager_role"
grep -A1 -F 'resources: ["iosxesoftwarerollouts"]' "$upgrade_manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
grep -A1 -F 'resources: ["iosxesoftwarerollouts/status"]' "$upgrade_manager_role" | \
  grep -Fq 'verbs: ["get", "update", "patch"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$upgrade_manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$upgrade_manager_role" | \
  grep -Fq 'verbs: ["create"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades/status"]' "$upgrade_manager_role" | \
  grep -Fq 'verbs: ["get", "update", "patch"]'

for annotation in \
  managed device-namespace device-name device-uid device-generation node-name node-uid \
  worker-username worker-protocol campaign-namespace campaign-name \
  campaign-uid plan-hash ledger-uid reservation-id source-secret-uid; do
  grep -Fq "topology.cisco.vk/$annotation" "$managed_render"
done
if grep -Fq 'topology.cisco.vk/source-secret-resource-version' "$managed_render"; then
  echo "managed leaf pinned a Secret resourceVersion instead of permitting same-UID rotation" >&2
  exit 1
fi
grep -Fq "k.startsWith('distribution.cisco.vk/')" "$managed_render"

# Managed topology must never leave the old shared worker identity broadly
# bound. Selected and unselected devices receive separate controller-owned,
# UID-derived bindings; the fixed ClusterRoles remain reusable roleRefs.
if has_named_binding "$managed_render" cisco-virtual-kubelet ||
   has_named_binding "$managed_render" cisco-virtual-kubelet-device; then
  echo "managed topology retained a shared VK RoleBinding" >&2
  exit 1
fi
grep -Fq 'kind: ClusterRole' "$managed_render"
grep -Fq '  name: cisco-virtual-kubelet' "$managed_render"
grep -Fq 'topology.cisco.vk/retire-shared-worker-access: rollout-v1' \
  "$chart_dir/templates/vk-rbac.yaml"
grep -Fq 'lookup "rbac.authorization.k8s.io/v1" "ClusterRoleBinding"' \
  "$chart_dir/templates/vk-rbac.yaml"
grep -Fq 'lookup "rbac.authorization.k8s.io/v1" "RoleBinding"' \
  "$chart_dir/templates/vk-rbac.yaml"
grep -Fq 'validateTopologyRetirement' "$chart_dir/templates/deployment.yaml"
grep -Fq 'lookup "cisco.vk/v1alpha1" "CiscoDevice" "" ""' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'status.nodeIdentity; request and complete its UID-bound legacy handoff first' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'requires controller.leaderElect=true until every isolated legacy identity' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'Offline `helm template`' "$repo_root/docs/topology-awareness.md"
grep -Fq 'GitOps pruning cannot discover' "$repo_root/docs/topology-awareness.md"

# Cross-field and qualification gates must fail before installation.
if helm template cvk "$chart_dir" --kube-version 1.34.9 \
    --set topology.enabled=true --set controller.leaderElect=true \
    >"$error_output" 2>&1; then
  echo "managed topology rendered below Kubernetes 1.35" >&2
  exit 1
fi
grep -Fq 'requires Kubernetes >=1.35.0' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set aggregator.enabled=true >"$error_output" 2>&1; then
  echo "managed topology rendered with aggregator mode" >&2
  exit 1
fi
grep -Fq 'requires aggregator.enabled=false' "$error_output"

# Schema validation and the template helper both reject overlapping managers.
if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set rbac.profile=strict \
    >"$error_output" 2>&1; then
  echo "managed topology rendered without leader election" >&2
  exit 1
fi
grep -Fq '/controller/leaderElect' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --skip-schema-validation \
    --set topology.enabled=true --set rbac.profile=strict \
    >"$error_output" 2>&1; then
  echo "managed topology bypassed the render-time leader-election guard" >&2
  exit 1
fi
grep -Fq 'requires controller.leaderElect=true' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set serviceAccount.vkName=custom-worker >"$error_output" 2>&1; then
  echo "managed topology rendered with an unaudited VK role name" >&2
  exit 1
fi
grep -Fq 'requires serviceAccount.vkName=cisco-virtual-kubelet' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    >"$error_output" 2>&1; then
  echo "managed topology rendered without strict RBAC" >&2
  exit 1
fi
grep -Fq 'requires rbac.profile=strict' "$error_output"

# Both values-schema validation and the render-time defense-in-depth check must
# reject a reconciler that does not implement the managed Phase 2 protocol.
if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set gnoi.enableWriteClass=true >"$error_output" 2>&1; then
  echo "managed topology rendered with generic write-class gNOI enabled" >&2
  exit 1
fi
grep -Fq '/gnoi/enableWriteClass' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --skip-schema-validation \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set gnoi.enableWriteClass=true >"$error_output" 2>&1; then
  echo "managed topology bypassed the render-time write-class guard" >&2
  exit 1
fi
grep -Fq 'managed topology Phase 2 supports campaign-owned IOSXESoftwareUpgrade only' \
  "$error_output"
grep -Fq 'gnoi.enableWriteClass=false' "$repo_root/docs/topology-awareness.md"
grep -Fq 'IOSXEOperationalAction' "$repo_root/charts/cisco-virtual-kubelet/README.md"
grep -Fq 'operations.cisco.vk/qualification-cohort: c9300' \
  "$repo_root/examples/topology/devices-and-workload.yaml"
grep -Fq 'distribution.cisco.vk/cache-domain: berlin' \
  "$repo_root/examples/topology/devices-and-workload.yaml"
grep -Fq 'kind: PodDisruptionBudget' \
  "$repo_root/examples/topology/devices-and-workload.yaml"
grep -Fq 'Phase 2 uses' \
  "$repo_root/examples/topology/devices-and-workload.yaml"
grep -Fq 'preferredDuringSchedulingIgnoredDuringExecution' \
  "$repo_root/docs/topology-awareness.md"
grep -Fq 'cisco.vk/device-maintenance=gnoi:NoSchedule' \
  "$repo_root/docs/topology-awareness.md"
grep -Fq 'do not yet prove production packet' \
  "$repo_root/docs/topology-awareness.md"
grep -Fq 'name: c9300' "$repo_root/examples/topology/iosxe-software-rollout.yaml"
grep -Fq 'name: berlin-mirror' "$repo_root/examples/topology/iosxe-software-rollout.yaml"
grep -Fq 'name: global' "$repo_root/examples/topology/iosxe-software-rollout.yaml"
grep -Fq 'Exactly one unscoped catch-all' "$repo_root/docs/topology-awareness.md"
grep -Fq 'every qualification cohort' "$repo_root/docs/topology-awareness.md"

# Generated schemas are part of the rollout safety contract and Helm never
# upgrades them implicitly. Keep the install copies byte-identical and assert
# the newest identity/revision transitions survived generation.
for crd_name in \
  cisco.vk_ciscodevices.yaml \
  ops.cisco.vk_iosxesoftwareupgrades.yaml \
  ops.cisco.vk_iosxesoftwarerollouts.yaml; do
  cmp "$repo_root/config/crd/$crd_name" "$chart_dir/crds/$crd_name"
done
grep -Fq 'message: physicalIdentity is write-once' \
  "$repo_root/config/crd/cisco.vk_ciscodevices.yaml"
grep -Fq 'status.nodeIdentity.physicalIdentity must equal the canonical declared' \
  "$repo_root/config/crd/cisco.vk_ciscodevices.yaml"
grep -Fq 'message: deployment UID and generation must be present together' \
  "$repo_root/config/crd/cisco.vk_ciscodevices.yaml"
grep -Fq 'observedWorkerConfigRevision:' \
  "$repo_root/config/crd/ops.cisco.vk_iosxesoftwareupgrades.yaml"
grep -Fq 'qualificationCohort:' \
  "$repo_root/config/crd/ops.cisco.vk_iosxesoftwarerollouts.yaml"
if grep -Eq 'requiredDeviceConditions|secretResourceVersion|source-secret-resource-version' \
    "$repo_root/config/crd/ops.cisco.vk_iosxesoftwareupgrades.yaml" \
    "$repo_root/config/crd/ops.cisco.vk_iosxesoftwarerollouts.yaml"; then
  echo "removed rollout source/health schema survived generation" >&2
  exit 1
fi

# Helm installs but never upgrades chart CRDs. An existing Helm-owned schema
# can reject plain server-side apply with a managedFields conflict, so every
# current upgrade guide must preserve, diff, and explicitly hand off only the
# reviewed CVK CRD objects.
for upgrade_doc in \
  "$repo_root/README.md" \
  "$repo_root/docs/getting-started.md" \
  "$repo_root/docs/operations.md" \
  "$repo_root/docs/topology-awareness.md" \
  "$repo_root/charts/cisco-virtual-kubelet/README.md" \
  "$repo_root/charts/cisco-virtual-kubelet/templates/NOTES.txt"; do
  grep -Fq 'cvk-crds-before-upgrade.yaml' "$upgrade_doc"
  grep -Fq 'kubectl diff --server-side --force-conflicts' "$upgrade_doc"
  grep -Fq 'kubectl apply --server-side --force-conflicts' "$upgrade_doc"
  grep -Fq -- '--field-manager=cvk-crd-upgrade' "$upgrade_doc"
done
if grep -E 'kubectl apply --server-side -f (cisco-virtual-kubelet|charts/cisco-virtual-kubelet)/crds/' \
    "$repo_root/README.md" \
    "$repo_root/docs/getting-started.md" \
    "$repo_root/docs/operations.md" \
    "$repo_root/docs/topology-awareness.md" \
    "$repo_root/charts/cisco-virtual-kubelet/README.md" \
    "$repo_root/charts/cisco-virtual-kubelet/templates/NOTES.txt"; then
  echo "a current upgrade guide still claims plain server-side CRD apply is reliable" >&2
  exit 1
fi

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set topology.policy.globalMaxConcurrentTransfers=0 >"$error_output" 2>&1; then
  echo "invalid transfer ceiling passed values schema" >&2
  exit 1
fi
grep -Fq 'globalMaxConcurrentTransfers' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set topology.policy.globalMaxUnavailable=101 >"$error_output" 2>&1; then
  echo "administrator unavailable ceiling above the controller bound passed values schema" >&2
  exit 1
fi
grep -Fq 'globalMaxUnavailable' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set-json 'topology.policy.projectedTopologyKeys=["topology.cisco.vk/rack"]' >"$error_output" 2>&1; then
  echo "projected topology outside required keys rendered" >&2
  exit 1
fi
grep -Fq 'is not in requiredTopologyKeys' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set topology.policy.namespace=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$error_output" 2>&1; then
  echo "invalid topology policy namespace passed values schema" >&2
  exit 1
fi
grep -Fq '/topology/policy/namespace' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set-json 'topology.policy.fleetSelector={"matchLabels":{},"matchExpressions":[{"key":"topology.cisco.vk/managed","operator":"Exists","values":["true"]}]}' >"$error_output" 2>&1; then
  echo "invalid label-selector operator/value combination passed schema" >&2
  exit 1
fi
grep -Fq "maxItems: got 1, want 0" "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set-json 'topology.policy.fleetSelector={"matchLabels":{"bad key":"true"},"matchExpressions":[]}' >"$error_output" 2>&1; then
  echo "invalid label-selector key passed schema" >&2
  exit 1
fi
grep -Fq "invalid propertyName 'bad key'" "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set-json 'topology.policy.fleetSelector={"matchLabels":{"app":"router"},"matchExpressions":[]}' >"$error_output" 2>&1; then
  echo "unprotected fleet enrollment selector rendered" >&2
  exit 1
fi
grep -Fq "invalid propertyName 'app'" "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set-json 'topology.policy.requiredTopologyKeys=["operations.cisco.vk/upgrade-ring"]' \
    --set-json 'topology.policy.projectedTopologyKeys=["operations.cisco.vk/upgrade-ring"]' >"$error_output" 2>&1; then
  echo "operational risk key rendered as a scheduler-visible Node projection" >&2
  exit 1
fi
grep -Fq '/topology/policy/projectedTopologyKeys/0' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set-json 'topology.policy.requiredTopologyKeys=["distribution.cisco.vk/cache-domain"]' \
    --set-json 'topology.policy.projectedTopologyKeys=["distribution.cisco.vk/cache-domain"]' >"$error_output" 2>&1; then
  echo "artifact distribution key rendered as a scheduler-visible Node projection" >&2
  exit 1
fi
grep -Fq '/topology/policy/projectedTopologyKeys/0' "$error_output"

echo "managed topology Helm render contract passed"
