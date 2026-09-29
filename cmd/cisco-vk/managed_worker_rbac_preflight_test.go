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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRenderedManagedWorkerClusterRoleContracts(t *testing.T) {
	manifestPath := os.Getenv("CVK_ADMISSION_MANIFEST")
	if manifestPath == "" {
		t.Skip("CVK_ADMISSION_MANIFEST is not set")
	}
	file, err := os.Open(filepath.Clean(manifestPath))
	if err != nil {
		t.Fatalf("open rendered managed manifest: %v", err)
	}
	defer file.Close()

	expected := make(map[string]struct{}, len(managedWorkerClusterRoleContracts()))
	for _, role := range managedWorkerClusterRoleContracts() {
		expected[role.Name] = struct{}{}
	}
	var objects []runtime.Object
	decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered managed manifest: %v", err)
		}
		if document["kind"] != "ClusterRole" {
			continue
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encode rendered ClusterRole: %v", err)
		}
		var role rbacv1.ClusterRole
		if err := json.Unmarshal(encoded, &role); err != nil {
			t.Fatalf("decode rendered ClusterRole: %v", err)
		}
		if _, managed := expected[role.Name]; managed {
			objects = append(objects, role.DeepCopy())
		}
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
	if err := verifyManagedWorkerClusterRoles(context.Background(), reader); err != nil {
		t.Fatalf("rendered managed worker RBAC contract: %v", err)
	}
}

func TestVerifyManagedWorkerClusterRoles(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func([]rbacv1.ClusterRole) []rbacv1.ClusterRole
		wantError string
	}{
		{name: "exact contracts"},
		{
			name: "baseline extra verb",
			mutate: func(roles []rbacv1.ClusterRole) []rbacv1.ClusterRole {
				roles[0].Rules[0].Verbs = append(roles[0].Rules[0].Verbs, "delete")
				return roles
			},
			wantError: "rules do not match",
		},
		{
			name: "completion extra resource",
			mutate: func(roles []rbacv1.ClusterRole) []rbacv1.ClusterRole {
				roles[1].Rules[0].Resources = append(roles[1].Rules[0].Resources, "secrets")
				return roles
			},
			wantError: "rules do not match",
		},
		{
			name: "terminating baseline role",
			mutate: func(roles []rbacv1.ClusterRole) []rbacv1.ClusterRole {
				stamp := metav1.NewTime(time.Now())
				roles[0].DeletionTimestamp = &stamp
				roles[0].Finalizers = []string{"test.cisco.vk/hold"}
				return roles
			},
			wantError: "is terminating",
		},
		{
			name: "aggregated authority",
			mutate: func(roles []rbacv1.ClusterRole) []rbacv1.ClusterRole {
				roles[0].AggregationRule = &rbacv1.AggregationRule{}
				return roles
			},
			wantError: "must not use rule aggregation",
		},
		{
			name: "missing completion role",
			mutate: func(roles []rbacv1.ClusterRole) []rbacv1.ClusterRole {
				return roles[:1]
			},
			wantError: "read managed worker ClusterRole",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			roles := managedWorkerClusterRoleContracts()
			if tc.mutate != nil {
				roles = tc.mutate(roles)
			}
			objects := make([]runtime.Object, 0, len(roles))
			for i := range roles {
				objects = append(objects, roles[i].DeepCopy())
			}
			scheme := runtime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
			err := verifyManagedWorkerClusterRoles(context.Background(), reader)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("exact role contracts rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("verify error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestVerifyManagedWorkerClusterRolesRejectsNilReader(t *testing.T) {
	if err := verifyManagedWorkerClusterRoles(context.Background(), nil); err == nil {
		t.Fatal("nil managed worker RBAC reader was accepted")
	}
}
