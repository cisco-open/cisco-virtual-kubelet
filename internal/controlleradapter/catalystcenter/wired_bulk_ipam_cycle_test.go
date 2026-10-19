// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Controlled native HTTPS and simulated reservation/device actors. No deployed
// worker, physical provisioning or real convergence proof is claimed.
func TestWiredBulkIPAMLifecycleFencesReplacement(t *testing.T) {
	for _, mode := range []string{"verified", "missing-allocation", "pool-before-dispatch", "reservation-during-dispatch", "pool-after-acceptance", "reservation-after-acceptance", "pool-during-proof", "pool-during-final-authority", "completion-during-final-authority", "missing-proof", "credential-before-dispatch", "credential-after-acceptance", "credential-during-proof", "credential-during-final-authority", "child-during-final-authority", "owned-before-dispatch", "owned-after-acceptance", "owned-during-proof", "owned-during-final-authority", "owner-lost-during-final-authority"} {
		t.Run(mode, func(t *testing.T) {
			ownedContext := strings.HasPrefix(mode, "owned-") || mode == "owner-lost-during-final-authority"
			lostOwner := mode == "owner-lost-during-final-authority"
			credentialContext := strings.HasPrefix(mode, "credential-") || mode == "child-during-final-authority" || ownedContext
			childOverride := mode == "child-during-final-authority"
			if credentialContext {
				mode = strings.Replace(mode, "credential-", "pool-", 1)
			}
			if childOverride {
				mode = "pool-during-final-authority"
			}
			if ownedContext {
				mode = strings.Replace(mode, "owned-", "pool-", 1)
			}
			if lostOwner {
				mode = "pool-during-final-authority"
			}
			ctx := context.Background()
			a, config, source, fixtureMembers, f := wiredBulkJournalFixture(t, false, false)
			doc := wiredBulkDependencyDocument(t)
			if credentialContext {
				doc = wiredBulkCredentialDocument(t, true)
			}
			if ownedContext {
				doc = wiredBulkOwnedCredentialDocument(t)
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw, source.raw = raw, raw
			config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global/Lab"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			ipam := installWiredBulkIPAMFixture(t, f)
			replacePool := func() { ipam.pools[0].ID = "replacement"; ipam.reservations[0].V4.GlobalPoolID = "replacement" }
			if credentialContext {
				credentials := newWiredBulkCredentialFixture()
				previous := f.networkReply
				f.networkReply = func(w http.ResponseWriter, r *http.Request) bool { return credentials.reply(t, w, r) || previous(w, r) }
				if ownedContext {
					installWiredBulkOwnedCredentials(t, a, config, source, credentials)
				}
				replacePool = func() {
					if childOverride {
						credentials.slots["building"]["cliCredentialsId"] = map[string]any{}
						return
					}
					credentials.metadata["cliCredential"][0].ID = "replacement"
					credentials.slots["area"]["cliCredentialsId"] = map[string]any{"credentialsId": "replacement"}
				}
			}
			if ownedContext {
				replacePool = func() {
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if lostOwner {
						owners := nc.Status.Configuration.Owners[:0]
						for _, owner := range nc.Status.Configuration.Owners {
							if owner.Section != "network_settings" {
								owners = append(owners, owner)
							}
						}
						nc.Status.Configuration.Owners = owners
					} else {
						nc.Status.Configuration.CredentialApplications[0].OperationID = strings.Repeat("d", 32)
					}
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "missing-allocation" {
				ipam.reservations = []ipReservation{}
			}
			batches, err := configuredWiredBulkBatches(config, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
			if mode == "missing-allocation" {
				if err == nil || op != nil || journalState(t, a).Operation != nil || f.calls != 0 {
					t.Fatal("missing allocation was prepared", err)
				}
				return
			}
			if err != nil || op == nil || !strings.HasPrefix(op.Resource, wiredBulkProvisionPrefix+"v2:") {
				t.Fatal("allocation-bound preparation unavailable", err)
			}
			if mode == "pool-before-dispatch" {
				replacePool()
			}
			if mode == "reservation-during-dispatch" {
				f.beforeInventory = func() {
					if journalState(t, a).Operation.Phase == "Submitting" {
						ipam.reservations[0].ID = "replacement"
					}
				}
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, fixtureMembers)
			_, err = a.dispatchWiredBulkProvision(ctx, config, source, permit)
			if mode == "pool-before-dispatch" || mode == "reservation-during-dispatch" {
				if err == nil || f.calls != 0 || journalState(t, a).Operation == nil {
					t.Fatal("dependency replacement dispatched or lost fence", err)
				}
				return
			}
			if err != nil || f.calls != 1 {
				t.Fatal("valid v2 dispatch failed", err)
			}
			members := wiredBulkReservationMembers(t, a)
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if mode == "pool-after-acceptance" {
				replacePool()
			}
			if mode == "reservation-after-acceptance" {
				ipam.reservations[0].ID = "replacement"
			}
			err = a.beginWiredBulkVerification(ctx, config, source, members, authorize, time.Now())
			if mode == "pool-after-acceptance" || mode == "reservation-after-acceptance" {
				if err == nil || journalState(t, a).Operation.Phase != "Accepted" || f.calls != 1 {
					t.Fatal("acknowledged replacement lost original fence", err)
				}
				return
			}
			if err != nil {
				t.Fatal("native verification failed", err)
			}
			proof := wiredBulkConvergenceProof(func(context.Context, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time) error {
				if mode == "pool-during-proof" {
					replacePool()
				}
				return nil
			})
			if mode == "missing-proof" {
				proof = nil
			}
			if mode == "pool-during-final-authority" {
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { replacePool(); return nil }
			}
			if mode == "completion-during-final-authority" {
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					f.taskEnd = f.taskEnd.Add(time.Second)
					return nil
				}
			}
			err = a.recordWiredBulkApplication(ctx, config, op.ID, batches[0], members, authorize, proof, time.Now().Add(5*time.Second))
			if (err == nil) != (mode == "verified") {
				t.Fatal("unproved dependency/authorization boundary published receipt", err)
			}
			state := journalState(t, a)
			if mode == "verified" {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 {
					t.Fatal("shared v2 receipt missing")
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("failed v2 verification retired original fence")
			}
			if f.calls != 1 {
				t.Fatal("v2 verification replayed provisioning")
			}
		})
	}
}
