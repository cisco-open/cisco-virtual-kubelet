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

// IOSXEConfigApplyLogSpec opts a device into apply-time auditing.
// Once an operator creates a log CR for a device, the
// ConfigReconciler appends one entry per completed reconcile —
// success, drift, or failure. The Status.Entries slice is a
// circular buffer trimmed to MaxEntries oldest-first.
//
// The log is namespaced and named to match its device — by
// convention the log lives in the same namespace as the
// IOSXEConfig CRs that target the device. A device with no log
// CR is silently skipped; auditing is opt-in.
type IOSXEConfigApplyLogSpec struct {
	// DeviceRef names the CiscoDevice this log belongs to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// MaxEntries caps Status.Entries[]. Older entries are dropped
	// in FIFO order when the cap is hit. The default keeps the CR
	// well under Kubernetes' soft 1.5 MiB per-object limit even
	// for chatty devices.
	// +kubebuilder:default=50
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=500
	// +optional
	MaxEntries int32 `json:"maxEntries,omitempty"`
}

// IOSXEConfigApplyLogStatus carries the audit trail.
type IOSXEConfigApplyLogStatus struct {
	// Entries is the chronological list of recent applies; index 0
	// is the oldest retained, last index is the most recent. Capped
	// to spec.maxEntries.
	// +optional
	Entries []ApplyLogEntry `json:"entries,omitempty"`

	// OldestRetainedAt records the timestamp of Entries[0]. Set
	// alongside Entries on every update so an operator can read the
	// retention window without scanning the slice.
	// +optional
	OldestRetainedAt *metav1.Time `json:"oldestRetainedAt,omitempty"`

	// TruncatedTotal counts entries dropped due to the MaxEntries
	// cap since the CR was created. Operators alert on rapid growth
	// — it indicates a chatty device or an under-sized cap.
	// +optional
	TruncatedTotal int64 `json:"truncatedTotal,omitempty"`

	// Conditions follows the standard Kubernetes shape.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ApplyLogEntry is one row of the audit log. Mirrors the engine
// Result fields the reconciler already produces, in compact form
// so the slice stays etcd-friendly.
type ApplyLogEntry struct {
	// Time is the wall-clock at which the engine returned. Set by
	// the controller, not the device.
	// +kubebuilder:validation:Required
	Time metav1.Time `json:"time"`

	// Phase is the engine's terminal phase (InSync | Drifted |
	// Failed | Paused).
	// +kubebuilder:validation:Required
	Phase string `json:"phase"`

	// Hash is the canonical-intent hash the apply landed on. Empty
	// when Phase != InSync (a failed apply doesn't pin a new
	// intent on the device).
	// +optional
	Hash string `json:"hash,omitempty"`

	// SourceCR is the IOSXEConfig CR that drove this reconcile,
	// expressed as namespace/name@generation so an operator can
	// `kubectl get -n <ns> iosxeconfig <name>` and inspect the
	// current shape.
	// +kubebuilder:validation:Required
	SourceCR string `json:"sourceCR"`

	// Families summarises per-family outcomes.
	// +optional
	Families []FamilyApplyOutcome `json:"families,omitempty"`

	// Message carries the engine's top-line failure message when
	// Phase != InSync. Empty on the happy path.
	// +optional
	Message string `json:"message,omitempty"`
}

// FamilyApplyOutcome compresses an engine.FamilyStatus down to the
// fields auditors actually inspect.
type FamilyApplyOutcome struct {
	// Family name (must match IOSXEConfig.spec.managedFamilies).
	// +kubebuilder:validation:Required
	Family string `json:"family"`

	// State is the per-family terminal state.
	// +kubebuilder:validation:Required
	State string `json:"state"`

	// OpCount is the number of transport ops the writer emitted.
	// +optional
	OpCount int32 `json:"opCount,omitempty"`
}

// IOSXEConfigApplyLog is the per-device audit-log CR. Operators
// create one per device they want auditing for; the log is opt-in
// to keep the controller's blast radius narrow.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxelog
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Entries",type=integer,JSONPath=`.status.entries[*].time`,description="recent entries count"
// +kubebuilder:printcolumn:name="Truncated",type=integer,JSONPath=`.status.truncatedTotal`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEConfigApplyLog struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEConfigApplyLogSpec   `json:"spec"`
	Status IOSXEConfigApplyLogStatus `json:"status,omitempty"`
}

// IOSXEConfigApplyLogList is the list type for IOSXEConfigApplyLog.
//
// +kubebuilder:object:root=true
type IOSXEConfigApplyLogList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEConfigApplyLog `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEConfigApplyLog{}, &IOSXEConfigApplyLogList{})
}
