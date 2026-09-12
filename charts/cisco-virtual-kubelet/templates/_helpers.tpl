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
{{- $devices := lookup "cisco.vk/v1alpha1" "CiscoDevice" "" "" -}}
{{- $statePresent := false -}}
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
topology.cisco.vk/admission-contract-version: "v1"
{{- end }}

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
{{- fail "topology.enabled=true requires aggregator.enabled=false; managed topology uses one identity-bound worker per CiscoDevice" -}}
{{- end -}}
{{- if not .Values.controller.leaderElect -}}
{{- fail "topology.enabled=true requires controller.leaderElect=true; only the elected manager may reconcile topology and rollout authority" -}}
{{- end -}}
{{- if ne .Values.serviceAccount.vkName "cisco-virtual-kubelet" -}}
{{- fail "topology.enabled=true requires serviceAccount.vkName=cisco-virtual-kubelet; managed workers combine the fixed cisco-virtual-kubelet-managed-worker role with the namespaced cisco-virtual-kubelet-device role" -}}
{{- end -}}
{{- if ne .Values.rbac.profile "strict" -}}
{{- fail "topology.enabled=true requires rbac.profile=strict; managed workers must not inherit disabled write-class operation permissions" -}}
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
{{- $policyName := include "cisco-virtual-kubelet.topologyPolicyName" . -}}
{{- $ledgerName := include "cisco-virtual-kubelet.topologyLedgerName" . -}}
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
