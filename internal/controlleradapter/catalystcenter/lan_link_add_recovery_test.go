// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

// Native HTTPS, fake Kubernetes and explicit proof/actor fixtures. No installed
// worker/Lease or real switch result attestation is claimed by these tests.
func dispatchedLANLinkAddFixture(t *testing.T, lost bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, *lanPreflightFixture, []*ops.ControllerConfigurationReservation, func()) {
	t.Helper()
	ctx := context.Background()
	a, config, source, want, f := lanLinkAddJournalFixture(t)
	if _, err := a.prepareLANLinkAdd(ctx, config, source, want.name); err != nil {
		t.Fatal(err)
	}
	members := []configurationDispatchFixtureMember{}
	for _, name := range []string{"edge0", "edge1"} {
		var d cvk.CiscoDevice
		var n core.Node
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: name}, &d); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
			t.Fatal(err)
		}
		members = append(members, configurationDispatchFixtureMember{device: &d, node: &n})
	}
	permit, revoke := configurationGroupDispatchPermitFixture(t, a, members)
	if lost {
		f.mode = "lost"
	}
	_, err := a.dispatchLANLinkAdd(ctx, config, source, want.name, permit)
	if (err == nil) == lost {
		t.Fatal("fixture dispatch outcome", err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.taskEnd = nc.Status.Configuration.Operation.SubmittedAt.Time
	rows := []*ops.ControllerConfigurationReservation{}
	if !lost {
		rows, err = contract.ReadAcknowledgedGroupReservations(ctx, a.statusReader, nc, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	return a, config, source, want, f, rows, revoke
}

func setLANLinkResult(f *lanPreflightFixture) {
	f.ports[0].IPv4, f.ports[1].IPv4 = "192.0.2.130", "192.0.2.131"
	f.ports[0].Mask, f.ports[1].Mask = "255.255.255.254", "255.255.255.254"
	f.topologyID = "refreshed-physical-link"
	f.discoveryAddresses = true
}

func TestLANLinkAddPublicAcknowledgedRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "running", "failed", "future-task", "changed-source", "changed-pool", "replaced-port", "replaced-peer", "revoked", "lost", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _, f, _, revoke := dispatchedLANLinkAddFixture(t, mode == "lost")
			setLANLinkResult(f)
			a.configurationAdmission = nil
			installFabricGroupAuthorityFixture(t, a)
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			original := current.Status.Configuration.Operation.DeepCopy()
			switch mode {
			case "unknown":
				current.Status.Configuration.Operation.Phase = "Unknown"
			case "pruned-group":
				current.Status.Configuration.Operation.PhysicalGroup = nil
			case "changed-source":
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			case "running", "failed":
				f.taskMode = mode
			case "future-task":
				f.taskEnd = time.Now().Add(time.Minute)
			case "changed-pool":
				f.poolChanged = true
			case "replaced-port":
				f.ports[1].ID = "replacement-port"
			case "replaced-peer":
				f.inventory[1].Serial = "FOC99999999"
			case "revoked":
				revoke()
			}
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
			}
			state := journalState(t, a)
			success := mode == "accepted" || mode == "unknown"
			expected := original.Phase
			if success {
				expected = "Verifying"
			}
			if state.Operation == nil || state.Operation.ID != original.ID || state.Operation.Phase != expected || f.posts != 1 || len(state.ActionApplications) != 0 {
				t.Fatal("recovery replayed, replaced or prematurely completed work", mode)
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
				t.Fatal("retained recovery was relabelled unsupported/ready", published.Status)
			}
			if success {
				found := false
				for _, condition := range published.Status.Conditions {
					found = found || condition.Reason == "GroupDeviceVerificationPending"
				}
				if !found {
					t.Fatal("independent proof gap was not reported")
				}
			}
			assertLANClaimsRetained(t, a, false)
		})
	}
}

