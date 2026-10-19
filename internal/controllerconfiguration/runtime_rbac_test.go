// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReadNetworkMutationRole(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = core.AddToScheme(scheme)
	_ = rbac.AddToScheme(scheme)
	for _, tc := range []struct {
		name   string
		mutate func(*ops.ControllerConfigurationReservation, *core.Pod, *rbac.RoleBinding, *rbac.ClusterRole)
		want   bool
	}{
		{"current", func(*ops.ControllerConfigurationReservation, *core.Pod, *rbac.RoleBinding, *rbac.ClusterRole) {}, true},
		{"read only", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.RoleRef.Name = managedprotocol.NetworkManagementReadOnlyClusterRole
		}, false},
		{"broadened role", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, _ *rbac.RoleBinding, c *rbac.ClusterRole) {
			c.Rules = append(c.Rules, rbac.PolicyRule{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}})
		}, false},
		{"missing status grant", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, _ *rbac.RoleBinding, c *rbac.ClusterRole) {
			for i, r := range c.Rules {
				if len(r.Resources) == 1 && r.Resources[0] == "controllerconfigurationreservations/status" {
					c.Rules = append(c.Rules[:i], c.Rules[i+1:]...)
					break
				}
			}
		}, false},
		{"aggregated role", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, _ *rbac.RoleBinding, c *rbac.ClusterRole) {
			c.AggregationRule = &rbac.AggregationRule{}
		}, false},
		{"other account", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.Subjects[0].Name = "other"
		}, false},
		{"other namespace", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.Subjects[0].Namespace = "other"
		}, false},
		{"additional subject", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.Subjects = append(b.Subjects, rbac.Subject{Kind: "Group", Name: "system:authenticated"})
		}, false},
		{"binding in wrong namespace", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.Namespace = "other"
		}, false},
		{"noncanonical binding", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.Name = "other"
		}, false},
		{"namespaced role", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.RoleRef.Kind = "Role"
		}, false},
		{"unexpected owner", func(_ *ops.ControllerConfigurationReservation, _ *core.Pod, b *rbac.RoleBinding, _ *rbac.ClusterRole) {
			b.OwnerReferences = []metav1.OwnerReference{{UID: "other"}}
		}, false},
		{"replaced pod", func(_ *ops.ControllerConfigurationReservation, p *core.Pod, _ *rbac.RoleBinding, _ *rbac.ClusterRole) {
			p.UID = "replacement"
		}, false},
		{"wrong username", func(r *ops.ControllerConfigurationReservation, _ *core.Pod, _ *rbac.RoleBinding, _ *rbac.ClusterRole) {
			r.Spec.DeviceWorkerUsername = "system:serviceaccount:lab:other"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab"}, Spec: ops.ControllerConfigurationReservationSpec{DeviceWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "pod-uid"}, DeviceWorkerUsername: "system:serviceaccount:lab:network"}}
			p := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "lab", UID: "pod-uid"}, Spec: core.PodSpec{ServiceAccountName: "network"}}
			b := &rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "network", Namespace: "lab", UID: "binding-uid"}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadWriteClusterRole}, Subjects: []rbac.Subject{{Kind: rbac.ServiceAccountKind, Namespace: "lab", Name: "network"}}}
			role := &rbac.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: managedprotocol.NetworkManagementReadWriteClusterRole, UID: "role-uid"}, Rules: managedprotocol.WorkerClusterRoleContracts()[managedprotocol.NetworkManagementReadWriteClusterRole]}
			tc.mutate(r, p, b, role)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(p, b, role).Build()
			if err := ReadNetworkMutationRole(context.Background(), c, r); (err == nil) != tc.want {
				t.Fatalf("err=%v want accepted=%v", err, tc.want)
			}
			if tc.want {
				// Simulate replacement between the first role read and its corroboration.
				changed := &changingRoleReader{Reader: c}
				if err := ReadNetworkMutationRole(context.Background(), changed, r); err == nil {
					t.Fatal("role changed during evidence collection")
				}
			}
		})
	}
}

type changingRoleReader struct {
	client.Reader
	reads int
}

func (r *changingRoleReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if _, ok := obj.(*rbac.ClusterRole); ok {
		r.reads++
		if r.reads > 1 {
			obj.SetUID("replacement")
		}
	}
	return nil
}
