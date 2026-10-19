// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These are audit fixtures, not native execution qualification. Each successor
// seals the predecessor's retained deletion; no fixture is installed in the lab.
type discoveryLineageFixture struct {
	state *cvk.NetworkControllerConfigurationStatus
	now   time.Time
	seq   int
}

func newDiscoveryLineageFixture() *discoveryLineageFixture {
	return &discoveryLineageFixture{state: &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "fixture", ConfigUID: "owner"}}}, now: time.Now().UTC().Add(-time.Hour).Truncate(time.Second)}
}
func discoveryLineageVersions(password, enable int64) []cvk.NetworkControllerConfigurationCredentialVersion {
	out := []cvk.NetworkControllerConfigurationCredentialVersion{}
	for _, v := range []struct {
		field   string
		version int64
	}{{"password_list", password}, {"enable_password_list", enable}} {
		if v.version > 0 {
			out = append(out, cvk.NetworkControllerConfigurationCredentialVersion{Path: "/discovery/0/" + v.field, Version: v.version, SourceAlias: "device", SecretName: "credentials", SecretKey: v.field})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
func discoveryLineageBase(versions []cvk.NetworkControllerConfigurationCredentialVersion) string {
	bindings := []discoveryCredentialVersionBinding{}
	for _, v := range versions {
		bindings = append(bindings, discoveryCredentialVersionBinding{Field: discoveryCredentialFieldForPath(v.Path), Version: v.Version, Alias: v.SourceAlias, SecretName: v.SecretName, SecretKey: v.SecretKey})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Field < bindings[j].Field })
	return discoveryCredentialInputPrefix + discoveryPublicDigest("fixture") + ":" + discoveryPublicDigest("public") + ":" + featureIdentityDigest(bindings)
}
func (f *discoveryLineageFixture) op(resource, action string, versions []cvk.NetworkControllerConfigurationCredentialVersion) cvk.NetworkControllerConfigurationOperation {
	f.seq++
	f.now = f.now.Add(time.Second)
	stamp := metav1.NewTime(f.now)
	return cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", f.seq), ConfigName: "fixture", ConfigUID: "owner", ConfigGeneration: int64(f.seq), ControllerGeneration: 1, Section: "inventory", Resource: resource, Action: action, Phase: "Verified", TaskID: fmt.Sprintf("task-%d", f.seq), SubmittedAt: &stamp, CredentialVersions: slices.Clone(versions)}
}
func (f *discoveryLineageFixture) create(t *testing.T, versions []cvk.NetworkControllerConfigurationCredentialVersion, context string) {
	t.Helper()
	resource := discoveryLineageBase(versions)
	if context != "" {
		resource = discoveryCredentialContinuation(resource, context)
	}
	op := f.op(resource, "Create", versions)
	id := fmt.Sprintf("job-%d", f.seq)
	stamp := metav1.NewTime(f.now)
	effect := discoveryCredentialPublicEffect{mask: discoveryCredentialRequiredMask, digest: discoveryPublicDigest("native-public")}
	f.state.ActionApplications = append(f.state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialEffectReceiptKey(discoveryPublicDigest("fixture"), id, effect), Operation: op, VerifiedAt: &stamp})
	f.state.CredentialApplications = append(f.state.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: "inventory", Resource: resource, ConfigUID: "owner", OperationID: op.ID, TargetID: id, Versions: slices.Clone(versions)})
}
func (f *discoveryLineageFixture) retire(t *testing.T, versions []cvk.NetworkControllerConfigurationCredentialVersion) string {
	t.Helper()
	h, err := discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	creator := h.latest[discoveryPublicDigest("fixture")]
	op := f.op(discoveryCredentialRetirePrefix+creator.receipt.Operation.ID+":"+creator.digest()+":"+discoveryLineageBase(versions), "Delete", versions)
	op.TargetID = creator.identity.id
	stamp := metav1.NewTime(f.now)
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialRetiredPrefix + creator.receipt.Operation.ID + ":" + op.TargetID, Operation: op, VerifiedAt: &stamp}
	f.state.ActionApplications = append(f.state.ActionApplications, receipt)
	return (&discoveryCredentialRetirementProof{receipt: receipt}).digest()
}
func TestDiscoveryCredentialRetirementLineage(t *testing.T) {
	f := newDiscoveryLineageFixture()
	one, two := discoveryLineageVersions(1, 1), discoveryLineageVersions(2, 0)
	f.create(t, one, "")
	context := f.retire(t, two)
	h, err := discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	expected := discoveryCredentialContinuation(discoveryLineageBase(two), context)
	if got, err := h.input(discoveryPublicDigest("fixture"), discoveryLineageBase(two)); err != nil || got != expected {
		t.Fatal("retirement did not bind the successor", err)
	}
	if _, err := h.input(discoveryPublicDigest("fixture"), discoveryLineageBase(one)); err == nil {
		t.Fatal("changed queued input accepted")
	}
	if err := h.absentRetired(map[string]discoveryMetadata{"fixture": {ID: "job-1"}}); err == nil {
		t.Fatal("retired UUID reappearance accepted")
	}
	f.create(t, two, context)
	h, err = discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal("valid replacement history", err)
	}
	for _, bad := range []struct {
		name     string
		versions []cvk.NetworkControllerConfigurationCredentialVersion
	}{
		{"rollback", discoveryLineageVersions(1, 0)},
		{"reintroduced-old-counter", discoveryLineageVersions(2, 1)},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if err := h.transition(discoveryPublicDigest("fixture"), bad.versions); err == nil {
				t.Fatal("version floor bypassed")
			}
		})
	}
	remapped := discoveryLineageVersions(2, 0)
	remapped[0].SourceAlias = "other"
	if err := h.transition(discoveryPublicDigest("fixture"), remapped); err == nil {
		t.Fatal("same-version source remap accepted")
	}
	three := discoveryLineageVersions(3, 2)
	context = f.retire(t, three)
	f.create(t, three, context)
	if _, err := discoveryCredentialLineages(f.state, "owner"); err != nil {
		t.Fatal("higher-counter reintroduction rejected", err)
	}
	if len(f.state.CredentialApplications) != 3 || len(f.state.ActionApplications) != 5 {
		t.Fatal("history overwritten")
	}
}
func TestDiscoveryCredentialRetirementHistoryTampering(t *testing.T) {
	f := newDiscoveryLineageFixture()
	f.create(t, discoveryLineageVersions(1, 1), "")
	context := f.retire(t, discoveryLineageVersions(2, 0))
	f.create(t, discoveryLineageVersions(2, 0), context)
	for _, test := range []struct {
		name string
		edit func(*cvk.NetworkControllerConfigurationStatus)
	}{
		{"delete-is-not-credential-delivery", func(s *cvk.NetworkControllerConfigurationStatus) {
			s.CredentialApplications = append(s.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: "inventory", Resource: "unrelated", OperationID: s.ActionApplications[1].Operation.ID})
		}},
		{"missing-root", func(s *cvk.NetworkControllerConfigurationStatus) {
			s.ActionApplications = s.ActionApplications[1:]
			s.CredentialApplications = s.CredentialApplications[1:]
		}},
		{"missing-retirement", func(s *cvk.NetworkControllerConfigurationStatus) {
			s.ActionApplications = append(s.ActionApplications[:1], s.ActionApplications[2:]...)
		}},
		{"altered-retirement-time", func(s *cvk.NetworkControllerConfigurationStatus) {
			stamp := metav1.NewTime(f.now.Add(-time.Second * 30))
			s.ActionApplications[1].VerifiedAt = &stamp
		}},
		{"wrong-retirement-uuid", func(s *cvk.NetworkControllerConfigurationStatus) {
			s.ActionApplications[1].Operation.TargetID = "foreign"
		}},
		{"duplicate-operation-id", func(s *cvk.NetworkControllerConfigurationStatus) {
			s.ActionApplications = append(s.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: "unrelated", Operation: *s.ActionApplications[1].Operation.DeepCopy()})
		}},
		{"pruned-retirement-key", func(s *cvk.NetworkControllerConfigurationStatus) { s.ActionApplications[1].Resource = "unrelated" }},
		{"pruned-version-receipt", func(s *cvk.NetworkControllerConfigurationStatus) {
			s.CredentialApplications = s.CredentialApplications[:1]
		}},
		{"reused-native-uuid", func(s *cvk.NetworkControllerConfigurationStatus) {
			r := &s.ActionApplications[2]
			r.Resource = strings.TrimSuffix(r.Resource, "job-3") + "job-1"
			s.CredentialApplications[1].TargetID = "job-1"
		}},
		{"fork", func(s *cvk.NetworkControllerConfigurationStatus) {
			r := *s.ActionApplications[2].DeepCopy()
			r.Operation.ID = strings.Repeat("f", 32)
			r.Resource = strings.TrimSuffix(r.Resource, "job-3") + "job-fork"
			s.ActionApplications = append(s.ActionApplications, r)
			app := *s.CredentialApplications[1].DeepCopy()
			app.OperationID = r.Operation.ID
			app.TargetID = "job-fork"
			s.CredentialApplications = append(s.CredentialApplications, app)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := f.state.DeepCopy()
			test.edit(state)
			if _, err := discoveryCredentialLineages(state, "owner"); err == nil {
				t.Fatal("tampered history accepted")
			}
		})
	}
}
func TestDiscoveryCredentialContinuationIdentity(t *testing.T) {
	base := discoveryLineageBase(discoveryLineageVersions(1, 0))
	context := strings.Repeat("a", 64)
	resource := discoveryCredentialContinuation(base, context)
	if got, link, ok := discoveryCredentialInputParts(resource); !ok || got != base || link != context {
		t.Fatal("continuation round trip")
	}
	for _, bad := range []string{strings.TrimSuffix(resource, context), resource + ":extra", strings.Replace(resource, context, strings.ToUpper(context), 1), strings.Replace(resource, ":v2:", ":v9:", 1)} {
		if _, _, ok := discoveryCredentialInputParts(bad); ok {
			t.Fatal("malformed continuation accepted")
		}
	}
}

