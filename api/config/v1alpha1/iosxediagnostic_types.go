// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IOSXEDiagnosticSpec declares a list of read-only IOS-XE
// operational ("show") commands to execute against the targeted
// device. The config driver invokes Cisco-IA's cli-exec RPC over
// the device's configured transport (RESTCONF or NETCONF; gNMI
// reports the capability missing) and writes the textual output
// back into status.results.
//
// Diagnostics are read-only; nothing in this CR can change device
// configuration. RBAC for IOSXEDiagnostic is therefore safely
// granted to operators who must NOT have IOSXEConfig write access
// (NOC viewers, dashboards, on-call triage).
type IOSXEDiagnosticSpec struct {
	// DeviceRef targets the CiscoDevice the commands run against.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// Commands is the ordered list of show / filesystem-read
	// commands to execute. Per-command failures populate
	// status.results[].err but do NOT abort the batch — operators
	// frequently want a best-effort capture across a list.
	//
	// Destructive commands (clear, reload, write erase) are
	// rejected by the admission webhook; the device-operations
	// RFC scopes a parallel CRD with stricter RBAC for those.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	Commands []string `json:"commands"`

	// Schedule, when set, makes the diagnostic a recurring
	// capture. Absent Schedule, the CR is one-shot — runs once
	// on Create / generation bump and stays in phase Completed
	// until deleted or the spec changes.
	// +optional
	Schedule *DiagnosticSchedule `json:"schedule,omitempty"`

	// NotBefore / NotAfter optionally bound a maintenance window.
	// The reconciler will not invoke commands before NotBefore;
	// after NotAfter the CR moves to phase Expired. Useful when
	// dropping an IOSXEDiagnostic into a GitOps repo whose merge
	// timing is not aligned to the operator's intended capture
	// window.
	// +optional
	NotBefore *metav1.Time `json:"notBefore,omitempty"`
	// +optional
	NotAfter *metav1.Time `json:"notAfter,omitempty"`

	// Retention controls what's preserved on the CR's status when
	// captures accumulate (only meaningful when Schedule is set —
	// without Schedule the CR holds a single capture).
	// +optional
	Retention *DiagnosticRetention `json:"retention,omitempty"`

	// AllowSecrets disables the default secret-redaction filter
	// (which strips lines matching enable-secret / community /
	// RADIUS-key patterns). Most operators leave this false; set
	// to true only when retention is internal and audit
	// requirements demand the unredacted output.
	// +kubebuilder:default=false
	// +optional
	AllowSecrets bool `json:"allowSecrets,omitempty"`

	// OutputSink controls where capture output is stored. Default
	// (nil or Inline=true) writes results into status.results[]
	// inline. The ConfigMap sink is for multi-MB outputs that would
	// otherwise blow past etcd's 1 MB per-object limit (canonical
	// example: `show running-config` on a busy chassis). Diagnostics
	// RFC §3.2 + §11.7 covers the trade-offs.
	// +optional
	OutputSink *DiagnosticOutputSink `json:"outputSink,omitempty"`
}

// DiagnosticOutputSink switches between inline status storage and
// ConfigMap fan-out. Exactly one mode is active at a time —
// admission rejects setting both Inline=true and ConfigMapRef.
type DiagnosticOutputSink struct {
	// Inline (the default when OutputSink is unset or this field
	// is true) stores capture output directly in
	// status.results[].commands[].output.
	// +kubebuilder:default=true
	// +optional
	Inline bool `json:"inline,omitempty"`

	// ConfigMapRef directs the reconciler to write each capture
	// to a fresh ConfigMap. Each capture lands in a distinct
	// ConfigMap whose name is `<NamePrefix><RFC3339-timestamp>`.
	// Status carries `configMapRef.name` per command so consumers
	// can fetch the body via `kubectl get configmap <name> -o
	// jsonpath='{.data.<command>}'`.
	//
	// The reconciler garbage-collects oldest ConfigMaps when their
	// count for this CR exceeds Retention.MaxResults — owner-ref
	// based cleanup, so deletion of the IOSXEDiagnostic CR cascades
	// to every captured ConfigMap.
	// +optional
	ConfigMapRef *DiagnosticConfigMapSink `json:"configMapRef,omitempty"`
}

