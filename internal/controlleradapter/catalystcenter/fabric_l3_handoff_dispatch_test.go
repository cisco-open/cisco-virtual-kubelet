// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testFabricL3IPHandoffDispatch(t *testing.T, native *client, key fabricL3IPHandoffIntent, mode string, afterPost *func()) {
	t.Helper()
	ctx := context.Background()
	recovery := strings.HasPrefix(mode, "dispatch-recovery-")
	a, config := journalFixture(t)
	a.client = native
	config.Spec.ManagedSections = []string{"fabric"}
	config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"border","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"border","border_types":["LAYER_3"],"local_autonomous_system_number":65000,"default_exit":true,"import_external_routes":true,"external_handoff_pool":"access","l3_handoffs":[{"name":"Transit","interfaces":[{"name":"GigabitEthernet1/0/1","virtual_networks":[{"name":"Users","vlan":300}]}]}]}]}}}`)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "border", UID: "device-uid"}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: "198.51.100.101", PhysicalIdentity: "foc12345678"}}
	device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-uid", NodeName: "border", NodeUID: "node-uid", PhysicalIdentity: "foc12345678"}
	node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "border", UID: "node-uid"}}
	node.Status.NodeInfo.MachineID = "foc12345678"
	for _, obj := range []ctrlclient.Object{device, node} {
		if err := a.statusWriter.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "dispatch-recovery-public" {
		testPublicHandoffPreparation(t, a, config)
	} else if _, err := a.prepareFabricL3IPHandoff(ctx, config, source, key); err != nil {
		t.Fatal(err)
	}
	permit, revoke := configurationGroupDispatchFixture(t, a, []configurationDispatchFixtureMember{{device, node}}, mode != "dispatch-recovery-public")
	if mode == "dispatch-denied" {
		revoke()
	}
	if mode == "dispatch-revoked" {
		*afterPost = revoke
	}
	var task Task
	if mode == "dispatch-recovery-public" {
		installFabricGroupAuthorityFixture(t, a)
		task = testPublicHandoffDispatch(t, a, config)
		a.configurationAdmission = nil
	} else {
		task, err = a.dispatchFabricL3IPHandoff(ctx, config, source, key, permit)
	}
	if (err == nil) != (mode == "dispatch-valid" || recovery) {
		t.Fatal("dispatch result", err)
	}
	stored := journalState(t, a).Operation
	if mode == "dispatch-valid" || recovery {
		if stored.Phase != "Accepted" || stored.TaskID != "task" || task.ID != "task" {
			t.Fatal("accepted task was not retained")
		}
	} else if mode == "dispatch-denied" {
		if stored.Phase != "Prepared" || stored.SubmittedAt != nil {
			t.Fatal("denied grant advanced the operation")
		}
	} else if stored.Phase != "Submitting" || stored.TaskID != "" {
		t.Fatal("uncertain submission was cleared")
	}
	if mode == "dispatch-revoked" && task.ID != "task" {
		t.Fatal("known native task was lost after receipt rejection")
	}
	restarted := *a
	if _, err := restarted.dispatchFabricL3IPHandoff(ctx, config, source, key, permit); err == nil {
		t.Fatal("restarted dispatch replayed")
	}

	if recovery {
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		expectedPhase := "Accepted"
		if mode == "dispatch-recovery-unknown" {
			current.Status.Configuration.Operation.Phase = "Unknown"
			expectedPhase = "Unknown"
		}
		if mode == "dispatch-recovery-no-task" {
			current.Status.Configuration.Operation.TaskID = ""
		}
		if err := a.statusWriter.Status().Update(ctx, current); err != nil {
			t.Fatal(err)
		}
		if mode == "dispatch-recovery-node" {
			if err := a.statusWriter.Delete(ctx, node); err != nil {
				t.Fatal(err)
			}
		}
		var list ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &list); err != nil {
			t.Fatal(err)
		}
		members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
		for i := range list.Items {
			members[i] = list.Items[i].DeepCopy()
		}
		if strings.HasPrefix(mode, "dispatch-recovery-public") {
			testPublicHandoffRecovery(t, &restarted, config, members, mode)
			return
		}

		err = restarted.beginFabricL3IPHandoffVerification(ctx, config, source, key, members, func(context.Context, []*ops.ControllerConfigurationReservation) error {
			if mode == "dispatch-recovery-denied" {
				return errors.New("fixture writer denied")
			}
			return nil
		}, time.Now())
		successful := mode == "dispatch-recovery-valid" || mode == "dispatch-recovery-unknown" || strings.HasPrefix(mode, "dispatch-recovery-publish-")
		if (err == nil) != successful {
			t.Fatal("recovery outcome", err)
		}
		if successful {
			expectedPhase = "Verifying"
		}
		state := journalState(t, a)
		if state.Operation.Phase != expectedPhase || len(state.ActionApplications) != 0 {
			t.Fatal("recovery changed completion or lost uncertainty")
		}
		if err := a.statusReader.List(ctx, &list); err != nil {
			t.Fatal(err)
		}
		for _, member := range list.Items {
			if member.Status.Completion != nil || member.Status.Grant.Phase != "Held" {
				t.Fatal("recovery released a reservation")
			}
		}
		if successful {
			if mode == "dispatch-recovery-valid" {
				testFabricL3IPHandoffApplicationPersistence(t, native, state.Operation.DeepCopy(), key, time.Now())
			}
			publication := restarted
			originalWriter := publication.statusWriter
			if mode == "dispatch-recovery-publish-lost" {
				publication.statusWriter = lostJournalAckClient{originalWriter}
			}
			retainedMembers := members
			if mode == "dispatch-recovery-publish-incomplete" {
				retainedMembers = nil
			}
			err := publication.recordFabricL3IPHandoffApplication(ctx, config, state.Operation.ID, key, retainedMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "dispatch-recovery-publish-denied" {
					return errors.New("fixture publication denied")
				}
				return nil
			}, time.Now())
			published := mode != "dispatch-recovery-publish-denied" && mode != "dispatch-recovery-publish-incomplete"
			acknowledged := published && mode != "dispatch-recovery-publish-lost"
			if (err == nil) != acknowledged {
				t.Fatal("publication outcome", err)
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if published {
				if current.Status.Configuration.Operation.Phase != "Verified" || len(current.Status.Configuration.ActionApplications) != 1 {
					t.Fatal("receipt was not atomic with verification")
				}
				if _, err := contract.GroupCompletionsForReceipt(current, members, time.Now()); err != nil {
					t.Fatal("receipt cannot derive group completion", err)
				}
				publication.statusWriter = originalWriter
				if err := publication.recordFabricL3IPHandoffApplication(ctx, config, state.Operation.ID, key, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err == nil {
					t.Fatal("restart repeated publication")
				}
			} else if current.Status.Configuration.Operation.Phase != "Verifying" || len(current.Status.Configuration.ActionApplications) != 0 {
				t.Fatal("rejected publication changed the journal")
			}
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			for _, member := range list.Items {
				if member.Status.Completion != nil || member.Status.Grant.Phase != "Held" {
					t.Fatal("publication released the group")
				}
			}
			if mode == "dispatch-recovery-valid" {
				testFabricHandoffCompletion(t, &publication, config)
			}
		}

	}
}
