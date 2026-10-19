// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func sdaTransitJournalFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, sdaTransitIntent, *sdaTransitFixture, []configurationDispatchFixtureMember) {
	t.Helper()
	ctx := context.Background()
	c, want, f, api := newSDATransitFixture(t)
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"fabric"}
	config.Spec.Source.Inline.Raw = sdaTransitTestModel(map[string]any{"name": want.name, "type": want.kind, "control_plane_devices": want.controlPlanes, "multicast_over_sda_transit": *want.multicast})
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.client = c
	var devices cvk.CiscoDeviceList
	if err := api.List(ctx, &devices); err != nil {
		t.Fatal(err)
	}
	members := []configurationDispatchFixtureMember{}
	for _, entry := range devices.Items {
		d := entry.DeepCopy()
		d.ResourceVersion = ""
		var node corev1.Node
		if err := api.Get(ctx, ctrlclient.ObjectKey{Name: d.Status.NodeIdentity.NodeName}, &node); err != nil {
			t.Fatal(err)
		}
		node.ResourceVersion = ""
		if err := a.statusWriter.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Create(ctx, &node); err != nil {
			t.Fatal(err)
		}
		if d.Name != "peer" {
			members = append(members, configurationDispatchFixtureMember{d, node.DeepCopy()})
		}
	}
	return a, config, want, f, members
}

func TestSDATransitJournalPreparation(t *testing.T) {
	for _, mode := range []string{"valid", "source-mismatch", "missing-member", "foreign-owner", "pruned", "bad-action", "bad-target", "missing-group", "bad-resource"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, want, f, members := sdaTransitJournalFixture(t)
			if mode == "source-mismatch" {
				want.name = "Other"
			}
			if mode == "missing-member" {
				if err := a.statusWriter.Delete(ctx, members[0].device); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "foreign-owner" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: "other", ConfigUID: "other"}}}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			var op *cvk.NetworkControllerConfigurationOperation
			var err error
			if strings.HasPrefix(mode, "bad-") || mode == "missing-group" {
				s, targets, e := a.client.sdaTransitTargets(ctx, a.statusReader, config.Namespace, want)
				if e != nil {
					t.Fatal(e)
				}
				resource, action, target := sdaTransitResource(s), "Create", s.digest
				if mode == "bad-action" {
					action = "Update"
				}
				if mode == "bad-target" {
					target = strings.Repeat("a", 64)
				}
				if mode == "missing-group" {
					targets = nil
				}
				if mode == "bad-resource" {
					resource += ":extra"
				}
				op, err = a.prepareConfiguration(ctx, config, nil, "fabric", resource, action, configurationResourceIdentity{target: target, groupTargets: targets})
			} else {
				op, err = a.prepareSDATransit(ctx, config, nil, want)
			}
			if mode == "valid" {
				if err != nil || op == nil || op.ParentID != "" || len(op.TargetID) != 64 || len(op.PhysicalGroup.Targets) != 2 || contract.ValidateGroupJournalIdentity(op) != nil {
					t.Fatal("missing atomic group preparation", err)
				}
				if err := a.revalidateSDATransitOperation(ctx, config, nil, want, op, "Prepared"); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || op != nil {
				t.Fatal("invalid preparation accepted", err)
			}
			if f.posts != 0 {
				t.Fatal("preparation submitted native request")
			}
			if mode == "pruned" {
				stored := journalState(t, a).Operation
				if stored == nil || stored.Phase != "Prepared" || stored.PhysicalGroup != nil {
					t.Fatal("pruned journal was cleared")
				}
			}
		})
	}
}

func TestSDATransitPermitDispatchAndRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown-task", "public-recovery", "public-unknown", "lost", "nil-permit", "revoked-before", "revoked-after", "source-before", "owner-before", "node-before", "failed-child", "running-child", "missing-readback", "source-after", "denied-verification"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, want, f, physical := sdaTransitJournalFixture(t)
			op, err := a.prepareSDATransit(ctx, config, nil, want)
			if err != nil {
				t.Fatal(err)
			}
			permit, revoke := configurationGroupDispatchPermitFixture(t, a, physical)
			if mode == "lost" {
				f.lost = true
			}
			if mode == "revoked-before" {
				revoke()
			}
			if mode == "revoked-after" {
				f.afterPost = revoke
			}
			if mode == "nil-permit" {
				permit = nil
			}
			if mode == "source-before" {
				config.Generation++
				config.Spec.Source.Inline.Raw = sdaTransitTestModel(map[string]any{"name": "Other", "type": want.kind, "control_plane_devices": want.controlPlanes})
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "owner-before" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "node-before" {
				n := physical[0].node.DeepCopy()
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(n), n); err != nil {
					t.Fatal(err)
				}
				n.UID = "replacement"
				if err := a.statusWriter.Update(ctx, n); err != nil {
					t.Fatal(err)
				}
			}
			task, err := a.dispatchSDATransit(ctx, config, nil, want, permit)
			early := mode == "nil-permit" || mode == "revoked-before" || mode == "source-before" || mode == "owner-before" || mode == "node-before"
			if early {
				if err == nil || f.posts != 0 || journalState(t, a).Operation.Phase != "Prepared" {
					t.Fatal("early refusal changed submission boundary", err)
				}
				return
			}
			if mode == "lost" || mode == "revoked-after" {
				if err == nil || f.posts != 1 || journalState(t, a).Operation.Phase != "Submitting" {
					t.Fatal("uncertainty not retained", err)
				}
				if mode == "revoked-after" && task.ID != "task" {
					t.Fatal("known task discarded")
				}
				if _, e := a.dispatchSDATransit(ctx, config, nil, want, permit); e == nil || f.posts != 1 {
					t.Fatal("uncertain dispatch replayed")
				}
				return
			}
			if err != nil || task.ID != "task" || f.posts != 1 || journalState(t, a).Operation.Phase != "Accepted" {
				t.Fatal("dispatch not acknowledged", err)
			}
			if _, e := a.dispatchSDATransit(ctx, config, nil, want, permit); e == nil || f.posts != 1 {
				t.Fatal("acknowledged dispatch replayed")
			}
			if mode == "unknown-task" || mode == "public-unknown" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Operation.Phase = "Unknown"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "failed-child" {
				f.taskMode = "failed"
			}
			if mode == "running-child" {
				f.taskMode = "running"
			}
			if mode == "missing-readback" {
				f.transits = []fabricTransit{}
			}
			if mode == "source-after" {
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
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
			before := journalState(t, a).Operation.DeepCopy()
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied-verification" {
					return errors.New("denied")
				}
				return nil
			}
			restarted := *a
			if mode == "public-recovery" || mode == "public-unknown" {
				restarted.workerPodName, restarted.workerPodUID = "connector", "connector-uid"
				restarted.configurationAdmission = &configurationAdmission{protocol: contract.GroupReservationProtocol, leaseNamespace: "leases", authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
				for iteration := 0; iteration < 3; iteration++ {
					nc, e := restarted.currentController(ctx)
					if e != nil {
						t.Fatal(e)
					}
					status := &configapi.NetworkControllerConfigStatus{}
					_, pending, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, status)
					if !pending || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
						t.Fatal("public group recovery not waiting", reason)
					}
					if iteration == 0 && reason != "ApplyProgressing" || iteration > 0 && reason != "GroupDeviceEvidencePending" {
						t.Fatal("incorrect public convergence boundary", reason)
					}
				}
			} else {
				err = restarted.beginSDATransitVerification(ctx, config, nil, want, members, authorize, time.Now())
			}
			positive := mode == "accepted" || mode == "unknown-task" || mode == "public-recovery" || mode == "public-unknown"
			if positive {
				if err != nil || journalState(t, a).Operation.Phase != "Verifying" {
					t.Fatal("acknowledged task did not recover", err)
				}
			} else if err == nil || !reflect.DeepEqual(before, journalState(t, a).Operation) {
				t.Fatal("failed evidence advanced journal", err)
			}
			state := journalState(t, a)
			if state.Operation.ID != op.ID || len(state.ActionApplications) != 0 || f.posts != 1 {
				t.Fatal("recovery replaced operation, published receipt or replayed")
			}
			if err := a.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("legacy finish released group", err)
			}
		})
	}
}

func TestSDATransitPublicUnknownWithoutTaskRetainsFence(t *testing.T) {
	ctx := context.Background()
	a, config, want, f, physical := sdaTransitJournalFixture(t)
	if _, err := a.prepareSDATransit(ctx, config, nil, want); err != nil {
		t.Fatal(err)
	}
	permit, _ := configurationGroupDispatchPermitFixture(t, a, physical)
	f.lost = true
	if _, err := a.dispatchSDATransit(ctx, config, nil, want, permit); err == nil || f.posts != 1 {
		t.Fatal("lost acknowledgement not retained", err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.Phase = "Unknown"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	before := journalState(t, a).Operation.DeepCopy()
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	a.configurationAdmission = &configurationAdmission{protocol: contract.GroupReservationProtocol, leaseNamespace: "leases", authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
	for i := 0; i < 3; i++ {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		status := &configapi.NetworkControllerConfigStatus{}
		_, pending, _, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
		if !pending || status.Phase != configapi.NetworkControllerConfigPhaseWaiting || !reflect.DeepEqual(before, journalState(t, a).Operation) || len(journalState(t, a).ActionApplications) != 0 || f.posts != 1 {
			t.Fatal("taskless unknown escaped its retained fence")
		}
	}
}
