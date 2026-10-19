// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type metadataConsumersReader struct {
	client.Reader
	mode           string
	lists, devices int
}

func (r *metadataConsumersReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := &client.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(o)
	}
	if o.Namespace != "" || o.LabelSelector != nil || o.FieldSelector != nil {
		return errors.New("consumer proof requires global Pod census")
	}
	r.lists++
	if r.mode == "denied" {
		return errors.New("Pod list denied")
	}
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	rows := list.(*core.PodList)
	if r.mode == "truncated" {
		rows.Continue = "next"
	}
	if r.mode == "late-orphan" && r.lists == 2 {
		other := rows.Items[0].DeepCopy()
		other.Name = "orphan"
		other.Spec.Containers[0].Env[2].Value = "old-device"
		rows.Items = append(rows.Items, *other)
	}
	return nil
}
func (r *metadataConsumersReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if d, ok := obj.(*cvk.CiscoDevice); ok {
		r.devices++
		if r.mode == "late-device" && r.devices == 2 {
			d.ResourceVersion = "changed"
		}
	}
	return nil
}
func TestMetadataConsumerPodFootprints(t *testing.T) {
	for _, mode := range []string{"current", "app", "orphan", "replaced", "unknown-identity", "matching", "unqualified-legacy", "value-from", "duplicate", "other-provider", "terminal", "deleting-orphan", "late-orphan", "late-device", "denied", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			b := metadataRuntimeTestBinding()
			d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "device", Namespace: "tenant", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{PhysicalIdentity: "different123"}}
			p := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "tenant", UID: "worker", Labels: map[string]string{"app.kubernetes.io/managed-by": "ciscodevice-controller"}}, Spec: core.PodSpec{ServiceAccountName: "network", Containers: []core.Container{{Name: "cisco-vk", Env: []core.EnvVar{{Name: managedprotocol.EnvDeviceNamespace, Value: "tenant"}, {Name: managedprotocol.EnvDeviceName, Value: "device"}, {Name: managedprotocol.EnvDeviceUID, Value: "device"}, {Name: managedprotocol.EnvWorkerMode, Value: managedprotocol.WorkerModeNetworkManagement}, {Name: managedprotocol.EnvExpectedWorkerUsername, Value: "system:serviceaccount:tenant:network"}}}}}}
			switch mode {
			case "app":
				p.Spec.ServiceAccountName = "apps"
				p.Spec.Containers[0].Env[3].Value = managedprotocol.WorkerModeAppHosting
				p.Spec.Containers[0].Env[4].Value = "system:serviceaccount:tenant:apps"
			case "replaced":
				d.UID = "replacement"
			case "unknown-identity":
				d.Spec.PhysicalIdentity = ""
			case "matching":
				d.Spec.PhysicalIdentity = "selected123"
			case "unqualified-legacy":
				p.Spec.Containers[0].Env[3].Value = managedprotocol.WorkerModeLegacy
			case "value-from":
				p.Spec.Containers[0].Env[0].ValueFrom = &core.EnvVarSource{FieldRef: &core.ObjectFieldSelector{FieldPath: "metadata.namespace"}}
			case "duplicate":
				p.Spec.Containers[0].Env = append(p.Spec.Containers[0].Env, p.Spec.Containers[0].Env[0])
			case "other-provider":
				p.Labels = map[string]string{"app.kubernetes.io/managed-by": "other"}
				p.Spec.Containers[0].Env = nil
			case "terminal":
				p.Status.Phase = core.PodSucceeded
				p.Spec.Containers[0].Env = nil
			case "deleting-orphan":
				now := metav1.Now()
				p.DeletionTimestamp = &now
				p.Finalizers = []string{"retain-test-worker"}
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = core.AddToScheme(scheme)
			objects := []client.Object{p}
			if mode != "orphan" && mode != "deleting-orphan" {
				objects = append(objects, d)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			reader := &metadataConsumersReader{Reader: c, mode: mode}
			err := ReadMetadataConsumerPods(context.Background(), reader, b, []string{"selected123"})
			allowed := mode == "current" || mode == "app" || mode == "other-provider" || mode == "terminal"
			if (err == nil) != allowed {
				t.Fatal("unexpected active consumer footprint proof", err)
			}
			if allowed && reader.lists != 2 {
				t.Fatal("consumer proof omitted repeated global census")
			}
		})
	}
}