func TestDiscoveryCredentialRetirementCapacity(t *testing.T) {
	fixture := newDiscoveryLineageFixture()
	fixture.create(t, discoveryLineageVersions(1, 1), "")
	history, err := discoveryCredentialLineages(fixture.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	creator := history.latest[discoveryPublicDigest("fixture")]
	versions := discoveryLineageVersions(2, 0)
	op := fixture.op(discoveryCredentialRetirePrefix+creator.receipt.Operation.ID+":"+creator.digest()+":"+discoveryLineageBase(versions), "Delete", versions)
	op.TargetID = creator.identity.id
	op.Phase = "Prepared"
	op.TaskID = ""
	op.SubmittedAt = nil
	for _, test := range []struct {
		name  string
		edit  func(*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation)
		valid bool
	}{
		{"room-for-both", func(*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation) {}, true},
		{"wrong-target", func(_ *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) {
			op.TargetID = "foreign"
		}, false},
		{"one-action-slot", func(s *cvk.NetworkControllerConfigurationStatus, _ *cvk.NetworkControllerConfigurationOperation) {
			for len(s.ActionApplications) < 255 {
				s.ActionApplications = append(s.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: fmt.Sprintf("unrelated-%d", len(s.ActionApplications))})
			}
		}, false},
		{"two-action-slots", func(s *cvk.NetworkControllerConfigurationStatus, _ *cvk.NetworkControllerConfigurationOperation) {
			for len(s.ActionApplications) < 254 {
				s.ActionApplications = append(s.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: fmt.Sprintf("unrelated-%d", len(s.ActionApplications))})
			}
		}, true},
		{"credential-budget", func(s *cvk.NetworkControllerConfigurationStatus, _ *cvk.NetworkControllerConfigurationOperation) {
			for len(s.CredentialApplications) < 256 {
				s.CredentialApplications = append(s.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: "sites", Resource: fmt.Sprintf("unrelated-%d", len(s.CredentialApplications))})
			}
		}, false},
		{"action-byte-budget", func(s *cvk.NetworkControllerConfigurationStatus, _ *cvk.NetworkControllerConfigurationOperation) {
			s.ActionApplications = append(s.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: strings.Repeat("x", 256<<10)})
		}, false},
		{"missing-creator-credential", func(s *cvk.NetworkControllerConfigurationStatus, _ *cvk.NetworkControllerConfigurationOperation) {
			s.CredentialApplications = nil
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := fixture.state.DeepCopy()
			candidate := op.DeepCopy()
			test.edit(state, candidate)
			before := state.DeepCopy()
			err := discoveryCredentialRetirementCapacity(state, candidate)
			if (err == nil) != test.valid {
				t.Fatal("unexpected admission result", err)
			}
			if !reflect.DeepEqual(before, state) {
				t.Fatal("capacity check modified audit")
			}
		})
	}
}

