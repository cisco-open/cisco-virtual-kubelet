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
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func validNetworkControllerConfigSpec() NetworkControllerConfigSpec {
	return NetworkControllerConfigSpec{
		ControllerRef:   NetworkControllerRef{Name: "campus-controller"},
		Scope:           "global",
		ManagedSections: []string{"sites", "inventory"},
		Source: NetworkControllerConfigurationSource{
			Inline: &runtime.RawExtension{Raw: []byte(`{"sites": {}}`)},
		},
		ModelSource: NetworkControllerNetAsCodeModelSource{
			Format:         "netascode-future-controller",
			ModelVersion:   "1.0.0",
			SchemaDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Resolved:       true,
			Exporter:       "nac-exporter@1.0.0",
			SourceRevision: "git:deadbeef",
		},
		SecretRefs: []NetworkControllerSecretRef{{
			Section: "inventory",
			Path:    "/credentials/password",
			Source:  "inventory-password",
		}},
		DriftDetectInterval: networkControllerConfigDurationPtr(5 * time.Minute),
		Mode:                NetworkControllerApplyModeReport,
		PrunePolicy:         NetworkControllerRetentionPolicyRetain,
		DeletionPolicy:      NetworkControllerRetentionPolicyRetain,
		TaskTimeout:         networkControllerConfigDurationPtr(30 * time.Minute),
	}
}

func networkControllerConfigDurationPtr(duration time.Duration) *metav1.Duration {
	return &metav1.Duration{Duration: duration}
}

func TestValidateNetworkControllerConfigSpecAcceptsFutureModel(t *testing.T) {
	spec := validNetworkControllerConfigSpec()
	if errs := ValidateNetworkControllerConfigSpec(&spec); len(errs) != 0 {
		t.Fatalf("unexpected errors for future Network as Code format: %v", errs.ToAggregate())
	}
}

func TestValidateNetworkControllerConfigSpecAllowsOmittedDurations(t *testing.T) {
	spec := validNetworkControllerConfigSpec()
	spec.DriftDetectInterval = nil
	spec.TaskTimeout = nil
	if errs := ValidateNetworkControllerConfigSpec(&spec); len(errs) != 0 {
		t.Fatalf("omitted durations should be defaultable: %v", errs.ToAggregate())
	}
}

func TestValidateNetworkControllerConfigSpecAllowsDurationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name                string
		driftDetectInterval time.Duration
		taskTimeout         time.Duration
	}{
		{name: "lower bounds", driftDetectInterval: 30 * time.Second, taskTimeout: time.Nanosecond},
		{name: "upper bounds", driftDetectInterval: 720 * time.Hour, taskTimeout: 720 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := validNetworkControllerConfigSpec()
			spec.DriftDetectInterval = networkControllerConfigDurationPtr(test.driftDetectInterval)
			spec.TaskTimeout = networkControllerConfigDurationPtr(test.taskTimeout)
			if errs := ValidateNetworkControllerConfigSpec(&spec); len(errs) != 0 {
				t.Fatalf("valid duration boundaries rejected: %v", errs.ToAggregate())
			}
		})
	}
}

func TestNetworkControllerSecretRefCannotAuthorizeSecret(t *testing.T) {
	typeOf := reflect.TypeOf(NetworkControllerSecretRef{})
	for _, forbidden := range []string{"Name", "Key"} {
		if _, exists := typeOf.FieldByName(forbidden); exists {
			t.Fatalf("NetworkControllerSecretRef must not expose Secret %s", forbidden)
		}
	}
	for _, required := range []string{"Section", "Path", "Source"} {
		if _, exists := typeOf.FieldByName(required); !exists {
			t.Fatalf("NetworkControllerSecretRef is missing %s", required)
		}
	}
}

