// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Native HTTPS fixtures, fake Kubernetes and explicit independent proof fixtures.
// This is an atomic ownership/recovery test, not installed device attestation.
func TestFabricDevicesBulkApplicationRequiresIndependentProof(t *testing.T) {
	for _, mode := range []string{"foreign-owner", "missing-owner", "controller-race", "record", "missing-proof", "denied-proof", "mutated-proof-input", "native-race", "managed-race", "source-race", "completion-race", "future-end", "partial", "failed-task", "missing-member", "authorization-denied", "authorization-race", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, wants, f := fabricDevicesBulkPreparationFixture(t, "prepare")
			if _, err := a.prepareFabricDevicesBulk(ctx, config, source, wants); err != nil {
				t.Fatal(err)
			}
			footprint := []configurationDispatchFixtureMember{}
			for _, name := range []string{"edge0", "edge1", "peer"} {
				var d cvk.CiscoDevice
				var n core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: name}, &d); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
					t.Fatal(err)
				}
				footprint = append(footprint, configurationDispatchFixtureMember{device: &d, node: &n})
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, footprint)
			if _, err := a.dispatchFabricDevicesBulk(ctx, config, source, wants, permit); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.taskEnd = nc.Status.Configuration.Operation.SubmittedAt.Time
			now := time.Now().UTC()
			members, err := contract.ReadAcknowledgedGroupReservations(ctx, a.statusReader, nc, now)
			if err != nil {
				t.Fatal(err)
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginFabricDevicesBulkVerification(ctx, config, source, members, authorize, now); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			proofCalls := 0
			proof := configurationGroupConvergenceProof(func(ctx context.Context, current *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
				proofCalls++
				if !reflect.DeepEqual(current, op) || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) || !completed.Equal(f.taskEnd) {
					t.Fatal("proof lost exact operation, peer/parent footprint or completion boundary")
				}
				switch mode {
				case "denied-proof":
					return errors.New("independent convergence denied")
				case "mutated-proof-input":
					current.Resource = "forged"
					targets[0].InventoryID = "forged"
				case "native-race":
					delete(f.created, "edge-0")
				case "managed-race":
					var d cvk.CiscoDevice
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: "peer"}, &d); err != nil {
						return err
					}
					d.Spec.Address = "198.51.100.199"
					return a.statusWriter.Update(ctx, &d)
				case "controller-race":
					current, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					current.Generation++
					return a.statusWriter.Update(ctx, current)
				case "source-race":
					config.Generation++
					return a.statusWriter.Update(ctx, config)
				case "completion-race":
					f.taskEnd = f.taskEnd.Add(-time.Millisecond)
				}
				return nil
			})
			switch mode {
			case "foreign-owner", "missing-owner":
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "foreign-owner" {
					current.Status.Configuration.Owners[0].ConfigUID = "foreign"
				} else {
					current.Status.Configuration.Owners = nil
				}
				if err := a.statusWriter.Status().Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			case "missing-proof":
				proof = nil
			case "future-end":
				f.taskEnd = now.Add(time.Second)
			case "partial":
				delete(f.created, "edge-0")
			case "failed-task":
				f.taskMode = "failed"
			case "missing-member":
				members = members[:1]
			case "authorization-denied":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("revoked") }
			case "authorization-race":
				authorize = func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
					member := current[len(current)-1].DeepCopy()
					member.Status.Grant.Phase = "Revoked"
					return a.statusWriter.Status().Update(ctx, member)
				}
			case "lost-response":
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			err = a.recordFabricDevicesBulkApplication(ctx, config, op.ID, members, authorize, proof, now)
			state := journalState(t, a)
			success := mode == "record" || mode == "mutated-proof-input"
			persisted := success || mode == "lost-response"
			if (err == nil) != success {
				t.Fatal("unexpected atomic publication", err)
			}
			if persisted {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID != op.ID {
					t.Fatal("verified operation and receipt were not retained together")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("incomplete proof changed verification")
			}
			if f.posts != 1 {
				t.Fatal("verification replayed native submission")
			}
			if (mode == "foreign-owner" || mode == "missing-owner" || mode == "missing-proof" || mode == "future-end" || mode == "partial" || mode == "failed-task") && proofCalls != 0 {
				t.Fatal("proof called without native prerequisites")
			}
			var retained ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &retained); err != nil {
				t.Fatal(err)
			}
			if len(retained.Items) != 3 {
				t.Fatal("peer claim omitted")
			}
			for _, member := range retained.Items {
				if member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Grant.Phase == "Released" {
					t.Fatal("receipt publisher released or settled a member")
				}
			}
		})
	}
}
