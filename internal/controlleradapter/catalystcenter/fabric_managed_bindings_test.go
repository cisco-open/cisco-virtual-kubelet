// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

type fabricBindingReader struct {
	ctrlclient.Reader
	mode        string
	lists, gets int
}

func (r *fabricBindingReader) List(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	r.lists++
	if err := r.Reader.List(ctx, list, opts...); err != nil {
		return err
	}
	rows := list.(*cvk.CiscoDeviceList)
	if r.lists == 2 {
		switch r.mode {
		case "late-alias":
			alias := *rows.Items[0].DeepCopy()
			alias.Name = "alias"
			alias.Namespace = "other"
			alias.UID = "alias"
			rows.Items = append(rows.Items, alias)
		case "late-device-change":
			rows.Items[0].ResourceVersion = "changed"
		}
	}
	return nil
}
func (r *fabricBindingReader) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	r.gets++
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if r.mode == "late-node-change" && r.gets > 2 {
		obj.SetUID("replacement")
	}
	return nil
}
func TestFabricManagedBindings(t *testing.T) {
	for _, mode := range []string{"valid", "missing-node", "unmanaged-member", "wrong-namespace", "duplicate-serial", "duplicate-id", "late-alias", "late-device-change", "late-node-change", "empty"} {
		t.Run(mode, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			targets := []Device{}
			objects := []ctrlclient.Object{}
			for _, name := range []string{"b", "a"} {
				ip := "198.51.100.101"
				if name == "b" {
					ip = "198.51.100.102"
				}
				targets = append(targets, Device{ID: name, Serial: "serial-" + name, ManagementIP: ip})
				device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab", UID: types.UID("device-" + name)}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, PhysicalIdentity: "serial-" + name, Address: ip}}
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-" + name, NodeName: name, NodeUID: "node-" + name, PhysicalIdentity: "serial-" + name}
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("node-" + name)}}
				node.Status.NodeInfo.MachineID = "serial-" + name
				if mode == "wrong-namespace" && name == "b" {
					device.Namespace = "other"
				}
				if mode != "unmanaged-member" || name != "b" {
					objects = append(objects, device)
				}
				if mode != "missing-node" || name != "b" {
					objects = append(objects, node)
				}
			}
			if mode == "duplicate-serial" {
				targets[1].Serial = targets[0].Serial
			}
			if mode == "duplicate-id" {
				targets[1].ID = targets[0].ID
			}
			if mode == "empty" {
				targets = nil
			}
			reader := &fabricBindingReader{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(), mode: mode}
			got, err := resolveConfigurationManagedBindings(context.Background(), reader, "lab", targets)
			valid := mode == "valid" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal("binding", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial group binding")
				}
				return
			}
			if mode == "empty" {
				if len(got) != 0 || reader.lists != 0 || reader.gets != 0 {
					t.Fatal("empty target work")
				}
				return
			}
			if len(got) != 2 || got[0].inventory.ID != "a" || reader.lists != 2 || reader.gets != 4 {
				t.Fatal("group shape/read scaling", len(got), reader.lists, reader.gets)
			}
			groupTargets, err := configurationGroupTargets("lab", got)
			if err != nil || len(groupTargets) != 2 || groupTargets[0].InventoryID != "a" || groupTargets[0].Device.UID != "device-a" || groupTargets[0].Node.UID != "node-a" {
				t.Fatal("group identity projection", err)
			}
			if _, err := configurationGroupTargets("other", got); err == nil {
				t.Fatal("cross-namespace group accepted")
			}
			for _, binding := range got {
				if string(binding.device.UID) != fmt.Sprint("device-", binding.inventory.ID) || string(binding.node.UID) != fmt.Sprint("node-", binding.inventory.ID) {
					t.Fatal("incorrect managed identities")
				}
			}
		})
	}
}
