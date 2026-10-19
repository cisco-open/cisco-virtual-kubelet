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
	"strings"
	"testing"
	"time"
)

// Explicit fake proof callback isolates publication races from device parsing;
// the real DeviceOperation proof bridge is exercised in separate fixture tests.
func TestLANLinkDeleteAtomicApplicationRequiresDeviceProof(t *testing.T) {
	for _, mode := range []string{"record", "exhausted-after", "missing-proof", "denied-proof", "native-result-missing", "native-race", "managed-race", "source-race", "completion-race", "future-end", "authorization-denied", "missing-member", "lost-response", "publish-completion"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, _, f, members := dispatchedLANLinkDeleteFixture(t, false)
			for i := range f.ports {
				f.ports[i].IPv4, f.ports[i].Mask = "", ""
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginLANLinkDeleteVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
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
				f.ports[0].IPv4 = "192.0.2.130"
			case "future-end":
				f.taskEnd = time.Now().Add(time.Minute)
			case "authorization-denied":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("denied") }
			case "missing-member":
				members = members[:1]
			case "lost-response":
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			now := time.Now().Add(3 * time.Second)
			err := a.recordLANLinkDeleteApplication(ctx, config, op.ID, members, authorize, proof, now)
			success := mode == "record" || mode == "exhausted-after" || mode == "publish-completion"
			persisted := success || mode == "lost-response"
			if (err == nil) != success {
				t.Fatal("unexpected atomic application outcome", err)
			}
			state := journalState(t, a)
			if persisted {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 2 || state.ActionApplications[1].Operation.ID != op.ID || !strings.HasPrefix(state.ActionApplications[1].Resource, "lan-link-delete:") {
					t.Fatal("atomic receipt/journal missing")
				}
				if state.ActionApplications[1].Operation.Resource != op.Resource {
					t.Fatal("proof rewrote original admitted operation")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 1 {
				t.Fatal("unverified device result completed")
			}
			if (mode == "missing-proof" || mode == "native-result-missing" || mode == "future-end") && proofCalls != 0 {
				t.Fatal("proof invoked before native prerequisites")
			}
			if f.posts != 2 {
				t.Fatal("verification resubmitted native update")
			}
			assertLANClaimsRetained(t, a, false)
			if mode == "publish-completion" {
				// Shared terminal checks use the real clock; do not claim future verification.
				if wait := time.Until(now.UTC().Truncate(time.Second)); wait > 0 {
					time.Sleep(wait)
				}
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
