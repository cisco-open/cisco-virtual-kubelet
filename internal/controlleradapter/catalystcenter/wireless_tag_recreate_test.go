// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Use actual lifecycle-produced, persisted receipts, including singleton and
// bulk creators followed by individual verified deletions. Public row resealing
// below ensures a stale outer hash cannot be the sole reason for rejection.
func qualifyWirelessTagRecreationCohorts(t *testing.T, original *cvk.NetworkControllerConfigurationStatus) {
	t.Helper()
	for _, creator := range original.ActionApplications {
		if creator.Operation.Action != "Create" || !wirelessTagPrivateSelected(creator.Operation.Resource) {
			continue
		}
		ns := strings.SplitN(creator.Operation.Resource, "/", 2)[0] + "/"
		keys := []string{}
		for _, effect := range creator.EffectReceipts {
			keys = append(keys, strings.Split(effect, ":")[0])
		}
		sort.Strings(keys)
		for _, mode := range []string{"complete", "reordered", "missing-creator", "missing-deletion", "partial-producer", "extra-member", "duplicate-key", "malformed-key", "foreign-owner", "foreign-name", "foreign-parent", "controller-regression", "unchanged-input", "backward-generation-new-source", "before-deletion", "key-change", "legacy-deletion", "native-uuid-change", "unrelated-history", "digest-history", "independent-copies"} {
			t.Run(ns+mode, func(t *testing.T) {
				state := original.DeepCopy()
				selected := append([]string{}, keys...)
				next := creator.Operation.DeepCopy()
				next.TaskID, next.TargetID = "", ""
				next.Phase = "Submitting"
				for _, row := range state.ActionApplications {
					if row.Operation.ConfigGeneration >= next.ConfigGeneration {
						next.ConfigGeneration = row.Operation.ConfigGeneration + 1
					}
				}
				at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				next.SubmittedAt = &at
				baseline, err := wirelessTagRecreationCandidate(state, ns, selected, next)
				if err != nil || len(baseline.Rows) != len(keys)+1 || len(baseline.NativeIDs) != len(keys) || !profileSiteDigestValid(baseline.Digest) {
					t.Fatal("complete persisted lifecycle lacks retirement candidate", err)
				}
				deleteIndex := -1
				for i, row := range state.ActionApplications {
					if strings.HasPrefix(row.Operation.Resource, ns) && row.Operation.Action == "Delete" {
						deleteIndex = i
						break
					}
				}
				switch mode {
				case "reordered":
					for i, j := 0, len(state.ActionApplications)-1; i < j; i, j = i+1, j-1 {
						state.ActionApplications[i], state.ActionApplications[j] = state.ActionApplications[j], state.ActionApplications[i]
					}
				case "missing-creator":
					for i, row := range state.ActionApplications {
						if row.Operation.ID == creator.Operation.ID {
							state.ActionApplications = append(state.ActionApplications[:i], state.ActionApplications[i+1:]...)
							break
						}
					}
				case "missing-deletion":
					state.ActionApplications = append(state.ActionApplications[:deleteIndex], state.ActionApplications[deleteIndex+1:]...)
				case "partial-producer":
					selected = selected[:len(selected)-1]
				case "extra-member":
					selected = append(selected, strings.Repeat("f", 64))
				case "duplicate-key":
					selected = append(selected, selected[0])
				case "malformed-key":
					selected[0] = "invalid"
				case "foreign-owner":
					next.ConfigUID = "other"
				case "foreign-name":
					next.ConfigName = "other"
				case "foreign-parent":
					next.ParentID = "other"
				case "controller-regression":
					next.ControllerGeneration = 0
				case "unchanged-input":
					old := state.ActionApplications[deleteIndex].Operation
					next.ConfigGeneration, next.SourceUID, next.SourceVersion = old.ConfigGeneration, old.SourceUID, old.SourceVersion
				case "backward-generation-new-source":
					next.ConfigGeneration, next.SourceVersion = 0, "other"
				case "before-deletion":
					early := metav1.NewTime(state.ActionApplications[deleteIndex].VerifiedAt.Add(-time.Second))
					next.SubmittedAt = &early
				case "key-change":
					row := &state.ActionApplications[deleteIndex]
					p := strings.Split(row.Operation.Resource, ":")
					p[len(p)-2] = strings.Repeat("f", 64)
					row.Operation.Resource = strings.Join(p, ":")
				case "legacy-deletion":
					row := &state.ActionApplications[deleteIndex]
					row.Operation.Resource = strings.Replace(row.Operation.Resource, wirelessTagBoundPrefix+"delete:", wirelessTagPrunePrefix, 1)
				case "native-uuid-change":
					row := &state.ActionApplications[deleteIndex]
					parts := strings.Split(row.EffectReceipts[0], ":")
					parts[1] = "replacement"
					row.Operation.TargetID = "replacement"
					row.EffectReceipts[0] = strings.Join(parts, ":")
				case "unrelated-history":
					state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Resource: "other-resource", Section: "templates"})
				case "digest-history":
					state.ActionApplications[deleteIndex].Operation.TaskID = "different-original-task"
				}
				if mode == "key-change" || mode == "legacy-deletion" || mode == "native-uuid-change" || mode == "digest-history" {
					row := &state.ActionApplications[deleteIndex]
					row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(*row) + ":" + strings.Repeat("d", 64)
				}
				before := state.DeepCopy()
				candidate, err := wirelessTagRecreationCandidate(state, ns, selected, next)
				valid := mode == "complete" || mode == "reordered" || mode == "unrelated-history" || mode == "digest-history" || mode == "independent-copies"
				if (err == nil) != valid {
					t.Fatal("incorrect recreation cohort eligibility", mode, err)
				}
				if !reflect.DeepEqual(before, state) {
					t.Fatal("candidate collection mutated retained history")
				}
				if !valid {
					return
				}
				if (candidate.Digest == baseline.Digest) != (mode != "digest-history") {
					t.Fatal("cohort digest lost exact history or depended on row order/unrelated rows")
				}
				candidate.Rows[0].EffectReceipts[0] = "changed"
				candidate.Keys[0] = "changed"
				if !reflect.DeepEqual(before, state) || !reflect.DeepEqual(keys, selected) {
					t.Fatal("candidate aliases retained history or caller keys")
				}
			})
		}
	}
}

func TestWirelessTagRecreationCohorts(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		t.Run(fmt.Sprintf("cohort-%t", cohort), func(t *testing.T) {
			exerciseWirelessTagReceipts(t, cohort, "membership-prune-complete", nil)
		})
	}
}
