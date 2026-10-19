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

func TestWiredBulkSequencePreflightsAllBatches(t *testing.T) {
	for _, mode := range []string{"sequence", "later-unmanaged", "cross-batch-alias", "later-native-race", "later-node-race", "missing-guard"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, members, f := newWiredBulkFixture(t)
			// Source input is b,a. The planned sequence below intentionally dispatches
			// a then b; each native snapshot still binds canonical physical identities.
			batches := []wiredBulkBatch{{path: "Global/Lab", members: []wiredBulkMember{members[1]}}, {path: "Global/Lab", members: []wiredBulkMember{members[0]}}}
			if mode == "cross-batch-alias" {
				batches[1].members[0] = members[1]
			}
			objects := []ctrlclient.Object{}
			for i, name := range []string{"a", "b"} {
				address := "198.51.100.100"
				if i == 1 {
					address = "198.51.100.101"
				}
				serial := "serial-" + name
				device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: types.UID(name + "-device"), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: address, PhysicalIdentity: serial}}
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: name, NodeUID: name + "-node", PhysicalIdentity: serial}
				node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-node")}}
				node.Status.NodeInfo.MachineID = serial
				if mode != "later-unmanaged" || i == 0 {
					objects = append(objects, device)
				}
				objects = append(objects, node)
			}
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := core.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			api := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			guards := 0
			guard := wiredBulkBatchGuard(func(_ context.Context, batch wiredBulkBatch, physical []Device) error {
				guards++
				if len(batch.members) != 1 || len(physical) != 1 {
					t.Fatal("guard scope differs from selected shard")
				}
				if mode == "later-native-race" {
					f.present["b"] = "foreign"
				}
				if mode == "later-node-race" {
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
			})
			if mode == "missing-guard" {
				guard = nil
			}
			planned, err := c.planManagedWiredBulkSequence(ctx, api, "lab", batches, guard)
			if mode == "later-unmanaged" || mode == "cross-batch-alias" || mode == "missing-guard" {
				if err == nil || planned != nil || f.calls != 0 || guards != 0 {
					t.Fatal("later invalid batch exposed earlier dispatch")
				}
				return
			}
			if err != nil || len(planned) != 2 || planned[0].write == nil || planned[1].write == nil || f.calls != 0 || guards != 0 {
				t.Fatal("sequence planning dispatched or lost batch", err)
			}
			_, err = planned[0].write.submit(ctx)
			if mode == "later-native-race" || mode == "later-node-race" {
				if err == nil || f.calls != 0 {
					t.Fatal("later batch change permitted earlier write")
				}
				return
			}
			if err != nil || f.calls != 1 || f.payload[0].DeviceID != "a" {
				t.Fatal("first batch not submitted", err)
			}
			// A stale plan cannot be reused after one native mutation. The journal
			// owner must settle that operation, then rebuild the complete sequence.
			if _, err := planned[1].write.submit(ctx); err == nil || f.calls != 1 {
				t.Fatal("old whole-plan snapshot permitted next batch")
			}
			fresh, err := c.planManagedWiredBulkSequence(ctx, api, "lab", batches, guard)
			if err != nil || len(fresh) != 2 || fresh[0].write != nil || fresh[1].write == nil {
				t.Fatal("replan did not preserve converged first batch", err)
			}
			if _, err := fresh[1].write.submit(ctx); err != nil || f.calls != 2 || f.payload[0].DeviceID != "b" {
				t.Fatal("replanned second batch not submitted", err)
			}
			final, err := c.planManagedWiredBulkSequence(ctx, api, "lab", batches, nil)
			if err != nil || final[0].write != nil || final[1].write != nil {
				t.Fatal("converged sequence requested mutation authority", err)
			}
		})
	}
}