func assertLANClaimsRetained(t *testing.T, a *adapter, completion bool) {
	t.Helper()
	var rows ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(context.Background(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows.Items) != 2 {
		t.Fatal("endpoint claim omitted")
	}
	for _, row := range rows.Items {
		if row.Status.Claim == nil || row.Status.ManagerSettledAt != nil || row.Status.Grant == nil || row.Status.Grant.Phase == "Released" || (row.Status.Completion != nil) != completion {
			t.Fatal("premature or missing endpoint settlement evidence")
		}
	}
}

func TestLANLinkAddAtomicApplicationRequiresDeviceProof(t *testing.T) {
	for _, mode := range []string{"record", "exhausted-after", "missing-proof", "denied-proof", "native-result-missing", "native-race", "managed-race", "source-race", "completion-race", "future-end", "authorization-denied", "missing-member", "lost-response", "publish-completion"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, _, f, members, _ := dispatchedLANLinkAddFixture(t, false)
			setLANLinkResult(f)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginLANLinkAddVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			proofCalls := 0
			proof := configurationGroupConvergenceProof(func(ctx context.Context, current *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
				proofCalls++
				if !reflect.DeepEqual(current, op) || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) || !completed.Equal(f.taskEnd) {
					t.Fatal("proof lost exact original operation/footprint/completion boundary")
				}
				switch mode {
				case "denied-proof":
					return errors.New("device convergence unavailable")
				case "native-race":
					f.ports[0].IPv4 = "192.0.2.132"
				case "managed-race":
					var node core.Node
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "edge1"}, &node); err != nil {
						return err
					}
					if err := a.statusWriter.Delete(ctx, &node); err != nil {
						return err
					}
					node.ResourceVersion = ""
					node.UID = "replacement-node"
					return a.statusWriter.Create(ctx, &node)
				case "source-race":
					config.Generation++
					return a.statusWriter.Update(ctx, config)
				case "completion-race":
					f.taskEnd = f.taskEnd.Add(-time.Millisecond)
				}
				current.Resource = "forged"
				targets[0].InventoryID = "forged"
				return nil
			})
			switch mode {
			case "exhausted-after":
				f.mode = "exhausted"
			case "missing-proof":
				proof = nil
			case "native-result-missing":
				f.ports[0].IPv4 = ""
			case "future-end":
				f.taskEnd = time.Now().Add(time.Minute)
			case "authorization-denied":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("denied") }
			case "missing-member":
				members = members[:1]
			case "lost-response":
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			err := a.recordLANLinkAddApplication(ctx, config, op.ID, members, authorize, proof, time.Now())
			success := mode == "record" || mode == "exhausted-after" || mode == "publish-completion"
			persisted := success || mode == "lost-response"
			if (err == nil) != success {
				t.Fatal("unexpected atomic application outcome", err)
			}
			state := journalState(t, a)
			if persisted {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID != op.ID || !strings.HasPrefix(state.ActionApplications[0].Resource, "lan-link:") {
					t.Fatal("atomic receipt/journal missing")
				}
				if state.ActionApplications[0].Operation.Resource != op.Resource {
					t.Fatal("proof rewrote original admitted operation")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("unverified device result completed")
			}
			if (mode == "missing-proof" || mode == "native-result-missing" || mode == "future-end") && proofCalls != 0 {
				t.Fatal("proof invoked before native prerequisites")
			}
			if f.posts != 1 {
				t.Fatal("verification resubmitted native update")
			}
			assertLANClaimsRetained(t, a, false)
			if mode == "publish-completion" {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.completeConfigurationGroup(ctx, current); err == nil {
					t.Fatal("missing worker/manager settlement released journal")
				}
				assertLANClaimsRetained(t, a, true)
				if journalState(t, a).Operation == nil {
					t.Fatal("journal retired before settlement")
				}
			}
		})
	}
}

func TestLANLinkAddLegacyCompletionRemainsFenced(t *testing.T) {
	a, _, _, _, _, _, _ := dispatchedLANLinkAddFixture(t, false)
	op := journalState(t, a).Operation.DeepCopy()
	binding, err := parseLANLinkAddResource(op.Resource, op.Action)
	if err != nil {
		t.Fatal(err)
	}
	op.Resource = lanLinkAddPrefix + binding.logical + ":" + binding.prepared
	// Build a local legacy fixture; never rewrite the stored accepted operation.
	task, submitted, targets := op.TaskID, op.SubmittedAt, op.PhysicalGroup.Targets
	op.Phase = "Prepared"
	op.TaskID = ""
	op.SubmittedAt = nil
	op.PhysicalGroup = nil
	legacy, err := contract.WithGroupIdentity(op, targets)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Phase = "Accepted"
	legacy.TaskID = task
	legacy.SubmittedAt = submitted
	if _, err := validateLANLinkAddCompletionOperation(legacy, time.Now()); !errors.Is(err, errLANLinkLegacyRecovery) {
		t.Fatal("legacy baseline was inferred", err)
	}
}
