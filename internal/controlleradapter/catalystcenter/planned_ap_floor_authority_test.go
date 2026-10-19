// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func floorConsumerCorrection(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, epoch, prior string) cvk.NetworkControllerConfigurationActionApplication {
	t.Helper()
	anchors, err := plannedAPIdentityRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	var anchor plannedAPIdentityRecord
	for _, r := range anchors {
		anchor = r
		break
	}
	base, _ := plannedAPPrivateParts(anchor.receipt.Operation.Resource)
	op := state.ActionApplications[2].Operation.DeepCopy()
	op.ID, op.ParentID, op.TargetID = strings.Repeat("d", 32), op.TargetID, anchor.id
	op.Resource = plannedAPFloorUpdatePrefix + strings.Join(base, ":") + ":" + prior + ":" + strings.Repeat("a", 64) + ":" + epoch
	return cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPAppliedResult(op, anchor), Operation: *op, VerifiedAt: op.SubmittedAt.DeepCopy()}
}

func TestPlannedAPFloorAuthoritySelection(t *testing.T) {
	for _, mode := range []string{"checkpoint", "latest", "later-floor", "missing-floor", "missing-epoch", "wrong-epoch", "legacy-producer", "stale-prior", "late-checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			state, floorRow := siteFloorEffectsFixture(t)
			floors, err := plannedAPFloorAuthorities(state)
			if err != nil {
				t.Fatal(err)
			}
			var key string
			for k := range floors {
				key = k
			}
			floor := floors[key]
			anchors, _ := plannedAPIdentityRecords(state)
			correction := floorConsumerCorrection(t, state, floorRow.Operation.ID, floor.digest)
			if mode != "checkpoint" {
				state.ActionApplications = append(state.ActionApplications, correction)
			}
			if mode == "later-floor" || mode == "late-checkpoint" {
				r := &state.ActionApplications[2]
				old := *r.DeepCopy()
				r.Operation.ID = strings.Repeat("e", 32)
				parts := strings.Split(strings.TrimPrefix(r.EffectReceipts[0], plannedAPFloorEffectPrefix), ":")
				parts[3], parts[4] = strings.Repeat("7", 64), siteReceiptDigest(correction)
				r.EffectReceipts[0] = plannedAPFloorEffectPrefix + strings.Join(parts, ":")
				root, _ := plannedAPFloorEffectRoot(r.EffectReceipts)
				base, _ := sitePrivateOperationParts(r.Operation.Resource)
				r.Operation.Resource = sitePrivateFloorUpdatePrefix + strings.Join(base, ":") + ":" + siteReceiptDigest(old) + ":" + root
				r.Resource = r.Resource[:len(r.Resource)-64] + root
				// Times deliberately tie: epoch identity selects the new floor.
			}
			if mode == "missing-floor" {
				state.ActionApplications = append(state.ActionApplications[:2], correction)
			}
			if mode == "missing-epoch" {
				state.ActionApplications[3].Operation.Resource = strings.TrimSuffix(correction.Operation.Resource, ":"+floorRow.Operation.ID)
			}
			if mode == "wrong-epoch" {
				state.ActionApplications[3].Operation.Resource = correction.Operation.Resource[:len(correction.Operation.Resource)-32] + strings.Repeat("9", 32)
			}
			if mode == "legacy-producer" {
				state.ActionApplications[3].Operation.Resource = plannedAPPrivateUpdatePrefix + strings.TrimSuffix(strings.TrimPrefix(correction.Operation.Resource, plannedAPFloorUpdatePrefix), ":"+floorRow.Operation.ID)
			}
			rows, err := plannedAPAppliedRecords(state)
			if mode == "missing-floor" || mode == "missing-epoch" {
				if err == nil {
					t.Fatal("lost floor binding was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			floors, err = plannedAPFloorAuthorities(state)
			if err != nil {
				t.Fatal(err)
			}
			mac, prior, epoch, err := plannedAPCurrentAuthority(key, anchors[key], rows, floors)
			if err != nil {
				t.Fatal(err)
			}
			latest := mode == "latest" || mode == "stale-prior"
			want := strings.Repeat("d", 64)
			if latest {
				want = strings.Repeat("a", 64)
			}
			if mode == "later-floor" || mode == "late-checkpoint" {
				want = strings.Repeat("7", 64)
			}
			if mac != want || latest && prior != siteReceiptDigest(correction) || !latest && prior != floors[key].digest || epoch != floors[key].floor.Operation.ID {
				t.Fatal("authority chosen by history/time instead of explicit epoch")
			}
			if mode == "wrong-epoch" || mode == "legacy-producer" || mode == "later-floor" {
				if plannedAPAppliedReceiptPresent(state, &state.ActionApplications[3].Operation) {
					t.Fatal("noncurrent correction acquired settlement authority")
				}
			}
			if mode == "stale-prior" {
				op := correction.Operation.DeepCopy()
				op.ID = strings.Repeat("8", 32)
				op.Phase = "Submitting"
				op.TaskID = ""
				if plannedAPApplyCapacity(state, op) == nil {
					t.Fatal("old checkpoint predecessor reused after correction")
				}
			}
			if mode == "late-checkpoint" {
				e := &plannedAPExecutor{identities: anchors, applied: rows, floorEffects: map[string]plannedAPFloorAuthority{key: floor}}
				if e.identityRecordsMatch(state) == nil {
					t.Fatal("late floor authority replacement escaped the dispatch guard")
				}
			}
		})
	}
}

func TestPlannedAPFloorAuthorityMembersAndSetOrder(t *testing.T) {
	state, floor := siteFloorEffectsFixture(t)
	other := *state.ActionApplications[1].DeepCopy()
	other.Operation.ID = strings.Repeat("b", 32)
	base, _ := plannedAPPrivateParts(other.Operation.Resource)
	base[0] = strings.Repeat("9", 64)
	other.Operation.Resource = plannedAPPrivateObservePrefix + strings.Join(base, ":")
	id := "33333333-3333-4333-8333-333333333333"
	other.Resource = plannedAPIdentityResource(&other.Operation, id, strings.Repeat("8", 64))
	effect := plannedAPFloorEffectPrefix + base[0] + ":" + id + ":" + base[2] + ":" + strings.Repeat("6", 64) + ":" + siteReceiptDigest(other)
	floor.EffectReceipts = append(floor.EffectReceipts, effect)
	root, err := plannedAPFloorEffectRoot(floor.EffectReceipts)
	if err != nil {
		t.Fatal(err)
	}
	floor.Operation.Resource = floor.Operation.Resource[:len(floor.Operation.Resource)-64] + root
	floor.Resource = floor.Resource[:len(floor.Resource)-64] + root
	state.ActionApplications[2] = floor
	state.ActionApplications = append(state.ActionApplications, other)
	floors, err := plannedAPFloorAuthorities(state)
	if err != nil || len(floors) != 2 {
		t.Fatal("multiple floor members missing", err)
	}
	firstKey := plannedAPResourceKey(state.ActionApplications[1].Operation.Resource)
	secondKey := plannedAPResourceKey(other.Operation.Resource)
	correction := floorConsumerCorrection(t, state, floor.Operation.ID, floors[firstKey].digest)
	// Pick the first AP explicitly; the fixture helper's map iteration must not
	// become identity authority in a two-member case.
	anchors, _ := plannedAPIdentityRecords(state)
	anchor := anchors[firstKey]
	base, _ = plannedAPPrivateParts(anchor.receipt.Operation.Resource)
	correction.Operation.TargetID = anchor.id
	correction.Operation.Resource = plannedAPFloorUpdatePrefix + strings.Join(base, ":") + ":" + floors[firstKey].digest + ":" + strings.Repeat("a", 64) + ":" + floor.Operation.ID
	correction.Resource = plannedAPAppliedResult(&correction.Operation, anchor)
	state.ActionApplications = append(state.ActionApplications, correction)
	rows, err := plannedAPAppliedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	firstMAC, _, _, err := plannedAPCurrentAuthority(firstKey, anchors[firstKey], rows, floors)
	if err != nil {
		t.Fatal(err)
	}
	secondMAC, _, _, err := plannedAPCurrentAuthority(secondKey, anchors[secondKey], rows, floors)
	if err != nil || firstMAC != strings.Repeat("a", 64) || secondMAC != strings.Repeat("6", 64) {
		t.Fatal("one AP correction changed another member's authority", err)
	}
	e := &plannedAPExecutor{identities: anchors, applied: rows, floorEffects: floors}
	state.ActionApplications[2].EffectReceipts[0], state.ActionApplications[2].EffectReceipts[1] = state.ActionApplications[2].EffectReceipts[1], state.ActionApplications[2].EffectReceipts[0]
	if err := e.identityRecordsMatch(state); err != nil {
		t.Fatal("API set ordering changed floor authority", err)
	}
}

func TestPlannedAPPrivateExpectedStateDoesNotAlias(t *testing.T) {
	e, o, _ := plannedAPKeyedFixture(t)
	rawBefore, _ := json.Marshal(o.native)
	typedBefore, _ := json.Marshal(o.actual)
	body := map[string]any{"position": map[string]any{"x": 7.0, "y": *o.actual.Position.Y, "z": *o.actual.Position.Z}}
	if _, err := e.privateUpdateResource(o, body); err != nil {
		t.Fatal(err)
	}
	rawAfter, _ := json.Marshal(o.native)
	typedAfter, _ := json.Marshal(o.actual)
	if string(rawBefore) != string(rawAfter) || string(typedBefore) != string(typedAfter) {
		t.Fatal("expected AP result mutated its original typed/native observation")
	}
	if err := e.checkPrivateBaseline(context.Background(), o); err != nil {
		t.Fatal("expected result calculation invalidated original baseline", err)
	}
}
