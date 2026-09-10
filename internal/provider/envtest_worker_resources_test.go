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

//go:build envtest

package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestEnvtest_CiscoDeviceWorkerResourcesAdmission(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	const namespace = "envtest-worker-resources"
	envtestNamespace(t, c, namespace)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, test := range []struct {
		name   string
		worker map[string]any
		valid  bool
		want   string
	}{
		{name: "omitted", valid: true},
		{name: "empty", worker: map[string]any{}, valid: true},
		{name: "clear-resources", worker: map[string]any{"resources": map[string]any{}}, valid: true},
		{name: "sized", worker: map[string]any{
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "100m", "memory": "128Mi", "ephemeral-storage": "5Gi"},
				"limits":   map[string]any{"cpu": int64(1), "memory": "512Mi", "ephemeral-storage": "12Gi"},
			},
			"tmpSizeLimit": "10Gi",
		}, valid: true},
		{name: "integer-size", worker: map[string]any{"tmpSizeLimit": int64(1024)}, valid: true},
		{name: "bad-quantity", worker: map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "fast"}}}},
		{name: "negative-request", worker: map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "-1Gi"}}}},
		{name: "negative-limit", worker: map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": int64(-1)}}}},
		{name: "request-exceeds-limit", worker: map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "2Gi"}, "limits": map[string]any{"memory": "1024Mi"}}}},
		{name: "unsupported-resource", worker: map[string]any{"resources": map[string]any{"requests": map[string]any{"example.com/gpu": int64(1)}}}},
		{name: "too-many-resources", worker: map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": "1", "memory": "1Gi", "ephemeral-storage": "1Gi", "example.com/gpu": "1"}}}, want: "at most 3"},
		{name: "zero-size", worker: map[string]any{"tmpSizeLimit": "0"}},
		{name: "negative-size", worker: map[string]any{"tmpSizeLimit": "-1Gi"}},
		{name: "bad-size", worker: map[string]any{"tmpSizeLimit": "large"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := map[string]any{"driver": "XE", "address": "192.0.2.10", "username": "admin"}
			if test.worker != nil {
				spec["worker"] = test.worker
			}
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "cisco.vk/v1alpha1", "kind": "CiscoDevice",
				"metadata": map[string]any{"name": test.name, "namespace": namespace},
				"spec":     spec,
			}}
			err := c.Create(ctx, object)
			if test.valid {
				if err != nil {
					t.Fatalf("valid worker config rejected: %v", err)
				}
				if test.name == "clear-resources" {
					var typed ciskov1.CiscoDevice
					if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: test.name}, &typed); err != nil {
						t.Fatal(err)
					}
					if typed.Spec.Worker == nil || typed.Spec.Worker.Resources == nil {
						t.Fatal("admission pruned explicit resources:{}, losing inheritance override")
					}
				}
				return
			}
			if err == nil || !apierrors.IsInvalid(err) {
				t.Fatalf("invalid worker config admission=%v", err)
			}
			if test.want != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("admission error=%v, want %q", err, test.want)
			}
		})
	}
}
