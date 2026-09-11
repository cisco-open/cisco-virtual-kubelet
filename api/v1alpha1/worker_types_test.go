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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestDeviceWorkerConfigValidation(t *testing.T) {
	positive := resource.MustParse("10Gi")
	zero := resource.MustParse("0")
	for _, test := range []struct {
		name   string
		config *DeviceWorkerConfig
		valid  bool
	}{
		{name: "omitted", valid: true},
		{name: "empty", config: &DeviceWorkerConfig{}, valid: true},
		{name: "storage cap", config: &DeviceWorkerConfig{TmpSizeLimit: &positive}, valid: true},
		{name: "zero storage cap", config: &DeviceWorkerConfig{TmpSizeLimit: &zero}},
		{name: "resources", config: &DeviceWorkerConfig{Resources: &DeviceWorkerResources{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceEphemeralStorage: resource.MustParse("5Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceEphemeralStorage: resource.MustParse("12Gi")},
		}}, valid: true},
		{name: "negative request", config: &DeviceWorkerConfig{Resources: &DeviceWorkerResources{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("-1Mi")}}}},
		{name: "negative limit", config: &DeviceWorkerConfig{Resources: &DeviceWorkerResources{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("-1")}}}},
		{name: "unknown resource", config: &DeviceWorkerConfig{Resources: &DeviceWorkerResources{Requests: corev1.ResourceList{"example.com/device": resource.MustParse("1")}}}},
		{name: "request exceeds limit", config: &DeviceWorkerConfig{Resources: &DeviceWorkerResources{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1024Mi")},
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate=%v, valid=%v", err, test.valid)
			}
		})
	}
}
