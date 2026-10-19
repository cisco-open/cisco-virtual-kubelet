// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Actual native APIs with simulated managed identities. Deliberate guard denial
// proves no write; this is neither installed actor nor accepted batch proof.
func TestLiveCatalystCenterWiredBulkSequencePlanning(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	members, before, allExisting := liveWiredBulkMembers(t, c, ctx)
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	declared := []string{}
	for _, site := range sites {
		declared = append(declared, site.NameHierarchy)
	}
	intent := inventoryIntent{}
	states := map[string]string{}
	objects := []ctrlclient.Object{}
	for i, member := range members {
		if member.desired.reference.Name == "" {
			t.Skip("native devices have no named selectors for resolved-source grouping")
		}
		if allExisting {
			member.state = "REPROVISION"
		}
		intent.devices = append(intent.devices, member.desired)
		states[member.desired.reference.Name] = member.state
		native, err := resolveConfigurationDevice(member.desired.reference, inventory)
		if err != nil {
			t.Fatal(err)
		}
		physical, err := topologyidentity.CanonicalPhysicalIdentity(native.Serial)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("bulk-%d", i)
		deviceUID := name + "-device"
		nodeUID := name + "-node"
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: types.UID(deviceUID), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: native.ManagementIP, PhysicalIdentity: physical}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: deviceUID, NodeName: name, NodeUID: nodeUID, PhysicalIdentity: physical}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(nodeUID)}}
		node.Status.NodeInfo.MachineID = physical
		objects = append(objects, device, node)
	}
	schema := runtime.NewScheme()
	if err := cvk.AddToScheme(schema); err != nil {
		t.Fatal(err)
	}
	if err := core.AddToScheme(schema); err != nil {
		t.Fatal(err)
	}
	api := fake.NewClientBuilder().WithScheme(schema).WithObjects(objects...).Build()
	denied := errors.New("qualification deliberately refuses all-member dispatch")
	for _, option := range []wiredBulkGrouping{{}, {root: "Global"}, {managedSites: []string{"Global"}}} {
		batches, err := groupWiredBulkMembers(intent, states, declared, option)
		if err != nil {
			t.Fatal(err)
		}
		guards := 0
		planned, err := c.planManagedWiredBulkSequence(ctx, api, "lab", batches, func(context.Context, wiredBulkBatch, []Device) error { guards++; return denied })
		if err != nil || len(planned) == 0 || guards != 0 {
			t.Fatal("live sequence preflight failed or dispatched", err)
		}
		var selected *configurationWrite
		for _, entry := range planned {
			if entry.write != nil {
				selected = entry.write
				break
			}
		}
		if selected == nil {
			t.Fatal("qualification lacks a pending guarded batch")
		}
		if _, err := selected.submit(ctx); !errors.Is(err, denied) || guards != 1 {
			t.Fatal("native sequence dispatch was not refused", err)
		}
		if _, err := selected.submit(ctx); err == nil || guards != 1 {
			t.Fatal("refused sequence closure replayed")
		}
		t.Logf("live grouped sequence batches=%d declared_members=%d passed all-batch preflight and one-use dispatch refusal; managed identities simulated", len(planned), len(members))
	}
	fresh, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		native, err := resolveConfigurationDevice(member.desired.reference, fresh)
		if err != nil {
			t.Fatal(err)
		}
		baseline, bound := before[native.ID]
		record, _, err := c.wiredProvisionedDevice(ctx, native.ID)
		if err != nil || !bound || record != baseline {
			t.Fatal("native provisioning identity changed during qualification", err)
		}
	}
	t.Log("all three lab identities/provisioning records unchanged; no native provisioning writes")
}