// DiagnosticConfigMapSink configures the ConfigMap output sink.
type DiagnosticConfigMapSink struct {
	// NamePrefix is prepended to every generated ConfigMap name.
	// The reconciler appends the capture timestamp; the resulting
	// name must be DNS-1123 compliant (lowercase alphanumeric +
	// dashes only). The admission webhook validates the prefix.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=200
	// Pattern allows trailing dash because the reconciler appends a
	// timestamp suffix to form the full ConfigMap name; a trailing
	// dash on the prefix yields a clean separator (e.g.
	// "running-snapshot-" + "20260428-065100" =
	// "running-snapshot-20260428-065100").
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9-])?$`
	NamePrefix string `json:"namePrefix"`

	// Namespace is the namespace the ConfigMaps are created in.
	// Empty defaults to the IOSXEDiagnostic CR's own namespace.
	// Cross-namespace targets need cluster-wide
	// configmaps-create RBAC on the cisco-vk pod's ServiceAccount.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// DiagnosticSchedule expresses a recurring capture cadence. Only one
// of Interval / Cron may be set; admission rejects setting both.
type DiagnosticSchedule struct {
	// Interval is a Go time.Duration string (e.g. "1h", "30s").
	// The reconciler captures, then requeues at this cadence.
	// Minimum enforced by the reconciler is 30 seconds — anything
	// below is rejected to protect the device CPU.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9]+(ns|us|µs|ms|s|m|h)$`
	Interval string `json:"interval,omitempty"`

	// Cron is a 5-field cron expression. Lower-cardinality
	// alternative to Interval for hourly / daily captures.
	// +optional
	Cron string `json:"cron,omitempty"`
}

// DiagnosticRetention bounds the CR's status size and per-command
// output length. Defaults are operator-friendly: 24 captures
// (one day at hourly cadence) and 64 KiB per command.
type DiagnosticRetention struct {
	// MaxResults is the maximum number of capture results retained
	// in status.results[]. The reconciler trims oldest-first when
	// the slice exceeds this bound.
	// +kubebuilder:default=24
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=200
	// +optional
	MaxResults int32 `json:"maxResults,omitempty"`

	// TruncateAt caps individual command outputs to N bytes. Output
	// longer than this is truncated and CommandOutput.Truncated is
	// set true. Default 64 KiB; the etcd 1 MB per-object limit
	// caps the practical aggregate at ~16 outputs of this size.
	// +kubebuilder:default="64KiB"
	// +kubebuilder:validation:Pattern=`^[0-9]+(B|KiB|MiB)$`
	// +optional
	TruncateAt string `json:"truncateAt,omitempty"`
}

