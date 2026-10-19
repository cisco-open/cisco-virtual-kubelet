// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Audit fixtures only. Native effects and persistence are qualified separately
// through the existing public revision lifecycle and real API scenarios.
func TestDiscoveryCredentialMetadataInputProtocol(t *testing.T) {
	f := newDiscoveryLineageFixture()
	effect := discoveryCredentialPublicEffect{mask: discoveryCredentialRequiredMask, digest: discoveryPublicDigest("metadata-public")}
	base := discoveryLineageBase(nil)
	prototype := discoveryCredentialScopedInputAs(base, effect, discoveryCredentialMetadataScopePrefix)
	op := f.op(prototype, "Create", nil)
	if _, ok := discoveryCredentialOperationInput(&op); !ok {
		t.Fatal("explicit metadata input rejected")
	}
	context := discoveryPublicDigest("retirement")
	continued := discoveryCredentialContinuation(prototype, context)
	canonical, parent, ok := discoveryCredentialInputParts(continued)
	observed, scoped := discoveryCredentialRetainedEffect(continued)
	if !ok || canonical != base || parent != context || !scoped || observed != effect || discoveryCredentialInputPrototype(continued) != prototype || !discoveryCredentialRetirementNextMatches(prototype, continued) || len(continued) > 512 {
		t.Fatal("metadata scope/continuation was weakened")
	}
	versions := discoveryLineageVersions(2, 0)
	for _, mode := range []string{"legacy-zero", "scope-zero", "continuation-zero", "metadata-with-versions", "scope-with-dropped-versions", "unknown-version", "wrong-target", "queued-scope-change"} {
		t.Run(mode, func(t *testing.T) {
			copy := op.DeepCopy()
			switch mode {
			case "legacy-zero":
				copy.Resource = base
			case "scope-zero":
				copy.Resource = discoveryCredentialScopedInput(base, effect)
			case "continuation-zero":
				copy.Resource = discoveryCredentialContinuation(base, context)
			case "metadata-with-versions":
				copy.CredentialVersions = versions
			case "scope-with-dropped-versions":
				copy.Resource = discoveryCredentialScopedInput(discoveryLineageBase(versions), effect)
			case "unknown-version":
				copy.Resource = strings.Replace(prototype, ":v4:", ":v99:", 1)
			case "wrong-target":
				copy.TargetID = "foreign-job"
			case "queued-scope-change":
				changed := effect
				changed.digest = discoveryPublicDigest("other-public")
				next := discoveryCredentialScopedInputAs(base, changed, discoveryCredentialMetadataScopePrefix)
				if discoveryCredentialRetirementNextMatches(next, continued) {
					t.Fatal("changed queued public scope accepted")
				}
				return
			}
			if _, ok := discoveryCredentialOperationInput(copy); ok {
				t.Fatal("missing or fabricated credential bindings accepted")
			}
		})
	}
}

func TestDiscoveryCredentialMetadataActionHistory(t *testing.T) {
	f := newDiscoveryLineageFixture()
	versions := discoveryLineageVersions(2, 0)
	f.create(t, versions, "")
	original := f.state.DeepCopy()
	history, err := discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	creator := history.latest[discoveryPublicDigest("fixture")]
	effect := discoveryCredentialPublicEffect{mask: discoveryCredentialRequiredMask, digest: discoveryPublicDigest("metadata-public")}
	prototype := discoveryCredentialScopedInputAs(discoveryLineageBase(nil), effect, discoveryCredentialMetadataScopePrefix)
	retire := f.op(discoveryCredentialRetirePrefix+creator.receipt.Operation.ID+":"+creator.digest()+":"+prototype, "Delete", nil)
	retire.TargetID = creator.identity.id
	if err := discoveryCredentialRetirementCapacity(f.state, &retire); err != nil {
		t.Fatal("metadata successor was not reserved before deletion", err)
	}
	if !reflect.DeepEqual(original, f.state) {
		t.Fatal("capacity check mutated journal")
	}
	stamp := metav1.NewTime(f.now)
	retirement := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialRetiredPrefix + creator.receipt.Operation.ID + ":" + retire.TargetID, Operation: retire, VerifiedAt: &stamp}
	f.state.ActionApplications = append(f.state.ActionApplications, retirement)
	context := (&discoveryCredentialRetirementProof{receipt: retirement}).digest()
	op := f.op(discoveryCredentialContinuation(prototype, context), "Create", nil)
	metadataStamp := metav1.NewTime(f.now)
	metadata := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialEffectReceiptKey(discoveryPublicDigest("fixture"), "metadata-job", effect), Operation: op, VerifiedAt: &metadataStamp}
	f.state.ActionApplications = append(f.state.ActionApplications, metadata)
	if !discoveryCredentialReceiptPairPresent(f.state, &op) {
		t.Fatal("action-only metadata proof was not recognized")
	}
	history, err = discoveryCredentialLineages(f.state, "owner")
	if err != nil || len(history.creators) != 2 || len(history.retired) != 1 {
		t.Fatal("metadata replacement lost ancestry", err)
	}
	if !reflect.DeepEqual(f.state.CredentialApplications, original.CredentialApplications) {
		t.Fatal("metadata creation invented delivery")
	}
	if err := history.transition(discoveryPublicDigest("fixture"), versions); err == nil {
		t.Fatal("removed credential counter was reused")
	}
	if err := history.transition(discoveryPublicDigest("fixture"), discoveryLineageVersions(3, 0)); err != nil {
		t.Fatal("advanced credential counter refused", err)
	}
	if input, err := history.input(discoveryPublicDigest("fixture"), prototype); err != nil || input != op.Resource {
		t.Fatal("settled metadata incarnation replanned", err)
	}
	c := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{UID: "owner"}}
	nc := &cvk.NetworkController{Status: cvk.NetworkControllerStatus{Configuration: f.state.DeepCopy()}}
	if !discoveryCredentialVersionedCreatorRecorded(nc, c) {
		t.Fatal("original versioned creator lost")
	}
	nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{metadata}
	nc.Status.Configuration.CredentialApplications = nil
	if discoveryCredentialVersionedCreatorRecorded(nc, c) {
		t.Fatal("unwitnessed metadata owner enabled versioned-owner routing")
	}
	for _, damage := range []string{"fabricated-delivery", "missing-versioned-pair", "metadata-version-binding", "queued-scope-change", "queued-scope-downgrade"} {
		t.Run(damage, func(t *testing.T) {
			state := f.state.DeepCopy()
			switch damage {
			case "fabricated-delivery":
				fake := *original.CredentialApplications[0].DeepCopy()
				fake.Resource, fake.OperationID, fake.TargetID = op.Resource, op.ID, "metadata-job"
				state.CredentialApplications = append(state.CredentialApplications, fake)
			case "missing-versioned-pair":
				state.CredentialApplications = nil
			case "metadata-version-binding":
				state.ActionApplications[2].Operation.CredentialVersions = versions
			case "queued-scope-change":
				different := effect
				different.digest = discoveryPublicDigest("different-public")
				changed := discoveryCredentialScopedInputAs(discoveryLineageBase(nil), different, discoveryCredentialMetadataScopePrefix)
				state.ActionApplications[1].Operation.Resource = discoveryCredentialRetirePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + changed
			case "queued-scope-downgrade":
				state.ActionApplications[1].Operation.Resource = discoveryCredentialRetirePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + discoveryLineageBase(nil)
			}
			if _, err := discoveryCredentialLineages(state, "owner"); err == nil {
				t.Fatal("damaged metadata/credential ancestry accepted")
			}
		})
	}
}
