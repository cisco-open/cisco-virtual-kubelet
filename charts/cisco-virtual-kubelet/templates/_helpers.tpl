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
