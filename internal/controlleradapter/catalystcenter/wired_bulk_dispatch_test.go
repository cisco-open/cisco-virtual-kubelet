// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Native HTTPS plus the existing shared lease/claim/worker fixtures. No live
// actor or switch is mutated by this test.
func wiredBulkJournalFixture(t *testing.T, create, prepared bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, []configurationDispatchFixtureMember, *wiredBulkFixture) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	c, batchMembers, f := newWiredBulkFixture(t)
	a.client = c
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"REPROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	f.present["a"], f.present["b"] = "old-a", "old-b"
	for i := range batchMembers {
		batchMembers[i].state = "REPROVISION"
	}
	if create {
		config.Spec.Source.Inline.Raw = []byte(strings.ReplaceAll(string(config.Spec.Source.Inline.Raw), "REPROVISION", "PROVISION"))
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		f.present = map[string]string{}
		for i := range batchMembers {
			batchMembers[i].state = "PROVISION"
		}
	}
	fixtureMembers := []configurationDispatchFixtureMember{}
	for i, name := range []string{"a", "b"} {
		address := "198.51.100.100"
		if i == 1 {
			address = "198.51.100.101"
		}
		serial := "serial-" + name
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: name, UID: types.UID(name + "-device"), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: address, PhysicalIdentity: serial}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: name, NodeUID: name + "-node", PhysicalIdentity: serial}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-node")}}
		node.Status.NodeInfo.MachineID = serial
		for _, object := range []ctrlclient.Object{device, node} {
			if err := a.statusWriter.Create(ctx, object); err != nil {
				t.Fatal(err)
			}
		}
		fixtureMembers = append(fixtureMembers, configurationDispatchFixtureMember{device: device, node: node})
	}
	snapshot, targets, err := c.wiredBulkTargets(ctx, a.statusReader, config.Namespace, "Global/Lab", batchMembers)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: snapshot.resource(snapshot.mask), Action: "Update", TargetID: snapshot.anchor, ParentID: snapshot.anchor, Phase: "Prepared"}
	if create {
		base.Action = "Create"
	}
	op, err := contract.WithGroupIdentity(base, targets)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
	if !prepared {
		nc.Status.Configuration.Operation = nil
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
	return a, config, source, fixtureMembers, f
}

func wiredBulkDispatchFixture(t *testing.T, create bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, *contract.GroupSubmissionPermit, func(), *wiredBulkFixture) {
	t.Helper()
	a, config, source, members, f := wiredBulkJournalFixture(t, create, true)
	permit, revoke := configurationGroupDispatchPermitFixture(t, a, members)
	return a, config, source, permit, revoke, f
}

func TestWiredBulkDispatchUsesSharedPermit(t *testing.T) {
	for _, mode := range []string{"accepted", "create", "partial-create", "partial", "lost", "denied", "missing-permit", "receipt-revoked", "changed-source", "replaced-device", "foreign-owner", "pruned-group", "changed-controller-after-claim", "late-device-replacement", "late-source-change"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, permit, revoke, f := wiredBulkDispatchFixture(t, mode == "create" || mode == "partial-create")
			switch mode {
			case "late-device-replacement", "late-source-change":
				f.beforeInventory = func() {
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					if nc.Status.Configuration.Operation.Phase != "Submitting" {
						return
					}
					f.beforeInventory = nil
					if mode == "late-device-replacement" {
						f.serialB = "REPLACED"
						return
					}
					fresh := config.DeepCopy()
					fresh.Generation++
					if err := a.statusWriter.Update(ctx, fresh); err != nil {
						t.Error(err)
					}
				}
			case "changed-controller-after-claim":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: "replacement", DeploymentName: "replacement", ReadyReplicas: 1}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "partial", "partial-create":
				f.partial = true
			case "lost":
				f.lost = true
			case "denied":
				revoke()
			case "missing-permit":
				permit = nil
			case "receipt-revoked":
				f.afterMutation = revoke
			case "changed-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "replaced-device":
				f.serialB = "REPLACED"
			case "foreign-owner", "pruned-group":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "foreign-owner" {
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				} else {
					nc.Status.Configuration.Operation.PhysicalGroup = nil
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			task, err := a.dispatchWiredBulkProvision(ctx, config, source, permit)
			accepted := mode == "accepted" || mode == "create" || mode == "partial-create" || mode == "partial"
			if (err == nil) != accepted {
				t.Fatal("dispatch outcome", err)
			}
			uncertain := mode == "lost" || mode == "receipt-revoked"
			lateRefusal := mode == "late-device-replacement" || mode == "late-source-change"
			expectedCalls := 0
			if accepted || uncertain {
				expectedCalls = 1
			}
			if f.calls != expectedCalls {
				t.Fatal("native dispatch count", f.calls)
			}
			op := journalState(t, a).Operation
			if accepted {
				if op.Phase != "Accepted" || op.TaskID != "batch-task" || task.ID != "batch-task" {
					t.Fatal("native acknowledgement not persisted")
				}
			} else if uncertain || lateRefusal {
				if op.Phase != "Submitting" || op.TaskID != "" || op.SubmittedAt == nil {
					t.Fatal("uncertain acceptance fence lost")
				}
				if mode == "receipt-revoked" && task.ID != "batch-task" {
					t.Fatal("known task discarded after receipt refusal")
				}
			} else if op.Phase != "Prepared" || op.SubmittedAt != nil {
				t.Fatal("refused dispatch advanced journal")
			}
			// A partial native operation is acknowledgement, not completion. Neither
			// it nor a replay after a simulated process restart can release the group.
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(nc.Status.Configuration.ActionApplications) != 0 {
				t.Fatal("dispatch fabricated verified receipt")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if len(reservations.Items) != 2 {
				t.Fatal("dispatch changed the retained member set")
			}
			for _, member := range reservations.Items {
				if member.Status.Claim == nil || member.Status.Grant == nil || member.Status.Grant.Phase == "Released" || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil {
					t.Fatal("dispatch released or completed a retained member")
				}
			}
			restarted := *a
			if _, err := restarted.dispatchWiredBulkProvision(ctx, config, source, permit); err == nil || f.calls != expectedCalls {
				t.Fatal("restart replayed native request")
			}
			expectedMethod := "PUT"
			if mode == "create" || mode == "partial-create" {
				expectedMethod = "POST"
			}
			if expectedCalls == 1 && f.method != expectedMethod {
				t.Fatal("wrong provisioning method", f.method)
			}
		})
	}
}
