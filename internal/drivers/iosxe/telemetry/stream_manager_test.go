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
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestStreamManagerReusesMatchingBucketAndRestartsChangedBucket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewStreamManager(ctx, nil, StreamManagerOptions{
		Reconnect: &configv1alpha1.ReconnectConfig{
			InitialBackoff: metav1.Duration{Duration: time.Millisecond},
			MaxBackoff:     metav1.Duration{Duration: time.Millisecond},
			MaxRetries:     1,
		},
	})
	defer m.Stop()

	spec := streamManagerTestSubscription(time.Second)
	if err := m.UpsertSubscription(spec); err != nil {
		t.Fatalf("first UpsertSubscription: %v", err)
	}
	first := m.streams[bucketFor(spec)]
	if first == nil {
		t.Fatal("first stream handle is nil")
	}
	firstGeneration := m.generation

	if err := m.UpsertSubscription(spec); err != nil {
		t.Fatalf("second UpsertSubscription: %v", err)
	}
	second := m.streams[bucketFor(spec)]
	if second != first {
		t.Fatalf("stream handle was replaced for identical spec: first=%p second=%p", first, second)
	}
	if m.generation != firstGeneration {
		t.Fatalf("generation=%d, want unchanged %d", m.generation, firstGeneration)
	}
	select {
	case <-first.ctx.Done():
		t.Fatal("reused stream context was cancelled")
	default:
	}

	changed := streamManagerTestSubscription(2 * time.Second)
	if err := m.UpsertSubscription(changed); err != nil {
		t.Fatalf("changed UpsertSubscription: %v", err)
	}
	restarted := m.streams[bucketFor(changed)]
	if restarted == nil {
		t.Fatal("restarted stream handle is nil")
	}
	if restarted == first {
		t.Fatal("changed sample interval reused the old stream handle")
	}
	if m.generation != firstGeneration+1 {
		t.Fatalf("generation=%d, want %d", m.generation, firstGeneration+1)
	}
	select {
	case <-first.ctx.Done():
	default:
		t.Fatal("old stream context was not cancelled")
	}
	select {
	case <-restarted.ctx.Done():
		t.Fatal("new stream context was unexpectedly cancelled")
	default:
	}
}

func TestParsePathOrigin(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		origin     string
		wantOrigin string
		wantElems  []string
		wantKeys   []map[string]string
	}{
		{
			name:       "iosxe module prefix",
			path:       "/Cisco-IOS-XE-environment-oper:environment-sensors/sensor",
			wantOrigin: "Cisco-IOS-XE-environment-oper",
			wantElems:  []string{"environment-sensors", "sensor"},
		},
		{
			name:       "openconfig module prefix with slash in key",
			path:       "/openconfig-interfaces:interfaces/interface[name=GigabitEthernet1/0/1]",
			wantOrigin: "openconfig-interfaces",
			wantElems:  []string{"interfaces", "interface"},
			wantKeys: []map[string]string{
				nil,
				{"name": "GigabitEthernet1/0/1"},
			},
		},
		{
			name:       "no module prefix",
			path:       "/interfaces/interface/state/counters",
			wantOrigin: "",
			wantElems:  []string{"interfaces", "interface", "state", "counters"},
		},
		{
			name:       "explicit origin override",
			path:       "/interfaces/interface/state/counters",
			origin:     "openconfig",
			wantOrigin: "openconfig",
			wantElems:  []string{"interfaces", "interface", "state", "counters"},
		},
		{
			name:       "subsequent prefix preserved",
			path:       "/root/other-module:child",
			wantOrigin: "",
			wantElems:  []string{"root", "other-module:child"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePath(tc.path, tc.origin)
			if err != nil {
				t.Fatalf("parsePath: %v", err)
			}
			if got.GetOrigin() != tc.wantOrigin {
				t.Fatalf("origin=%q, want %q", got.GetOrigin(), tc.wantOrigin)
			}
			if len(got.GetElem()) != len(tc.wantElems) {
				t.Fatalf("elem count=%d, want %d: %+v", len(got.GetElem()), len(tc.wantElems), got.GetElem())
			}
			for i, want := range tc.wantElems {
				if got.GetElem()[i].GetName() != want {
					t.Fatalf("elem[%d]=%q, want %q", i, got.GetElem()[i].GetName(), want)
				}
				if i < len(tc.wantKeys) && len(tc.wantKeys[i]) > 0 {
					for key, value := range tc.wantKeys[i] {
						if got.GetElem()[i].GetKey()[key] != value {
							t.Fatalf("elem[%d].key[%q]=%q, want %q", i, key, got.GetElem()[i].GetKey()[key], value)
						}
					}
				}
			}
		})
	}
}

func TestStreamHandleUsesSubscriptionOriginOverride(t *testing.T) {
	spec := streamManagerTestSubscription(time.Second)
	spec.Origin = "openconfig"
	spec.Paths = []string{"/interfaces/interface/state/counters"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newStreamHandle(ctx, bucketFor(spec), []configv1alpha1.TelemetrySubscription{spec}, "test", cancel)
	if len(h.subscriptions) != 1 {
		t.Fatalf("subscription count=%d, want 1", len(h.subscriptions))
	}
	if got := h.subscriptions[0].GetPath().GetOrigin(); got != "openconfig" {
		t.Fatalf("path origin=%q, want openconfig", got)
	}
}

func streamManagerTestSubscription(sampleInterval time.Duration) configv1alpha1.TelemetrySubscription {
	return configv1alpha1.TelemetrySubscription{
		Name:           "environmental",
		Paths:          []string{"/Cisco-IOS-XE-environment-oper:environment-sensors/sensor[name=temperature]"},
		Mode:           configv1alpha1.TelemetryModeStream,
		StreamMode:     configv1alpha1.TelemetryStreamModeSample,
		SampleInterval: metav1.Duration{Duration: sampleInterval},
		Encoding:       configv1alpha1.TelemetryEncodingProto,
	}
}