func TestValidateNetworkControllerConfigSpecRejectsUnsafeIntentShapes(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*NetworkControllerConfigSpec)
		wantErr string
	}{
		{
			name: "both sources",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.Source.ConfigMapRef = &ConfigMapKeyRef{Name: "intent", Key: "config.yaml"}
			},
			wantErr: "exactly one",
		},
		{
			name: "neither source",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.Source = NetworkControllerConfigurationSource{}
			},
			wantErr: "exactly one",
		},
		{
			name:    "unresolved model",
			mutate:  func(spec *NetworkControllerConfigSpec) { spec.ModelSource.Resolved = false },
			wantErr: "must be resolved",
		},
		{
			name:    "missing model version",
			mutate:  func(spec *NetworkControllerConfigSpec) { spec.ModelSource.ModelVersion = "" },
			wantErr: "model version",
		},
		{
			name:    "unqualified model format",
			mutate:  func(spec *NetworkControllerConfigSpec) { spec.ModelSource.Format = "catalyst-center" },
			wantErr: "netascode-*",
		},
		{
			name: "duplicate managed section",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.ManagedSections = []string{"sites", "sites"}
			},
			wantErr: "Duplicate value",
		},
		{
			name: "secret outside ownership",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.SecretRefs[0].Section = "wireless"
			},
			wantErr: "managed section",
		},
		{
			name: "invalid JSON pointer escape",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.SecretRefs[0].Path = "/credentials/~2password"
			},
			wantErr: "RFC 6901",
		},
		{
			name: "invalid intent Secret source alias",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.SecretRefs[0].Source = "Inventory_Password"
			},
			wantErr: "DNS-label-style alias",
		},
		{
			name: "missing intent Secret source alias",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.SecretRefs[0].Source = ""
			},
			wantErr: "DNS-label-style alias",
		},
		{
			name: "duplicate intent Secret injection",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.SecretRefs = append(spec.SecretRefs, spec.SecretRefs[0])
			},
			wantErr: "Duplicate value",
		},
		{
			name: "zero drift interval",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.DriftDetectInterval = networkControllerConfigDurationPtr(0)
			},
			wantErr: "at least 30s",
		},
		{
			name: "negative drift interval",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.DriftDetectInterval = networkControllerConfigDurationPtr(-time.Second)
			},
			wantErr: "at least 30s",
		},
		{
			name: "drift interval too short",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.DriftDetectInterval.Duration = 10 * time.Second
			},
			wantErr: "at least 30s",
		},
		{
			name: "drift interval above maximum",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.DriftDetectInterval = networkControllerConfigDurationPtr(720*time.Hour + time.Nanosecond)
			},
			wantErr: "at most 720h",
		},
		{
			name: "zero task timeout",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.TaskTimeout = networkControllerConfigDurationPtr(0)
			},
			wantErr: "greater than 0s",
		},
		{
			name: "negative task timeout",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.TaskTimeout = networkControllerConfigDurationPtr(-time.Second)
			},
			wantErr: "greater than 0s",
		},
		{
			name: "task timeout above maximum",
			mutate: func(spec *NetworkControllerConfigSpec) {
				spec.TaskTimeout = networkControllerConfigDurationPtr(720*time.Hour + time.Nanosecond)
			},
			wantErr: "at most 720h",
		},
		{
			name:    "invalid apply mode",
			mutate:  func(spec *NetworkControllerConfigSpec) { spec.Mode = "revert" },
			wantErr: "Unsupported value",
		},
		{
			name:    "invalid prune policy",
			mutate:  func(spec *NetworkControllerConfigSpec) { spec.PrunePolicy = "Prune" },
			wantErr: "Unsupported value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validNetworkControllerConfigSpec()
			test.mutate(&spec)
			errs := ValidateNetworkControllerConfigSpec(&spec)
			if len(errs) == 0 {
				t.Fatalf("expected error containing %q", test.wantErr)
			}
			if got := errs.ToAggregate().Error(); !strings.Contains(got, test.wantErr) {
				t.Fatalf("errors=%q, want substring %q", got, test.wantErr)
			}
		})
	}
}

func TestNetworkControllerConfigSchemeAndDeepCopy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, kind := range []string{"NetworkControllerConfig", "NetworkControllerConfigList"} {
		if _, err := scheme.New(GroupVersion.WithKind(kind)); err != nil {
			t.Fatalf("scheme.New(%s): %v", kind, err)
		}
	}

	original := &NetworkControllerConfig{
		Spec: NetworkControllerConfigSpec{
			DriftDetectInterval: networkControllerConfigDurationPtr(5 * time.Minute),
			TaskTimeout:         networkControllerConfigDurationPtr(30 * time.Minute),
			ManagedSections:     []string{"sites"},
			Source: NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{
				Raw: []byte(`{"sites": {"name": "original"}}`),
			}},
			SecretRefs: []NetworkControllerSecretRef{{Section: "sites", Path: "/password", Source: "site-password"}},
		},
		Status: NetworkControllerConfigStatus{
			SectionStatus: []NetworkControllerSectionStatus{{Name: "sites"}},
			Tasks:         []NetworkControllerTaskStatus{{ID: "task-1"}},
			Drift:         []NetworkControllerDriftEntry{{Section: "sites", Resource: "site:a"}},
			Conditions:    []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
		},
	}
	copy := original.DeepCopy()
	copy.Spec.DriftDetectInterval.Duration = 10 * time.Minute
	copy.Spec.TaskTimeout.Duration = time.Hour
	copy.Spec.ManagedSections[0] = "inventory"
	copy.Spec.Source.Inline.Raw[0] = '['
	copy.Spec.SecretRefs[0].Source = "other-source"
	copy.Status.SectionStatus[0].Name = "inventory"
	copy.Status.Tasks[0].ID = "task-2"
	copy.Status.Drift[0].Resource = "site:b"
	copy.Status.Conditions[0].Type = "Other"
	if original.Spec.DriftDetectInterval.Duration != 5*time.Minute ||
		original.Spec.TaskTimeout.Duration != 30*time.Minute ||
		original.Spec.ManagedSections[0] != "sites" ||
		original.Spec.Source.Inline.Raw[0] != '{' ||
		original.Spec.SecretRefs[0].Source != "site-password" ||
		original.Status.SectionStatus[0].Name != "sites" ||
		original.Status.Tasks[0].ID != "task-1" ||
		original.Status.Drift[0].Resource != "site:a" ||
		original.Status.Conditions[0].Type != "Ready" {
		t.Fatalf("DeepCopy shares mutable state with source: %+v", original)
	}
}
