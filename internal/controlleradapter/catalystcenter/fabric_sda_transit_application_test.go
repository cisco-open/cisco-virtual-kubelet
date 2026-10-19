// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func readySDATransitApplicationEvidence(t *testing.T, a *adapter, collect func() error, members []*ops.ControllerConfigurationReservation, now time.Time) {
	t.Helper()
	if err := collect(); !errors.Is(err, errSDATransitDeviceEvidencePending) {
		t.Fatal("expected new read requests", err)
	}
	var leaves ops.DeviceOperationList
	if err := a.statusReader.List(context.Background(), &leaves); err != nil || len(leaves.Items) != len(members) {
		t.Fatal("requests", err)
	}
	for i := range leaves.Items {
		leaf := &leaves.Items[i]
		var member *ops.ControllerConfigurationReservation
		for _, m := range members {
			if m.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
				member = m
			}
		}
		if member == nil {
			t.Fatal("foreign read")
		}
		stampLANProofEnvelope(leaf, member, now.Add(-2*time.Second), now.Add(-time.Second), now)
		leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: leaf.Spec.Operation.Commands[0], Output: "Processor board ID " + member.Spec.PhysicalIdentity + "\n"}, {Command: leaf.Spec.Operation.Commands[1], Output: "SYNTHETIC SEMANTIC FIXTURE"}}
		if err := a.statusWriter.Update(context.Background(), leaf); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSDATransitApplicationRequiresIndependentProof(t *testing.T) {
	for _, mode := range []string{"record", "nil-proof", "proof-denied", "pending", "missing-member", "disabled", "undeclared", "wrong-operation", "proof-copy", "native-during-proof", "task-during-proof", "source-during-proof", "post-proof-native", "post-proof-denied", "post-proof-member-revoked", "second-proof-native", "second-proof-denied", "duplicate-receipt", "receipt-stripped", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, op, members, f, now := sdaTransitDeviceEvidenceFixture(t)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			ready := func() error {
				_, err := a.collectSDATransitDeviceEvidence(ctx, config, source, want, op, members, authorize, now)
				return err
			}
			if mode != "pending" {
				readySDATransitApplicationEvidence(t, a, ready, members, now)
			}
			proofs := 0
			proof := sdaTransitConvergenceProof(func(_ context.Context, current sdaTransitIntent, operation *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time, devices map[string]sdaTransitDeviceEvidence) error {
				proofs++
				if !reflect.DeepEqual(current, want) || !reflect.DeepEqual(operation, op) || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) || completed.IsZero() || len(devices) != len(members) {
					t.Fatal("proof is detached from admitted operation")
				}
				for _, e := range devices {
					if len(e.outputs) != 2 || e.outputs[1] != "SYNTHETIC SEMANTIC FIXTURE" {
						t.Fatal("proof lacks complete fresh evidence")
					}
				}
				switch mode {
				case "proof-denied":
					return errors.New("semantic verifier refused")
				case "second-proof-denied":
					if proofs == 2 {
						return errors.New("semantic proof expired before persistence")
					}
				case "second-proof-native":
					if proofs == 2 {
						f.transits[0].ID = "replacement"
					}
				case "proof-copy":
					current.controlPlanes[0] = "foreign"
					operation.TaskID = "foreign"
					targets[0].InventoryID = "foreign"
					for key, e := range devices {
						e.outputs[1] = "foreign"
						delete(devices, key)
					}
				case "native-during-proof":
					f.transits[0].ID = "replacement"
				case "task-during-proof":
					f.stamp += 1000
				case "source-during-proof":
					config.Generation++
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			})
			operationID := op.ID
			switch mode {
			case "nil-proof":
				proof = nil
			case "missing-member":
				members = members[:1]
			case "disabled":
				a.configurationAdmission = nil
			case "undeclared":
				want.name = "absent"
			case "wrong-operation":
				operationID = "foreign"
			case "post-proof-native", "post-proof-denied", "post-proof-member-revoked":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					if proofs == 0 {
						return nil
					}
					if mode == "post-proof-denied" {
						return errors.New("authority expired")
					}
					if mode == "post-proof-native" {
						f.transits[0].ID = "replacement"
						return nil
					}
					m := members[0].DeepCopy()
					m.Status.Grant.Phase = "Revoked"
					return a.statusWriter.Status().Update(ctx, m)
				}
			case "duplicate-receipt":
				nc, _ := a.currentController(ctx)
				verified := op.DeepCopy()
				verified.Phase = "Verified"
				at := metav1.NewTime(now)
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: "prior", Operation: *verified, VerifiedAt: &at}}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "receipt-stripped":
				a.statusWriter = sdaApplicationStrippingClient{a.statusWriter}
			case "lost-response":
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			err := a.recordSDATransitApplication(ctx, config, operationID, want, members, authorize, proof, now)
			success := mode == "record" || mode == "proof-copy"
			persisted := success || mode == "lost-response"
			if (err == nil) != success {
				t.Fatal("wrong receipt outcome", err)
			}
			state := journalState(t, a)
			if persisted {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 {
					t.Fatal("atomic receipt missing")
				}
				receipt := state.ActionApplications[0]
				expected := op.DeepCopy()
				expected.Phase = "Verified"
				if !reflect.DeepEqual(&receipt.Operation, expected) || receipt.Resource == op.Resource || receipt.Operation.CreatedResourceID != "" {
					t.Fatal("journal or ownership changed")
				}
				nc, _ := a.currentController(ctx)
				if _, err := contract.GroupCompletionsForReceipt(nc, members, now); err != nil {
					t.Fatal("receipt cannot prove complete group", err)
				}
				if err := a.recordSDATransitApplication(ctx, config, op.ID, want, members, authorize, proof, now); err == nil {
					t.Fatal("replayed completion")
				}
			} else if mode != "receipt-stripped" && (state.Operation.Phase != "Verifying" || len(state.ActionApplications) != map[bool]int{true: 1, false: 0}[mode == "duplicate-receipt"]) {
				t.Fatal("failed proof changed journal")
			}
			for _, m := range members {
				var current ops.ControllerConfigurationReservation
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(m), &current); err != nil {
					t.Fatal(err)
				}
				if current.Status.Completion != nil || current.Status.ManagerSettledAt != nil {
					t.Fatal("receipt publisher settled member")
				}
			}
			if f.posts != 1 {
				t.Fatal("completion replayed native mutation")
			}
			if success && proofs != 2 {
				t.Fatal("semantic proof not repeated at persistence", proofs)
			}
			if mode == "record" {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := contract.FinishVerifiedControllerGroup(ctx, a.statusReader, a.statusWriter, current, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, now); err == nil {
					t.Fatal("unsettled members allowed journal retirement")
				}
				completions, err := contract.GroupCompletionsForReceipt(current, members, now)
				if err != nil {
					t.Fatal(err)
				}
				// Simulated worker/manager actions. Production receipt publication
				// leaves these boundaries to their existing independent actors.
				for _, member := range members {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
						t.Fatal(err)
					}
					member.Status.Completion = completions[member.UID]
					member.Status.Grant.Phase = "Released"
					member.Status.ManagerSettledAt = &metav1.Time{Time: now}
					if err := a.statusWriter.Status().Update(ctx, member); err != nil {
						t.Fatal(err)
					}
				}
				if err := contract.FinishVerifiedControllerGroup(ctx, a.statusReader, a.statusWriter, current, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, now); err != nil {
					t.Fatal("settled receipt did not retire", err)
				}
				state := journalState(t, a)
				if state.Operation != nil || len(state.ActionApplications) != 1 || f.posts != 1 {
					t.Fatal("retirement lost receipt or replayed mutation")
				}
			}
		})
	}
}

type sdaApplicationStrippingClient struct{ ctrlclient.Client }
type sdaApplicationStrippingWriter struct{ ctrlclient.SubResourceWriter }

func (c sdaApplicationStrippingClient) Status() ctrlclient.SubResourceWriter {
	return sdaApplicationStrippingWriter{c.Client.Status()}
}
func (w sdaApplicationStrippingWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Verified" {
		nc = nc.DeepCopy()
		nc.Status.Configuration.ActionApplications = nil
		return w.SubResourceWriter.Update(ctx, nc, opts...)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
