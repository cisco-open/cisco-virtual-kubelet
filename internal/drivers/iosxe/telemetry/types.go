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

package telemetry

import (
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

const (
	DefaultEventChannelCapacity = 4096

	DropReasonBufferOverflow = "buffer_overflow"
)

// StreamID identifies one live gNMI Subscribe RPC managed by a subscriber.
type StreamID string

// NotificationEvent preserves the raw gNMI notification. Phase 1 consumers log
// it only; later phases can add mapping without changing the stream pump.
type NotificationEvent struct {
	StreamID          StreamID
	SubscriptionNames []string
	Notification      *gpb.Notification
	Path              string
	Updates           int
}

// SubscriptionState is the internal counter set projected into
// IOSXETelemetry.status.observedSubscriptionState.
type SubscriptionState struct {
	Name             string
	StreamID         StreamID
	LastUpdate       *metav1.Time
	MessagesReceived int64
	DroppedEvents    map[string]int64
	Reconnects       int64
	CurrentBackoff   time.Duration
	LastError        string
	Running          bool
	Failed           bool
}

func (s SubscriptionState) ToStatus() configv1alpha1.ObservedSubscriptionState {
	out := configv1alpha1.ObservedSubscriptionState{
		Name:             s.Name,
		StreamID:         string(s.StreamID),
		LastUpdate:       s.LastUpdate,
		MessagesReceived: s.MessagesReceived,
		Reconnects:       s.Reconnects,
		CurrentBackoff:   metav1.Duration{Duration: s.CurrentBackoff},
		LastError:        s.LastError,
	}
	if len(s.DroppedEvents) > 0 {
		out.DroppedEvents = make(map[string]int64, len(s.DroppedEvents))
		for k, v := range s.DroppedEvents {
			out.DroppedEvents[k] = v
		}
	}
	return out
}

func subscriptionEnabled(s configv1alpha1.TelemetrySubscription) bool {
	return s.Enabled == nil || *s.Enabled
}

func subscriptionEncoding(s configv1alpha1.TelemetrySubscription) string {
	if s.Encoding == "" {
		return configv1alpha1.TelemetryEncodingProto
	}
	return s.Encoding
}

func streamMode(s configv1alpha1.TelemetrySubscription) string {
	if s.StreamMode == "" {
		return configv1alpha1.TelemetryStreamModeTargetDef
	}
	return s.StreamMode
}
