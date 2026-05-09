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

// IOSXETelemetrySpec declares MDT-over-gNMI subscriptions for one CiscoDevice.
// The subscriber maps telemetry notifications to OpenTelemetry logs and
// metrics; trace emission is deferred.
type IOSXETelemetrySpec struct {
	// DeviceRef targets the CiscoDevice this telemetry stream applies to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="deviceRef.name must be non-empty"
	DeviceRef corev1.LocalObjectReference `json:"deviceRef"`

	// Subscriptions is the non-empty set of gNMI Subscribe requests to run.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Subscriptions []TelemetrySubscription `json:"subscriptions"`

	// Reconnect controls retry/backoff after a Subscribe RPC fails.
	// +optional
	Reconnect *ReconnectConfig `json:"reconnect,omitempty"`

	// CardinalityLimits bounds mapper output.
	// +optional
	CardinalityLimits *CardinalityLimits `json:"cardinalityLimits,omitempty"`

	// Timestamps controls whether collector receive time is used in the mapping
	// pipeline.
	// +optional
	Timestamps *TimestampConfig `json:"timestamps,omitempty"`

	// Mapping carries path-to-signal mapping, filtering, and classification
	// controls.
	// +optional
	Mapping *MappingConfig `json:"mapping,omitempty"`

	// Output declares the desired signal families.
	Output OutputConfig `json:"output"`
}

// TelemetrySubscription is one logical subscription entry. The reconciler
// multiplexes compatible entries into shared gNMI Subscribe RPCs per device.
// +kubebuilder:validation:XValidation:rule="!(has(self.suppressRedundant) && self.suppressRedundant && self.streamMode != 'ON_CHANGE')",message="suppressRedundant requires streamMode=ON_CHANGE"
// +kubebuilder:validation:XValidation:rule="!(has(self.heartbeatInterval) && self.streamMode != 'ON_CHANGE')",message="heartbeatInterval requires streamMode=ON_CHANGE"
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
	// +kubebuilder:validation:MaxItems=256
	// +listType=set
	Paths []string `json:"paths"`

	// Origin overrides gNMI Path.Origin for all paths in this subscription.
	// +optional
	Origin string `json:"origin,omitempty"`

	// PreservePathPrefix keeps the YANG module prefix on the first PathElem
	// name instead of lifting it into Path.Origin. Set true for IOS-XE native
	// YANG paths (Cisco-IOS-XE-*-oper / Cisco-IOS-XE-*-cfg) — IOS-XE gnxi
	// rejects those module names as Path.Origin and expects the prefix to
	// stay on the element (RFC 7951 module-qualified form). Leave unset
	// (default false) for OpenConfig paths where the openconfig-* prefix
	// canonically lives in Path.Origin.
	// +optional
	PreservePathPrefix *bool `json:"preservePathPrefix,omitempty"`

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

// CardinalityLimits bounds mapper output.
type CardinalityLimits struct {
	// MaxSeriesPerSubscription is the per-subscription series cap.
	// +kubebuilder:default=10000
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxSeriesPerSubscription int32 `json:"maxSeriesPerSubscription,omitempty"`

	// MaxInstruments caps the number of distinct OTel metric instruments
	// (gauge + sum, keyed by name) the emitter will register before refusing
	// new instruments. Once reached, additional metric points whose name has
	// not been seen are dropped and counted in the cap-drop self-metric.
	// Operators raise this on chassis with broad subscription paths
	// (BGP-per-neighbor, interfaces-per-port).
	// +kubebuilder:default=1024
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxInstruments int32 `json:"maxInstruments,omitempty"`

	// OnExceeded is restricted to dropNewSeries in v1alpha1.
	// +kubebuilder:default=dropNewSeries
	// +kubebuilder:validation:Enum=dropNewSeries
	// +optional
	OnExceeded string `json:"onExceeded,omitempty"`
}

// TimestampConfig controls timestamp source selection for mapping.
type TimestampConfig struct {
	// UseCollectorTimestamp defaults true in Phase 1.
	// +kubebuilder:default=true
	// +optional
	UseCollectorTimestamp *bool `json:"useCollectorTimestamp,omitempty"`
}

// MappingConfig carries mapper controls for aliases, metric type overrides,
// resource attributes, and filters. Hard list caps prevent CRDs from
// growing unboundedly large; raise via per-CR review if your fleet's
// canonical-path surface justifies it.
type MappingConfig struct {
	// IncludeListKeysInMetricName preserves legacy metric naming that embeds
	// YANG list-key selectors in metric names. It defaults false so list-key
	// values are emitted as labels only, avoiding one instrument per entity.
	// +optional
	IncludeListKeysInMetricName *bool `json:"includeListKeysInMetricName,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=512
	PathAliases []PathAlias `json:"pathAliases,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=512
	MetricTypeOverrides []MetricTypeOverride `json:"metricTypeOverrides,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=256
	Transitions []Transition `json:"transitions,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=512
	ResourceAttributes []ResourceAttribute `json:"resourceAttributes,omitempty"`
	// +optional
	Filter *FilterConfig `json:"filter,omitempty"`
}

// Transition declares a watched state leaf and the values that bound an
// unhealthy interval.
type Transition struct {
	// Path is the canonical telemetry path to watch.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// HealthyValues are leaf values that close an unhealthy interval.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	HealthyValues []string `json:"healthyValues"`

	// UnhealthyValues are leaf values that open an unhealthy interval.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	UnhealthyValues []string `json:"unhealthyValues"`
}

// FilterConfig carries allow/deny rules for mapper stages.
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

// PathAlias renames mapped telemetry paths by prefix.
type PathAlias struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Prefix string `json:"prefix"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Rename string `json:"rename"`
}

// MetricTypeOverride pins a mapped metric type for a path prefix.
type MetricTypeOverride struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Prefix string `json:"prefix"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=gauge;sum
	Type string `json:"type"`
}

// ResourceAttribute maps a telemetry path to a resource attribute key.
type ResourceAttribute struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
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
// counters surfaced by the stream manager and emitters.
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

	// LogRecordsEmitted is the count of OTel LogRecords produced by the
	// mapper-and-logs-emitter pipeline for this subscription. Phase 2.
	// +optional
	LogRecordsEmitted int64 `json:"logRecordsEmitted,omitempty"`

	// MetricPointsEmitted is the count of OTel metric points produced by the
	// mapper-and-metrics-emitter pipeline for this subscription. Phase 3.
	// +optional
	MetricPointsEmitted int64 `json:"metricPointsEmitted,omitempty"`

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
