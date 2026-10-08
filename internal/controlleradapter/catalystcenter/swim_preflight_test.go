// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestResolveSWIMTarget(t *testing.T) {
	d := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{UID: types.UID("uid-1")}, Spec: ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, Address: "192.0.2.10", PhysicalIdentity: "ABC123"}}
	items := []Device{{ID: "id-1", Serial: "abc123", ManagementIP: "192.0.2.10", Reachability: "Reachable"}}
	got, err := resolveSWIMTarget(d, items)
	if err != nil || got.ID != "id-1" {
		t.Fatalf("device=%+v err=%v", got, err)
	}
	cases := []struct {
		name   string
		change func(*ciskov1.CiscoDevice, *[]Device)
	}{
		{"missing serial", func(d *ciskov1.CiscoDevice, _ *[]Device) { d.Spec.PhysicalIdentity = "" }},
		{"wrong address", func(d *ciskov1.CiscoDevice, _ *[]Device) { d.Spec.Address = "192.0.2.11" }},
		{"unreachable", func(_ *ciskov1.CiscoDevice, items *[]Device) { (*items)[0].Reachability = "Unreachable" }},
		{"duplicate", func(_ *ciskov1.CiscoDevice, items *[]Device) { *items = append(*items, (*items)[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyDevice := d.DeepCopy()
			copyItems := append([]Device(nil), items...)
			tc.change(copyDevice, &copyItems)
			if _, err := resolveSWIMTarget(copyDevice, copyItems); err == nil {
				t.Fatal("expected preflight failure")
			}
		})
	}
}

func TestResolveSWIMImage(t *testing.T) {
	images := []Image{{ID: "image-1"}}
	if _, err := resolveSWIMImage("image-1", images); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSWIMImage("other", images); err == nil {
		t.Fatal("expected unknown image rejection")
	}
	if _, err := resolveSWIMImage("image-1", append(images, images[0])); err == nil {
		t.Fatal("expected duplicate image rejection")
	}
}
