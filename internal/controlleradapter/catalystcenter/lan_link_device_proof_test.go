// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Native HTTPS and explicit fake worker results; not deployed actor qualification.
func TestLANLinkDeviceOperationProofBridge(t *testing.T) {
	for _, mode := range []string{"verified", "pending", "failed", "old-generation", "not-ready", "stale-result", "before-task", "future-result", "truncated", "redacted", "wrong-command", "wrong-owner", "wrong-worker", "wrong-serial", "wrong-address", "down-neighbor", "denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f, members, _ := dispatchedLANLinkAddFixture(t, false)
			setLANLinkResult(f)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginLANLinkAddVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			now := time.Now().UTC().Truncate(time.Second).Add(5 * time.Second)
			if mode == "stale-result" {
				now = now.Add(10 * time.Minute)
			}
			proof := a.lanLinkDeviceConvergenceProof(config, source, want, members, authorize, now)
			for range 2 {
				if err := proof(ctx, op, op.PhysicalGroup.Targets, f.taskEnd); !errors.Is(err, errLANLinkDeviceProofPending) {
					t.Fatal("missing worker evidence did not remain pending", err)
				}
			}
			if mode == "pending" {
				offline := a.lanLinkDeviceConvergenceProof(config, source, want, members, authorize, now.Add(10*time.Minute))
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
			native, _, err := a.client.lanLinkCompletionTargets(ctx, a.statusReader, config.Namespace, want, op, now)
			if err != nil {
				t.Fatal(err)
			}
			endpoints := native.dependencies.adjacency.endpoints
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
				stampLANProofFixture(leaf, member, endpoint, peer, now.Add(-2*time.Second), now.Add(-time.Second), now)
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
						leaf.Status.Outputs[1].Output = strings.ReplaceAll(leaf.Status.Outputs[1].Output, endpoint.port.IPv4, "192.0.2.134")
					case "down-neighbor":
						leaf.Status.Outputs[2].Output = strings.ReplaceAll(leaf.Status.Outputs[2].Output, " UP ", " INIT ")
					}
				}
				// DeviceOperation is not a status subresource in this fake fixture.
				if err := a.statusWriter.Update(ctx, leaf); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "denied" {
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("revoked") }
				proof = a.lanLinkDeviceConvergenceProof(config, source, want, members, authorize, now)
			}
			err = a.recordLANLinkAddApplication(ctx, config, op.ID, members, authorize, proof, now)
			if (err == nil) != (mode == "verified") {
				t.Fatal("unexpected worker proof publication", err)
			}
			state := journalState(t, a)
			if mode == "verified" {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 {
					t.Fatal("worker proof did not reach atomic receipt")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("unqualified worker result completed")
			}
			if f.posts != 1 {
				t.Fatal("verification repeated LAN update")
			}
			assertLANClaimsRetained(t, a, false)
		})
	}
}

// Explicit fake manager identity and device-worker results, never a lab attestation.
func stampLANProofFixture(leaf *ops.DeviceOperation, member *ops.ControllerConfigurationReservation, endpoint, peer lanLinkEndpoint, created, started, finished time.Time) {
	stampLANProofEnvelope(leaf, member, created, started, finished)
	for i, output := range lanCLIOutputs(endpoint, peer) {
		leaf.Status.Outputs = append(leaf.Status.Outputs, ops.DeviceOperationOutput{Command: leaf.Spec.Operation.Commands[i], Output: output})
	}
}

func stampLANProofEnvelope(leaf *ops.DeviceOperation, member *ops.ControllerConfigurationReservation, created, started, finished time.Time) {
	leaf.UID, leaf.Generation = types.UID(leaf.Name+"-uid"), 1
	leaf.CreationTimestamp = metav1.NewTime(created)
	leaf.Annotations[managedprotocol.AnnotationManaged] = "true"
	leaf.Annotations[managedprotocol.AnnotationDeviceNamespace] = member.Namespace
	leaf.Annotations[managedprotocol.AnnotationDeviceName] = member.Spec.Device.Name
	leaf.Annotations[managedprotocol.AnnotationDeviceUID] = member.Spec.Device.UID
	leaf.Annotations[managedprotocol.AnnotationNetworkWorkerUsername] = member.Spec.DeviceWorkerUsername
	leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] = member.Spec.DeviceWorkerPod.Name
	leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = member.Spec.DeviceWorkerPod.UID
	start, end := metav1.NewTime(started), metav1.NewTime(finished)
	leaf.Status = ops.DeviceOperationStatus{Phase: ops.OperationPhaseSucceeded, ObservedGeneration: leaf.Generation, StartTime: &start, CompletionTime: &end, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: leaf.Generation, LastTransitionTime: end}}}
}
