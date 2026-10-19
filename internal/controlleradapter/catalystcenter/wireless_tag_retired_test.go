// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Construct structural archive fixtures from real lifecycle-produced rows.
// Synthetic successor effects are deliberately not native/MAC qualification.
func appendWirelessTagTestRecreation(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, creator cvk.NetworkControllerConfigurationActionApplication, step int) cvk.NetworkControllerConfigurationActionApplication {
	t.Helper()
	ns := strings.SplitN(creator.Operation.Resource, "/", 2)[0] + "/"
	keys := []string{}
	for _, effect := range creator.EffectReceipts {
		keys = append(keys, strings.Split(effect, ":")[0])
	}
	sort.Strings(keys)
	op := creator.Operation.DeepCopy()
	op.ID, op.TaskID = fmt.Sprintf("%032x", 0xffffff00+step), fmt.Sprintf("recreated-task-%d", step)
	for _, row := range state.ActionApplications {
		if row.Operation.ConfigGeneration >= op.ConfigGeneration {
			op.ConfigGeneration = row.Operation.ConfigGeneration + 1
		}
	}
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	op.SubmittedAt = &at
	cohort, err := wirelessTagRecreationCandidate(state, ns, keys, op)
	if err != nil {
		t.Fatal("closed predecessor", err)
	}
	canonical, keyID, pred, _ := wirelessTagPrivateParts(op.Resource)
	ordinary := ns + wirelessTagBoundPrefix + "write:" + strings.TrimPrefix(canonical, ns) + ":" + wirelessTagProducedGroup(op.Resource) + ":" + keyID + ":" + pred
	op.Resource, err = wirelessTagRecreationResource(ordinary, cohort)
	if err != nil {
		t.Fatal("recreate encoding", err)
	}
	effects := []string{}
	for i, key := range keys {
		effects = append(effects, fmt.Sprintf("%s:recreated-%d-%d:%s", key, step, i, strings.Repeat("b", 64)))
	}
	op.Phase = "Verifying"
	state.Operation = op.DeepCopy()
	before := state.DeepCopy()
	staged, err := wirelessTagArchivePredecessor(state, op, effects)
	if err != nil || !reflect.DeepEqual(state, before) {
		t.Fatal("predecessor staging failed or mutated the caller", err)
	}
	*state = *staged
	op.Phase = "Verified"
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "network_profiles", Operation: *op, VerifiedAt: &at, EffectReceipts: effects}
	row.Resource = wirelessTagReceiptBase(op, effects) + ":" + wirelessTagRowSeal(row)
	state.ActionApplications = append(state.ActionApplications, row)
	state.Operation = op.DeepCopy()
	state.ActionApplications[len(state.ActionApplications)-1].Resource += ":" + wirelessTagBookkeeping(state, op.ID)
	if !wirelessTagReceiptPresent(state, op) {
		t.Fatal("new creator not atomically representable with complete archive")
	}
	return *state.ActionApplications[len(state.ActionApplications)-1].DeepCopy()
}

func appendWirelessTagTestDeletion(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, creator cvk.NetworkControllerConfigurationActionApplication, originals []cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	for i, effect := range creator.EffectReceipts {
		parts := strings.Split(effect, ":")
		var row cvk.NetworkControllerConfigurationActionApplication
		for _, old := range originals {
			if old.Operation.Action == "Delete" && strings.HasPrefix(old.EffectReceipts[0], parts[0]+":") {
				row = *old.DeepCopy()
			}
		}
		if row.Operation.ID == "" {
			t.Fatal("missing original member tombstone template")
		}
		row.Operation.ID = fmt.Sprintf("%032x", 0xffff0000+i)
		row.Operation.TaskID = fmt.Sprintf("delete-recreated-%d", i)
		row.Operation.TargetID = parts[1]
		row.Operation.ConfigGeneration = creator.Operation.ConfigGeneration + 1
		row.Operation.SubmittedAt = creator.VerifiedAt.DeepCopy()
		row.VerifiedAt = creator.VerifiedAt.DeepCopy()
		digest, _ := wirelessTagLineageDigest(creator.Operation.Resource)
		var err error
		row.Operation.Resource, err = wirelessTagLineageResource(row.Operation.Resource, "delete", digest)
		if err != nil {
			t.Fatal("lineage deletion fixture", err)
		}
		row.EffectReceipts = []string{effect}
		row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(row) + ":" + strings.Repeat("d", 64)
		state.ActionApplications = append(state.ActionApplications, row)
	}
	if _, err := wirelessTagRecords(state); err != nil {
		t.Fatal("structural recreated deletion", err)
	}
}

