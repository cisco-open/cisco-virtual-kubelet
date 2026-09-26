#!/usr/bin/env bash

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$chart_dir/../.." && pwd)"
scratch_dir="$(mktemp -d)"
trap 'rm -rf -- "$scratch_dir"' EXIT

default_render="$scratch_dir/default.yaml"
vk_pull_policy_render="$scratch_dir/vk-pull-policy.yaml"
managed_render="$scratch_dir/managed.yaml"
managed_short_account_render="$scratch_dir/managed-short-accounts.yaml"
managed_upgrade_render="$scratch_dir/managed-upgrade.yaml"
managed_drain_render="$scratch_dir/managed-drain.yaml"
managed_lease_namespace_render="$scratch_dir/managed-lease-namespace.yaml"
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

has_named_service_account() {
  local manifest="$1"
  local expected_name="$2"
  awk -v expected="$expected_name" '
    /^---$/ { kind = ""; metadata = 0; next }
    /^kind: / { kind = $2; next }
    /^metadata:$/ { metadata = 1; next }
    metadata && /^  name: / {
      if (kind == "ServiceAccount" && $2 == expected) found = 1
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
# legacy chart and receive no managed enablement, authority objects, or
# admission. The inert split-account coordinates remain available so a live
# reverse handoff can identify the exact former accounts.
helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.28.0 >"$default_render"
if grep -Eq -- '--enable-managed-topology|name: cisco-virtual-kubelet-(app-hosting|network-management)-|kind: ValidatingAdmissionPolicy|name: cvk-cisco-virtual-kubelet-topology-(policy|ledger)|name: cvk-cisco-virtual-kubelet-managed-topology-manager' "$default_render"; then
  echo "managed topology resources leaked into the default render" >&2
  exit 1
fi
grep -Fq -- '- --topology-policy-namespace=cisco-vk-system' "$default_render"
grep -Fq -- '- --topology-policy-name=cvk-cisco-virtual-kubelet-topology-policy' "$default_render"
grep -Fq -- '- --vk-image-pull-policy=IfNotPresent' "$default_render"
grep -Fq -- '- --app-hosting-service-account=cvk-cisco-virtual-kubelet-app-hosting' "$default_render"
grep -Fq -- '- --app-hosting-access-mode=readWrite' "$default_render"
grep -Fq -- '- --network-management-service-account=cvk-cisco-virtual-kubelet-network-management' "$default_render"
grep -Fq -- '- --network-management-access-mode=readOnly' "$default_render"
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
  --set image.pullPolicy=Always \
  --set vkImage.pullPolicy=Never >"$vk_pull_policy_render"
grep -Fq -- '- --vk-image-pull-policy=Never' "$vk_pull_policy_render"
grep -Fq 'imagePullPolicy: Always' "$vk_pull_policy_render"

helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict >"$managed_render"

# Valid short account names can overlap fixed CEL vocabulary. The manager must
# canonicalize only the exact chart-bound literals, not matching substrings in
# annotation keys or other compiled policy text.
helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.appHosting.serviceAccountName=managed \
  --set topology.workerAccounts.networkManagement.serviceAccountName=network \
  >"$managed_short_account_render"

helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.networkManagement.accessMode=readWrite \
  --set gnoi.enableSoftwareUpgrade=true >"$managed_upgrade_render"

helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.networkManagement.accessMode=readWrite \
  --set gnoi.enableSoftwareUpgrade=true \
  --set topology.policy.workloadDrain.enabled=true \
  --set-json 'topology.policy.workloadDrain.allowedNamespaces=["apps","edge-services"]' \
  --set topology.policy.workloadDrain.maxTimeoutSeconds=900 \
  --set topology.policy.workloadDrain.maxPods=8 \
  --set topology.policy.workloadDrain.maxTerminationGraceSeconds=180 >"$managed_drain_render"
helm template cvk "$chart_dir" --namespace cisco-vk-system --set topology.enabled=true --set controller.leaderElect=true --set rbac.profile=strict \
  --set config.leaseNamespace=cvk-leases >"$managed_lease_namespace_render"

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
      -run '^TestRenderedManaged(AdmissionContract|WorkerClusterRoleContracts)$' -count=1
)

(
  cd "$repo_root"
  CVK_ADMISSION_MANIFEST="$managed_short_account_render" \
    CVK_ADMISSION_APP_SERVICE_ACCOUNT=managed \
    CVK_ADMISSION_NETWORK_SERVICE_ACCOUNT=network \
    GOCACHE="${GOCACHE:-/tmp/cvk-topology-gocache}" \
    go test ./cmd/cisco-vk \
      -run '^TestRenderedManagedAdmissionContract$' -count=1
)

grep -Fq -- '- --enable-managed-topology' "$managed_render"
grep -Fq -- '- --leader-elect' "$managed_render"
grep -Fq -- '- --topology-policy-namespace=cisco-vk-system' "$managed_render"
grep -Fq -- '- --topology-policy-name=cvk-cisco-virtual-kubelet-topology-policy' "$managed_render"
grep -Fq -- '- --app-hosting-service-account=cvk-cisco-virtual-kubelet-app-hosting' "$managed_render"
grep -Fq -- '- --app-hosting-access-mode=readWrite' "$managed_render"
grep -Fq -- '- --network-management-service-account=cvk-cisco-virtual-kubelet-network-management' "$managed_render"
grep -Fq -- '- --network-management-access-mode=readOnly' "$managed_render"

# Derived names reserve suffix space before truncation, so long release names
# cannot collapse the two cluster-reserved identities onto the same DNS label.
long_name_render="$scratch_dir/long-name.yaml"
long_prefix="$(printf 'a%.0s' {1..80})"
helm template cvk "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set fullnameOverride="$long_prefix" \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict >"$long_name_render"
long_app_sa="$(sed -n 's/.*--app-hosting-service-account=//p' "$long_name_render")"
long_network_sa="$(sed -n 's/.*--network-management-service-account=//p' "$long_name_render")"
test "${#long_app_sa}" -le 63
test "${#long_network_sa}" -le 63
test "$long_app_sa" != "$long_network_sa"
[[ "$long_app_sa" == *-app-hosting ]]
[[ "$long_network_sa" == *-network-management ]]

peer_render="$scratch_dir/peer-release.yaml"
helm template cvk-peer "$chart_dir" \
  --namespace cisco-vk-system \
  --kube-version 1.35.0 \
  --set topology.enabled=true \
  --set controller.leaderElect=true \
  --set rbac.profile=strict >"$peer_render"
grep -Fq -- '- --app-hosting-service-account=cvk-peer-cisco-virtual-kubelet-app-hosting' "$peer_render"
grep -Fq -- '- --network-management-service-account=cvk-peer-cisco-virtual-kubelet-network-management' "$peer_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-topology-policy' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-topology-ledger' "$managed_render"
grep -Fq 'topology.cisco.vk/admission-policy-prefix: "cvk-cisco-virtual-kubelet"' "$managed_render"
test "$(grep -c '^    topology.cisco.vk/admission-contract-version: "v2"$' "$managed_render")" -eq 55
test "$(grep -c '^    helm.sh/resource-policy: keep$' "$managed_render")" -eq 66
grep -Fq '"globalMaxConcurrentTransfers":1' "$managed_render"
grep -Fq '"domainMaxConcurrentTransfers":{"topology.kubernetes.io/region":1}' "$managed_render"
grep -Fq '"appHostingServiceAccountName":"cvk-cisco-virtual-kubelet-app-hosting"' "$managed_render"
grep -Fq '"networkManagementServiceAccountName":"cvk-cisco-virtual-kubelet-network-management"' "$managed_render"
grep -Fq '"configLeaseNamespace":""' "$managed_render"
grep -Fq 'topology.cisco.vk/app-hosting-service-account: "cvk-cisco-virtual-kubelet-app-hosting"' "$managed_render"
grep -Fq 'topology.cisco.vk/network-management-service-account: "cvk-cisco-virtual-kubelet-network-management"' "$managed_render"
grep -Fq 'topology.cisco.vk/config-lease-namespace: ""' "$managed_render"
grep -Fq '"configLeaseNamespace":"cvk-leases"' "$managed_lease_namespace_render"
grep -Fq 'topology.cisco.vk/config-lease-namespace: "cvk-leases"' "$managed_lease_namespace_render"
grep -Fq 'name: CONFIG_LEASE_NAMESPACE' "$managed_lease_namespace_render"
grep -Fq 'value: "cvk-leases"' "$managed_lease_namespace_render"
if grep -Fq '"workloadDrain"' "$managed_render"; then
  echo "disabled workload drain changed the v1 administrator policy" >&2
  exit 1
fi
grep -Fq '"workloadDrain":{' "$managed_drain_render"
grep -Fq '"allowedNamespaces":["apps","edge-services"]' "$managed_drain_render"
grep -Fq '"enabled":true' "$managed_drain_render"
grep -Fq '"maxTimeoutSeconds":900' "$managed_drain_render"
grep -Fq '"maxPods":8' "$managed_drain_render"
grep -Fq '"maxTerminationGraceSeconds":180' "$managed_drain_render"
grep -Fq 'ledger.json: ""' "$managed_render"
if grep -Eq '^[[:space:]]+topology\.cisco\.vk/ledger-uid:' "$managed_render"; then
  echo "render contains a fake ledger UID annotation" >&2
  exit 1
fi

test "$(grep -c '^kind: ValidatingAdmissionPolicy$' "$managed_render")" -eq 27
test "$(grep -c '^kind: ValidatingAdmissionPolicyBinding$' "$managed_render")" -eq 27
test "$(grep -c '^    topology.cisco.vk/admission-contract-digest: "sha256:02c0e65602ac0ebcc3d19b15bd7cbcd7c3840c081d72f7541efbafc394f2ee76"$' "$managed_render")" -eq 2
grep -Fq 'upgrade this release once with topology.enabled=true before disabling topology' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'prior topology-enabled manager preflight' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq '(not (empty (get $legacyNodeMarkerMatchResources "excludeResourceRules")))' \
  "$chart_dir/templates/_helpers.tpl"

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
assert_policy_shape managed-node 1 7 5
assert_policy_shape legacy-node-marker 1 1 1
assert_policy_shape managed-pod-status 1 2 3
assert_policy_shape managed-pod-delete 1 2 4
assert_policy_shape managed-drain-pod 1 5 3
assert_policy_shape managed-device 0 7 14
assert_policy_shape managed-rollout 0 1 6
assert_policy_shape managed-upgrade-leaf 1 5 8
assert_policy_shape managed-maintenance-lease 1 11 8
assert_policy_shape topology-policy 1 4 5
assert_policy_shape topology-ledger 1 4 4

grep -Fq 'name: cvk-cisco-virtual-kubelet-managed-maintenance-lease' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-managed-pod-delete' "$managed_render"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-pod-delete/,/^---$/p' \
  "$managed_render" >"$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'operations: ["DELETE"]' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'resources: ["pods"]' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'has(oldObject.metadata.deletionTimestamp)' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'request.options.preconditions.uid' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'request.options.gracePeriodSeconds == 0' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'cisco-vk-managed-' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'cisco-vk-legacy-' "$scratch_dir/managed-pod-delete-policy.yaml"
grep -Fq 'name: cvk-cisco-virtual-kubelet-managed-drain-pod' "$managed_render"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-drain-pod/,/^---$/p' \
  "$managed_render" | grep -Fq 'operations: ["CREATE", "UPDATE", "DELETE"]'
grep -Fq 'validationActions: [Deny]' "$managed_render"
grep -Fq "request.userInfo.username == \"system:serviceaccount:cisco-vk-system:cisco-virtual-kubelet-controller\"" "$managed_render"
grep -Fq "variables.managerCreate || variables.managerAdopt ||" "$managed_render"
grep -Fq "only the manager may create, safely adopt, rebind worker metadata, or delete a managed Lease" "$managed_render"
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
grep -Fq "object.status.workerDrain.observedSessionToken == object.status.managerDrain.sessionToken" "$managed_render"
grep -Fq "object.status.workerDrain.observedControlRevision <= object.status.managerDrain.controlRevision" "$managed_render"
grep -Fq "object.status.managerDrain.pods.exists(p, p.uid == uid)" "$managed_render"
grep -Fq "f == 'ops.cisco.vk/iosxe-rollout-drain'" "$managed_render"
grep -Fq "variables.oldSession == '' || variables.newSession == ''" "$managed_render"
grep -Fq "variables.oldSession == variables.newSession" "$managed_render"
grep -Fq "'^(software-upgrade|software-drain|operational-action)/" "$managed_render"
grep -Fq "maintenance-session-token'].matches(" "$managed_render"
grep -Fq -- "-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-" "$managed_render"

# Drain mutation authority is created only for the administrator allowlist and
# never adds direct Pod delete privileges.
if grep -Fq 'app.kubernetes.io/component: workload-drain' "$managed_render"; then
  echo "disabled workload drain rendered namespace mutation RBAC" >&2
  exit 1
fi
test "$(grep -c 'app.kubernetes.io/component: workload-drain' "$managed_drain_render")" -eq 8
test "$(grep -c 'operations.cisco.vk/drain-authority: cleanup' "$managed_drain_render")" -eq 4
test "$(grep -c 'operations.cisco.vk/drain-authority: eviction' "$managed_drain_render")" -eq 4
test "$(grep -c 'helm.sh/resource-policy: keep' "$managed_drain_render")" -ge 8
grep -Fq 'namespace: "apps"' "$managed_drain_render"
grep -Fq 'namespace: "edge-services"' "$managed_drain_render"
grep -Fq 'resources: ["pods/eviction"]' "$managed_drain_render"
workload_drain_rbac="$scratch_dir/workload-drain-rbac.yaml"
sed -n '/app.kubernetes.io\/component: workload-drain/,/^---$/p' \
  "$managed_drain_render" >"$workload_drain_rbac"
if grep -A1 -F 'resources: ["pods"]' "$workload_drain_rbac" | grep -Eq 'delete'; then
  echo "workload drain RBAC grants direct delete" >&2
  exit 1
fi
grep -A1 -F 'resources: ["pods"]' "$workload_drain_rbac" | \
  grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
grep -A1 -F 'resources: ["pods/eviction"]' "$workload_drain_rbac" | \
  grep -Fq 'verbs: ["create"]'
grep -Fq 'lookup "rbac.authorization.k8s.io/v1" "Role" $namespace $cleanupRoleName' \
  charts/cisco-virtual-kubelet/templates/topology-rbac.yaml
grep -Fq 'lookup "rbac.authorization.k8s.io/v1" "RoleBinding" $namespace $cleanupRoleName' \
  charts/cisco-virtual-kubelet/templates/topology-rbac.yaml
if grep -Fqi 'statefulset' "$workload_drain_rbac"; then
  echo "workload drain RBAC implies unsupported StatefulSet eligibility" >&2
  exit 1
fi
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
grep -Fq "the UID-bound isolated legacy worker marker is manager-created and removable only after an exact worker transition" "$managed_render"
grep -Fq "the isolated legacy worker marker must match the durable handoff phase and CiscoDevice UID" "$managed_render"
grep -Fq "only the manager may remove or change a released Node handoff marker" "$managed_render"
grep -Fq "manager-owned CiscoDevice identity, topology, health, app/network worker revisions, handoff, lock, and maintenance status cannot be forged" "$managed_render"
grep -Fq "object.status.workerRevision == oldObject.status.workerRevision" "$managed_render"
grep -Fq "object.status.networkWorkerRevision == oldObject.status.networkWorkerRevision" "$managed_render"
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
grep -Fq "only the manager may remove the CiscoDevice cleanup finalizer" "$managed_render"
grep -Fq "oldObject.metadata.finalizers.exists(f, f == 'cisco.vk/device-cleanup')" "$managed_render"
grep -Fq "CiscoDevice finalizers and owner references are manager-owned after managed Node identity, legacy handoff, or isolated worker state is established" "$managed_render"
grep -Fq "all CiscoDevice status is manager-owned once managed Node identity or legacy handoff state exists" "$managed_render"
grep -Fq "the generated worker identity must encode the Pod's exact bound virtual Node" "$managed_render"
grep -Fq ':cisco-vk-legacy-[a-z0-9]([-a-z0-9.]{0,61}[a-z0-9])?-[a-f0-9]{8}$' "$managed_render"
grep -Fq "check('manage-ledger').allowed()" "$managed_render"
grep -Fq "!object.data['ledger.json'].matches('^\\\\s*\$')" "$managed_render"
grep -Fq 'an existing topology ledger cannot be emptied, including through break-glass' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-shared-worker-serviceaccount' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-generated-worker-serviceaccount' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-shared-worker-token' "$managed_render"
grep -Fq 'name: cvk-cisco-virtual-kubelet-shared-worker-token-secret' "$managed_render"
grep -Fq 'resources: ["serviceaccounts/token"]' "$managed_render"
grep -Fq "object.type == 'kubernetes.io/service-account-token'" "$managed_render"
grep -Fq 'functional worker ServiceAccount names may be recorded once and are immutable thereafter' "$managed_render"
grep -Fq 'the config Lease namespace may be recorded once and is immutable thereafter' "$managed_render"
grep -Fq 'only the topology manager may create, update, or delete a reserved worker ServiceAccount' "$managed_render"
grep -Fq 'a reserved worker token must be requested by a kubelet and bound to one exact Pod UID' "$managed_render"
grep -Fq 'legacy token Secrets are forbidden for reserved worker ServiceAccounts' "$managed_render"
test "$(grep -Fc 'has(object.spec.template.spec.serviceAccountName)' "$managed_render")" -eq 2
test "$(grep -Fc 'has(oldObject.spec.template.spec.serviceAccountName)' "$managed_render")" -eq 2
test "$(grep -Fc 'has(object.spec.serviceAccountName)' "$managed_render")" -eq 2
test "$(grep -Fc 'has(oldObject.spec.serviceAccountName)' "$managed_render")" -eq 3

worker_deployment_policy="$scratch_dir/shared-worker-deployment.yaml"
sed -n '/name: cvk-cisco-virtual-kubelet-shared-worker-deployment/,/^---$/p' \
  "$managed_render" >"$worker_deployment_policy"
grep -Fq 'resources: ["deployments", "deployments/status"]' \
  "$worker_deployment_policy"
grep -Fq "'system:serviceaccount:kube-system:deployment-controller'" \
  "$worker_deployment_policy"
grep -Fq "request.subResource == 'status'" "$worker_deployment_policy"
grep -Fq 'object.metadata.uid == oldObject.metadata.uid' \
  "$worker_deployment_policy"
grep -Fq "'deployment.kubernetes.io/revision' in object.metadata.annotations" \
  "$worker_deployment_policy"
grep -Fq 'object.spec == oldObject.spec' "$worker_deployment_policy"

worker_replicaset_policy="$scratch_dir/shared-worker-replicaset.yaml"
sed -n '/name: cvk-cisco-virtual-kubelet-shared-worker-replicaset/,/^---$/p' \
  "$managed_render" >"$worker_replicaset_policy"
grep -Fq 'resources: ["replicasets", "replicasets/status"]' \
  "$worker_replicaset_policy"
grep -Fq "'system:serviceaccount:kube-system:replicaset-controller'" \
  "$worker_replicaset_policy"
grep -Fq "request.subResource == 'status'" "$worker_replicaset_policy"
grep -Fq 'object.metadata.uid == oldObject.metadata.uid' \
  "$worker_replicaset_policy"
grep -Fq 'object.spec == oldObject.spec' "$worker_replicaset_policy"

worker_pod_update_policy="$scratch_dir/shared-worker-pod-update.yaml"
sed -n '/name: cvk-cisco-virtual-kubelet-shared-worker-pod-update/,/^---$/p' \
  "$managed_render" >"$worker_pod_update_policy"
grep -Fq 'resources: ["pods", "pods/status", "pods/ephemeralcontainers", "pods/resize"]' \
  "$worker_pod_update_policy"
grep -Fq "request.subResource == 'status'" "$worker_pod_update_policy"
grep -Fq "request.userInfo.username == 'system:node:' + oldObject.spec.nodeName" \
  "$worker_pod_update_policy"
grep -Fq "request.userInfo.groups.exists(g, g == 'system:nodes')" \
  "$worker_pod_update_policy"
grep -Fq "request.userInfo.groups.exists(g, g == 'system:authenticated')" \
  "$worker_pod_update_policy"
grep -Fq 'object.metadata.annotations == oldObject.metadata.annotations' \
  "$worker_pod_update_policy"
grep -Fq 'object.spec == oldObject.spec' "$worker_pod_update_policy"
grep -Fq 'a reserved worker Pod is immutable except for status written by its exact authenticated node' \
  "$worker_pod_update_policy"

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

app_ro_role="$scratch_dir/app-ro-role.yaml"
app_rw_role="$scratch_dir/app-rw-role.yaml"
app_device_role="$scratch_dir/app-device-role.yaml"
network_global_role="$scratch_dir/network-global-role.yaml"
network_lease_ro_role="$scratch_dir/network-lease-ro-role.yaml"
network_lease_rw_role="$scratch_dir/network-lease-rw-role.yaml"
network_ro_role="$scratch_dir/network-ro-role.yaml"
network_rw_role="$scratch_dir/network-rw-role.yaml"
network_upgrade_role="$scratch_dir/network-upgrade-role.yaml"
sed -n '/name: cisco-virtual-kubelet-app-hosting-read-only/,/^---$/p' "$managed_render" >"$app_ro_role"
sed -n '/name: cisco-virtual-kubelet-app-hosting-read-write/,/^---$/p' "$managed_render" >"$app_rw_role"
sed -n '/name: cisco-virtual-kubelet-app-hosting-device-read/,/^---$/p' "$managed_render" >"$app_device_role"
sed -n '/name: cisco-virtual-kubelet-network-management-global-read/,/^---$/p' "$managed_render" >"$network_global_role"
sed -n '/name: cisco-virtual-kubelet-network-management-lease-read-only/,/^---$/p' "$managed_render" >"$network_lease_ro_role"
sed -n '/name: cisco-virtual-kubelet-network-management-lease-read-write/,/^---$/p' "$managed_render" >"$network_lease_rw_role"
sed -n '/name: cisco-virtual-kubelet-network-management-read-only/,/^---$/p' "$managed_render" >"$network_ro_role"
sed -n '/name: cisco-virtual-kubelet-network-management-read-write/,/^---$/p' "$managed_render" >"$network_rw_role"
sed -n '/name: cisco-virtual-kubelet-network-management-read-write/,/^---$/p' "$managed_upgrade_render" >"$network_upgrade_role"

# Expand the chart's simple RBAC rule shape into group|resource|verb tuples.
# This proves the read-write roles are semantic strict supersets even when a
# YAML rule groups resources differently from its read-only counterpart.
role_permissions() {
  awk '
    function clear(values, key) {
      for (key in values) delete values[key]
    }
    function parse_list(line, values, parts, count, position, value) {
      clear(values)
      sub(/^[^[]*\[/, "", line)
      sub(/\].*$/, "", line)
      count = split(line, parts, ",")
      for (position = 1; position <= count; position++) {
        value = parts[position]
        gsub(/^[[:space:]\"]+|[[:space:]\"]+$/, "", value)
        values[position] = value
      }
      return count
    }
    function emit(group_index, resource_index, verb_index) {
      for (group_index = 1; group_index <= group_count; group_index++)
        for (resource_index = 1; resource_index <= resource_count; resource_index++)
          for (verb_index = 1; verb_index <= verb_count; verb_index++)
            print groups[group_index] "|" resources[resource_index] "|" verbs[verb_index]
    }
    /^  - apiGroups: \[/ {
      group_count = parse_list($0, groups)
      clear(resources)
      resource_count = 0
      collecting_resources = 0
      next
    }
    /^    resources: \[/ {
      resource_count = parse_list($0, resources)
      collecting_resources = 0
      next
    }
    /^    resources:[[:space:]]*$/ {
      clear(resources)
      resource_count = 0
      collecting_resources = 1
      next
    }
    collecting_resources && /^      - / {
      value = $0
      sub(/^      - /, "", value)
      resources[++resource_count] = value
      next
    }
    /^    verbs: \[/ {
      verb_count = parse_list($0, verbs)
      collecting_resources = 0
      emit()
    }
  ' "$1" | sort -u
}

assert_strict_permission_superset() {
  local read_only_role="$1"
  local read_write_role="$2"
  local label="$3"
  local read_only_permissions="$scratch_dir/${label}-read-only-permissions.txt"
  local read_write_permissions="$scratch_dir/${label}-read-write-permissions.txt"
  local missing_permissions="$scratch_dir/${label}-missing-permissions.txt"
  local added_permissions="$scratch_dir/${label}-added-permissions.txt"

  role_permissions "$read_only_role" >"$read_only_permissions"
  role_permissions "$read_write_role" >"$read_write_permissions"
  comm -23 "$read_only_permissions" "$read_write_permissions" >"$missing_permissions"
  if [[ -s "$missing_permissions" ]]; then
    echo "$label read-write profile is missing read-only permissions:" >&2
    sed 's/^/  /' "$missing_permissions" >&2
    exit 1
  fi
  comm -13 "$read_only_permissions" "$read_write_permissions" >"$added_permissions"
  if [[ ! -s "$added_permissions" ]]; then
    echo "$label read-write profile is not a strict permission superset" >&2
    exit 1
  fi
}

assert_strict_permission_superset "$app_ro_role" "$app_rw_role" app-hosting
assert_strict_permission_superset "$network_ro_role" "$network_rw_role" network-management
assert_strict_permission_superset "$network_lease_ro_role" "$network_lease_rw_role" network-management-lease

# Profile ClusterRoles have fixed cluster-wide names and may be shared by
# multiple CVK releases. Their rules must therefore be release-independent;
# feature gates constrain controller registration/use, not a shared role.
cmp "$network_rw_role" "$network_upgrade_role"
profile_template="$scratch_dir/profile-template.yaml"
sed -n '1,/^{{- end }}$/p' \
  "$chart_dir/templates/topology-rbac.yaml" >"$profile_template"
if grep -Fq '.Values.gnoi.' "$profile_template"; then
  echo "fixed worker profile rules depend on per-release gNOI gates" >&2
  exit 1
fi

for worker_role in \
  "$app_ro_role" "$app_rw_role" "$app_device_role" \
  "$network_global_role" "$network_lease_ro_role" "$network_lease_rw_role" \
  "$network_ro_role" "$network_rw_role"; do
  if grep -Eq 'resources: \["(serviceaccounts|serviceaccounts/token|rolebindings|clusterrolebindings|roles|clusterroles)"\]|resources: \["\*"\]|verbs: \["\*"\]' "$worker_role"; then
    echo "managed worker profile retained token, RBAC, or wildcard authority" >&2
    exit 1
  fi
done

# Read-only app hosting is observation-only; read-write adds only the bounded
# virtual-kubelet status/cleanup contract.
grep -A1 -F 'resources: ["pods"]' "$app_ro_role" | grep -Fq 'verbs: ["get", "list", "watch"]'
if grep -Eq 'nodes/status|pods/status|events|leases|secrets|delete' "$app_ro_role"; then
  echo "app-hosting read-only profile retained execution authority" >&2
  exit 1
fi
grep -A1 -F 'resources: ["nodes/status"]' "$app_rw_role" | grep -Fq 'verbs: ["get", "update", "patch"]'
grep -A1 -F 'resources: ["pods/status"]' "$app_rw_role" | grep -Fq 'verbs: ["get", "update", "patch"]'
grep -A1 -F 'resources: ["pods"]' "$app_rw_role" | grep -Fq 'verbs: ["get", "list", "watch", "delete"]'
grep -Fq 'resources: ["configmaps", "secrets", "services"]' "$app_rw_role"
grep -A1 -F 'resources: ["leases"]' "$app_rw_role" | grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
grep -A1 -F 'resources: ["selfsubjectreviews"]' "$app_rw_role" | grep -Fq 'verbs: ["create"]'
if grep -Eq 'pods/(log|exec)|verbs:.*(create.*pods|delete.*nodes)' "$app_rw_role"; then
  echo "app-hosting read-write profile retained broad workload authority" >&2
  exit 1
fi
if grep -Fq 'resources: ["ciscodevices"]' "$app_ro_role" ||
   grep -Fq 'resources: ["ciscodevices"]' "$app_rw_role" ||
   grep -Fq 'resources: ["iosxesoftwareupgrades", "iosxeoperationalactions"]' "$app_rw_role"; then
  echo "cluster-bound app profile retained tenant operation reads" >&2
  exit 1
fi
grep -A1 -F 'resources: ["ciscodevices"]' "$app_device_role" | grep -Fq 'verbs: ["get"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades", "iosxeoperationalactions"]' "$app_device_role" | grep -Fq 'verbs: ["get", "list", "watch"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades/status"]' "$app_device_role" | grep -Fq 'verbs: ["get", "update", "patch"]'
if grep -Eq 'create|delete|secrets|nodes|pods|leases' "$app_device_role"; then
  echo "app-hosting device support exceeds its admission-fenced drain inventory role" >&2
  exit 1
fi

# Global network support is read-only. All config, result, operation, and
# Lease mutation stays in the selected namespaced profile.
if grep -Fq 'resources: ["ciscodevices"]' "$network_global_role"; then
  echo "network global support role retained namespaced CiscoDevice reads" >&2
  exit 1
fi
grep -Fq 'resources: ["iosxeconfigdefaults"]' "$network_global_role"
grep -A1 -F 'resources: ["selfsubjectreviews"]' "$network_global_role" | grep -Fq 'verbs: ["create"]'
test "$(grep -c 'verbs: \["create"\]' "$network_global_role")" -eq 1
if grep -Eq 'verbs:.*(update|patch|delete)' "$network_global_role"; then
  echo "network global-read support role contains persisted-object mutation" >&2
  exit 1
fi
grep -A1 -F 'resources: ["leases"]' "$network_lease_ro_role" | grep -Fq 'verbs: ["get", "list", "watch"]'
grep -A1 -F 'resources: ["leases"]' "$network_lease_rw_role" | grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
if grep -Eq 'secrets|configmaps|ciscodevices|iosxe|nxos|pods|nodes|events|create|delete' \
    "$network_lease_ro_role" "$network_lease_rw_role"; then
  echo "alternate-namespace lease support roles contain non-Lease authority" >&2
  exit 1
fi
grep -Fq 'resources: ["deviceoperations/status"]' "$network_ro_role"
grep -Fq 'resources: ["ciscodevices"]' "$network_ro_role"
grep -Fq '      - iosxetelemetries/status' "$network_ro_role"
grep -A1 -F 'resources: ["deviceoperations"]' "$network_ro_role" | grep -Fq 'verbs: ["get", "list", "watch", "create", "delete"]'
grep -A1 -F 'resources: ["leases"]' "$network_ro_role" | grep -Fq 'verbs: ["get", "list", "watch"]'
if grep -Eq 'iosxeconfigs/status|nxosconfigs/status|iosxesoftwareupgrades/status|iosxeoperationalactions/status|secrets' "$network_ro_role"; then
  echo "network read-only profile retained device-mutation authority" >&2
  exit 1
fi
grep -Fq 'resources: ["iosxeconfigs", "nxosconfigs", "iosxetelemetries", "iosxediagnostics"]' "$network_rw_role"
grep -Fq 'resources: ["iosxeconfigrevisions"]' "$network_rw_role"
grep -Fq 'resources: ["ciscodevices"]' "$network_rw_role"
grep -Fq 'resources: ["secrets"]' "$network_rw_role"
grep -A1 -F 'resources: ["leases"]' "$network_rw_role" | grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
if grep -Eq 'resources: \["(nodes/status|pods/status)"\]|verbs: \["\*"\]' "$network_rw_role"; then
  echo "network read-write profile crossed into app identity or wildcard authority" >&2
  exit 1
fi
grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$network_rw_role" | grep -Fq 'verbs: ["update", "patch"]'
grep -A1 -F 'resources: ["iosxesoftwareupgrades/status"]' "$network_rw_role" | grep -Fq 'verbs: ["get", "update", "patch"]'
grep -A1 -F 'resources: ["iosxeoperationalactions"]' "$network_rw_role" | grep -Fq 'verbs: ["update", "patch"]'
grep -A1 -F 'resources: ["iosxeoperationalactions/status"]' "$network_rw_role" | grep -Fq 'verbs: ["get", "update", "patch"]'

manager_role="$scratch_dir/manager-role.yaml"
sed -n '/name: cvk-cisco-virtual-kubelet-managed-topology-manager/,/^---$/p' "$managed_render" >"$manager_role"
grep -A1 -F 'resources: ["iosxesoftwareupgrades"]' "$manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
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
grep -A1 -F 'resources: ["iosxediagnostics"]' "$manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
grep -A3 -F 'resources: ["pods"]' "$manager_role" | \
  grep -Fq 'verbs: ["get", "list", "watch", "patch", "delete"]'
if grep -A3 -F 'resources: ["pods"]' "$manager_role" | \
    grep -F 'verbs:' | grep -Eq 'create|update|deletecollection'; then
  echo "managed topology manager retained broad Pod mutation" >&2
  exit 1
fi
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
  grep -Fq 'verbs: ["get", "list", "watch", "update", "patch"]'
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

# A fresh managed render has no legacy third account or binding. The manager
# creates the two functional accounts only in namespaces that host workers.
if has_named_binding "$managed_render" cisco-virtual-kubelet ||
   has_named_binding "$managed_render" cisco-virtual-kubelet-device; then
  echo "managed topology retained a shared VK RoleBinding" >&2
  exit 1
fi
if has_named_service_account "$managed_render" cisco-virtual-kubelet; then
  echo "fresh managed topology rendered the legacy VK ServiceAccount" >&2
  exit 1
fi
for role in \
  cisco-virtual-kubelet-app-hosting-read-only \
  cisco-virtual-kubelet-app-hosting-read-write \
  cisco-virtual-kubelet-app-hosting-device-read \
  cisco-virtual-kubelet-network-management-global-read \
  cisco-virtual-kubelet-network-management-lease-read-only \
  cisco-virtual-kubelet-network-management-lease-read-write \
  cisco-virtual-kubelet-network-management-read-only \
  cisco-virtual-kubelet-network-management-read-write; do
  grep -Fq "      - $role" "$manager_role"
done
grep -Fq 'topology.cisco.vk/retire-shared-worker-access: rollout-v1' \
  "$chart_dir/templates/vk-rbac.yaml"
grep -Fq 'lookup "rbac.authorization.k8s.io/v1" "ClusterRoleBinding"' \
  "$chart_dir/templates/vk-rbac.yaml"
grep -Fq 'lookup "rbac.authorization.k8s.io/v1" "RoleBinding"' \
  "$chart_dir/templates/vk-rbac.yaml"
grep -Fq 'validateTopologyRetirement' "$chart_dir/templates/deployment.yaml"
grep -Fq 'lookup "cisco.vk/v1alpha1" "CiscoDevice" "" ""' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'lookup "v1" "ConfigMap" "" ""' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'meta.helm.sh/release-name' "$chart_dir/templates/_helpers.tpl"
grep -Fq 'meta.helm.sh/release-namespace' "$chart_dir/templates/_helpers.tpl"
grep -Fq 'owns more than one retained managed topology policy' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'managed topology policy coordinates are immutable after bootstrap' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'managed topology admission prefix is immutable after bootstrap' \
  "$chart_dir/templates/_helpers.tpl"
grep -Fq 'topology worker account names are immutable after policy bootstrap' \
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

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.appHosting.serviceAccountName=shared-worker \
    --set topology.workerAccounts.networkManagement.serviceAccountName=shared-worker \
    >"$error_output" 2>&1; then
  echo "managed topology accepted one identity for both worker planes" >&2
  exit 1
fi
grep -Fq 'topology worker account names must be distinct' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --namespace cisco-vk-system \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.appHosting.serviceAccountName=cisco-vk-system \
    >"$error_output" 2>&1; then
  echo "managed topology accepted an app worker identity equal to the policy namespace" >&2
  exit 1
fi
grep -Fq 'managed admission contract bindings must be pairwise distinct' "$error_output"
grep -Fq 'policy namespace and app-hosting ServiceAccount' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --namespace cisco-vk-system \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.networkManagement.serviceAccountName=cvk-cisco-virtual-kubelet \
    >"$error_output" 2>&1; then
  echo "managed topology accepted a network worker identity equal to the admission prefix" >&2
  exit 1
fi
grep -Fq 'managed admission contract bindings must be pairwise distinct' "$error_output"
grep -Fq 'admission policy prefix and network-management ServiceAccount' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.appHosting.serviceAccountName=cisco-vk-managed-lab-01234567 \
    >"$error_output" 2>&1; then
  echo "managed topology accepted an app-hosting identity from the generated worker namespace" >&2
  exit 1
fi
grep -Fq 'overlaps the reserved generated worker identity namespace' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.networkManagement.serviceAccountName=cisco-vk-legacy-lab-abcdef12 \
    >"$error_output" 2>&1; then
  echo "managed topology accepted a network-management identity from the generated worker namespace" >&2
  exit 1
fi
grep -Fq 'overlaps the reserved generated worker identity namespace' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.appHosting.serviceAccountName=cisco-virtual-kubelet-controller \
    >"$error_output" 2>&1; then
  echo "managed topology accepted the controller identity as a worker" >&2
  exit 1
fi
grep -Fq 'must be distinct from controller and legacy VK identity' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.workerAccounts.appHosting.accessMode=disabled \
    --set topology.workerAccounts.networkManagement.accessMode=disabled \
    >"$error_output" 2>&1; then
  echo "managed topology accepted both worker planes disabled" >&2
  exit 1
fi
grep -Fq 'topology workerAccounts cannot both be disabled' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict --set gnoi.enableSoftwareUpgrade=true \
    >"$error_output" 2>&1; then
  echo "software-upgrade gate accepted a read-only network identity" >&2
  exit 1
fi
grep -Fq 'networkManagement' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --skip-schema-validation \
    --set topology.enabled=true --set controller.leaderElect=true \
    --set rbac.profile=strict --set gnoi.enableSoftwareUpgrade=true \
    >"$error_output" 2>&1; then
  echo "software-upgrade gate bypassed the render-time network profile check" >&2
  exit 1
fi
grep -Fq 'requires topology.workerAccounts.networkManagement.accessMode=readWrite' "$error_output"

# App readOnly is a supported network-only/observer deployment. It emits the
# profile flag but launches no app worker at runtime.
app_read_only_render="$scratch_dir/app-read-only.yaml"
helm template cvk "$chart_dir" --kube-version 1.35.0 \
  --set topology.enabled=true --set controller.leaderElect=true \
  --set rbac.profile=strict \
  --set topology.workerAccounts.appHosting.accessMode=readOnly \
  >"$app_read_only_render"
grep -Fq -- '- --app-hosting-access-mode=readOnly' "$app_read_only_render"

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
grep -Fq 'opt-in managed drain uses only policy/v1 Eviction' \
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
    --set gnoi.enableSoftwareUpgrade=true \
    --set topology.policy.workloadDrain.enabled=true >"$error_output" 2>&1; then
  echo "workload drain without an explicit namespace allowlist passed values schema" >&2
  exit 1
fi
grep -Fq 'allowedNamespaces' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=false \
    --set gnoi.enableSoftwareUpgrade=true \
    --set topology.policy.workloadDrain.enabled=true \
    --set-json 'topology.policy.workloadDrain.allowedNamespaces=["apps"]' \
    >"$error_output" 2>&1; then
  echo "workload drain rendered while managed topology was disabled" >&2
  exit 1
fi
grep -Fq '/topology/enabled' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set gnoi.enableSoftwareUpgrade=false \
    --set topology.policy.workloadDrain.enabled=true \
    --set-json 'topology.policy.workloadDrain.allowedNamespaces=["apps"]' \
    >"$error_output" 2>&1; then
  echo "workload drain rendered while gNOI software upgrade was disabled" >&2
  exit 1
fi
grep -Fq '/gnoi/enableSoftwareUpgrade' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set gnoi.enableSoftwareUpgrade=true \
    --set gnoi.disabled=true \
    --set topology.policy.workloadDrain.enabled=true \
    --set-json 'topology.policy.workloadDrain.allowedNamespaces=["apps"]' \
    >"$error_output" 2>&1; then
  echo "workload drain rendered while the global gNOI kill switch was active" >&2
  exit 1
fi
grep -Fq '/gnoi/disabled' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set topology.policy.workloadDrain.maxPods=33 >"$error_output" 2>&1; then
  echo "workload drain pod cap above the controller bound passed values schema" >&2
  exit 1
fi
grep -Fq 'maxPods' "$error_output"

if helm template cvk "$chart_dir" --kube-version 1.35.0 \
    --set topology.enabled=true \
    --set controller.leaderElect=true \
    --set rbac.profile=strict \
    --set gnoi.enableSoftwareUpgrade=true \
    --set topology.policy.workloadDrain.enabled=true \
    --set-json 'topology.policy.workloadDrain.allowedNamespaces=["apps"]' \
    --set topology.workerAccounts.networkManagement.accessMode=readWrite \
    --set topology.policy.workloadDrain.maxTimeoutSeconds=300 \
    --set topology.policy.workloadDrain.maxTerminationGraceSeconds=181 >"$error_output" 2>&1; then
  echo "workload drain caps without the completion buffer rendered" >&2
  exit 1
fi
grep -Fq 'maxTerminationGraceSeconds plus 120' "$error_output"

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
