// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Immutable audit fixtures only. Persisting a projection does not qualify a
// public ownership change, native operation or credential delivery.
func discoveryCredentialCohortFixture(t *testing.T, password, enable int64) *cvk.NetworkControllerConfigurationStatus {
	t.Helper()
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "config-c", ConfigUID: "owner-c"}}}
	for i, uid := range []string{"owner-a", "owner-b", "owner-c"} {
		f := newDiscoveryLineageFixture()
		f.seq = i * 2
		versions := discoveryLineageVersions(password, enable)
		if i == 0 {
			versions = discoveryLineageVersions(2, 5)
		}
		if i == 1 {
			versions = discoveryLineageVersions(3, 0)
		}
		f.create(t, versions, "")
		if i < 2 {
			f.retire(t, discoveryLineageVersions(99, 0))
		}
		for j := range f.state.ActionApplications {
			f.state.ActionApplications[j].Operation.ConfigUID = uid
			f.state.ActionApplications[j].Operation.ConfigName = "config-" + strings.TrimPrefix(uid, "owner-")
		}
		for j := range f.state.CredentialApplications {
			f.state.CredentialApplications[j].ConfigUID = uid
		}
		if i < 2 {
			retirement := &f.state.ActionApplications[1].Operation
			parts, ok := discoveryCredentialRetirementParts(retirement)
			if !ok {
				t.Fatal("invalid fixture retirement")
			}
			pair := featureIdentityDigest([]any{f.state.ActionApplications[0], f.state.CredentialApplications[0]})
			retirement.Resource = strings.Replace(retirement.Resource, ":"+parts.pair+":", ":"+pair+":", 1)
		}
		state.ActionApplications = append(state.ActionApplications, f.state.ActionApplications...)
		state.CredentialApplications = append(state.CredentialApplications, f.state.CredentialApplications...)
	}
	return state
}

var credentialCohortModes = []string{"complete", "empty-current", "reordered", "open-archive", "missing-pair", "missing-creator", "missing-retirement", "unknown-receipt", "unknown-application", "foreign-pending", "group-cancellation", "duplicate-operation", "foreign-operation-collision", "native-id-reuse", "owner-name", "wrong-reservation", "counter-reuse", "removed-field-reuse"}

func TestDiscoveryCredentialHistoryCohort(t *testing.T) {
	for _, mode := range credentialCohortModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryCredentialHistoryCohort(t, mode, nil) })
	}
}

