// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestConfigurationManagedDeviceBinding(t *testing.T) {
	target := Device{ID: "inventory", Serial: "SERIAL", ManagementIP: "198.51.100.101"}
	base := cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device"}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: target.ManagementIP, PhysicalIdentity: "Serial"}}
	base.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeName: "switch", NodeUID: "node", PhysicalIdentity: "serial"}
	for _, scenario := range []string{"valid", "namespace", "serial", "address", "uid", "node", "duplicate", "deleting", "driver"} {
		d := base.DeepCopy()
		switch scenario {
		case "namespace":
			d.Namespace = "other"
		case "serial":
			d.Spec.PhysicalIdentity = "other"
		case "address":
			d.Spec.Address = "198.51.100.103"
		case "uid":
			d.Status.NodeIdentity.DeviceUID = "replacement"
		case "node":
			d.Status.NodeIdentity.NodeName = "replacement"
		case "deleting":
			now := metav1.Now()
			d.DeletionTimestamp = &now
		case "driver":
			d.Spec.Driver = cvk.DeviceDriverNXOS
		}
		devices := []cvk.CiscoDevice{*d}
		if scenario == "duplicate" {
			other := d.DeepCopy()
			other.Name = "alias"
			other.Namespace = "other"
			devices = append(devices, *other)
		}
		got, err := configurationManagedDevice("lab", target, devices)
		if scenario != "valid" {
			if err == nil || got != nil {
				t.Fatal("accepted invalid managed binding", scenario)
			}
			continue
		}
		if err != nil || got.UID != base.UID {
			t.Fatal("valid binding rejected", err)
		}
		got.Status.NodeIdentity.NodeUID = "changed"
		if devices[0].Status.NodeIdentity.NodeUID != "node" {
			t.Fatal("returned aliased status")
		}
	}
}

func TestConfigurationManagedNodeRejectsReplacement(t *testing.T) {
	d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "device"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "SERIAL"}}
	d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeName: "node", NodeUID: "node-uid", PhysicalIdentity: "serial"}
	for _, scenario := range []string{"valid", "replacement", "missing-inventory", "conflicting-inventory", "wrong-device"} {
		device := d.DeepCopy()
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "node-uid"}}
		node.Status.NodeInfo.MachineID = "serial"
		node.Status.NodeInfo.SystemUUID = "SERIAL"
		switch scenario {
		case "replacement":
			node.UID = "replacement"
		case "missing-inventory":
			node.Status.NodeInfo = corev1.NodeSystemInfo{}
		case "conflicting-inventory":
			node.Status.NodeInfo.SystemUUID = "other"
		case "wrong-device":
			device.UID = "replacement"
		}
		err := configurationManagedNode(device, node)
		if (scenario == "valid") != (err == nil) {
			t.Fatal("incorrect live node decision", scenario, err)
		}
	}
}

func TestResolveConfigurationManagedBinding(t *testing.T) {
	target := Device{ID: "inventory", Serial: "SERIAL", ManagementIP: "198.51.100.101"}
	base := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device"}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: target.ManagementIP, PhysicalIdentity: "serial"}}
	base.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeName: "switch", NodeUID: "node", PhysicalIdentity: "serial"}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node"}}
	node.Status.NodeInfo.MachineID = "serial"
	for _, scenario := range []string{"valid", "missing-node", "replaced-node", "cross-namespace-alias", "changed-declaration"} {
		t.Run(scenario, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			objects := []ctrlclient.Object{base.DeepCopy()}
			n := node.DeepCopy()
			if scenario == "replaced-node" {
				n.UID = "replacement"
			}
			if scenario != "missing-node" {
				objects = append(objects, n)
			}
			if scenario == "cross-namespace-alias" {
				alias := base.DeepCopy()
				alias.Namespace = "other"
				alias.UID = "alias"
				objects = append(objects, alias)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			var apiReader ctrlclient.Reader = reader
			if scenario == "changed-declaration" {
				apiReader = changingBindingReader{Client: reader}
			}
			d, n, err := resolveConfigurationManagedBinding(context.Background(), apiReader, "lab", target)
			if scenario != "valid" {
				if err == nil || d != nil || n != nil {
					t.Fatal("invalid binding accepted")
				}
				return
			}
			if err != nil || d.UID != "device" || n.UID != "node" {
				t.Fatal("binding", err)
			}
		})
	}
}

type changingBindingReader struct{ ctrlclient.Client }

func (r changingBindingReader) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if err := r.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if _, ok := obj.(*corev1.Node); ok {
		var device cvk.CiscoDevice
		if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "switch"}, &device); err != nil {
			return err
		}
		device.Spec.Address = "198.51.100.103"
		return r.Client.Update(ctx, &device)
	}
	return nil
}
