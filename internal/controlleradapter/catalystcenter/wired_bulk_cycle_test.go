// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A complete private stage composition with native HTTPS and simulated
// Kubernetes actors/device proof. This is not deployed or live qualification.
func TestWiredBulkPrivateCycleUsesSharedLifecycle(t *testing.T) {
	for _, mode := range []string{"provision", "reprovision", "source-provision", "source-reprovision", "ipam-provision", "ipam-reprovision", "credentials-provision", "credentials-reprovision", "combined-provision", "combined-reprovision", "only-ipam-provision", "only-ipam-reprovision", "only-credentials-provision", "only-credentials-reprovision", "only-combined-provision", "only-combined-reprovision", "owned-provision", "owned-reprovision", "owned-metadata-provision", "owned-metadata-reprovision"} {
		create := mode == "provision" || strings.HasSuffix(mode, "-provision")
		name := "reprovision"
		if create {
			name = "provision"
		}
		if mode == "source-provision" || mode == "source-reprovision" {
			name = "source-" + name
		}
		if strings.HasPrefix(mode, "ipam-") {
			name = "ipam-" + name
		}
		if strings.HasPrefix(mode, "credentials-") || strings.HasPrefix(mode, "combined-") {
			name = mode
		}
		dependencyOnly := strings.HasPrefix(mode, "only-")
		if dependencyOnly {
			name = mode
		}
		ownedCredentials := strings.HasPrefix(mode, "owned-")
		if ownedCredentials {
			name = mode
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, fixtureMembers, f := wiredBulkJournalFixture(t, create, false)
			var originalMetadata *cvk.NetworkControllerConfigurationActionApplication
			declared := []string{"Global", "Global/Lab", "Global/Lab/Building"}
			if name == "source-provision" || name == "source-reprovision" {
				config.Spec.Source.Inline.Raw = wiredBulkSourceWithSites(t, source, wiredBulkSitesFixture)
				source.raw = config.Spec.Source.Inline.Raw
				declared = nil
			}
			if strings.HasPrefix(name, "ipam-") {
				raw, err := json.Marshal(wiredBulkDependencyDocument(t))
				if err != nil {
					t.Fatal(err)
				}
				if create {
					raw = []byte(strings.ReplaceAll(string(raw), "REPROVISION", "PROVISION"))
				}
				config.Spec.Source.Inline.Raw, source.raw = raw, raw
				declared = nil
				installWiredBulkIPAMFixture(t, f)
			}
			credentialContext := strings.HasPrefix(name, "credentials-") || strings.HasPrefix(name, "combined-")
			if credentialContext {
				raw, err := json.Marshal(wiredBulkCredentialDocument(t, strings.HasPrefix(name, "combined-")))
				if err != nil {
					t.Fatal(err)
				}
				if create {
					raw = []byte(strings.ReplaceAll(string(raw), "REPROVISION", "PROVISION"))
				}
				config.Spec.Source.Inline.Raw, source.raw = raw, raw
				declared = nil
				installWiredBulkIPAMFixture(t, f)
				previous := f.networkReply
				credentials := newWiredBulkCredentialFixture()
				f.networkReply = func(w http.ResponseWriter, r *http.Request) bool { return credentials.reply(t, w, r) || previous(w, r) }
			}
			if dependencyOnly {
				kind := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(mode, "only-"), "-reprovision"), "-provision")
				raw, err := json.Marshal(wiredBulkDependencyOnlyDocument(t, kind))
				if err != nil {
					t.Fatal(err)
				}
				if create {
					raw = []byte(strings.ReplaceAll(string(raw), "REPROVISION", "PROVISION"))
				}
				config.Spec.Source.Inline.Raw, source.raw = raw, raw
				declared = nil
				installWiredBulkIPAMFixture(t, f)
				if kind != "ipam" {
					credentials := newWiredBulkCredentialFixture()
					previous := f.networkReply
					f.networkReply = func(w http.ResponseWriter, r *http.Request) bool { return credentials.reply(t, w, r) || previous(w, r) }
				}
			}
			if ownedCredentials {
				doc := wiredBulkOwnedCredentialDocument(t)
				if strings.HasPrefix(mode, "owned-metadata-") {
					doc = wiredBulkOwnedMetadataDocument(t)
				}
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if create {
					raw = []byte(strings.ReplaceAll(string(raw), "REPROVISION", "PROVISION"))
				}
				config.Spec.Source.Inline.Raw, source.raw = raw, raw
				declared = nil
				installWiredBulkIPAMFixture(t, f)
				previous := f.networkReply
				credentials := newWiredBulkCredentialFixture()
				f.networkReply = func(w http.ResponseWriter, r *http.Request) bool { return credentials.reply(t, w, r) || previous(w, r) }
				installWiredBulkOwnedCredentials(t, a, config, source, credentials)
				if strings.HasPrefix(mode, "owned-metadata-") {
					originalMetadata = journalState(t, a).ActionApplications[0].DeepCopy()
				}
			}
			config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global/Lab"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			batches, err := configuredWiredBulkBatches(config, source, declared)
			if err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
			if err != nil || op == nil {
				t.Fatal("prepare", err)
			}
			version := "v1:"
			if strings.HasPrefix(name, "ipam-") || credentialContext || dependencyOnly || ownedCredentials {
				version = "v2:"
			}
			if !strings.HasPrefix(op.Resource, wiredBulkProvisionPrefix+version) {
				t.Fatal("prepared group lost its dependency protocol version")
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, fixtureMembers)
			task, err := a.dispatchWiredBulkProvision(ctx, config, source, permit)
			if err != nil || task.ID != "batch-task" || f.calls != 1 {
				t.Fatal("dispatch", err)
			}
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			members := []*ops.ControllerConfigurationReservation{}
			for i := range list.Items {
				members = append(members, &list.Items[i])
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if err := a.beginWiredBulkVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
				t.Fatal("native verification", err)
			}
			if journalState(t, a).Operation.Phase != "Verifying" {
				t.Fatal("native observation skipped verification boundary")
			}
			if err := a.recordWiredBulkApplication(ctx, config, op.ID, batches[0], members, authorize, nil, time.Now()); err == nil {
				t.Fatal("missing device proof bypassed final verification")
			}
			proofCalls := 0
			proof := wiredBulkConvergenceProof(func(_ context.Context, current *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
				proofCalls++
				if completed.IsZero() || !completed.Equal(f.taskEnd) {
					t.Fatal("native completion boundary missing from proof")
				}
				if current.ID != op.ID || current.Phase != "Verifying" || len(targets) != 2 {
					t.Fatal("simulated proof lost complete operation binding")
				}
				return nil
			})
			if err := a.recordWiredBulkApplication(ctx, config, op.ID, batches[0], members, authorize, proof, time.Now()); err != nil {
				t.Fatal("receipt", err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expectedReceipts := 1
			if originalMetadata != nil {
				expectedReceipts++
			}
			if proofCalls != 1 || nc.Status.Configuration.Operation.Phase != "Verified" || len(nc.Status.Configuration.ActionApplications) != expectedReceipts {
				t.Fatal("shared receipt not published exactly once")
			}
			if originalMetadata != nil {
				found := false
				for _, receipt := range nc.Status.Configuration.ActionApplications {
					if reflect.DeepEqual(receipt, *originalMetadata) {
						found = true
					}
				}
				if !found {
					t.Fatal("bulk publication changed independent credential owner receipt")
				}
			}
			if _, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now()); err == nil {
				t.Fatal("verified group skipped settlement")
			}
			now := time.Now()
			completion, err := contract.GroupCompletionsForReceipt(nc, members, now)
			if err != nil {
				t.Fatal(err)
			}
			// Only this fixture supplies simulated device release/manager markers.
			for _, member := range members {
				member.Status.Completion = completion[member.UID]
				member.Status.Grant.Phase = "Released"
				member.Status.ManagerSettledAt = &metav1.Time{Time: now}
				if err := a.statusWriter.Status().Update(ctx, member); err != nil {
					t.Fatal(err)
				}
			}
			if err := contract.FinishVerifiedControllerGroup(ctx, a.statusReader, a.statusWriter, nc, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, now); err != nil {
				t.Fatal("finish", err)
			}
			restarted := *a
			prepared, err := restarted.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
			if err != nil || prepared != nil || journalState(t, a).Operation != nil || f.calls != 1 {
				t.Fatal("settled revision replayed after restart", err)
			}
		})
	}
}
