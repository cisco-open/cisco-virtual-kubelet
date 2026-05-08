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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	IOSXETelemetryPhasePending   = "Pending"
	IOSXETelemetryPhaseStreaming = "Streaming"
	IOSXETelemetryPhaseDegraded  = "Degraded"
	IOSXETelemetryPhaseFailed    = "Failed"

	TelemetryModeStream          = "STREAM"
	TelemetryStreamModeSample    = "SAMPLE"
	TelemetryStreamModeOnChange  = "ON_CHANGE"
	TelemetryStreamModeTargetDef = "TARGET_DEFINED"

	TelemetryEncodingProto    = "PROTO"
	TelemetryEncodingJSONIETF = "JSON_IETF"

	TelemetryOnExceededDropNewSeries = "dropNewSeries"

	TelemetrySignalMetrics = "metrics"
	TelemetrySignalLogs    = "logs"
	TelemetrySignalTraces  = "traces"
)

// IOSXETelemetrySpec declares MDT-over-gNMI subscriptions for one
// CiscoDevice. Phase 1 opens and drains gNMI Subscribe streams and reports
// lifecycle/status; metric/log/trace emission is intentionally deferred.
type IOSXETelemetrySpec struct {
	// DeviceRef targets the CiscoDevice this telemetry stream applies to.
	// +kubebuilder:validation:Required
	DeviceRef corev1.LocalObjectReference `json:"deviceRef"`

	// Subscriptions is the non-empty set of gNMI Subscribe requests to run.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Subscriptions []TelemetrySubscription `json:"subscriptions"`

	// Reconnect controls retry/backoff after a Subscribe RPC fails.
	// +optional
	Reconnect *ReconnectConfig `json:"reconnect,omitempty"`

	// CardinalityLimits bounds future mapper output. Present in Phase 1 for
	// API compatibility; the Phase 1 reconciler does not map series.
	// +optional
	CardinalityLimits *CardinalityLimits `json:"cardinalityLimits,omitempty"`

	// Timestamps controls whether collector receive time is used later in the
	// mapping pipeline. Phase 1 records the API field only.
	// +optional
	Timestamps *TimestampConfig `json:"timestamps,omitempty"`

	// Mapping carries future path-to-signal mapping controls. Phase 1 stores
	// the struct but does not execute mapping, filtering, or classification.
	// +optional
	Mapping *MappingConfig `json:"mapping,omitempty"`

	// Output declares the desired signal families. Phase 1 validates and stores
	// the setting only; emission is deferred to later phases.
	Output OutputConfig `json:"output"`
}

// TelemetrySubscription is one logical subscription entry. The reconciler
// multiplexes compatible entries into shared gNMI Subscribe RPCs per device.
type TelemetrySubscription struct {
	// Name is a DNS-1123-ish stable identifier used in status.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Enabled defaults true. Disabled entries are retained in spec but not
	// opened on the device.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Paths is the non-empty set of gNMI paths to subscribe to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths"`

	// Mode is restricted to STREAM in v1alpha1.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=STREAM
	Mode string `json:"mode"`

	// StreamMode controls the gNMI subscription mode per path.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=SAMPLE;ON_CHANGE;TARGET_DEFINED
	StreamMode string `json:"streamMode"`

	// SampleInterval is used for SAMPLE subscriptions.
	// +optional
	SampleInterval metav1.Duration `json:"sampleInterval,omitempty"`

	// HeartbeatInterval requests periodic target heartbeats when supported.
	// +optional
	HeartbeatInterval *metav1.Duration `json:"heartbeatInterval,omitempty"`

	// SuppressRedundant maps to gNMI suppress_redundant.
	// +optional
	SuppressRedundant *bool `json:"suppressRedundant,omitempty"`

	// Encoding selects the gNMI Subscribe response encoding.
	// +kubebuilder:default=PROTO
	// +kubebuilder:validation:Enum=PROTO;JSON_IETF
	// +optional
	Encoding string `json:"encoding,omitempty"`
}

// ReconnectConfig controls exponential reconnect behaviour.
type ReconnectConfig struct {
	// InitialBackoff is the first retry delay.
	// +kubebuilder:default="1s"
	// +optional
	InitialBackoff metav1.Duration `json:"initialBackoff,omitempty"`

	// MaxBackoff caps retry delay growth.
	// +kubebuilder:default="30s"
	// +optional
	MaxBackoff metav1.Duration `json:"maxBackoff,omitempty"`

	// MaxRetries caps consecutive retries. Zero means retry forever.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	// +optional
	MaxRetries int32 `json:"maxRetries,omitempty"`
}

