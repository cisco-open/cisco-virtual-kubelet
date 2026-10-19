// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func siteFloorEffectsFixture(t *testing.T, bind ...func(*cvk.NetworkControllerConfigurationOperation)) (*cvk.NetworkControllerConfigurationStatus, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	e, o, anchor := plannedAPKeyedFixture(t)
	created := siteCreatorTestRecord()
	created.Operation.ID = strings.Repeat("f", 32)
	created.Operation.ParentID = "building"
	created.Operation.Resource = sitePrivateCreateResource(desiredSite{kind: "floor", path: o.want.floor}, &configapi.NetworkControllerConfig{}, e.comparison)
	at := metav1.NewTime(time.Now().Add(-2 * time.Second).UTC().Truncate(time.Second))
	created.Operation.SubmittedAt, created.VerifiedAt = &at, &at
	anchor.Operation.SubmittedAt, anchor.VerifiedAt = &at, &at
	if len(bind) > 0 {
		bind[0](&created.Operation)
		bind[0](&anchor.Operation)
	}
	created.Resource = siteCreatedResource(&created.Operation, "floor", siteOwnedTestUUID, strings.Repeat("b", 64))
	effect := plannedAPFloorEffectPrefix + strings.TrimPrefix(plannedAPResource(o.want), "planned-ap:") + ":" + o.actual.ID + ":" + e.comparison.keyID + ":" + strings.Repeat("d", 64) + ":" + siteReceiptDigest(anchor)
	root, err := plannedAPFloorEffectRoot([]string{effect})
	if err != nil {
		t.Fatal(err)
	}
	op := created.Operation.DeepCopy()
	op.ID, op.Action, op.TargetID, op.TaskID = strings.Repeat("c", 32), "Update", siteOwnedTestUUID, "original-floor-task"
	op.Resource = sitePrivateFloorUpdatePrefix + strings.TrimPrefix(created.Operation.Resource, sitePrivateCreatePrefix) + ":" + siteReceiptDigest(created) + ":" + root
	verified := metav1.NewTime(at.Add(time.Second))
	op.SubmittedAt = &verified
	creators, err := siteCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{created}})
	if err != nil {
		t.Fatal(err)
	}
	creator, ok := siteCreatorForResource(creators, op.Resource)
	if !ok {
		t.Fatal("floor creator missing")
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteAppliedResult(op, creator, strings.Repeat("e", 64)), Operation: *op, VerifiedAt: &verified, EffectReceipts: []string{effect}}
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{created, anchor, row}, Operation: op.DeepCopy()}
	return state, row
}

func TestSiteFloorEffectReceiptBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "missing-effects", "changed-effect", "changed-prior", "changed-root", "missing-result-root", "missing-producer-root", "wrong-key", "wrong-owner", "wrong-parent", "future-anchor", "missing-anchor", "pruned-anchor", "missing-task", "unverified", "duplicate-operation", "legacy-effects", "AP-is-floor", "copy"} {
		t.Run(mode, func(t *testing.T) {
			state, row := siteFloorEffectsFixture(t)
			r := &state.ActionApplications[2]
			switch mode {
			case "missing-effects":
				r.EffectReceipts = nil
			case "changed-effect":
				r.EffectReceipts[0] = strings.Replace(r.EffectReceipts[0], strings.Repeat("d", 64), strings.Repeat("a", 64), 1)
			case "changed-prior":
				r.EffectReceipts[0] = r.EffectReceipts[0][:len(r.EffectReceipts[0])-64] + strings.Repeat("0", 64)
			case "changed-root":
				r.Operation.Resource = r.Operation.Resource[:len(r.Operation.Resource)-64] + strings.Repeat("0", 64)
			case "missing-result-root":
				r.Resource = r.Resource[:len(r.Resource)-65]
			case "missing-producer-root":
				r.Operation.Resource = r.Operation.Resource[:len(r.Operation.Resource)-65]
			case "wrong-key", "wrong-owner", "wrong-parent", "future-anchor", "pruned-anchor":
				anchor := &state.ActionApplications[1]
				if mode == "wrong-key" {
					anchor.Operation.Resource = strings.ReplaceAll(anchor.Operation.Resource, strings.Repeat("e", 64), strings.Repeat("a", 64))
				} else if mode == "wrong-owner" {
					anchor.Operation.ConfigUID = "foreign"
				} else if mode == "wrong-parent" {
					anchor.Operation.ParentID = "33333333-3333-4333-8333-333333333333"
				} else if mode == "future-anchor" {
					at := *r.VerifiedAt.DeepCopy()
					at.Time = at.Add(time.Second)
					anchor.VerifiedAt = &at
				} else {
					anchor.Resource = "pruned-anchor"
				}
			case "missing-task":
				r.Operation.TaskID = ""
			case "unverified":
				r.Operation.Phase = "Verifying"
			case "legacy-effects":
				legacy := sitePrivateUpdatePrefix + strings.TrimPrefix(state.ActionApplications[0].Operation.Resource, sitePrivateCreatePrefix) + ":" + siteReceiptDigest(state.ActionApplications[0])
				r.Operation.Resource = legacy
				r.Resource = r.Resource[:len(r.Resource)-65]
			case "AP-is-floor":
				anchor := &state.ActionApplications[1]
				parts := strings.Split(strings.TrimPrefix(anchor.Resource, plannedAPPrivateIdentityPrefix), ":")
				anchor.Resource = plannedAPIdentityResource(&anchor.Operation, siteOwnedTestUUID, parts[4])
				r.EffectReceipts[0] = strings.Replace(r.EffectReceipts[0], plannedAPIdentityTestUUID, siteOwnedTestUUID, 1)
				root, err := plannedAPFloorEffectRoot(r.EffectReceipts)
				if err != nil {
					t.Fatal(err)
				}
				r.Operation.Resource = r.Operation.Resource[:len(r.Operation.Resource)-64] + root
				r.Resource = r.Resource[:len(r.Resource)-64] + root
			}
			if mode == "missing-anchor" {
				state.ActionApplications = append(state.ActionApplications[:1], *r)
			}
			if mode == "duplicate-operation" {
				state.ActionApplications = append(state.ActionApplications, row)
			}
			rows, err := siteAppliedRecords(state)
			valid := mode == "valid" || mode == "copy"
			if (err == nil) != valid || siteAppliedReceiptPresent(state, &row.Operation) != valid {
				t.Fatal("invalid or pruned floor effect authority accepted", err)
			}
			if mode == "copy" {
				for _, copy := range rows {
					copy.EffectReceipts[0] = "changed"
				}
				if state.ActionApplications[2].EffectReceipts[0] == "changed" {
					t.Fatal("decoded effect receipt aliases stored status")
				}
			}
		})
	}
}

