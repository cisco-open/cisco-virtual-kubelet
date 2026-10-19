// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWiredBulkManagedIdentityBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "missing-device", "alias", "wrong-address", "wrong-driver", "node-replaced", "guard-address-race", "guard-node-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, members, f := newWiredBulkFixture(t)
			objects := []ctrlclient.Object{}
			for i, name := range []string{"a", "b"} {
				serial := "serial-" + name
				address := "198.51.100.100"
				if i == 1 {
					address = "198.51.100.101"
				}
				device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: types.UID(name + "-device"), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: address, PhysicalIdentity: serial}}
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: name, NodeUID: name + "-node", PhysicalIdentity: serial}
				node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-node")}}
				node.Status.NodeInfo.MachineID = serial
				if i == 1 {
					switch mode {
					case "missing-device":
						objects = append(objects, node)
						continue
					case "wrong-address":
						device.Spec.Address = "198.51.100.103"
					case "wrong-driver":
						device.Spec.Driver = cvk.DeviceDriverNXOS
					case "node-replaced":
						node.UID = "replacement"
					case "alias":
						alias := device.DeepCopy()
						alias.Namespace = "other"
						alias.Name = "alias"
						alias.UID = "alias"
						objects = append(objects, alias)
					}
				}
				objects = append(objects, device, node)
			}
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := core.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			guard := func(_ context.Context, physical []Device) error {
				if len(physical) != 2 {
					t.Fatal("guard lost member")
				}
				if mode == "guard-address-race" {
					var device cvk.CiscoDevice
					if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "b"}, &device); err != nil {
						t.Fatal(err)
					}
					device.Spec.Address = "198.51.100.103"
					if err := api.Update(ctx, &device); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "guard-node-race" {
					var node core.Node
					if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "b"}, &node); err != nil {
						t.Fatal(err)
					}
					if err := api.Delete(ctx, &node); err != nil {
						t.Fatal(err)
					}
					node.ResourceVersion = ""
					node.UID = "replacement"
					if err := api.Create(ctx, &node); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			write, targets, err := c.planManagedWiredBulkProvision(ctx, api, "lab", "Global/Lab", members, guard)
			if mode != "valid" && mode != "guard-address-race" && mode != "guard-node-race" {
				if err == nil || write != nil || targets != nil || f.calls != 0 {
					t.Fatal("unsafe group returned partial evidence")
				}
				return
			}
			if err != nil || write == nil || len(targets) != 2 || len(write.identity.groupTargets) != 2 {
				t.Fatal("managed group not bound", err)
			}
			_, err = write.submit(ctx)
			if mode != "valid" {
				if err == nil || f.calls != 0 {
					t.Fatal("stale managed group submitted")
				}
				return
			}
			if err != nil || f.calls != 1 {
				t.Fatal("managed batch not dispatched", err)
			}
		})
	}
}
