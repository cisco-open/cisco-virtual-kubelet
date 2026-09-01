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
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func validNetworkControllerSpec() NetworkControllerSpec {
	return NetworkControllerSpec{
		Type:                "future-controller",
		Endpoint:            "https://controller.example.test/api",
		CredentialSecretRef: NetworkControllerSecretReference{Name: "controller-credentials"},
		IntentSecretSources: []NetworkControllerIntentSecretSource{
			{Alias: "inventory-password", Name: "inventory-credentials", Key: "password"},
			{Alias: "radius-secret", Name: "network-secrets", Key: "radius.shared-secret"},
		},
		Connection: NetworkControllerConnectionPolicy{
			RequestTimeout:        networkControllerDurationPtr(30 * time.Second),
			HealthCheckInterval:   networkControllerDurationPtr(time.Minute),
			MaxConcurrentRequests: 4,
		},
	}
}

func networkControllerDurationPtr(duration time.Duration) *metav1.Duration {
	return &metav1.Duration{Duration: duration}
}

func TestValidateNetworkControllerSpecAllowsUnregisteredWellFormedType(t *testing.T) {
	spec := validNetworkControllerSpec()
	if errs := ValidateNetworkControllerSpec(&spec); len(errs) != 0 {
		t.Fatalf("unexpected errors for extensible controller type: %v", errs.ToAggregate())
	}
}

func TestValidateNetworkControllerSpecAllowsOmittedDurations(t *testing.T) {
	spec := validNetworkControllerSpec()
	spec.Connection.RequestTimeout = nil
	spec.Connection.HealthCheckInterval = nil
	if errs := ValidateNetworkControllerSpec(&spec); len(errs) != 0 {
		t.Fatalf("omitted durations should be defaultable: %v", errs.ToAggregate())
	}
}

func TestValidateNetworkControllerSpecAllowsDurationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name                string
		requestTimeout      time.Duration
		healthCheckInterval time.Duration
	}{
		{name: "lower bounds", requestTimeout: time.Nanosecond, healthCheckInterval: 30 * time.Second},
		{name: "upper bounds", requestTimeout: 24 * time.Hour, healthCheckInterval: 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := validNetworkControllerSpec()
			spec.Connection.RequestTimeout = networkControllerDurationPtr(test.requestTimeout)
			spec.Connection.HealthCheckInterval = networkControllerDurationPtr(test.healthCheckInterval)
			if errs := ValidateNetworkControllerSpec(&spec); len(errs) != 0 {
				t.Fatalf("valid duration boundaries rejected: %v", errs.ToAggregate())
			}
		})
	}
}

func TestValidateNetworkControllerSpecRejectsUnsafeConnectionShapes(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*NetworkControllerSpec)
		wantErr string
	}{
		{
			name:    "invalid type",
			mutate:  func(spec *NetworkControllerSpec) { spec.Type = "Catalyst_Center" },
			wantErr: "type",
		},
		{
			name:    "HTTP endpoint",
			mutate:  func(spec *NetworkControllerSpec) { spec.Endpoint = "http://controller.example.test" },
			wantErr: "HTTPS",
		},
		{
			name:    "endpoint without hostname",
			mutate:  func(spec *NetworkControllerSpec) { spec.Endpoint = "https://:443" },
			wantErr: "HTTPS",
		},
		{
			name:    "URL credentials",
			mutate:  func(spec *NetworkControllerSpec) { spec.Endpoint = "https://admin:secret@controller.example.test" },
			wantErr: "userinfo",
		},
		{
			name:    "missing credential Secret",
			mutate:  func(spec *NetworkControllerSpec) { spec.CredentialSecretRef.Name = "" },
			wantErr: "credential Secret",
		},
		{
			name: "invalid intent Secret alias",
			mutate: func(spec *NetworkControllerSpec) {
				spec.IntentSecretSources[0].Alias = "Inventory_Password"
			},
			wantErr: "DNS-label-style alias",
		},
		{
			name: "duplicate intent Secret alias",
			mutate: func(spec *NetworkControllerSpec) {
				spec.IntentSecretSources[1].Alias = spec.IntentSecretSources[0].Alias
			},
			wantErr: "Duplicate value",
		},
		{
			name: "invalid intent Secret name",
			mutate: func(spec *NetworkControllerSpec) {
				spec.IntentSecretSources[0].Name = "Other_Namespace/secret"
			},
			wantErr: "name",
		},
		{
			name: "missing intent Secret name",
			mutate: func(spec *NetworkControllerSpec) {
				spec.IntentSecretSources[0].Name = ""
			},
			wantErr: "Secret name",
		},
		{
			name: "invalid intent Secret key",
			mutate: func(spec *NetworkControllerSpec) {
				spec.IntentSecretSources[0].Key = "bad/key"
			},
			wantErr: "key",
		},
		{
			name: "missing intent Secret key",
			mutate: func(spec *NetworkControllerSpec) {
				spec.IntentSecretSources[0].Key = ""
			},
			wantErr: "key",
		},
		{
			name: "CA and insecure TLS conflict",
			mutate: func(spec *NetworkControllerSpec) {
				spec.TLS = &NetworkControllerTLSConfig{
					CAConfigMapRef:     &NetworkControllerConfigMapKeyReference{Name: "controller-ca", Key: "ca.crt"},
					InsecureSkipVerify: true,
				}
			},
			wantErr: "cannot be combined",
		},
		{
			name: "zero request timeout",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.RequestTimeout = networkControllerDurationPtr(0)
			},
			wantErr: "greater than 0s",
		},
		{
			name: "negative request timeout",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.RequestTimeout = networkControllerDurationPtr(-time.Second)
			},
			wantErr: "greater than 0s",
		},
		{
			name: "request timeout above maximum",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.RequestTimeout = networkControllerDurationPtr(24*time.Hour + time.Nanosecond)
			},
			wantErr: "at most 24h",
		},
		{
			name: "zero health check interval",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.HealthCheckInterval = networkControllerDurationPtr(0)
			},
			wantErr: "at least 30s",
		},
		{
			name: "negative health check interval",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.HealthCheckInterval = networkControllerDurationPtr(-time.Second)
			},
			wantErr: "at least 30s",
		},
		{
			name: "health checks too frequent",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.HealthCheckInterval.Duration = 5 * time.Second
			},
			wantErr: "at least 30s",
		},
		{
			name: "health check interval above maximum",
			mutate: func(spec *NetworkControllerSpec) {
				spec.Connection.HealthCheckInterval = networkControllerDurationPtr(24*time.Hour + time.Nanosecond)
			},
			wantErr: "at most 24h",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validNetworkControllerSpec()
			test.mutate(&spec)
			errs := ValidateNetworkControllerSpec(&spec)
			if len(errs) == 0 {
				t.Fatalf("expected error containing %q", test.wantErr)
			}
			if got := errs.ToAggregate().Error(); !strings.Contains(got, test.wantErr) {
				t.Fatalf("errors=%q, want substring %q", got, test.wantErr)
			}
		})
	}
}

