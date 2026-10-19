// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

func deviceBindingFixture(t *testing.T) (*ops.ControllerConfigurationReservation, *cvk.CiscoDevice, *core.Node, *core.Pod, time.Time) {
	t.Helper()
	_, r, _, now := claimFixture(t)
	r.Spec.Node = ops.ConfigurationReservationReference{Name: "switch", UID: "node-uid"}
	r.Spec.DeviceGeneration = 1
	r.Spec.ManagementAddress = "192.0.2.1"
	r.Spec.PhysicalIdentity = "serial123"
	r.Spec.DeviceWorkerPod = ops.ConfigurationReservationReference{Name: "xe-worker", UID: "pod-uid"}
	r.Spec.DeviceWorkerRevision = "revision"
	r.Spec.DeviceWorkerUsername = "system:serviceaccount:lab:network"
	started, ready := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(-time.Second))
	d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "switch", Namespace: "lab", UID: "device-uid", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: "192.0.2.1", PhysicalIdentity: "serial123"}, Status: cvk.DeviceStatus{NodeIdentity: &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-uid", NodeName: "switch", NodeUID: "node-uid", PhysicalIdentity: "serial123"}, NetworkWorkerRevision: &cvk.DeviceNetworkWorkerRevisionStatus{DesiredRevision: "revision", ObservedRevision: "revision", PodUID: "pod-uid", PodStartTime: &started, PodReadyTime: &ready}}}
	n := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node-uid", Annotations: map[string]string{managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: "lab", managedprotocol.AnnotationDeviceName: "switch", managedprotocol.AnnotationDeviceUID: "device-uid", managedprotocol.AnnotationNodeUID: "node-uid", managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version, managedprotocol.AnnotationNetworkWorkerUsername: r.Spec.DeviceWorkerUsername}}, Status: core.NodeStatus{NodeInfo: core.NodeSystemInfo{MachineID: "serial123", SystemUUID: "serial123"}}}
	p := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "xe-worker", Namespace: "lab", UID: "pod-uid", Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: "revision"}}, Spec: core.PodSpec{ServiceAccountName: "network"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
	return r, d, n, p, now
}

func TestDeviceWorkerBinding(t *testing.T) {
	r, d, n, p, now := deviceBindingFixture(t)
	ready := *d.Status.NetworkWorkerRevision.PodReadyTime
	if err := ValidateDeviceWorkerBinding(r, d, n, p, now); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*cvk.CiscoDevice, *core.Node, *core.Pod){
		"replaced device":    func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { d.UID = "other" },
		"changed generation": func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { d.Generation++ },
		"different address":  func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { d.Spec.Address = "192.0.2.2" },
		"replaced node":      func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { n.UID = "other" },
		"different serial":   func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { n.Status.NodeInfo.MachineID = "OTHER" },
		"worker unobserved": func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) {
			d.Status.NetworkWorkerRevision.ObservedRevision = ""
		},
		"replaced pod":    func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { p.UID = "other" },
		"terminating pod": func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { p.DeletionTimestamp = &ready },
		"unready pod":     func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { p.Status.Conditions = nil },
		"wrong account":   func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) { p.Spec.ServiceAccountName = "other" },
		"wrong node binding": func(d *cvk.CiscoDevice, n *core.Node, p *core.Pod) {
			n.Annotations[managedprotocol.AnnotationDeviceUID] = "other"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dd, nn, pp := d.DeepCopy(), n.DeepCopy(), p.DeepCopy()
			mutate(dd, nn, pp)
			if ValidateDeviceWorkerBinding(r, dd, nn, pp, now) == nil {
				t.Fatal("accepted stale device binding")
			}
		})
	}
	scheme := runtime.NewScheme()
	_ = cvk.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(d, n, p).Build()
	if err := ReadDeviceWorkerBinding(context.Background(), c, r, now); err != nil {
		t.Fatal(err)
	}
	if ReadDeviceWorkerBinding(context.Background(), &changingDeviceReader{Reader: c}, r, now) == nil {
		t.Fatal("accepted binding assembled across a changed device")
	}
}

type changingDeviceReader struct {
	client.Reader
	deviceReads int
}

func (r *changingDeviceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if d, ok := obj.(*cvk.CiscoDevice); ok {
		r.deviceReads++
		if r.deviceReads > 1 {
			d.ResourceVersion = "changed"
		}
	}
	return nil
}