func qualifyWirelessTagRetiredChains(t *testing.T, a *adapter, original *cvk.NetworkControllerConfigurationStatus) {
	t.Helper()
	for _, creator := range original.ActionApplications {
		if creator.Operation.Action != "Create" || !wirelessTagPrivateSelected(creator.Operation.Resource) {
			continue
		}
		ns := strings.SplitN(creator.Operation.Resource, "/", 2)[0] + "/"
		for _, mode := range []string{"valid", "two-generations", "reordered", "missing-creator", "missing-tombstone", "orphan", "changed-parent-digest", "changed-history", "owner", "parent", "key", "generation", "unchanged-input", "stable-native-id", "cross-key-alias", "ancestor-task-reused", "nonce-reuse", "unarchived-predecessor", "truncated-chain", "future-archive", "oversized-archive", "dropped-latest-lineage", "changed-latest-lineage"} {
			t.Run("retired/"+ns+mode, func(t *testing.T) {
				if mode == "cross-key-alias" && len(creator.EffectReceipts) < 2 {
					t.Skip("cross-member alias requires a bulk producer")
				}
				state := original.DeepCopy()
				newCreator := appendWirelessTagTestRecreation(t, state, creator, 1)
				if mode == "dropped-latest-lineage" || mode == "changed-latest-lineage" {
					appendWirelessTagTestDeletion(t, state, newCreator, original.ActionApplications)
					row := &state.ActionApplications[len(state.ActionApplications)-1]
					parts := strings.Split(row.Operation.Resource, ":")
					at := len(parts) - 4
					if mode == "dropped-latest-lineage" {
						parts = append(parts[:at], parts[at+1:]...)
						row.Operation.Resource = strings.Replace(strings.Join(parts, ":"), wirelessTagLineagePrefix, wirelessTagBoundPrefix, 1)
					} else {
						parts[at] = strings.Repeat("f", 64)
						row.Operation.Resource = strings.Join(parts, ":")
					}
					row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(*row) + ":" + strings.Repeat("d", 64)
					if _, err := wirelessTagRecords(state); err == nil {
						t.Fatal("latest result dropped or changed the creator lineage")
					}
					return
				}
				if mode == "two-generations" || mode == "truncated-chain" {
					appendWirelessTagTestDeletion(t, state, newCreator, original.ActionApplications)
					newCreator = appendWirelessTagTestRecreation(t, state, newCreator, 2)
				}
				currentIndex := -1
				for i, row := range state.ActionApplications {
					if row.Operation.ID == newCreator.Operation.ID {
						currentIndex = i
					}
				}
				altered := false
				for i := 0; i < len(state.ActionApplications); i++ {
					row := &state.ActionApplications[i]
					if !strings.HasPrefix(row.Resource, wirelessTagRetiredPrefix) {
						continue
					}
					switch mode {
					case "missing-creator", "missing-tombstone":
						if (mode == "missing-creator") == (row.Operation.Action == "Create") {
							state.ActionApplications = append(state.ActionApplications[:i], state.ActionApplications[i+1:]...)
							altered = true
						}
					case "orphan":
						_, rest, _ := strings.Cut(strings.TrimPrefix(row.Resource, wirelessTagRetiredPrefix), ":")
						row.Resource = wirelessTagRetiredPrefix + strings.Repeat("f", 64) + ":" + rest
						altered = true
					case "changed-history":
						digest, _, _ := strings.Cut(strings.TrimPrefix(row.Resource, wirelessTagRetiredPrefix), ":")
						row.Operation.TaskID = "different-task"
						row.Resource = wirelessTagRetiredPrefix + digest + ":" + wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(*row) + ":" + strings.Repeat("d", 64)
						altered = true
					case "unarchived-predecessor":
						_, row.Resource, _ = strings.Cut(strings.TrimPrefix(row.Resource, wirelessTagRetiredPrefix), ":")
						altered = true
					case "truncated-chain":
						if !wirelessTagRecreateSelected(row.Operation.Resource) && row.Operation.Action == "Create" {
							state.ActionApplications = append(state.ActionApplications[:i], state.ActionApplications[i+1:]...)
							altered = true
						}
					case "future-archive":
						row.Resource = strings.Replace(row.Resource, wirelessTagRetiredPrefix, "wireless-tag-retired:v9:", 1)
						altered = true
					case "oversized-archive":
						row.Resource += strings.Repeat("a", 512)
						altered = true
					}
					if altered {
						break
					}
				}
				if !altered {
					row := &state.ActionApplications[currentIndex]
					switch mode {
					case "changed-parent-digest":
						p := strings.Split(row.Operation.Resource, ":")
						p[len(p)-4] = strings.Repeat("f", 64)
						row.Operation.Resource = strings.Join(p, ":")
					case "owner":
						row.Operation.ConfigUID = "other"
					case "parent":
						row.Operation.ParentID = "other"
					case "key":
						p := strings.Split(row.Operation.Resource, ":")
						p[len(p)-2] = strings.Repeat("f", 64)
						row.Operation.Resource = strings.Join(p, ":")
					case "generation":
						row.Operation.ControllerGeneration = 0
					case "unchanged-input":
						for _, old := range original.ActionApplications {
							if old.Operation.Action == "Delete" && strings.HasPrefix(old.Operation.Resource, ns) {
								row.Operation.ConfigGeneration, row.Operation.SourceUID, row.Operation.SourceVersion = old.Operation.ConfigGeneration, old.Operation.SourceUID, old.Operation.SourceVersion
							}
						}
					case "stable-native-id":
						for i, old := range creator.EffectReceipts {
							p := strings.Split(row.EffectReceipts[i], ":")
							p[1] = strings.Split(old, ":")[1]
							row.EffectReceipts[i] = strings.Join(p, ":")
						}
					case "cross-key-alias":
						p := strings.Split(row.EffectReceipts[0], ":")
						p[1] = strings.Split(creator.EffectReceipts[1], ":")[1]
						row.EffectReceipts[0] = strings.Join(p, ":")
					case "ancestor-task-reused":
						row.Operation.TaskID = creator.Operation.TaskID
					case "nonce-reuse":
						row.Operation.ID = creator.Operation.ID
					}
					if mode != "valid" && mode != "two-generations" && mode != "reordered" {
						row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(*row) + ":" + strings.Repeat("d", 64)
					}
				}
				if mode == "reordered" {
					for i, j := 0, len(state.ActionApplications)-1; i < j; i, j = i+1, j-1 {
						state.ActionApplications[i], state.ActionApplications[j] = state.ActionApplications[j], state.ActionApplications[i]
					}
				}
				_, err := wirelessTagRecords(state)
				valid := mode == "valid" || mode == "reordered" || mode == "two-generations" || mode == "stable-native-id"
				if (err == nil) != valid {
					t.Fatal("incorrect complete archive chain acceptance", mode, err)
				}
				if mode == "valid" || mode == "two-generations" {
					if !wirelessTagReceiptPresent(state, state.Operation) {
						t.Fatal("complete archived status lacks active receipt bookkeeping")
					}
					ctx := context.Background()
					before, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					written := before.DeepCopy()
					written.Status.Configuration = state.DeepCopy()
					if err := a.statusWriter.Status().Update(ctx, written); err != nil {
						t.Fatal("persist complete archived fixture", err)
					}
					t.Cleanup(func() {
						fresh, err := a.currentController(ctx)
						if err != nil {
							t.Fatal(err)
						}
						fresh.Status.Configuration = before.Status.Configuration.DeepCopy()
						if err := a.statusWriter.Status().Update(ctx, fresh); err != nil {
							t.Fatal("restore original qualification fixture", err)
						}
					})
					stored, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					// The API contract is the serialized status. Optional empty/nil
					// representations can differ after a round trip without changing
					// any public field or the exact active bookkeeping digest.
					want, _ := json.Marshal(state)
					actual, _ := json.Marshal(stored.Status.Configuration)
					if string(want) != string(actual) || !wirelessTagReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
						t.Fatal("archived fixture lost serialized status or active receipt bookkeeping")
					}
				}
			})
		}
	}
}