func TestNetworkControllerSchemeAndDeepCopy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, kind := range []string{"NetworkController", "NetworkControllerList"} {
		if _, err := scheme.New(GroupVersion.WithKind(kind)); err != nil {
			t.Fatalf("scheme.New(%s): %v", kind, err)
		}
	}

	original := &NetworkController{
		Spec: NetworkControllerSpec{
			Connection: NetworkControllerConnectionPolicy{
				RequestTimeout:      networkControllerDurationPtr(30 * time.Second),
				HealthCheckInterval: networkControllerDurationPtr(time.Minute),
			},
			TLS: &NetworkControllerTLSConfig{
				CAConfigMapRef: &NetworkControllerConfigMapKeyReference{Name: "ca", Key: "ca.pem"},
			},
			IntentSecretSources: []NetworkControllerIntentSecretSource{{
				Alias: "inventory-password", Name: "intent-secrets", Key: "password",
			}},
		},
		Status: NetworkControllerStatus{
			Capabilities: []NetworkControllerCapabilityStatus{{Name: "config", Supported: true}},
			NetAsCode: &NetworkControllerNetAsCodeStatus{
				ModelVersions: []string{"1.0"},
				Sections:      []string{"sites"},
			},
			Worker:     &NetworkControllerWorkerStatus{Name: "worker", DeploymentName: "worker"},
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
		},
	}
	copy := original.DeepCopy()
	copy.Spec.Connection.RequestTimeout.Duration = time.Minute
	copy.Spec.Connection.HealthCheckInterval.Duration = 2 * time.Minute
	copy.Spec.TLS.CAConfigMapRef.Name = "other-ca"
	copy.Spec.IntentSecretSources[0].Name = "other-secret"
	copy.Status.Capabilities[0].Name = "inventory"
	copy.Status.NetAsCode.ModelVersions[0] = "2.0"
	copy.Status.NetAsCode.Sections[0] = "inventory"
	copy.Status.Worker.Name = "other-worker"
	copy.Status.Conditions[0].Type = "Other"
	if original.Spec.Connection.RequestTimeout.Duration != 30*time.Second ||
		original.Spec.Connection.HealthCheckInterval.Duration != time.Minute ||
		original.Spec.TLS.CAConfigMapRef.Name != "ca" ||
		original.Spec.IntentSecretSources[0].Name != "intent-secrets" ||
		original.Status.Capabilities[0].Name != "config" ||
		original.Status.NetAsCode.ModelVersions[0] != "1.0" ||
		original.Status.NetAsCode.Sections[0] != "sites" ||
		original.Status.Worker.Name != "worker" ||
		original.Status.Conditions[0].Type != "Ready" {
		t.Fatalf("DeepCopy shares mutable state with source: %+v", original)
	}
}

func TestValidateNetworkControllerSpecCapsIntentSecretSources(t *testing.T) {
	spec := validNetworkControllerSpec()
	spec.IntentSecretSources = make([]NetworkControllerIntentSecretSource, 129)
	for i := range spec.IntentSecretSources {
		spec.IntentSecretSources[i] = NetworkControllerIntentSecretSource{
			Alias: fmt.Sprintf("secret-%03d", i),
			Name:  "intent-secrets",
			Key:   "key",
		}
	}
	err := ValidateNetworkControllerSpec(&spec).ToAggregate()
	if err == nil || !strings.Contains(err.Error(), "Too many") {
		t.Fatalf("intent Secret source limit error=%v", err)
	}
}