// CardinalityLimits bounds future mapper output. Phase 1 records and
// validates the values but does not create time series.
type CardinalityLimits struct {
	// MaxSeriesPerSubscription is the per-subscription series cap.
	// +kubebuilder:default=10000
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxSeriesPerSubscription int32 `json:"maxSeriesPerSubscription,omitempty"`

	// OnExceeded is restricted to dropNewSeries in v1alpha1.
	// +kubebuilder:default=dropNewSeries
	// +kubebuilder:validation:Enum=dropNewSeries
	// +optional
	OnExceeded string `json:"onExceeded,omitempty"`
}

// TimestampConfig controls timestamp source selection for future mapping.
type TimestampConfig struct {
	// UseCollectorTimestamp defaults true in Phase 1.
	// +kubebuilder:default=true
	// +optional
	UseCollectorTimestamp *bool `json:"useCollectorTimestamp,omitempty"`
}

// MappingConfig is intentionally unused by the Phase 1 reconciler. It is
// present so users can author manifests against the stable v1alpha1 shape.
type MappingConfig struct {
	// +optional
	PathAliases []PathAlias `json:"pathAliases,omitempty"`
	// +optional
	MetricTypeOverrides []MetricTypeOverride `json:"metricTypeOverrides,omitempty"`
	// +optional
	ResourceAttributes []ResourceAttribute `json:"resourceAttributes,omitempty"`
	// +optional
	Filter *FilterConfig `json:"filter,omitempty"`
}

// FilterConfig carries allow/deny rules for future mapper stages.
type FilterConfig struct {
	// +optional
	WirePath *FilterRules `json:"wirePath,omitempty"`
	// +optional
	MetricName *FilterRules `json:"metricName,omitempty"`
}

// FilterRules is an ordered allow/deny rule set.
type FilterRules struct {
	// +optional
	Allow []string `json:"allow,omitempty"`
	// +optional
	Deny []string `json:"deny,omitempty"`
}

// PathAlias renames future mapped metric paths by prefix.
type PathAlias struct {
	Prefix string `json:"prefix,omitempty"`
	Rename string `json:"rename,omitempty"`
}

// MetricTypeOverride pins a future mapped metric type for a path prefix.
type MetricTypeOverride struct {
	Prefix string `json:"prefix,omitempty"`

	// +kubebuilder:validation:Enum=gauge;sum
	Type string `json:"type,omitempty"`
}

// ResourceAttribute maps a telemetry path to a resource attribute key.
type ResourceAttribute struct {
	Path string `json:"path,omitempty"`
	Key  string `json:"key,omitempty"`
}

// OutputConfig declares the requested OpenTelemetry signal families.
type OutputConfig struct {
	// Signal is a subset of metrics, logs, and traces.
	// +optional
	// +listType=set
	// +kubebuilder:validation:items:Enum=metrics;logs;traces
	Signal []string `json:"signal,omitempty"`
}

// IOSXETelemetryStatus reports subscriber lifecycle and per-subscription
// counters surfaced by the Phase 1 stream manager.
type IOSXETelemetryStatus struct {
	// Phase summarises the CR's lifecycle state.
	// +kubebuilder:validation:Enum=Pending;Streaming;Degraded;Failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedSubscriptionState carries per-subscription stream counters.
	// +optional
	// +listType=map
	// +listMapKey=name
	ObservedSubscriptionState []ObservedSubscriptionState `json:"observedSubscriptionState,omitempty"`
}

// ObservedSubscriptionState is the status projection for one subscription.
type ObservedSubscriptionState struct {
	Name string `json:"name"`

	// +optional
	StreamID string `json:"streamID,omitempty"`

	// +optional
	LastUpdate *metav1.Time `json:"lastUpdate,omitempty"`

	// +optional
	MessagesReceived int64 `json:"messagesReceived,omitempty"`

	// DroppedEvents is keyed by reason, for example buffer_overflow.
	// +optional
	DroppedEvents map[string]int64 `json:"droppedEvents,omitempty"`

	// +optional
	Reconnects int64 `json:"reconnects,omitempty"`

	// +optional
	CurrentBackoff metav1.Duration `json:"currentBackoff,omitempty"`

	// +optional
	LastError string `json:"lastError,omitempty"`
}

// IOSXETelemetry is the Schema for the iosxetelemetries API.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxetel
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXETelemetry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXETelemetrySpec   `json:"spec"`
	Status IOSXETelemetryStatus `json:"status,omitempty"`
}

// IOSXETelemetryList is the list type for IOSXETelemetry.
//
// +kubebuilder:object:root=true
type IOSXETelemetryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXETelemetry `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXETelemetry{}, &IOSXETelemetryList{})
}
