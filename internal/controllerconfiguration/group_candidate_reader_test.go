// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func groupReaderFixture(t *testing.T) (client.Client, *cvk.NetworkController, time.Time) {
	t.Helper()
	group, members, _, now := groupCandidateFixture(t)
	_, nc, deployment, rs, pod, _ := controllerWorkerFixture(t)
	pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(now)
	nc.Status.Configuration = group.Status.Configuration.DeepCopy()
	op := nc.Status.Configuration.Operation
	nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: op.Section, ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}
	intent := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: op.ConfigName, UID: types.UID(op.ConfigUID), Generation: op.ConfigGeneration}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: nc.Name}, Scope: "global", ManagedSections: []string{op.Section}, Mode: config.NetworkControllerApplyModeApply, ModelSource: config.NetworkControllerNetAsCodeModelSource{Resolved: true, ModelVersion: "0.5.0"}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{}`)}}}}
	objects := []client.Object{nc, deployment, rs, pod, intent}
	for _, m := range members {
		m.Node.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] = m.Worker.Name
		m.Node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = string(m.Worker.UID)
		objects = append(objects, m.Device, m.Node, m.Worker)
	}
	scheme := runtime.NewScheme()
	_ = cvk.AddToScheme(scheme)
	_ = config.AddToScheme(scheme)
	_ = ops.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithIndex(&core.Pod{}, "spec.nodeName", func(obj client.Object) []string { return []string{obj.(*core.Pod).Spec.NodeName} }).WithStatusSubresource(nc, &cvk.CiscoDevice{}, &ops.ControllerConfigurationReservation{}).WithObjects(objects...).Build()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	return c, nc, now
}

func TestReadGroupReservationCandidates(t *testing.T) {
	for _, mode := range []string{"valid", "late-alias", "late-device-version", "late-node-version", "late-worker-version", "missing-worker", "overlapping-connector", "changed-intent", "stale-controller"} {
		t.Run(mode, func(t *testing.T) {
			c, nc, now := groupReaderFixture(t)
			if mode == "stale-controller" {
				nc.ResourceVersion = "stale"
			}
			reader := &changingGroupReader{Reader: c, mode: mode, reads: map[string]int{}}
			candidates, err := ReadGroupReservationCandidates(context.Background(), reader, nc, now)
			if mode != "valid" {
				if err == nil || candidates != nil {
					t.Fatal("inconsistent group accepted", mode, err)
				}
				return
			}
			if err != nil || len(candidates) != 2 {
				t.Fatal("valid snapshot refused", err)
			}
			if reader.deviceLists != 2 {
				t.Fatal("exclusivity not rechecked", reader.deviceLists)
			}
			if candidates[0].Spec.PhysicalIdentity != "serial-a" || candidates[1].Spec.PhysicalIdentity != "serial-b" {
				t.Fatal("noncanonical result")
			}
		})
	}
}

type changingGroupReader struct {
	client.Reader
	mode        string
	deviceLists int
	reads       map[string]int
}

func (r *changingGroupReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	if devices, ok := list.(*cvk.CiscoDeviceList); ok {
		r.deviceLists++
		if r.deviceLists == 2 && r.mode == "late-alias" {
			alias := devices.Items[0].DeepCopy()
			alias.Name = "alias"
			alias.Namespace = "foreign"
			alias.UID = "alias"
			devices.Items = append(devices.Items, *alias)
		}
		if r.deviceLists == 2 && r.mode == "late-device-version" {
			devices.Items[0].ResourceVersion = "changed"
		}
	}
	if pods, ok := list.(*core.PodList); ok && (r.mode == "overlapping-connector" || (r.mode == "late-connector" && r.deviceLists >= 2)) && len(pods.Items) > 0 {
		alias := pods.Items[0].DeepCopy()
		alias.Name = "other"
		alias.UID = "other"
		pods.Items = append(pods.Items, *alias)
	}
	return nil
}
func (r *changingGroupReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	switch value := obj.(type) {
	case *core.Node:
		r.reads["node/"+key.Name]++
		if r.mode == "missing-worker" {
			delete(value.Annotations, managedprotocol.AnnotationNetworkWorkerPodName)
		}
		if r.mode == "late-node-version" && r.reads["node/"+key.Name] > 1 {
			value.ResourceVersion = "changed"
		}
	case *core.Pod:
		r.reads["pod/"+key.Name]++
		if r.mode == "late-worker-version" && key.Name == "worker-a" && r.reads["pod/"+key.Name] > 1 {
			value.ResourceVersion = "changed"
		}
	case *config.NetworkControllerConfig:
		if r.mode == "changed-intent" || (r.mode == "late-intent" && r.deviceLists >= 2) {
			value.Generation++
		}
	}
	return nil
}