func exerciseDiscoveryCredentialHistoryCohort(t *testing.T, mode string, persist func(*cvk.NetworkControllerConfigurationStatus) *cvk.NetworkControllerConfigurationStatus) {
	t.Helper()
	password, enable := int64(4), int64(6)
	if mode == "counter-reuse" {
		password = 3
	}
	if mode == "removed-field-reuse" {
		enable = 5
	}
	state := discoveryCredentialCohortFixture(t, password, enable)
	baseline, err := discoveryCredentialHistoryCohort(discoveryCredentialCohortFixture(t, 4, 6), "owner-c")
	if err != nil {
		t.Fatal("valid cohort fixture", err)
	}
	switch mode {
	case "empty-current":
		state.ActionApplications = state.ActionApplications[:4]
		state.CredentialApplications = state.CredentialApplications[:2]
	case "reordered":
		slices.Reverse(state.ActionApplications)
		slices.Reverse(state.CredentialApplications)
	case "missing-retirement":
		state.ActionApplications[1].VerifiedAt = nil
	case "open-archive":
		state.ActionApplications = append(state.ActionApplications[:1], state.ActionApplications[2:]...)
	case "missing-pair":
		state.CredentialApplications = state.CredentialApplications[1:]
	case "missing-creator":
		state.ActionApplications = state.ActionApplications[1:]
	case "unknown-receipt":
		state.ActionApplications[0].Resource = "discovery-credential-record:v99:reserved"
	case "unknown-application":
		state.CredentialApplications[0].Resource = "discovery-credential-input:v99:reserved"
	case "foreign-pending":
		state.Operation = state.ActionApplications[0].Operation.DeepCopy()
	case "group-cancellation":
		op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("d", 32), ConfigName: "config-a", ConfigUID: "owner-a", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Resource: "fabric-pending-event:receipt", TargetID: "event-id", ParentID: "fabric-id", Action: "Update", Phase: "Prepared"}, []contract.GroupTargetIdentity{{InventoryID: "inventory-a", PhysicalIdentity: "serial-a", Device: ops.ConfigurationReservationReference{Name: "device-a", UID: "device-a"}, Node: ops.ConfigurationReservationReference{Name: "node-a", UID: "node-a"}}})
		if err != nil {
			t.Fatal(err)
		}
		stamp := metav1.NewTime(time.Now().UTC().Add(-time.Hour).Truncate(time.Second))
		op.Phase = "Cancelling"
		op.CancellationRequestedAt = &stamp
		state.GroupCancellations = append(state.GroupCancellations, cvk.NetworkControllerConfigurationGroupCancellation{OperationID: op.ID, Operation: *op, Members: []ops.ConfigurationReservationReference{{Name: "member-a", UID: "member-a"}}, SettledAt: stamp})
	case "duplicate-operation":
		state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
	case "foreign-operation-collision":
		row := *state.ActionApplications[0].DeepCopy()
		row.Resource = "pnp-register:" + strings.Repeat("a", 64)
		row.Operation.Resource = row.Resource
		row.Operation.CredentialVersions = nil
		state.ActionApplications = append(state.ActionApplications, row)
	case "native-id-reuse":
		identity, err := parseDiscoveryCredentialReceipt(state.ActionApplications[4].Resource)
		if err != nil {
			t.Fatal(err)
		}
		identity.effect.digest = discoveryPublicDigest("different-native-public")
		state.ActionApplications[4].Resource = discoveryCredentialEffectReceiptKey(identity.name, "job-1", identity.effect)
		state.CredentialApplications[2].TargetID = "job-1"
	case "owner-name":
		state.ActionApplications[4].Operation.ConfigName = "different-config"
	case "wrong-reservation":
		state.Owners[0].ConfigUID = "other"
	}
	if persist != nil {
		state = persist(state)
	}
	before := state.DeepCopy()
	cohort, err := discoveryCredentialHistoryCohort(state, "owner-c")
	success := mode == "complete" || mode == "empty-current" || mode == "reordered"
	if (err == nil) != success || (cohort != nil) != success {
		t.Fatal("unexpected cohort projection", mode, err)
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("projection mutated original owners or receipt history")
	}
	// The existing public execution parser must keep rejecting foreign history
	// until a qualified custody record can authorize the projection.
	if _, err := discoveryCredentialLineages(state, "owner-c"); err == nil {
		t.Fatal("structural projection enabled unsealed public lineage execution")
	}
	if !success {
		return
	}
	if len(cohort.archived) != 2 || !lowerDiscoveryDigest(cohort.archiveDigest) || cohort.archiveDigest != baseline.archiveDigest {
		t.Fatal("archive digest changed with current rows or ordering")
	}
	for _, test := range []struct {
		name             string
		password, enable int64
		ok               bool
	}{{"same-current", 4, 6, true}, {"archived-password", 3, 6, false}, {"removed-field", 4, 5, false}, {"queued-is-not-delivered", 4, 6, true}} {
		t.Run(test.name, func(t *testing.T) {
			desired := discoveryLineageVersions(test.password, test.enable)
			for i := range desired {
				desired[i].Path = strings.Replace(desired[i].Path, "/0/", "/9/", 1)
			}
			err := cohort.transition(discoveryPublicDigest("fixture"), desired)
			if (err == nil) != test.ok {
				t.Fatal("cross-owner logical-field floor lost", err)
			}
		})
	}
	for _, id := range []string{"job-1", "job-3"} {
		if err := cohort.absentRetired(map[string]discoveryMetadata{"renamed": {ID: id, Name: "renamed"}}); err == nil {
			t.Fatal("archived native UUID returned", id)
		}
	}
	if err := cohort.absentRetired(map[string]discoveryMetadata{"current": {ID: "job-5", Name: "fixture"}}); err != nil {
		t.Fatal("active current UUID mistaken for retired history", err)
	}
}

func TestDiscoveryCredentialCohortDigestRequiresAuthority(t *testing.T) {
	state := discoveryCredentialCohortFixture(t, 4, 6)
	before, err := discoveryCredentialHistoryCohort(state, "owner-c")
	if err != nil {
		t.Fatal(err)
	}
	// Removing a whole closed owner can still be structurally well formed. The
	// changed digest is why an independently retained custody seal is mandatory.
	state.ActionApplications = state.ActionApplications[2:]
	state.CredentialApplications = state.CredentialApplications[1:]
	after, err := discoveryCredentialHistoryCohort(state, "owner-c")
	if err != nil {
		t.Fatal(err)
	}
	if before.archiveDigest == after.archiveDigest || before.allDigest == after.allDigest {
		t.Fatal("complete archived-owner loss did not change the seal input")
	}
	seal := discoveryCredentialHistorySealPrefix + before.archiveDigest
	if before.validateArchiveSeal(seal) != nil || after.validateArchiveSeal(seal) == nil {
		t.Fatal("archive loss was accepted against the retained seal")
	}
}

func TestDiscoveryCredentialCohortSealAcrossReservation(t *testing.T) {
	state := discoveryCredentialCohortFixture(t, 4, 6)
	state.ActionApplications = state.ActionApplications[:4]
	state.CredentialApplications = state.CredentialApplications[:2]
	previous := state.DeepCopy()
	previous.Owners[0] = cvk.NetworkControllerSectionOwner{Section: "inventory", ConfigName: "config-b", ConfigUID: "owner-b"}
	closed, err := discoveryCredentialHistoryCohort(previous, "owner-b")
	if err != nil {
		t.Fatal(err)
	}
	successor, err := discoveryCredentialHistoryCohort(state, "owner-c")
	if err != nil {
		t.Fatal(err)
	}
	// Compare derived views only; no public transfer or controller write occurs.
	if closed.allDigest != successor.archiveDigest || successor.validateArchiveSeal(discoveryCredentialHistorySealPrefix+closed.allDigest) != nil {
		t.Fatal("reservation metadata changed the retained history seal")
	}
	for _, seal := range []string{"", "credential-discovery-history:v99:" + closed.allDigest, discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)} {
		if successor.validateArchiveSeal(seal) == nil {
			t.Fatal("legacy, future or altered seal authorized archive consumption")
		}
	}
}
