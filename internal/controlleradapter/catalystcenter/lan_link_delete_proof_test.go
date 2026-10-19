// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

// Native HTTPS plus explicit fake worker envelopes. Not installed or hardware
// deconfiguration qualification.
func TestLANLinkDeleteDeviceOperationProofBridge(t *testing.T) {
	for _, mode := range []string{"verified", "settled", "settled-workers", "pending", "failed", "old-generation", "not-ready", "stale-result", "before-task", "future-result", "truncated", "redacted", "wrong-command", "wrong-owner", "wrong-worker", "wrong-serial", "wrong-address", "remaining-isis", "denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f, members := dispatchedLANLinkDeleteFixture(t, false)
			if mode == "settled-workers" {
				a.configurationAdmission = nil
				installFabricGroupAuthorityFixture(t, a)
			}
			for i := range f.ports {
				f.ports[i].IPv4, f.ports[i].Mask = "", ""
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginLANLinkDeleteVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			now := time.Now().UTC().Truncate(time.Second).Add(5 * time.Second)
			if mode == "stale-result" {
				now = now.Add(10 * time.Minute)
			}
			proof := a.lanLinkDeleteDeviceProof(config, source, want, members, authorize, now)
			for range 2 {
				if err := proof(ctx, op, op.PhysicalGroup.Targets, f.taskEnd); !errors.Is(err, errLANLinkDeviceProofPending) {
					t.Fatal("missing worker evidence did not remain pending", err)
				}
			}
			if mode == "pending" {
				offline := a.lanLinkDeleteDeviceProof(config, source, want, members, authorize, now.Add(10*time.Minute))
				if err := offline(ctx, op, op.PhysicalGroup.Targets, f.taskEnd); !errors.Is(err, errLANLinkDeviceProofPending) {
					t.Fatal("offline read lost its pending identity", err)
				}
			}
			var leaves ops.DeviceOperationList
			if err := a.statusReader.List(ctx, &leaves); err != nil {
				t.Fatal(err)
			}
			if len(leaves.Items) != 2 {
				t.Fatal("read operations missing or duplicated", len(leaves.Items))
			}
			native, _, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, op, now)
			if err != nil {
				t.Fatal(err)
			}
			endpoints := native.endpoints
			for i := range leaves.Items {
				leaf := &leaves.Items[i]
				var member *ops.ControllerConfigurationReservation
				for _, candidate := range members {
					if candidate.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
						member = candidate
					}
				}
				if member == nil {
					t.Fatal("read escaped admitted group")
				}
				endpoint, peer := endpoints.primary, endpoints.secondary
				if member.Spec.InventoryID != endpoint.device.ID {
					endpoint, peer = peer, endpoint
				}
				stampLANDeleteProofFixture(leaf, member, endpoint, peer, now.Add(-2*time.Second), now.Add(-time.Second), now)
				if i == 0 {
					switch mode {
					case "pending":
						leaf.Status.Phase = ops.OperationPhasePending
					case "failed":
						leaf.Status.Phase = ops.OperationPhaseFailed
					case "old-generation":
						leaf.Status.ObservedGeneration = 0
					case "stale-result":
						leaf.CreationTimestamp = metav1.NewTime(f.taskEnd.Add(time.Second))
						leaf.Status.StartTime = &metav1.Time{Time: f.taskEnd.Add(2 * time.Second)}
						leaf.Status.CompletionTime = &metav1.Time{Time: f.taskEnd.Add(3 * time.Second)}
						leaf.Status.Conditions[0].LastTransitionTime = *leaf.Status.CompletionTime
					case "not-ready":
						leaf.Status.Conditions[0].Status = metav1.ConditionFalse
					case "before-task":
						leaf.CreationTimestamp = metav1.NewTime(f.taskEnd)
					case "future-result":
						leaf.Status.CompletionTime = &metav1.Time{Time: now.Add(time.Second)}
					case "truncated":
						leaf.Status.Outputs[0].Truncated = true
					case "redacted":
						leaf.Status.Outputs[0].Redacted = true
					case "wrong-command":
						leaf.Spec.Operation.Commands[0] = "show clock"
					case "wrong-owner":
						leaf.OwnerReferences[0].UID = "foreign"
					case "wrong-worker":
						leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = "foreign"
					case "wrong-serial":
						leaf.Status.Outputs[0].Output = "Processor board ID FOC99999999\n"
					case "wrong-address":
						leaf.Status.Outputs[1].Output = strings.ReplaceAll(leaf.Status.Outputs[1].Output, "no ip address", "ip address 192.0.2.130 255.255.255.254")
					case "remaining-isis":
						leaf.Status.Outputs[1].Output = strings.ReplaceAll(leaf.Status.Outputs[1].Output, "end\n", " ip router isis UNDERLAY\nend\n")
					}
				}
				// DeviceOperation is not a status subresource in this fake fixture.
				if err := a.statusWriter.Update(ctx, leaf); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "denied" {
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("revoked") }
				proof = a.lanLinkDeleteDeviceProof(config, source, want, members, authorize, now)
			}
			err = a.recordLANLinkDeleteApplication(ctx, config, op.ID, members, authorize, proof, now)
			if (err == nil) != (mode == "verified" || mode == "settled" || mode == "settled-workers") {
				t.Fatal("unexpected worker proof publication", err)
			}
			state := journalState(t, a)
			if mode == "verified" || mode == "settled" || mode == "settled-workers" {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 2 {
					t.Fatal("worker proof did not reach atomic receipt")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 1 {
				t.Fatal("unqualified worker result completed")
			}
			if f.posts != 2 {
				t.Fatal("verification repeated LAN update")
			}
			assertLANClaimsRetained(t, a, false)
			if mode == "settled-workers" {
				if wait := time.Until(now); wait > 0 {
					time.Sleep(wait)
				}
				settleLANDeleteWithWorkers(t, a)
				if f.posts != 2 {
					t.Fatal("worker settlement replayed native deletion")
				}
			}
			if mode == "settled" {
				// Proof envelopes above use a controlled future clock; terminal
				// actor checks use wall time. Wait for that boundary explicitly.
				if wait := time.Until(now); wait > 0 {
					time.Sleep(wait)
				}
				a.configurationAdmission = nil
				testFabricHandoffCompletion(t, a, config, true)
				state := journalState(t, a)
				if state.Operation != nil || len(state.ActionApplications) != 2 || f.posts != 2 {
					t.Fatal("DELETE terminal recovery lost retained receipts or replayed")
				}
			}
		})
	}
}

func lanDeleteCLIOutputs(endpoint lanLinkEndpoint) []string {
	return []string{"Cisco IOS XE Software\nProcessor board ID " + endpoint.device.Serial + "\n", "Building configuration...\nCurrent configuration : 100 bytes\n!\ninterface " + endpoint.port.PortName + "\n no switchport\n no ip address\n shutdown\nend\n"}
}
func stampLANDeleteProofFixture(leaf *ops.DeviceOperation, member *ops.ControllerConfigurationReservation, endpoint, _ lanLinkEndpoint, created, started, finished time.Time) {
	stampLANProofEnvelope(leaf, member, created, started, finished)
	for i, output := range lanDeleteCLIOutputs(endpoint) {
		leaf.Status.Outputs = append(leaf.Status.Outputs, ops.DeviceOperationOutput{Command: leaf.Spec.Operation.Commands[i], Output: output})
	}
}
