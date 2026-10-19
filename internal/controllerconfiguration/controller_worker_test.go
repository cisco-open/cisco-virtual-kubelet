// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func controllerWorkerFixture(t *testing.T) (*ops.ControllerConfigurationReservation, *cvk.NetworkController, *apps.Deployment, *apps.ReplicaSet, *core.Pod, time.Time) {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)
	replicas := int32(1)
	controlled := true
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab"}, Spec: ops.ControllerConfigurationReservationSpec{Controller: ops.ConfigurationReservationReference{Name: "cc", UID: "controller-uid"}, ControllerGeneration: 1, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "pod-uid"}, ControllerUsername: "system:serviceaccount:lab:connector"}}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: "lab", UID: "controller-uid", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center"}, Status: cvk.NetworkControllerStatus{Worker: &cvk.NetworkControllerWorkerStatus{Name: "connector", DeploymentName: "connector", ReadyReplicas: 1}}}
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"worker": "connector"}, Annotations: map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Args: []string{"controller-worker", "--controller-namespace=lab", "--controller-name=cc", "--controller-uid=controller-uid", "--controller-generation=1", "--controller-type=catalyst-center"}}}}}
	deployment := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "lab", UID: "deployment-uid", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": "cc", "cisco.vk/network-controller-uid": "controller-uid"}}, Spec: apps.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: *template.DeepCopy()}, Status: apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "replica", Namespace: "lab", UID: "replica-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "connector", UID: "deployment-uid", Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: *template.DeepCopy()}}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "lab", UID: "pod-uid", Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "replica", UID: "replica-uid", Controller: &controlled}}}, Spec: *template.Spec.DeepCopy(), Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(now)}}}}
	return r, nc, deployment, rs, pod, now
}

func TestControllerWorkerBinding(t *testing.T) {
	r, nc, deployment, rs, pod, now := controllerWorkerFixture(t)
	cases := map[string]func(*cvk.NetworkController, *apps.Deployment, *apps.ReplicaSet, *core.Pod){
		"valid": func(*cvk.NetworkController, *apps.Deployment, *apps.ReplicaSet, *core.Pod) {},
		"wrong deployment binding": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			d.Annotations["cisco.vk/network-controller-uid"] = "other"
		},
		"replaced replicaset": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) { rs.UID = "other" },
		"wrong parent": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			rs.OwnerReferences[0].UID = "other"
		},
		"replaced pod": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) { p.UID = "other" },
		"old template": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			p.Annotations["cisco.vk/controller-contract-hash"] = strings.Repeat("b", 64)
		},
		"wrong args": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			p.Spec.Containers[0].Args = append(p.Spec.Containers[0].Args, "--controller-uid=other")
		},
		"split override": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			p.Spec.Containers[0].Args = append(p.Spec.Containers[0].Args, "--controller-uid", "other")
		},
		"wrong account": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			p.Spec.ServiceAccountName = "other"
		},
		"unready": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			p.Status.Conditions = nil
		},
		"paused": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			n.Spec.Paused = true
		},
		"unobserved deployment": func(n *cvk.NetworkController, d *apps.Deployment, rs *apps.ReplicaSet, p *core.Pod) {
			d.Status.ObservedGeneration = 0
		},
	}
	scheme := runtime.NewScheme()
	_ = cvk.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			n, d, s, p := nc.DeepCopy(), deployment.DeepCopy(), rs.DeepCopy(), pod.DeepCopy()
			mutate(n, d, s, p)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(n, d, s, p).Build()
			err := ReadControllerWorkerBinding(context.Background(), c, r, now)
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("stale connector accepted")
			}
		})
	}
	t.Run("overlapping pods", func(t *testing.T) {
		other := pod.DeepCopy()
		other.Name = "overlap"
		other.UID = "overlap-uid"
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc, deployment, rs, pod, other).Build()
		if ReadControllerWorkerBinding(context.Background(), c, r, now) == nil {
			t.Fatal("overlap accepted")
		}
	})
}
