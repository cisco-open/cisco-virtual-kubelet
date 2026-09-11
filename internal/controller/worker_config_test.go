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

package controller

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestReconcile_WorkerResourcesAndStorage(t *testing.T) {
	for _, test := range []struct {
		name, defaults, size string
		override             *ciskov1.DeviceWorkerConfig
		cpu, tmp             string
	}{
		{name: "legacy defaults"},
		{name: "release defaults", defaults: `{"requests":{"cpu":"100m","ephemeral-storage":"5Gi"},"limits":{"memory":"512Mi","ephemeral-storage":"12Gi"}}`, size: "10Gi", cpu: "100m", tmp: "10Gi"},
		{name: "device replaces resources", defaults: `{"requests":{"cpu":"100m"},"limits":{"cpu":"200m"}}`, size: "10Gi", override: &ciskov1.DeviceWorkerConfig{Resources: &ciskov1.DeviceWorkerResources{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}}}, cpu: "500m", tmp: "10Gi"},
		{name: "device clears resource defaults", defaults: `{"requests":{"cpu":"100m"}}`, size: "10Gi", override: &ciskov1.DeviceWorkerConfig{Resources: &ciskov1.DeviceWorkerResources{}}, tmp: "10Gi"},
		{name: "device storage cap", size: "10Gi", override: &ciskov1.DeviceWorkerConfig{TmpSizeLimit: quantityPointer("20Gi")}, tmp: "20Gi"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(envCVKWorkerResources, test.defaults)
			t.Setenv(envCVKWorkerTmpSizeLimit, test.size)
			device := newDevice("sized-worker", "default")
			device.Spec.Worker = test.override
			r := reconcilerFor(t, device)
			ctx := context.Background()
			if _, err := r.Reconcile(ctx, reconcileRequest(device.Namespace, device.Name)); err != nil {
				t.Fatal(err)
			}
			var deployment appsv1.Deployment
			if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name + deploymentSuffix}, &deployment); err != nil {
				t.Fatal(err)
			}
			resources := deployment.Spec.Template.Spec.Containers[0].Resources
			cpu, exists := resources.Requests[corev1.ResourceCPU]
			if test.cpu == "" && exists || test.cpu != "" && (!exists || cpu.Cmp(resource.MustParse(test.cpu)) != 0) {
				t.Fatalf("cpu=%s, want %s", cpu.String(), test.cpu)
			}
			if test.override != nil && test.override.Resources != nil && len(resources.Limits) != len(test.override.Resources.Limits) {
				t.Fatal("per-device resources unexpectedly merged inherited limits")
			}
			for _, volume := range deployment.Spec.Template.Spec.Volumes {
				if volume.Name != "tmp" {
					continue
				}
				if volume.EmptyDir == nil {
					t.Fatal("missing tmp emptyDir")
				}
				if test.tmp == "" && volume.EmptyDir.SizeLimit != nil || test.tmp != "" && (volume.EmptyDir.SizeLimit == nil || volume.EmptyDir.SizeLimit.Cmp(resource.MustParse(test.tmp)) != 0) {
					t.Fatalf("tmp limit=%v, want %s", volume.EmptyDir.SizeLimit, test.tmp)
				}
			}
			var configMap corev1.ConfigMap
			if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: device.Name + configMapSuffix}, &configMap); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(configMap.Data[configFileName], "worker:") {
				t.Fatal("Kubernetes worker settings leaked into runtime device configuration")
			}
			for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
				if env.Name == envCVKWorkerResources || env.Name == envCVKWorkerTmpSizeLimit {
					t.Fatal("manager-only worker defaults propagated into worker environment")
				}
			}
		})
	}
}

func quantityPointer(value string) *resource.Quantity {
	quantity := resource.MustParse(value)
	return &quantity
}

func TestWorkerDefaultsRejectInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ name, resources, size string }{
		{name: "unknown field", resources: `{"request":{"cpu":"100m"}}`},
		{name: "unknown resource", resources: `{"requests":{"gpu":"1"}}`},
		{name: "trailing object", resources: `{} {}`},
		{name: "invalid quantity", resources: `{"requests":{"cpu":"fast"}}`},
		{name: "negative request", resources: `{"requests":{"memory":"-1Gi"}}`},
		{name: "request exceeds limit", resources: `{"requests":{"cpu":"2"},"limits":{"cpu":"1"}}`},
		{name: "zero tmp", size: "0"},
		{name: "negative tmp", size: "-1Gi"},
		{name: "invalid tmp", size: "big"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(envCVKWorkerResources, test.resources)
			t.Setenv(envCVKWorkerTmpSizeLimit, test.size)
			if _, err := resolveDeviceWorkerConfig(nil); err == nil {
				t.Fatal("invalid worker defaults accepted")
			}
		})
	}
}

func TestChartWorkerDefaultsReachManager(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is unavailable")
	}
	output, err := exec.Command(helm, "template", "cvk", "../../charts/cisco-virtual-kubelet", "--show-only", "templates/deployment.yaml",
		"--set", "worker.resources.requests.cpu=100m", "--set", "worker.resources.requests.ephemeral-storage=5Gi", "--set", "worker.resources.limits.memory=512Mi", "--set", "worker.tmpSizeLimit=10Gi").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, output)
	}
	var deployment appsv1.Deployment
	if err := yaml.Unmarshal(output, &deployment); err != nil {
		t.Fatal(err)
	}
	resources, ok := findEnvVar(deployment.Spec.Template.Spec.Containers[0].Env, envCVKWorkerResources)
	if !ok {
		t.Fatal("Helm worker resources missing from manager")
	}
	size, ok := findEnvVar(deployment.Spec.Template.Spec.Containers[0].Env, envCVKWorkerTmpSizeLimit)
	if !ok {
		t.Fatal("Helm worker tmp size missing from manager")
	}
	t.Setenv(envCVKWorkerResources, resources.Value)
	t.Setenv(envCVKWorkerTmpSizeLimit, size.Value)
	resolved, err := resolveDeviceWorkerConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	requests := corev1.ResourceList(resolved.Resources.Requests)
	if requests.Cpu().Cmp(resource.MustParse("100m")) != 0 || requests.StorageEphemeral().Cmp(resource.MustParse("5Gi")) != 0 || resolved.TmpSizeLimit.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Fatal("Helm worker settings did not round-trip through controller defaults")
	}
}

func TestChartWorkerDefaultsRejectMalformedOrUnsupportedValues(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is unavailable")
	}
	for _, setting := range []string{
		"worker.tmpSizeLimit=0",
		"worker.tmpSizeLimit=-1Gi",
		"worker.tmpSizeLimit=large",
		"worker.resources.requests.gpu=1",
		"worker.resources.requests.memory=-1Gi",
	} {
		t.Run(setting, func(t *testing.T) {
			output, err := exec.Command(helm, "template", "cvk", "../../charts/cisco-virtual-kubelet", "--set-string", setting).CombinedOutput()
			if err == nil {
				t.Fatalf("Helm accepted invalid worker default %s", setting)
			}
			if !strings.Contains(string(output), "worker") {
				t.Fatalf("Helm failed for an unrelated reason: %s", output)
			}
		})
	}
}
