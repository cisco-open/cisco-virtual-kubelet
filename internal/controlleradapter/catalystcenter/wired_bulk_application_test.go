// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Native HTTPS plus fake Kubernetes claims/authorization/device proofs. This
// exercises atomic persistence, not installed actors or real device attestation.
func TestWiredBulkApplicationUsesSharedAtomicReceipt(t *testing.T) {
	for _, mode := range []string{"recover-accepted", "recover-unknown", "recover-running", "recover-failed", "recover-partial", "recover-owner", "recover-source", "record", "missing-proof", "proof-denied", "native-proof-race", "source-proof-race", "authorization-denied", "revoked-during-authorization", "missing-member", "lost-response", "undeclared", "future-task-end", "completion-proof-race", "subsecond-unrepresentable-end"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			c, members, f := newWiredBulkFixture(t)
			a.client = c
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"REPROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			f.present["a"], f.present["b"] = "old-a", "old-b"
			for i := range members {
				members[i].state = "REPROVISION"
			}
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
			}
			batch := wiredBulkBatch{path: "Global/Lab", members: members}
			snapshot, targets, err := c.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			base := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: snapshot.resource(snapshot.mask), Action: "Update", TargetID: snapshot.anchor, ParentID: snapshot.anchor, Phase: "Prepared"}
			op, err := contract.WithGroupIdentity(base, targets)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			op.Phase = "Verifying"
			op.TaskID = "batch-task"
			op.SubmittedAt = &metav1.Time{Time: now.Add(-time.Second)}
			if strings.HasPrefix(mode, "recover-") {
				op.Phase = "Accepted"
				if mode == "recover-unknown" {
					op.Phase = "Unknown"
				}
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			reservations := []*ops.ControllerConfigurationReservation{}
			for _, target := range targets {
				raw, _ := json.Marshal([]string{contract.GroupReservationProtocol, string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity})
				sum := sha256.Sum256(raw)
				name := fmt.Sprintf("config-group-%x", sum[:16])
				r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: name, UID: types.UID(name + "-uid"), Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.GroupReservationProtocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: config.Name, UID: string(config.UID)}, Device: target.Device, Node: target.Node, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}, ControllerUsername: "system:serviceaccount:lab:connector", ControllerGeneration: nc.Generation, ConfigGeneration: config.Generation, OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, PhysicalIdentity: target.PhysicalIdentity, InventoryID: target.InventoryID}}
				holder, err := contract.Holder(r.Namespace, r)
				if err != nil {
					t.Fatal(err)
				}
				r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", Holder: holder, GrantedAt: metav1.NewTime(now.Add(-3 * time.Second))}
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-2 * time.Second))}
				if err := a.statusWriter.Create(ctx, r); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
				reservations = append(reservations, r)
			}
			if strings.HasPrefix(mode, "recover-") {
				if mode == "recover-running" {
					f.taskMode = "running"
				}
				if mode == "recover-failed" {
					f.taskMode = "failed"
				}
				if mode == "recover-partial" {
					delete(f.present, "b")
				}
				if mode == "recover-owner" {
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					current.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				}
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				err = a.beginWiredBulkVerification(ctx, config, source, reservations, func(ctx context.Context, _ []*ops.ControllerConfigurationReservation) error {
					calls++
					if mode == "recover-source" {
						var changed configapi.NetworkControllerConfig
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &changed); err != nil {
							return err
						}
						changed.Generation++
						return a.statusWriter.Update(ctx, &changed)
					}
					return nil
				}, now)
				success := mode == "recover-accepted" || mode == "recover-unknown"
				if (err == nil) != success {
					t.Fatal("acknowledged bulk recovery outcome", mode, err)
				}
				state := journalState(t, a)
				expected := "Accepted"
				if mode == "recover-unknown" {
					expected = "Unknown"
				}
				if success {
					expected = "Verifying"
				}
				if state.Operation.Phase != expected || state.Operation.ID != op.ID || state.Operation.TaskID != op.TaskID || len(state.ActionApplications) != 0 || f.calls != 0 {
					t.Fatal("recovery replayed, lost fence or published completion")
				}
				expectedCalls := 1
				if mode == "recover-owner" {
					expectedCalls = 0
				}
				if calls != expectedCalls {
					t.Fatal("unexpected recovery actor authorization", calls)
				}
				for _, r := range reservations {
					var current ops.ControllerConfigurationReservation
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), &current); err != nil {
						t.Fatal(err)
					}
					if current.Status.Grant.Phase != "Held" || current.Status.Completion != nil || current.Status.ManagerSettledAt != nil {
						t.Fatal("recovery released or completed member")
					}
				}
				return
			}
			proofCalls, authorizations := 0, 0
			proof := wiredBulkConvergenceProof(func(_ context.Context, operation *cvk.NetworkControllerConfigurationOperation, physical []contract.GroupTargetIdentity, completed time.Time) error {
				proofCalls++
				if completed.IsZero() || !completed.Equal(f.taskEnd) {
					t.Fatal("native completion boundary missing from proof")
				}
				if operation.ID != op.ID || len(physical) != 2 {
					t.Fatal("proof lost operation/member binding")
				}
				switch mode {
				case "proof-denied":
					return errors.New("device convergence unproved")
				case "completion-proof-race":
					f.taskEnd = f.taskEnd.Add(-time.Millisecond)
				case "native-proof-race":
					f.present["a"] = "replacement"
				case "source-proof-race":
					var changed configapi.NetworkControllerConfig
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &changed); err != nil {
						return err
					}
					changed.Generation++
					return a.statusWriter.Update(ctx, &changed)
				}
				return nil
			})
			if mode == "subsecond-unrepresentable-end" {
				f.taskEnd = now.Add(100 * time.Millisecond)
				now = now.Add(900 * time.Millisecond)
			}
			if mode == "future-task-end" {
				f.taskEnd = now.Add(time.Second)
			}
			if mode == "missing-proof" {
				proof = nil
			}
			authorize := func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
				authorizations++
				if mode == "authorization-denied" {
					return errors.New("denied")
				}
				if mode == "revoked-during-authorization" {
					r := current[len(current)-1].DeepCopy()
					r.Status.Grant.Phase = "Revoked"
					return a.statusWriter.Status().Update(ctx, r)
				}
				return nil
			}
			if mode == "missing-member" {
				reservations = reservations[:1]
			}
			if mode == "lost-response" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "undeclared" {
				batch.members = append([]wiredBulkMember(nil), members...)
				batch.members[0].desired.reference.Name = "absent"
			}
			err = a.recordWiredBulkApplication(ctx, config, op.ID, batch, reservations, authorize, proof, now)
			state := journalState(t, a)
			persisted := mode == "record" || mode == "lost-response"
			if mode == "record" {
				if err != nil {
					t.Fatal("atomic receipt failed", err)
				}
			} else if err == nil {
				t.Fatal("unsafe or ambiguous receipt accepted")
			}
			if f.calls != 0 {
				t.Fatal("verification submitted native request")
			}
			if persisted {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID != op.ID {
					t.Fatal("atomic receipt/journal missing")
				}
				if mode == "record" && authorizations != 1 {
					t.Fatal("missing group actor authorization")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("failed verification advanced journal")
			}
			if (mode == "missing-proof" || mode == "future-task-end" || mode == "subsecond-unrepresentable-end") && proofCalls != 0 {
				t.Fatal("missing proof invoked callback")
			}
			for _, r := range reservations {
				var current ops.ControllerConfigurationReservation
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), &current); err != nil {
					t.Fatal(err)
				}
				if current.Status.Completion != nil || current.Status.ManagerSettledAt != nil {
					t.Fatal("receipt publisher completed/settled member")
				}
			}
			if mode == "record" {
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				batches := []wiredBulkBatch{batch}
				dispatchGuard := func(context.Context, wiredBulkBatch, []Device) error {
					t.Fatal("owned planning dispatched")
					return nil
				}
				if _, err := a.planOwnedWiredBulkSequence(ctx, config, source, batches, dispatchGuard, now); err == nil {
					t.Fatal("verified but unsettled journal permitted new planning")
				}
				if _, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, now); err == nil {
					t.Fatal("verified but unsettled journal permitted new preparation")
				}
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				completion, err := contract.GroupCompletionsForReceipt(current, reservations, now)
				if err != nil {
					t.Fatal(err)
				}
				// Simulated worker release/manager settlement; the shared finish contract
				// still verifies the complete correlated marker set before clearing.
				for _, r := range reservations {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), r); err != nil {
						t.Fatal(err)
					}
					r.Status.Completion = completion[r.UID]
					r.Status.Grant.Phase = "Released"
					r.Status.ManagerSettledAt = &metav1.Time{Time: now}
					if err := a.statusWriter.Status().Update(ctx, r); err != nil {
						t.Fatal(err)
					}
				}
				if err := contract.FinishVerifiedControllerGroup(ctx, a.statusReader, a.statusWriter, current, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, now); err != nil {
					t.Fatal("shared settlement boundary failed", err)
				}
				planned, err := a.planOwnedWiredBulkSequence(ctx, config, source, batches, dispatchGuard, now)
				if err != nil || len(planned) != 1 || planned[0].write != nil || f.calls != 0 {
					t.Fatal("owned settled PUT was replayed", err)
				}
				prepared, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, now)
				if err != nil || prepared != nil || journalState(t, a).Operation != nil || f.calls != 0 {
					t.Fatal("owned settled PUT fabricated another prepared batch", err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				next, err := a.planOwnedWiredBulkSequence(ctx, config, source, batches, dispatchGuard, now)
				if err != nil || len(next) != 1 || next[0].write == nil {
					t.Fatal("new declared revision suppressed by old receipt", err)
				}
				if _, err := next[0].write.submit(ctx); err == nil || f.calls != 0 {
					t.Fatal("owned sequence bypassed Submitting journal")
				}
				prepared, err = a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, now)
				if err != nil || prepared == nil || prepared.Phase != "Prepared" || prepared.TaskID != "" || prepared.SubmittedAt != nil || prepared.ID == op.ID || prepared.PhysicalGroup == nil || prepared.ConfigGeneration != config.Generation || f.calls != 0 {
					t.Fatal("new revision did not prepare a fresh bounded group", err)
				}
				if err := contract.ValidateGroupJournalIdentity(prepared); err != nil {
					t.Fatal("prepared batch has invalid shared group identity", err)
				}
				if _, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, now); err == nil || f.calls != 0 {
					t.Fatal("restarted preparation replaced the retained journal")
				}
			}

		})
	}
}