func TestDiscoveryCredentialRetirementHistoricalRollback(t *testing.T) {
	for _, mode := range []string{"lower-counter", "source-change-same-counter", "removed-counter-reused"} {
		t.Run(mode, func(t *testing.T) {
			f := newDiscoveryLineageFixture()
			f.create(t, discoveryLineageVersions(2, 1), "")
			desired := discoveryLineageVersions(1, 1)
			if mode == "source-change-same-counter" {
				desired = discoveryLineageVersions(2, 1)
				desired[0].SourceAlias = "different"
			}
			if mode == "removed-counter-reused" {
				context := f.retire(t, discoveryLineageVersions(3, 0))
				f.create(t, discoveryLineageVersions(3, 0), context)
				desired = discoveryLineageVersions(3, 1)
			}
			// Seal even the invalid transition consistently. Structural hashing alone
			// must not make a historical credential rollback acceptable on restart.
			f.retire(t, desired)
			if _, err := discoveryCredentialLineages(f.state, "owner"); err == nil {
				t.Fatal("sealed historical rollback accepted")
			}
		})
	}
}

func TestDiscoveryCredentialTerminalPruneHistory(t *testing.T) {
	f := newDiscoveryLineageFixture()
	versions := discoveryLineageVersions(2, 0)
	f.create(t, versions, "")
	history, err := discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	creator := history.latest[discoveryPublicDigest("fixture")]
	op := f.op(discoveryCredentialPrunePrefix+creator.receipt.Operation.ID+":"+creator.digest()+":"+discoveryPublicDigest("validated empty model"), "Delete", nil)
	op.TargetID = creator.identity.id
	stamp := metav1.NewTime(f.now)
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialRetiredPrefix + creator.receipt.Operation.ID + ":" + op.TargetID, Operation: op, VerifiedAt: &stamp}
	f.state.ActionApplications = append(f.state.ActionApplications, receipt)
	history, err = discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal("terminal prune rejected", err)
	}
	resource, err := history.input(creator.identity.name, discoveryLineageBase(versions))
	if err != nil {
		t.Fatal(err)
	}
	candidate := f.op(resource, "Create", versions)
	candidate.Phase = "Prepared"
	candidate.TaskID = ""
	candidate.SubmittedAt = nil
	candidate.ConfigGeneration = op.ConfigGeneration
	if err := discoveryCredentialCreateCapacity(f.state, &candidate); err == nil {
		t.Fatal("same-input terminal recreation admitted")
	}
	candidate.ConfigGeneration++
	if err := discoveryCredentialCreateCapacity(f.state, &candidate); err != nil {
		t.Fatal("later input blocked", err)
	}
	copy := f.state.DeepCopy()
	copy.ActionApplications[1].Operation.CredentialVersions = versions
	if _, err := discoveryCredentialLineages(copy, "owner"); err == nil {
		t.Fatal("terminal prune claimed credential delivery")
	}
	context := (&discoveryCredentialRetirementProof{receipt: receipt}).digest()
	f.create(t, versions, context)
	if _, err := discoveryCredentialLineages(f.state, "owner"); err != nil {
		t.Fatal("terminal successor chain rejected", err)
	}
	// Even hash-consistent child evidence cannot bypass the later-input fence.
	child := f.state.DeepCopy()
	child.ActionApplications[2].Operation.ConfigGeneration = op.ConfigGeneration
	if _, err := discoveryCredentialLineages(child, "owner"); err == nil {
		t.Fatal("same-input child accepted")
	}
}
