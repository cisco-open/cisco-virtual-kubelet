// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type prunePreparedGroupClient struct{ ctrlclient.Client }
type prunePreparedGroupWriter struct{ ctrlclient.SubResourceWriter }

func (c prunePreparedGroupClient) Status() ctrlclient.SubResourceWriter {
	return prunePreparedGroupWriter{c.Client.Status()}
}
func (w prunePreparedGroupWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		copy := nc.DeepCopy()
		copy.Status.Configuration.Operation.PhysicalGroup = nil
		return w.SubResourceWriter.Update(ctx, copy, opts...)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestFabricPendingEventAtomicPrepare(t *testing.T) { testFabricAtomicGroupPrepare(t, "event") }
func TestFabricL2HandoffAtomicPrepare(t *testing.T)    { testFabricAtomicGroupPrepare(t, "l2") }
func TestFabricL3IPHandoffAtomicPrepare(t *testing.T)  { testFabricAtomicGroupPrepare(t, "l3") }
func TestFabricSDAHandoffAtomicPrepare(t *testing.T)   { testFabricAtomicGroupPrepare(t, "sda") }
func TestFabricPortAssignmentAtomicPrepare(t *testing.T) {
	testFabricAtomicGroupPrepare(t, "port-assignment")
}

func TestFabricPortChannelAtomicPrepare(t *testing.T) {
	testFabricAtomicGroupPrepare(t, "port-channel")
}
func testFabricAtomicGroupPrepare(t *testing.T, kind string) {
	for _, mode := range []string{"valid", "pruned", "empty", "missing", "other-resource", "wrong-action", "malformed-resource"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			targets := []contract.GroupTargetIdentity{{InventoryID: "device-id", PhysicalIdentity: "serial-a", Device: ops.ConfigurationReservationReference{Name: "a", UID: "device-a"}, Node: ops.ConfigurationReservationReference{Name: "a", UID: "node-a"}}}
			if mode == "empty" {
				targets = []contract.GroupTargetIdentity{}
			}
			if mode == "missing" {
				targets = nil
			}
			resource := pendingFabricEventResource("Global/Lab/Building", "building", fabricPendingEvent{ID: "event-id", FabricID: "fabric-id"})
			action := "Update"
			if kind != "event" {
				resource = "fabric-l2-handoff-create:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
				action = "Create"
				if kind == "l3" {
					resource = "fabric-l3-ip-handoff-create:" + strings.Repeat("a", 64)
				}
				if kind == "port-assignment" {
					resource = "fabric-port-assignment-create:" + strings.Repeat("a", 64)
				}
				if kind == "port-channel" {
					resource = "fabric-port-channel-create:" + strings.Repeat("a", 64)
				}
				if kind == "sda" {
					resource = "fabric-sda-handoff-create:" + strings.Repeat("a", 64)
				}
			}
			if mode == "wrong-action" {
				action = "Delete"
			}
			if mode == "malformed-resource" {
				if kind == "event" {
					t.Skip("handoff format check")
				}
				resource = strings.Split(resource, ":")[0] + ":invalid"
			}
			if mode == "other-resource" {
				resource = "ordinary"
			}
			op, err := a.prepareConfiguration(ctx, config, nil, "fabric", resource, action, configurationResourceIdentity{target: "event-id", parent: "fabric-id", groupTargets: targets})
			if (err == nil) != (mode == "valid") {
				t.Fatal("atomic prepare", err)
			}
			if mode != "valid" && mode != "pruned" {
				return
			}
			stored := journalState(t, a).Operation
			if stored == nil || stored.Phase != "Prepared" {
				t.Fatal("missing retained operation")
			}
			if mode == "valid" {
				if op == nil || contract.ValidateGroupJournalIdentity(stored) != nil {
					t.Fatal("group not stored atomically")
				}
				// Caller mutation must not alter the persisted target set.
				targets[0].Node.UID = "changed"
				if journalState(t, a).Operation.PhysicalGroup.Targets[0].Node.UID != "node-a" {
					t.Fatal("mutable group alias")
				}
			}
			// Even pruning cannot route the reserved action through legacy helpers.
			for _, err := range []error{a.startConfiguration(ctx, config, nil, stored.ID), a.advanceConfiguration(ctx, stored.ID, "Unknown", ""), a.finishConfiguration(ctx, stored.ID)} {
				if err == nil {
					t.Fatal("group bypass accepted")
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = "Submitting"
			at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			nc.Status.Configuration.Operation.SubmittedAt = &at
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := a.recordConfigurationCompleted(ctx, stored.ID); err == nil {
				t.Fatal("group accepted taskless synchronous completion")
			}
			if journalState(t, a).Operation.Phase != "Submitting" {
				t.Fatal("rejected completion changed the journal")
			}
		})
	}
}
