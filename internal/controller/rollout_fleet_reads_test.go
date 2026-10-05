// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// These deliberately unhealthy fleet members exercise inventory and identity,
// not permission to disrupt. Missing independent health must stay unknown.
func fleetReadFixture(index int) (*ciskov1.CiscoDevice, *corev1.Node) {
	name := fmt.Sprintf("fleet-%04d", index)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "fleet", UID: types.UID(name + "-device")},
		Spec:       ciskov1.DeviceSpec{PhysicalIdentity: name},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-node")}}
	bindFleetReadFixture(device, node)
	return device, node
}

func bindFleetReadFixture(device *ciskov1.CiscoDevice, node *corev1.Node) {
	node.Annotations = map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
		managedprotocol.AnnotationNodeUID:         string(node.UID),
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
	}
	node.Status.NodeInfo.MachineID = device.Spec.PhysicalIdentity
	node.Status.NodeInfo.SystemUUID = device.Spec.PhysicalIdentity
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		PhysicalIdentity: device.Spec.PhysicalIdentity,
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{}
}

type fleetCountingReader struct {
	client.Reader
	requests  int
	failNodes bool
}

func (r *fleetCountingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	r.requests++
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (r *fleetCountingReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	r.requests++
	if _, nodes := list.(*corev1.NodeList); nodes && r.failNodes {
		return fmt.Errorf("injected Node list outage")
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestFleetAssessmentUsesBoundedFreshReads(t *testing.T) {
	ctx := context.Background()
	objects := make([]client.Object, 0, 2000)
	for i := 0; i < 1000; i++ {
		d, n := fleetReadFixture(i)
		objects = append(objects, d, n)
	}
	c := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).WithObjects(objects...).Build()
	rd := &fleetCountingReader{Reader: c}
	r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: rd}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}
	members, _, err := r.currentFleetMembers(ctx, &ops.IOSXESoftwareRollout{}, policy, topologyrollout.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1000 || rd.requests > 2 {
		t.Fatalf("fleet assessment: members=%d API requests=%d; want 1000 members and at most 2 uncached requests", len(members), rd.requests)
	}
	for _, member := range members {
		if member.Healthy || member.HealthKnown {
			t.Fatal("absent health became qualified")
		}
	}
	for _, scenario := range []string{"outage", "rebound", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			n := &corev1.Node{}
			if err := c.Get(ctx, client.ObjectKey{Name: "fleet-0999"}, n); err != nil {
				t.Fatal(err)
			}
			rd.failNodes = scenario == "outage"
			if scenario == "rebound" {
				n.Annotations[managedprotocol.AnnotationDeviceUID] = "another-device"
				if err := c.Update(ctx, n); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing" {
				if err := c.Delete(ctx, n); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := r.currentFleetMembers(ctx, &ops.IOSXESoftwareRollout{}, policy, topologyrollout.Policy{}); err == nil {
				t.Fatal("stale or unavailable fleet snapshot was accepted")
			}
		})
	}
}
