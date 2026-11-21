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

package ap

import (
	"context"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetDeviceResourcesPodCapacity(t *testing.T) {
	tests := []struct {
		name    string
		maxPods int32
		want    int64
	}{
		{name: "AP default", want: 2},
		{name: "configured lower capacity", maxPods: 1, want: 1},
		{name: "shared CRD default", maxPods: 16, want: 2},
		{name: "over capacity", maxPods: 3, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Driver{config: &v1alpha1.DeviceSpec{MaxPods: tt.maxPods}}
			resources, err := d.GetDeviceResources(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			podCapacity := (*resources)[corev1.ResourcePods]
			if got := podCapacity.Value(); got != tt.want {
				t.Fatalf("pod capacity = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidatePodRejectsUnsupportedRuntimeSettings(t *testing.T) {
	base := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app", UID: "123e4567-e89b-12d3-a456-426614174000"},
		Spec: corev1.PodSpec{
			DNSPolicy:       corev1.DNSDefault,
			SecurityContext: &corev1.PodSecurityContext{},
			Containers:      []corev1.Container{{Name: "app", Image: "https://artifacts.example/app.tar", ImagePullPolicy: corev1.PullIfNotPresent}},
		},
	}
	d := &Driver{}
	if err := d.validatePod(base); err != nil {
		t.Fatalf("valid API-defaulted pod: %v", err)
	}
	tests := map[string]func(*corev1.Pod){
		"multiple applications": func(p *corev1.Pod) {
			p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "sidecar", Image: p.Spec.Containers[0].Image})
		},
		"invalid UID":   func(p *corev1.Pod) { p.UID = "" },
		"envFrom":       func(p *corev1.Pod) { p.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{Prefix: "IGNORED"}} },
		"pod security":  func(p *corev1.Pod) { b := true; p.Spec.SecurityContext.RunAsNonRoot = &b },
		"pod resources": func(p *corev1.Pod) { p.Spec.Resources = &corev1.ResourceRequirements{} },
		"readiness gate": func(p *corev1.Pod) {
			p.Spec.ReadinessGates = []corev1.PodReadinessGate{{ConditionType: "example.net/ready"}}
		},
		"cluster DNS":       func(p *corev1.Pod) { p.Spec.DNSPolicy = corev1.DNSClusterFirst },
		"service injection": func(p *corev1.Pod) { b := true; p.Spec.EnableServiceLinks = &b },
		"token injection":   func(p *corev1.Pod) { b := true; p.Spec.AutomountServiceAccountToken = &b },
		"image pull always": func(p *corev1.Pod) { p.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways },
		"runtime class":     func(p *corev1.Pod) { s := "gvisor"; p.Spec.RuntimeClassName = &s },
		"deadline":          func(p *corev1.Pod) { n := int64(60); p.Spec.ActiveDeadlineSeconds = &n },
		"volume device":     func(p *corev1.Pod) { p.Spec.Containers[0].VolumeDevices = []corev1.VolumeDevice{{Name: "disk"}} },
		"container restart": func(p *corev1.Pod) {
			policy := corev1.ContainerRestartPolicyAlways
			p.Spec.Containers[0].RestartPolicy = &policy
		},
		"windows": func(p *corev1.Pod) { p.Spec.OS = &corev1.PodOS{Name: corev1.Windows} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			pod := base.DeepCopy()
			mutate(pod)
			if err := d.validatePod(pod); err == nil {
				t.Fatal("unsupported setting accepted")
			}
		})
	}
}