// IOSXEDiagnosticStatus reports the most recent capture and (when
// Schedule is set) the rolling history.
type IOSXEDiagnosticStatus struct {
	// Phase summarises the CR's lifecycle state.
	// +kubebuilder:validation:Enum=Pending;Capturing;Completed;Failed;Scheduled;Expired
	// +optional
	Phase string `json:"phase,omitempty"`

	// CommandCount mirrors len(spec.commands) — populated by the
	// reconciler purely so the kubectl printer column "Commands"
	// has a scalar to render. Kubernetes CRD printer JSONPath
	// expressions can't compute array length, so a scalar field
	// is the only path to a populated column.
	// +optional
	CommandCount int32 `json:"commandCount,omitempty"`

	// ObservedGeneration is the .metadata.generation the
	// reconciler last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastCapture is the wall-clock of the most recent successful
	// capture. Used by the scheduler to compute NextCapture.
	// +optional
	LastCapture *metav1.Time `json:"lastCapture,omitempty"`

	// NextCapture, when Schedule is set, is the wall-clock at
	// which the reconciler will fire the next capture.
	// +optional
	NextCapture *metav1.Time `json:"nextCapture,omitempty"`

	// Results is the rolling history of captures. Without
	// Schedule it holds at most one entry; with Schedule it holds
	// up to MaxResults entries, oldest first.
	// +optional
	Results []DiagnosticCapture `json:"results,omitempty"`

	// Conditions reports a Ready signal (True when the most-recent
	// capture succeeded for every command).
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// DiagnosticCapture is one capture's full result list. CapturedAt is
// the wall-clock at which the reconciler invoked the first command
// in the batch; per-command timings are not recorded — operators
// who need them should reduce the batch to one command per CR.
type DiagnosticCapture struct {
	// CapturedAt is the timestamp of the first command in the batch.
	// +kubebuilder:validation:Required
	CapturedAt metav1.Time `json:"capturedAt"`

	// Commands holds one entry per spec.commands input, in the
	// same order. Output is the device's raw text; truncated and
	// redaction flags are surfaced explicitly so consumers can
	// decide whether to chase a fuller body via a follow-up CR.
	Commands []CommandOutput `json:"commands"`

	// TransportError, when set, indicates a transport-level
	// failure (broken dial, auth) that prevented some or all of
	// the batch from running. Per-command failures live in
	// CommandOutput.Err; this field is the "the whole RPC channel
	// fell over" case.
	// +optional
	TransportError string `json:"transportError,omitempty"`
}

// CommandOutput records one show command's text output along with
// flags surfacing any post-processing the reconciler applied.
type CommandOutput struct {
	// Command is the input from spec.commands[i].
	// +kubebuilder:validation:Required
	Command string `json:"command"`

	// Output is the device's raw textual reply (stripped of
	// trailing whitespace and possibly redacted / truncated per
	// spec.allowSecrets and spec.retention.truncateAt).
	// +optional
	Output string `json:"output,omitempty"`

	// Err, when set, carries a per-command failure reason. The
	// other commands in the batch still ran.
	// +optional
	Err string `json:"err,omitempty"`

	// Truncated is true when Output was clipped at TruncateAt.
	// Consumers needing the full body should re-run with a higher
	// retention.truncateAt or use the ConfigMap sink (Phase D).
	// +optional
	Truncated bool `json:"truncated,omitempty"`

	// Redacted is true when the secret-redaction filter dropped
	// at least one line. Operators with elevated audit rights
	// can replay with spec.allowSecrets: true.
	// +optional
	Redacted bool `json:"redacted,omitempty"`

	// ConfigMapRef, when set, points at the ConfigMap holding
	// the full output for this command. Populated only when
	// spec.outputSink.configMapRef is set. The ConfigMap's
	// data["<sanitised command name>"] holds the text. The
	// reconciler owns lifecycle: the CR is the OwnerReference,
	// so deleting the CR cascades to every captured ConfigMap.
	// +optional
	ConfigMapRef *CapturedConfigMapRef `json:"configMapRef,omitempty"`
}

// CapturedConfigMapRef pins one ConfigMap that holds a single
// command's full output. Mirrors corev1.LocalObjectReference but
// includes Namespace because the sink may target a different
// namespace than the IOSXEDiagnostic CR's own.
type CapturedConfigMapRef struct {
	// Name of the ConfigMap.
	Name string `json:"name"`
	// Namespace of the ConfigMap (empty == same as the CR).
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// Key inside ConfigMap.data that holds this command's output.
	Key string `json:"key"`
}

// IOSXEDiagnostic is the per-device diagnostic-capture CR. Multiple
// IOSXEDiagnostics may target the same device — they each capture
// independently and never lease (no mutation, no contention).
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxediag
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Commands",type=integer,JSONPath=`.status.commandCount`,description="count of input commands"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule.interval`,description="capture cadence; empty for one-shot CRs"
// +kubebuilder:printcolumn:name="Next",type=string,JSONPath=`.status.nextCapture`,description="next scheduled capture (RFC3339; empty for one-shot CRs). string-typed because kubectl's date renderer treats future timestamps as <invalid>."
// +kubebuilder:printcolumn:name="Last",type=date,JSONPath=`.status.lastCapture`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEDiagnostic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEDiagnosticSpec   `json:"spec"`
	Status IOSXEDiagnosticStatus `json:"status,omitempty"`
}

// IOSXEDiagnosticList is the list type for IOSXEDiagnostic.
//
// +kubebuilder:object:root=true
type IOSXEDiagnosticList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEDiagnostic `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEDiagnostic{}, &IOSXEDiagnosticList{})
}
