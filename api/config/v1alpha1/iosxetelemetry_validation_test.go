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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validTelemetrySpec() IOSXETelemetrySpec {
	return IOSXETelemetrySpec{
		DeviceRef: corev1.LocalObjectReference{Name: "edge-01"},
		Subscriptions: []TelemetrySubscription{{
			Name:       "environmental",
			Paths:      []string{"/Cisco-IOS-XE-environment-oper:environment-sensors"},
			Mode:       TelemetryModeStream,
			StreamMode: TelemetryStreamModeSample,
			Encoding:   TelemetryEncodingProto,
		}},
		Output: OutputConfig{Signal: []string{TelemetrySignalMetrics}},
	}
}

func TestValidateIOSXETelemetrySpecRejectRules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*IOSXETelemetrySpec)
		wantErr string
	}{
		{
			name:    "empty subscriptions",
			mutate:  func(s *IOSXETelemetrySpec) { s.Subscriptions = nil },
			wantErr: "subscriptions",
		},
		{
			name: "mode must be stream",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Subscriptions[0].Mode = "POLL"
			},
			wantErr: "mode",
		},
		{
			name: "encoding enum",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Subscriptions[0].Encoding = "ASCII"
			},
			wantErr: "encoding",
		},
		{
			name: "suppress redundant requires on change",
			mutate: func(s *IOSXETelemetrySpec) {
				v := true
				s.Subscriptions[0].SuppressRedundant = &v
			},
			wantErr: "suppressRedundant requires streamMode=ON_CHANGE",
		},
		{
			name: "heartbeat interval requires on change",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Subscriptions[0].HeartbeatInterval = &metav1.Duration{Duration: 5 * time.Minute}
			},
			wantErr: "heartbeatInterval requires streamMode=ON_CHANGE",
		},
		{
			name: "onExceeded enum",
			mutate: func(s *IOSXETelemetrySpec) {
				s.CardinalityLimits = &CardinalityLimits{
					MaxSeriesPerSubscription: 100,
					OnExceeded:               "evictOldSeries",
				}
			},
			wantErr: "onExceeded",
		},
		{
			name: "max series lower bound",
			mutate: func(s *IOSXETelemetrySpec) {
				s.CardinalityLimits = &CardinalityLimits{MaxSeriesPerSubscription: 0}
			},
			wantErr: "maxSeriesPerSubscription",
		},
		{
			name: "output signal enum",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Output.Signal = []string{"metrics", "profiles"}
			},
			wantErr: "signal",
		},
		{
			name: "transition path required",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Mapping = &MappingConfig{Transitions: []Transition{{
					HealthyValues:   []string{"up"},
					UnhealthyValues: []string{"down"},
				}}}
			},
			wantErr: "path",
		},
		{
			name: "transition healthy values required",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Mapping = &MappingConfig{Transitions: []Transition{{
					Path:            "/interfaces/interface[name=*]/state/oper-status",
					UnhealthyValues: []string{"down"},
				}}}
			},
			wantErr: "healthyValues",
		},
		{
			name: "transition unhealthy values required",
			mutate: func(s *IOSXETelemetrySpec) {
				s.Mapping = &MappingConfig{Transitions: []Transition{{
					Path:          "/interfaces/interface[name=*]/state/oper-status",
					HealthyValues: []string{"up"},
				}}}
			},
			wantErr: "unhealthyValues",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := validTelemetrySpec()
			tc.mutate(&spec)
			errs := ValidateIOSXETelemetrySpec(&spec)
			if len(errs) == 0 {
				t.Fatalf("expected validation error containing %q", tc.wantErr)
			}
			if !strings.Contains(errs.ToAggregate().Error(), tc.wantErr) {
				t.Fatalf("errors=%v, want substring %q", errs.ToAggregate(), tc.wantErr)
			}
		})
	}
}

func TestValidateIOSXETelemetrySpecAcceptsPhase1Shape(t *testing.T) {
	spec := validTelemetrySpec()
	if errs := ValidateIOSXETelemetrySpec(&spec); len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %v", errs.ToAggregate())
	}
}
