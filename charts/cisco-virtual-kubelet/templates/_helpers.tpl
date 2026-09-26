{{/*
Expand the name of the chart.
*/}}
{{- define "cisco-virtual-kubelet.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "cisco-virtual-kubelet.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart label.
*/}}
{{- define "cisco-virtual-kubelet.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "cisco-virtual-kubelet.labels" -}}
helm.sh/chart: {{ include "cisco-virtual-kubelet.chart" . }}
{{ include "cisco-virtual-kubelet.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "cisco-virtual-kubelet.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cisco-virtual-kubelet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Resolve the controller image.
Falls back to .Values.image when controllerImage.repository is empty.
*/}}
{{- define "cisco-virtual-kubelet.controllerImage" -}}
{{- $repo := .Values.controllerImage.repository | default .Values.image.repository }}
{{- $tag  := .Values.controllerImage.tag        | default .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" $repo $tag }}
{{- end }}

{{/*
Resolve the controller image pull policy.
Falls back to .Values.image.pullPolicy when controllerImage.pullPolicy is empty.
*/}}
{{- define "cisco-virtual-kubelet.controllerImagePullPolicy" -}}
{{- .Values.controllerImage.pullPolicy | default .Values.image.pullPolicy }}
{{- end }}

{{/*
Resolve the VK image string passed as --vk-image to the controller.
Falls back to .Values.image when vkImage.repository is empty.
*/}}
{{- define "cisco-virtual-kubelet.vkImage" -}}
{{- $repo := .Values.vkImage.repository | default .Values.image.repository }}
{{- $tag  := .Values.vkImage.tag        | default .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" $repo $tag }}
{{- end }}

{{/*
Resolve the per-device VK image pull policy.
Falls back to .Values.image.pullPolicy when vkImage.pullPolicy is empty.
*/}}
{{- define "cisco-virtual-kubelet.vkImagePullPolicy" -}}
{{- .Values.vkImage.pullPolicy | default .Values.image.pullPolicy }}
{{- end }}

{{/*
Controller ServiceAccount name.
*/}}
{{- define "cisco-virtual-kubelet.controllerServiceAccountName" -}}
{{- .Values.serviceAccount.controllerName }}
{{- end }}

{{/*
VK ServiceAccount name.
*/}}
{{- define "cisco-virtual-kubelet.vkServiceAccountName" -}}
{{- .Values.serviceAccount.vkName }}
{{- end }}

{{/* Shared managed app-hosting ServiceAccount name. */}}
{{- define "cisco-virtual-kubelet.appHostingServiceAccountName" -}}
{{- if .Values.topology.workerAccounts.appHosting.serviceAccountName -}}
{{- .Values.topology.workerAccounts.appHosting.serviceAccountName -}}
{{- else -}}
{{- $prefix := include "cisco-virtual-kubelet.fullname" . | trunc 51 | trimSuffix "-" -}}
{{- printf "%s-app-hosting" $prefix -}}
{{- end -}}
{{- end }}

{{/* Shared managed network-management ServiceAccount name. */}}
{{- define "cisco-virtual-kubelet.networkManagementServiceAccountName" -}}
{{- if .Values.topology.workerAccounts.networkManagement.serviceAccountName -}}
{{- .Values.topology.workerAccounts.networkManagement.serviceAccountName -}}
{{- else -}}
{{- $prefix := include "cisco-virtual-kubelet.fullname" . | trunc 44 | trimSuffix "-" -}}
{{- printf "%s-network-management" $prefix -}}
{{- end -}}
{{- end }}

{{/*
Resolve the telemetry OTLP endpoint injected into the controller pod.
The controller copies this value into per-device VK pods when it creates
their Deployments.
*/}}
{{- define "cisco-virtual-kubelet.telemetryOtlpEndpoint" -}}
{{- $collector := index .Values "collector" -}}
{{- if .Values.telemetry.otlp.endpoint -}}
{{- .Values.telemetry.otlp.endpoint -}}
{{- else if and $collector (index $collector "enabled") -}}
{{- printf "%s-collector:4317" .Release.Name -}}
{{- end -}}
{{- end }}

{{/*
A live Helm downgrade from managed topology is allowed only after the manager
has completed every reverse writer handoff and the exact retained policy,
ledger, and manager authority still exist. `lookup` intentionally makes this a
live-cluster operation. Offline rendering cannot enumerate pre-existing
cross-namespace identities and is therefore not a supported prune source for
this one-time transition.
*/}}
{{- define "cisco-virtual-kubelet.validateTopologyRetirement" -}}
{{- if not .Values.topology.enabled -}}
{{- include "cisco-virtual-kubelet.validateWorkerAccountIdentityLock" . -}}
{{- $fullname := include "cisco-virtual-kubelet.fullname" . -}}
{{- $managerRoleName := printf "%s-managed-topology-manager" $fullname -}}
{{- $managerRole := lookup "rbac.authorization.k8s.io/v1" "ClusterRole" "" $managerRoleName -}}
{{- $managerBinding := lookup "rbac.authorization.k8s.io/v1" "ClusterRoleBinding" "" $managerRoleName -}}
{{- $sharedWorkerName := include "cisco-virtual-kubelet.vkServiceAccountName" . -}}
{{- $sharedClusterBinding := lookup "rbac.authorization.k8s.io/v1" "ClusterRoleBinding" "" $sharedWorkerName -}}
{{- $sharedDeviceBinding := lookup "rbac.authorization.k8s.io/v1" "RoleBinding" .Release.Namespace (printf "%s-device" $sharedWorkerName) -}}
{{- $policyNamespace := include "cisco-virtual-kubelet.topologyPolicyNamespace" . -}}
{{- $policyName := include "cisco-virtual-kubelet.topologyPolicyName" . -}}
{{- $ledgerName := include "cisco-virtual-kubelet.topologyLedgerName" . -}}
{{- $policy := lookup "v1" "ConfigMap" $policyNamespace $policyName -}}
{{- $ledger := lookup "v1" "ConfigMap" $policyNamespace $ledgerName -}}
{{- $legacyNodeMarkerPolicyName := printf "%s-legacy-node-marker" $fullname -}}
{{- $legacyNodeMarkerPolicy := lookup "admissionregistration.k8s.io/v1" "ValidatingAdmissionPolicy" "" $legacyNodeMarkerPolicyName -}}
{{- $legacyNodeMarkerBinding := lookup "admissionregistration.k8s.io/v1" "ValidatingAdmissionPolicyBinding" "" $legacyNodeMarkerPolicyName -}}
{{- $devices := lookup "cisco.vk/v1alpha1" "CiscoDevice" "" "" -}}
{{- $generatedWorkerState := false -}}
{{- /* A namespaced object is tenant-forgeable. Only a cluster-admin-owned
      binding with the generated authority shape may retain the global
      admission boundary during the access-before-marker crash window. */ -}}
{{- $clusterBindings := lookup "rbac.authorization.k8s.io/v1" "ClusterRoleBinding" "" "" -}}
{{- range $binding := (get $clusterBindings "items" | default (list)) -}}
{{- $annotations := dig "metadata" "annotations" (dict) $binding -}}
{{- $roleRef := get $binding "roleRef" | default (dict) -}}
{{- $subjects := get $binding "subjects" | default (list) -}}
{{- $deviceNamespace := get $annotations "topology.cisco.vk/device-namespace" | default "" -}}
{{- $deviceName := get $annotations "topology.cisco.vk/device-name" | default "" -}}
{{- $deviceUID := get $annotations "topology.cisco.vk/device-uid" | default "" -}}
{{- $managedBinding := and
      (eq (get $annotations "topology.cisco.vk/managed" | default "") "true")
      (eq (get $roleRef "name" | default "") "cisco-virtual-kubelet-managed-worker") -}}
{{- $legacyBinding := and
      (eq (get $annotations "topology.cisco.vk/worker-mode" | default "") "legacy")
      (eq (get $roleRef "name" | default "") "cisco-virtual-kubelet") -}}
{{- if and (or $managedBinding $legacyBinding)
      (eq (get $annotations "topology.cisco.vk/worker-protocol" | default "") "rollout-v1")
      (ne $deviceNamespace "") (ne $deviceName "") (ne $deviceUID "")
      (eq (get $roleRef "apiGroup" | default "") "rbac.authorization.k8s.io")
      (eq (get $roleRef "kind" | default "") "ClusterRole")
      (eq (len $subjects) 1) -}}
{{- $subject := first $subjects -}}
{{- if and
      (eq (get $subject "kind" | default "") "ServiceAccount")
      (eq (get $subject "apiGroup" | default "") "")
      (eq (get $subject "namespace" | default "") $deviceNamespace)
      (ne (get $subject "name" | default "") "") -}}
{{- $generatedWorkerState = true -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $statePresent := $generatedWorkerState -}}
{{- range $device := (get $devices "items" | default (list)) -}}
{{- $status := get $device "status" | default (dict) -}}
{{- $annotations := dig "metadata" "annotations" (dict) $device -}}
{{- if or (hasKey $status "nodeIdentity") (hasKey $status "legacyHandoff") (ne (get $annotations "topology.cisco.vk/isolated-legacy-worker" | default "") "") -}}
{{- $statePresent = true -}}
{{- end -}}
{{- end -}}
{{- $retainedTopology := or $statePresent (not (empty $managerRole)) (not (empty $managerBinding)) (not (empty $policy)) (not (empty $ledger)) -}}
{{- if $retainedTopology -}}
{{- if or (empty $managerRole) (empty $managerBinding) (empty $policy) (empty $ledger) -}}
{{- fail (printf "topology.enabled=false found an incomplete retained topology boundary; restore the exact %s policy/ledger and %s role/binding before retirement" $policyName $managerRoleName) -}}
{{- end -}}
{{- if or (empty $legacyNodeMarkerPolicy) (empty $legacyNodeMarkerBinding) -}}
{{- fail (printf "topology.enabled=false requires retained %s admission policy and binding to protect released Node handoff markers; upgrade this release once with topology.enabled=true before disabling topology" $legacyNodeMarkerPolicyName) -}}
{{- end -}}
{{- $legacyNodeMarkerDigest := include "cisco-virtual-kubelet.legacyNodeMarkerAdmissionDigest" . -}}
{{- if or
      (ne (dig "metadata" "annotations" "topology.cisco.vk/admission-contract-version" "" $legacyNodeMarkerPolicy) "v2")
      (ne (dig "metadata" "annotations" "topology.cisco.vk/admission-contract-digest" "" $legacyNodeMarkerPolicy) $legacyNodeMarkerDigest)
      (ne (dig "metadata" "annotations" "helm.sh/resource-policy" "" $legacyNodeMarkerPolicy) "keep")
      (ne (dig "spec" "failurePolicy" "" $legacyNodeMarkerPolicy) "Fail") -}}
{{- fail (printf "topology.enabled=false requires retained %s policy contract v2 digest %s with failurePolicy=Fail; re-enable topology with this chart before retirement" $legacyNodeMarkerPolicyName $legacyNodeMarkerDigest) -}}
{{- end -}}
{{- $legacyNodeMarkerGeneration := dig "metadata" "generation" 0 $legacyNodeMarkerPolicy -}}
{{- $legacyNodeMarkerObservedGeneration := dig "status" "observedGeneration" 0 $legacyNodeMarkerPolicy -}}
{{- $legacyNodeMarkerStatus := get $legacyNodeMarkerPolicy "status" | default (dict) -}}
{{- $legacyNodeMarkerWarnings := dig "status" "typeChecking" "expressionWarnings" (list) $legacyNodeMarkerPolicy -}}
{{- if or
      (eq (toString $legacyNodeMarkerGeneration) "0")
      (ne (toString $legacyNodeMarkerGeneration) (toString $legacyNodeMarkerObservedGeneration))
      (not (hasKey $legacyNodeMarkerStatus "typeChecking"))
      (ne (len $legacyNodeMarkerWarnings) 0) -}}
{{- fail (printf "topology.enabled=false requires retained %s policy generation to be observed and warning-free; wait for API-server compilation or re-enable topology before retirement" $legacyNodeMarkerPolicyName) -}}
{{- end -}}
{{- $legacyNodeMarkerActions := dig "spec" "validationActions" (list) $legacyNodeMarkerBinding -}}
{{- $legacyNodeMarkerBindingSpec := get $legacyNodeMarkerBinding "spec" | default (dict) -}}
{{- $legacyNodeMarkerMatchResources := get $legacyNodeMarkerBindingSpec "matchResources" | default (dict) -}}
{{- if or
      (ne (dig "metadata" "annotations" "topology.cisco.vk/admission-contract-version" "" $legacyNodeMarkerBinding) "v2")
      (ne (dig "metadata" "annotations" "topology.cisco.vk/admission-contract-digest" "" $legacyNodeMarkerBinding) $legacyNodeMarkerDigest)
      (ne (dig "metadata" "annotations" "helm.sh/resource-policy" "" $legacyNodeMarkerBinding) "keep")
      (ne (dig "spec" "policyName" "" $legacyNodeMarkerBinding) $legacyNodeMarkerPolicyName)
      (not (empty (get $legacyNodeMarkerBindingSpec "paramRef")))
      (ne (get $legacyNodeMarkerMatchResources "matchPolicy" | default "Equivalent") "Equivalent")
      (not (empty (get $legacyNodeMarkerMatchResources "namespaceSelector")))
      (not (empty (get $legacyNodeMarkerMatchResources "objectSelector")))
      (not (empty (get $legacyNodeMarkerMatchResources "resourceRules")))
      (not (empty (get $legacyNodeMarkerMatchResources "excludeResourceRules")))
      (ne (len $legacyNodeMarkerActions) 1) -}}
{{- fail (printf "topology.enabled=false requires retained %s binding to enforce only Deny for the exact unparameterized global policy; re-enable topology before retirement" $legacyNodeMarkerPolicyName) -}}
{{- end -}}
{{- if ne (first $legacyNodeMarkerActions) "Deny" -}}
{{- fail (printf "topology.enabled=false requires retained %s binding to enforce only Deny for the exact unparameterized global policy; re-enable topology before retirement" $legacyNodeMarkerPolicyName) -}}
{{- end -}}
{{- if or (not (empty $sharedClusterBinding)) (not (empty $sharedDeviceBinding)) -}}
{{- fail "topology.enabled=false is blocked until the manager has retired both release-wide shared worker bindings" -}}
{{- end -}}
{{- if not .Values.controller.leaderElect -}}
{{- fail "retained managed topology state requires controller.leaderElect=true until every isolated legacy identity and admission dependency is retired" -}}
{{- end -}}
{{- if .Values.aggregator.enabled -}}
{{- fail "retained managed topology state requires aggregator.enabled=false; reverse handoff uses isolated per-device workers" -}}
{{- end -}}
{{- if ne (dig "metadata" "annotations" "topology.cisco.vk/managed-policy" "" $policy) "true" -}}
{{- fail (printf "topology.enabled=false requires retained policy %s/%s to carry topology.cisco.vk/managed-policy=true" $policyNamespace $policyName) -}}
{{- end -}}
{{- if ne (dig "metadata" "annotations" "topology.cisco.vk/admission-policy-prefix" "" $policy) $fullname -}}
{{- fail (printf "topology.enabled=false requires retained policy %s/%s to preserve admission prefix %s" $policyNamespace $policyName $fullname) -}}
{{- end -}}
{{- if ne (dig "metadata" "annotations" "topology.cisco.vk/managed-ledger" "" $ledger) "true" -}}
{{- fail (printf "topology.enabled=false requires retained ledger %s/%s to carry topology.cisco.vk/managed-ledger=true" $policyNamespace $ledgerName) -}}
{{- end -}}
{{- $policyLedgerUID := dig "metadata" "annotations" "topology.cisco.vk/ledger-uid" "" $policy -}}
{{- $ledgerUID := dig "metadata" "uid" "" $ledger -}}
{{- if or (eq $policyLedgerUID "") (eq $ledgerUID "") (ne $policyLedgerUID $ledgerUID) -}}
{{- fail (printf "topology.enabled=false requires retained policy %s/%s to remain bound to live ledger UID %s" $policyNamespace $policyName $ledgerUID) -}}
{{- end -}}
{{- range $device := (get $devices "items" | default (list)) -}}
{{- $status := get $device "status" | default (dict) -}}
{{- if hasKey $status "nodeIdentity" -}}
{{- fail (printf "topology.enabled=false is blocked: CiscoDevice %s/%s still has managed status.nodeIdentity; request and complete its UID-bound legacy handoff first" (dig "metadata" "namespace" "" $device) (dig "metadata" "name" "" $device)) -}}
{{- end -}}
{{- if hasKey $status "legacyHandoff" -}}
{{- $phase := dig "legacyHandoff" "phase" "" $status -}}
{{- if ne $phase "Complete" -}}
{{- fail (printf "topology.enabled=false is blocked: CiscoDevice %s/%s legacy handoff is %s, not Complete" (dig "metadata" "namespace" "" $device) (dig "metadata" "name" "" $device) $phase) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Resolve the effective OTLP insecure flag. The bundled collector listens on
plaintext gRPC, so enabling it forces insecure export.
*/}}
{{- define "cisco-virtual-kubelet.telemetryOtlpInsecure" -}}
{{- if .Values.collector.enabled -}}true{{- else -}}{{- .Values.telemetry.otlp.insecure -}}{{- end -}}
{{- end }}

{{/*
Resolve the YANG models mount path. When telemetry.yangModels.configMapName
is set the models are mounted at telemetry.yangModels.mountPath (default
/var/lib/cvk/yang). When the ConfigMap is not configured we fall back to
telemetry.yangModelsDir for backward compatibility.
*/}}
{{- define "cisco-virtual-kubelet.yangModelsMountPath" -}}
{{- if .Values.telemetry.yangModels.configMapName -}}
{{- .Values.telemetry.yangModels.mountPath | default "/var/lib/cvk/yang" -}}
{{- else -}}
{{- .Values.telemetry.yangModelsDir -}}
{{- end -}}
{{- end }}

{{/*
Guard against multi-replica deployments without leader election. When
replicaCount > 1 with controller.leaderElect=false the operator opens
duplicate gNMI Subscribe RPCs to every device — split-brain. Render-time
failure is the only safe behavior.
*/}}
{{- define "cisco-virtual-kubelet.validateLeaderElection" -}}
{{- if and (gt (int .Values.replicaCount) 1) (not .Values.controller.leaderElect) -}}
{{- fail "replicaCount > 1 requires controller.leaderElect=true; otherwise replicas open duplicate gNMI Subscribe RPCs to each device" -}}
{{- end -}}
{{- end }}

{{/*
Resolve the namespaced administrator policy and its UID-bound ledger. These
names are deterministic chart inputs; the live ConfigMap UID is deliberately
never templated.
*/}}
{{- define "cisco-virtual-kubelet.topologyPolicyNamespace" -}}
{{- .Values.topology.policy.namespace | default .Release.Namespace -}}
{{- end }}

{{- define "cisco-virtual-kubelet.topologyPolicyName" -}}
{{- .Values.topology.policy.name | default (printf "%s-topology-policy" (include "cisco-virtual-kubelet.fullname" .)) -}}
{{- end }}

{{- define "cisco-virtual-kubelet.topologyLedgerName" -}}
{{- .Values.topology.ledger.name | default (printf "%s-topology-ledger" (include "cisco-virtual-kubelet.fullname" .)) -}}
{{- end }}

{{- define "cisco-virtual-kubelet.managerUsername" -}}
{{- printf "system:serviceaccount:%s:%s" .Release.Namespace (include "cisco-virtual-kubelet.controllerServiceAccountName" .) -}}
{{- end }}

{{/*
Admission resources are retained across Helm disable/uninstall until the
operator completes the explicit managed-topology retirement procedure.
The fixed version is independently checked by the manager; it is not itself
trusted as proof that the policy expressions are intact.
*/}}
{{- define "cisco-virtual-kubelet.topologyAdmissionResourceAnnotations" -}}
helm.sh/resource-policy: keep
topology.cisco.vk/admission-contract-version: "v2"
{{- end }}

{{/* Compiled digest of the legacy Node audit-marker policy Spec.
     The prior topology-enabled manager preflight verifies the live Spec before
     retirement; Helm later uses this stamp only to prove that the required
     retained generation was installed. */}}
{{- define "cisco-virtual-kubelet.legacyNodeMarkerAdmissionDigest" -}}
sha256:02c0e65602ac0ebcc3d19b15bd7cbcd7c3840c081d72f7541efbafc394f2ee76
{{- end }}

{{/*
Lock the two cluster-reserved functional worker names and Lease authority
namespace to the retained policy. Missing JSON keys are accepted only for the
one-time migration from the earlier policy schema; protected annotations and
the manager preflight keep the effective identity fail closed.
*/}}
{{- define "cisco-virtual-kubelet.validateWorkerAccountIdentityLock" -}}
{{- $root := . -}}
{{- $appAccount := include "cisco-virtual-kubelet.appHostingServiceAccountName" . -}}
{{- $networkAccount := include "cisco-virtual-kubelet.networkManagementServiceAccountName" . -}}
{{- $configLeaseNamespace := .Values.config.leaseNamespace | default "" -}}
{{- $policyNamespace := include "cisco-virtual-kubelet.topologyPolicyNamespace" . -}}
{{- $policyName := include "cisco-virtual-kubelet.topologyPolicyName" . -}}
{{- $fullname := include "cisco-virtual-kubelet.fullname" . -}}
{{- $ownedPolicies := list -}}
{{- $configMaps := lookup "v1" "ConfigMap" "" "" -}}
{{- range $candidate := (get $configMaps "items" | default (list)) -}}
{{- $annotations := dig "metadata" "annotations" (dict) $candidate -}}
{{- if and
      (eq (get $annotations "meta.helm.sh/release-name" | default "") $root.Release.Name)
      (eq (get $annotations "meta.helm.sh/release-namespace" | default "") $root.Release.Namespace)
      (eq (get $annotations "topology.cisco.vk/managed-policy" | default "") "true") -}}
{{- $ownedPolicies = append $ownedPolicies $candidate -}}
{{- end -}}
{{- end -}}
{{- if gt (len $ownedPolicies) 1 -}}
{{- fail (printf "Helm release %s/%s owns more than one retained managed topology policy; restore a single authoritative policy before upgrading" .Release.Namespace .Release.Name) -}}
{{- end -}}
{{- if eq (len $ownedPolicies) 1 -}}
{{- $existingPolicy := first $ownedPolicies -}}
{{- $existingPolicyNamespace := dig "metadata" "namespace" "" $existingPolicy -}}
{{- $existingPolicyName := dig "metadata" "name" "" $existingPolicy -}}
{{- if or (ne $existingPolicyNamespace $policyNamespace) (ne $existingPolicyName $policyName) -}}
{{- fail (printf "managed topology policy coordinates are immutable after bootstrap: Helm release %s/%s owns retained policy %s/%s, not resolved policy %s/%s; retire managed topology and re-enroll before changing them" .Release.Namespace .Release.Name $existingPolicyNamespace $existingPolicyName $policyNamespace $policyName) -}}
{{- end -}}
{{- $existingAnnotations := dig "metadata" "annotations" (dict) $existingPolicy -}}
{{- $existingPrefix := get $existingAnnotations "topology.cisco.vk/admission-policy-prefix" | default "" -}}
{{- if ne $existingPrefix $fullname -}}
{{- fail (printf "managed topology admission prefix is immutable after bootstrap: retained policy %s/%s records %q, not resolved prefix %q; retire managed topology and re-enroll before changing fullnameOverride or nameOverride" $policyNamespace $policyName $existingPrefix $fullname) -}}
{{- end -}}
{{- $existingLeaseNamespace := get $existingAnnotations "topology.cisco.vk/config-lease-namespace" | default "" -}}
{{- if ne $existingLeaseNamespace $configLeaseNamespace -}}
{{- fail (printf "CONFIG_LEASE_NAMESPACE is immutable after topology bootstrap: retained policy %s/%s records %q, not %q; retire managed topology and re-enroll before moving lease authority" $policyNamespace $policyName $existingLeaseNamespace $configLeaseNamespace) -}}
{{- end -}}
{{- $existingPolicyJSON := dig "data" "policy.json" "" $existingPolicy -}}
{{- if eq $existingPolicyJSON "" -}}
{{- fail (printf "existing topology policy %s/%s has no policy.json; restore the retained policy before changing worker accounts" $policyNamespace $policyName) -}}
{{- end -}}
{{- $recordedPolicy := mustFromJson $existingPolicyJSON -}}
{{- if not (kindIs "map" $recordedPolicy) -}}
{{- fail (printf "existing topology policy %s/%s policy.json must be a JSON object" $policyNamespace $policyName) -}}
{{- end -}}
{{- range $accountLock := list
      (dict "key" "appHostingServiceAccountName" "resolved" $appAccount)
      (dict "key" "networkManagementServiceAccountName" "resolved" $networkAccount) -}}
{{- $key := get $accountLock "key" -}}
{{- if hasKey $recordedPolicy $key -}}
{{- $recordedName := get $recordedPolicy $key -}}
{{- if not (kindIs "string" $recordedName) -}}
{{- fail (printf "existing topology policy %s/%s has an invalid %s identity lock" $policyNamespace $policyName $key) -}}
{{- end -}}
{{- if eq $recordedName "" -}}
{{- fail (printf "existing topology policy %s/%s has an empty %s identity lock" $policyNamespace $policyName $key) -}}
{{- end -}}
{{- if ne $recordedName (get $accountLock "resolved") -}}
{{- fail (printf "topology worker account names are immutable after policy bootstrap: %s is recorded as %q in %s/%s; retire managed topology and re-enroll before renaming it" $key $recordedName $policyNamespace $policyName) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if hasKey $recordedPolicy "configLeaseNamespace" -}}
{{- $recordedLeaseNamespace := get $recordedPolicy "configLeaseNamespace" -}}
{{- if not (kindIs "string" $recordedLeaseNamespace) -}}
{{- fail (printf "existing topology policy %s/%s has an invalid configLeaseNamespace identity lock" $policyNamespace $policyName) -}}
{{- end -}}
{{- if ne $recordedLeaseNamespace $configLeaseNamespace -}}
{{- fail (printf "CONFIG_LEASE_NAMESPACE is immutable after topology bootstrap: configLeaseNamespace is recorded as %q in %s/%s, not %q; retire managed topology and re-enroll before moving lease authority" $recordedLeaseNamespace $policyNamespace $policyName $configLeaseNamespace) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Managed topology is a production trust-boundary change, not a soft feature
hint. Fail closed unless the chart is rendered for the qualified Kubernetes
floor and the per-device worker architecture required by the controller.
Cross-field checks below mirror invariants that JSON Schema cannot express.
*/}}
{{- define "cisco-virtual-kubelet.validateManagedTopology" -}}
{{- if .Values.topology.enabled -}}
{{- if semverCompare "<1.35.0-0" .Capabilities.KubeVersion.Version -}}
{{- fail (printf "topology.enabled=true requires Kubernetes >=1.35.0 (render target is %s); no admission-policy fallback is supported" .Capabilities.KubeVersion.Version) -}}
{{- end -}}
{{- if .Values.aggregator.enabled -}}
{{- fail "topology.enabled=true requires aggregator.enabled=false; managed topology uses separate app-hosting and network-management worker planes" -}}
{{- end -}}
{{- if not .Values.controller.leaderElect -}}
{{- fail "topology.enabled=true requires controller.leaderElect=true; only the elected manager may reconcile topology and rollout authority" -}}
{{- end -}}
{{- if ne .Values.serviceAccount.vkName "cisco-virtual-kubelet" -}}
{{- fail "topology.enabled=true requires serviceAccount.vkName=cisco-virtual-kubelet so an existing topology-disabled worker can be identified and retired safely" -}}
{{- end -}}
{{- if ne .Values.rbac.profile "strict" -}}
{{- fail "topology.enabled=true requires rbac.profile=strict; managed workers must not inherit disabled write-class operation permissions" -}}
{{- end -}}
{{- $appAccount := include "cisco-virtual-kubelet.appHostingServiceAccountName" . -}}
{{- $networkAccount := include "cisco-virtual-kubelet.networkManagementServiceAccountName" . -}}
{{- range $account := list $appAccount $networkAccount -}}
{{- if not (regexMatch `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$` $account) -}}
{{- fail (printf "topology worker account name %q must be a non-empty DNS label of at most 63 characters" $account) -}}
{{- end -}}
{{- if regexMatch `^cisco-vk-(managed|legacy)-[a-z0-9]([-a-z0-9.]{0,61}[a-z0-9])?-[a-f0-9]{8}$` $account -}}
{{- fail (printf "topology worker account name %q overlaps the reserved generated worker identity namespace" $account) -}}
{{- end -}}
{{- end -}}
{{- range $mode := list .Values.topology.workerAccounts.appHosting.accessMode .Values.topology.workerAccounts.networkManagement.accessMode -}}
{{- if not (has $mode (list "disabled" "readOnly" "readWrite")) -}}
{{- fail (printf "topology worker access mode %q must be one of disabled, readOnly, or readWrite" $mode) -}}
{{- end -}}
{{- end -}}
{{- if eq $appAccount $networkAccount -}}
{{- fail "topology worker account names must be distinct" -}}
{{- end -}}
{{- range $identity := list .Values.serviceAccount.controllerName .Values.serviceAccount.vkName -}}
{{- if or (eq $appAccount $identity) (eq $networkAccount $identity) -}}
{{- fail (printf "topology worker account names must be distinct from controller and legacy VK identity %q" $identity) -}}
{{- end -}}
{{- end -}}
{{- $fullname := include "cisco-virtual-kubelet.fullname" . -}}
{{- $managerUsername := include "cisco-virtual-kubelet.managerUsername" . -}}
{{- $policyNamespace := include "cisco-virtual-kubelet.topologyPolicyNamespace" . -}}
{{- $policyName := include "cisco-virtual-kubelet.topologyPolicyName" . -}}
{{- $ledgerName := include "cisco-virtual-kubelet.topologyLedgerName" . -}}
{{- $seenAdmissionBindings := dict -}}
{{- range $binding := list
      (dict "name" "admission policy prefix" "value" $fullname)
      (dict "name" "authenticated manager username" "value" $managerUsername)
      (dict "name" "policy namespace" "value" $policyNamespace)
      (dict "name" "policy name" "value" $policyName)
      (dict "name" "ledger name" "value" $ledgerName)
      (dict "name" "app-hosting ServiceAccount" "value" $appAccount)
      (dict "name" "network-management ServiceAccount" "value" $networkAccount) -}}
{{- $value := get $binding "value" -}}
{{- if hasKey $seenAdmissionBindings $value -}}
{{- fail (printf "managed admission contract bindings must be pairwise distinct: %s and %s both resolve to %q" (get $seenAdmissionBindings $value) (get $binding "name") $value) -}}
{{- end -}}
{{- $_ := set $seenAdmissionBindings $value (get $binding "name") -}}
{{- end -}}
{{- include "cisco-virtual-kubelet.validateWorkerAccountIdentityLock" . -}}
{{- if and (eq .Values.topology.workerAccounts.appHosting.accessMode "disabled") (eq .Values.topology.workerAccounts.networkManagement.accessMode "disabled") -}}
{{- fail "topology workerAccounts cannot both be disabled" -}}
{{- end -}}
{{- if and .Values.gnoi.enableSoftwareUpgrade (ne .Values.topology.workerAccounts.networkManagement.accessMode "readWrite") -}}
{{- fail "gnoi.enableSoftwareUpgrade=true requires topology.workerAccounts.networkManagement.accessMode=readWrite" -}}
{{- end -}}
{{- if .Values.gnoi.enableWriteClass -}}
{{- fail "managed topology Phase 2 supports campaign-owned IOSXESoftwareUpgrade only; set gnoi.enableWriteClass=false" -}}
{{- end -}}
{{- range $key, $_ := .Values.topology.policy.fleetSelector.matchLabels -}}
{{- if not (regexMatch `^topology\.cisco\.vk/[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$` $key) -}}
{{- fail (printf "topology.policy.fleetSelector matchLabels key %q is outside the protected topology.cisco.vk/* enrollment namespace" $key) -}}
{{- end -}}
{{- end -}}
{{- range $expression := .Values.topology.policy.fleetSelector.matchExpressions -}}
{{- if not (regexMatch `^topology\.cisco\.vk/[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$` $expression.key) -}}
{{- fail (printf "topology.policy.fleetSelector matchExpressions key %q is outside the protected topology.cisco.vk/* enrollment namespace" $expression.key) -}}
{{- end -}}
{{- end -}}
{{- if eq $policyName $ledgerName -}}
{{- fail "topology policy and ledger ConfigMaps must have different names" -}}
{{- end -}}
{{- $required := dict -}}
{{- range $key := .Values.topology.policy.requiredTopologyKeys -}}
{{- $_ := set $required $key true -}}
{{- end -}}
{{- range $key := .Values.topology.policy.projectedTopologyKeys -}}
{{- if not (regexMatch `^(topology\.kubernetes\.io/(region|zone)|topology\.cisco\.vk/[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)$` $key) -}}
{{- fail (printf "topology.policy.projectedTopologyKeys contains %q, which is not a scheduler-visible topology key" $key) -}}
{{- end -}}
{{- if not (hasKey $required $key) -}}
{{- fail (printf "topology.policy.projectedTopologyKeys contains %q, which is not in requiredTopologyKeys" $key) -}}
{{- end -}}
{{- end -}}
{{- $domainKeys := dict -}}
{{- range $key, $_ := .Values.topology.policy.domainMaxConcurrentTransfers -}}
{{- if not (hasKey $required $key) -}}
{{- fail (printf "topology.policy.domainMaxConcurrentTransfers key %q is not in requiredTopologyKeys" $key) -}}
{{- end -}}
{{- $_ := set $domainKeys $key true -}}
{{- end -}}
{{- range $key, $_ := .Values.topology.policy.domainMaxUnavailable -}}
{{- if not (hasKey $required $key) -}}
{{- fail (printf "topology.policy.domainMaxUnavailable key %q is not in requiredTopologyKeys" $key) -}}
{{- end -}}
{{- $_ := set $domainKeys $key true -}}
{{- end -}}
{{- if gt (len $domainKeys) 16 -}}
{{- fail "topology policy combined domain budget maps may contain at most 16 distinct keys" -}}
{{- end -}}
{{- end -}}
{{- end }}
