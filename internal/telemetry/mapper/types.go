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

package mapper

import (
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

const (
	SignalKindMetric SignalKind = "metrics"
	SignalKindLog    SignalKind = "logs"
	SignalKindTrace  SignalKind = "traces"
	SignalKindDrop   SignalKind = "drop"

	DropReasonCardinalityLimit = "cardinality_limit"
	DropReasonFilter           = "filter"

	SeverityInfo  Severity = "INFO"
	SeverityWarn  Severity = "WARN"
	SeverityError Severity = "ERROR"
)

// SignalKind identifies the OpenTelemetry signal a mapped event is ready for.
type SignalKind string

// Severity carries the OTel log severity text selected by the mapper.
type Severity string

// KeyValue is the mapper's pure-data attribute shape. Emitters translate it
// into signal-specific OpenTelemetry attribute types.
type KeyValue struct {
	Key   string
	Value string
}

// EventContext contains the per-subscription API surface needed to map a raw
// gNMI notification. It intentionally depends only on the public config API so
// this package can be imported by a standalone Collector receiver later.
type EventContext struct {
	Device             string
	Subscription       string
	StreamID           string
	Mapping            *configv1alpha1.MappingConfig
	Output             configv1alpha1.OutputConfig
	CardinalityLimits  *configv1alpha1.CardinalityLimits
	Timestamps         *configv1alpha1.TimestampConfig
	ResourceAttributes map[string]string

	// ReceiveTime is optional test/control input. When zero and collector
	// timestamps are enabled, Process uses time.Now().
	ReceiveTime time.Time
}

// MappedEvent is the structured, side-effect-free output of Mapper.Process.
// Phase 2 emits only SignalKindLog records externally. Metric events are kept
// in this shape for Phase 3 and drop events feed CVK self-metrics.
type MappedEvent struct {
	Signal     SignalKind
	Name       string
	Attributes []KeyValue
	Resource   []KeyValue
	Timestamp  time.Time

	NumberValue *float64
	Body        string
	Severity    Severity

	CanonicalPath string
	SeriesKey     string
	DropReason    string
}

func signalEnabled(output configv1alpha1.OutputConfig, signal SignalKind) bool {
	if len(output.Signal) == 0 {
		return true
	}
	for _, s := range output.Signal {
		if s == string(signal) {
			return true
		}
	}
	return false
}