func TestPlannedAPFloorEffectSetRoot(t *testing.T) {
	state, row := siteFloorEffectsFixture(t)
	first := row.EffectReceipts[0]
	second := strings.Replace(first, plannedAPIdentityTestUUID, "33333333-3333-4333-8333-333333333333", 1)
	parts := strings.Split(strings.TrimPrefix(second, plannedAPFloorEffectPrefix), ":")
	parts[0] = strings.Repeat("9", 64)
	second = plannedAPFloorEffectPrefix + strings.Join(parts, ":")
	a, err := plannedAPFloorEffectRoot([]string{first, second})
	b, otherErr := plannedAPFloorEffectRoot([]string{second, first})
	if err != nil || otherErr != nil || a != b {
		t.Fatal("root depends on API set order")
	}
	left, right := *row.DeepCopy(), *row.DeepCopy()
	left.EffectReceipts, right.EffectReceipts = []string{first, second}, []string{second, first}
	if siteReceiptDigest(left) != siteReceiptDigest(right) || left.EffectReceipts[0] != first {
		t.Fatal("public predecessor digest depends on API set order or mutates its input")
	}
	for _, effects := range [][]string{nil, {""}, {first, first}, {first, strings.Replace(first, strings.Repeat("d", 64), strings.Repeat("0", 64), 1)}, {first, strings.Replace(second, "33333333-3333-4333-8333-333333333333", plannedAPIdentityTestUUID, 1)}, make([]string, 257)} {
		if _, err := plannedAPFloorEffectRoot(effects); err == nil {
			t.Fatal("invalid or aliased effect set accepted")
		}
	}
	// Neither a plain floor update nor an AP creator may acquire effect scope.
	state.ActionApplications[0].EffectReceipts = []string{first}
	if _, err := siteCreatedRecords(state); err == nil {
		t.Fatal("site creator accepted floor-related state")
	}
	state, _ = siteFloorEffectsFixture(t)
	state.ActionApplications[1].EffectReceipts = []string{first}
	if _, err := plannedAPIdentityRecords(state); err == nil {
		t.Fatal("AP identity receipt accepted an extra native effect")
	}
}

func TestSiteFloorEffectsPreserveLegacyReceiptDigest(t *testing.T) {
	creator, applied := siteAppliedTestRecords()
	for _, row := range []cvk.NetworkControllerConfigurationActionApplication{creator, applied} {
		// The old struct's JSON field order/omission is a persisted contract.
		legacy := struct {
			Section    string                                      `json:"section"`
			Resource   string                                      `json:"resource"`
			Operation  cvk.NetworkControllerConfigurationOperation `json:"operation"`
			VerifiedAt *metav1.Time                                `json:"verifiedAt,omitempty"`
		}{row.Section, row.Resource, row.Operation, row.VerifiedAt}
		raw, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if siteReceiptDigest(row) != hex.EncodeToString(sum[:]) {
			t.Fatal("new optional effect field changed legacy predecessor digests")
		}
	}
	state, row := siteFloorEffectsFixture(t)
	state.ActionApplications = state.ActionApplications[:2]
	op := row.Operation.DeepCopy()
	op.Phase, op.TaskID = "Submitting", ""
	// Capacity reserves bounded metadata; dispatch separately binds native effects
	// and requires explicit handoff opt-in plus source/owner guards.
	if err := siteApplyCapacity(state, op); err != nil {
		t.Fatal("bounded floor effect capacity refused", err)
	}
}
