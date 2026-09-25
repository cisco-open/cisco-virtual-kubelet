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
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestCurrentWorkerPodIdentityDoesNotWaitForReadinessPreflight(t *testing.T) {
	ctx := context.Background()
	device := newDevice("switch-preflight", "edge")
	device.UID = "device-uid"
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: perDeviceDeploymentLabels(device.Name)},
		Spec: corev1.PodSpec{
			ServiceAccountName: managedprotocol.AppHostingServiceAccount,
			Containers:         []corev1.Container{{Name: "cisco-vk", Image: "cisco-vk:test"}},
		},
	}
	revision, err := managedWorkerPodTemplateRevision(&template)
	if err != nil {
		t.Fatal(err)
	}
	template.Annotations = map[string]string{managedprotocol.AnnotationWorkerConfigRevision: revision}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: device.Name + deploymentSuffix,
			UID: "deployment-uid", Generation: 1,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: perDeviceDeploymentLabels(device.Name)},
			Template: template,
		},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "switch-preflight-rs", UID: "replicaset-uid",
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment")),
		},
	}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "switch-preflight-pod", UID: "pod-uid",
			Labels:      perDeviceDeploymentLabels(device.Name),
			Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: revision},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(replicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
			},
		},
		Spec:   corev1.PodSpec{ServiceAccountName: managedprotocol.AppHostingServiceAccount},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r := reconcilerFor(t, device, deployment, replicaSet, pod)

	current, err := soleCurrentWorkerPod(ctx, r.Client, device, deployment,
		perDeviceDeploymentLabels(device.Name), revision)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.UID != pod.UID {
		t.Fatalf("current preflight Pod = %#v, want UID %q", current, pod.UID)
	}
	ready, err := soleReadyWorkerPod(ctx, r.Client, device, deployment,
		perDeviceDeploymentLabels(device.Name), revision)
	if err != nil {
		t.Fatal(err)
	}
	if ready != nil {
		t.Fatalf("non-ready preflight Pod was treated as rollout-ready: %#v", ready)
	}
}

func TestNetworkWorkerHealthProbesAreRevisioned(t *testing.T) {
	template := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "cisco-vk", Image: "cisco-vk:test",
	}}}}
	before, err := managedWorkerPodTemplateRevision(&template)
	if err != nil {
		t.Fatal(err)
	}
	configureNetworkWorkerHealthProbes(&template)
	after, err := managedWorkerPodTemplateRevision(&template)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("network health contract did not affect the PodTemplate revision")
	}
	container := template.Spec.Containers[0]
	if len(container.Ports) != 1 || container.Ports[0].Name != "health" ||
		container.Ports[0].ContainerPort != 8081 || container.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("network health port = %#v", container.Ports)
	}
	assertProbe := func(name string, probe *corev1.Probe, path string, initialDelay, period int32) {
		t.Helper()
		if probe == nil || probe.HTTPGet == nil || probe.HTTPGet.Path != path ||
			probe.HTTPGet.Port.StrVal != "health" || probe.InitialDelaySeconds != initialDelay ||
			probe.PeriodSeconds != period || probe.TimeoutSeconds != 1 || probe.FailureThreshold != 3 {
			t.Fatalf("%s probe = %#v", name, probe)
		}
	}
	assertProbe("readiness", container.ReadinessProbe, "/readyz", 2, 5)
	assertProbe("liveness", container.LivenessProbe, "/healthz", 10, 10)
}
