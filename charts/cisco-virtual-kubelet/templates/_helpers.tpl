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
{{- $tag  := .Values.controllerImage.tag        | default .Values.image.tag }}
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
{{- $tag  := .Values.vkImage.tag        | default .Values.image.tag }}
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
