// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func siteRecreationFixture(t *testing.T, latest bool) (*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	creator, applied := siteAppliedTestRecords()
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "sites", ConfigName: "config", ConfigUID: "owner"}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	if latest {
		state.ActionApplications = append(state.ActionApplications, applied)
	}
	appendSiteRetirementFixture(t, state, 2, "d", "retire-task-1", time.Now().Add(-80*time.Millisecond))
	return stageSiteRecreationFixture(t, state, 3, "e", "recreate-task-1", "22222222-2222-4222-8222-222222222222", time.Now().Add(-40*time.Millisecond))
}
func appendSiteRetirementFixture(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, generation int64, id, task string, submitted time.Time) {
	t.Helper()
	creators, err := siteCreatedRecords(state)
	if err != nil || len(creators) != 1 {
		t.Fatal("fixture creator invalid", err)
	}
	var creator siteCreatorRecord
	for _, c := range creators {
		creator = c
	}
	applied, err := siteAppliedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	path := siteResourcePathDigest(creator.receipt.Operation.Resource)
	prior := strings.Repeat("0", 64)
	if row, found := applied[path]; found {
		prior = siteReceiptDigest(row)
	}
	parts, _ := sitePrivateCreationParts(creator.receipt.Operation.Resource)
	stamp := metav1.NewTime(submitted)
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat(id, 32), Section: "sites", Resource: siteRetirementPrefix + strings.Join([]string{creator.kind, path, "i", parts[3], siteReceiptDigest(creator.receipt), prior, strings.Repeat("b", 64), strings.Repeat("c", 64)}, ":"), Action: "Delete", Phase: "Verified", ConfigName: creator.receipt.Operation.ConfigName, ConfigUID: creator.receipt.Operation.ConfigUID, ConfigGeneration: generation, ControllerGeneration: 1, TargetID: creator.id, ParentID: creator.receipt.Operation.ParentID, TaskID: task, SubmittedAt: &stamp}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteRetiredResource(op), Operation: *op, VerifiedAt: &stamp}
	state.ActionApplications = append(state.ActionApplications, row)
	state.Operation = nil
	if _, err := siteRetiredRecords(state); err != nil {
		t.Fatal("fixture retirement invalid", err)
	}
}
func stageSiteRecreationFixture(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, generation int64, id, task, target string, submitted time.Time) (*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	cohorts, err := siteCurrentRetirementCohorts(state)
	if err != nil || len(cohorts) != 1 {
		t.Fatal("fixture cohort invalid", err)
	}
	var c siteHistoryCohort
	for _, cohort := range cohorts {
		c = cohort
	}
	base, _ := sitePrivateCreationParts(c.Creator.Operation.Resource)
	stamp := metav1.NewTime(submitted)
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat(id, 32), Section: "sites", Resource: siteRecreatePrefix + strings.Join(base, ":") + ":" + c.Retired.Operation.ID + ":" + c.Digest, Action: "Create", Phase: "Verifying", ConfigName: c.Retired.Operation.ConfigName, ConfigUID: c.Retired.Operation.ConfigUID, ConfigGeneration: generation, ControllerGeneration: 1, ParentID: c.Retired.Operation.ParentID, TaskID: task, SubmittedAt: &stamp}
	state.Operation = op.DeepCopy()
	successor := op.DeepCopy()
	successor.Phase = "Verified"
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteCreatedResource(successor, c.Kind, target, strings.Repeat("f", 64)), Operation: *successor, VerifiedAt: &stamp}
	return state, op, row
}
func publishSiteRecreationFixture(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, row cvk.NetworkControllerConfigurationActionApplication) *cvk.NetworkControllerConfigurationStatus {
	t.Helper()
	before := state.DeepCopy()
	working, err := siteArchivePredecessor(state, op)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("archive mutated predecessor before publication")
	}
	if _, err := siteCreatedRecords(working); err == nil {
		t.Fatal("partial archive was admitted without verified successor")
	}
	working, err = siteCreationPublication(state, op, row)
	if err != nil {
		t.Fatal("creator publication rejected complete lifecycle", err)
	}
	if _, err := siteCreatedRecords(working); err != nil {
		t.Fatal("complete archived lifecycle rejected", err)
	}
	return working
}

func TestSiteRecreationPublicationBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "old-uuid", "old-task", "row-frame", "lost-history", "capacity", "wrong-phase", "wrong-active"} {
		t.Run(mode, func(t *testing.T) {
			state, op, row := siteRecreationFixture(t, true)
			switch mode {
			case "old-uuid":
				row.Resource = siteCreatedResource(&row.Operation, "area", siteOwnedTestUUID, strings.Repeat("f", 64))
			case "old-task":
				op.TaskID = state.ActionApplications[0].Operation.TaskID
				state.Operation = op.DeepCopy()
				row.Operation.TaskID = op.TaskID
			case "row-frame":
				row.Operation.ConfigGeneration++
			case "lost-history":
				state.ActionApplications = state.ActionApplications[1:]
			case "capacity":
				fillSiteRetirementHistory(state, false)
			case "wrong-phase":
				op.Phase = "Submitting"
				state.Operation = op.DeepCopy()
			case "wrong-active":
				state.Operation.TaskID = "different-task"
			}
			before := state.DeepCopy()
			out, err := siteCreationPublication(state, op, row)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("publication acceptance mismatch: %v", err)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("publication changed input before CAS")
			}
			if err == nil && (!siteCreatedReceiptPresent(out, out.Operation) || len(out.ActionApplications) != len(before.ActionApplications)+1) {
				t.Fatal("publication lost predecessor or successor")
			}
		})
	}
}

func TestSiteRecreationCohortChain(t *testing.T) {
	for _, latest := range []bool{false, true} {
		t.Run(fmt.Sprintf("latest-%t", latest), func(t *testing.T) {
			state, op, row := siteRecreationFixture(t, latest)
			first := publishSiteRecreationFixture(t, state, op, row)
			appendSiteRetirementFixture(t, first, 4, "5", "retire-task-2", time.Now().Add(-20*time.Millisecond))
			second, next, newRow := stageSiteRecreationFixture(t, first, 5, "6", "recreate-task-2", "33333333-3333-4333-8333-333333333333", time.Now().Add(-10*time.Millisecond))
			second = publishSiteRecreationFixture(t, second, next, newRow)
			archives, err := siteHistoryCohorts(second)
			if err != nil || len(archives) != 2 {
				t.Fatal("repeated recreation lost complete history", err)
			}
			creators, err := siteCreatedRecords(second)
			if err != nil || len(creators) != 1 {
				t.Fatal("historical creators became current", err)
			}
			if latest {
				if len(second.ActionApplications) != 6 {
					t.Fatal("applied predecessor row lost")
				}
			} else if len(second.ActionApplications) != 5 {
				t.Fatal("unexpected archive growth")
			}
			applied, err := siteAppliedRecords(second)
			if err != nil || len(applied) != 0 {
				t.Fatal("historical updates became current", err)
			}
			retired, err := siteRetiredRecords(second)
			if err != nil || len(retired) != 0 {
				t.Fatal("historical retirements became current", err)
			}
		})
	}
}

var siteHistoryFailureModes = []string{"missing-creator", "missing-retired", "missing-applied", "orphan", "edit-producer", "edit-time", "edit-digest", "unknown-version", "duplicate", "borrowed-task", "reused-uuid", "same-input", "foreign-owner", "wrong-parent", "wrong-key", "lost-parent", "oversized"}

func TestSiteRecreationHistoryFences(t *testing.T) {
	for _, mode := range siteHistoryFailureModes {
		t.Run(mode, func(t *testing.T) {
			state, op, row := siteRecreationFixture(t, true)
			valid := publishSiteRecreationFixture(t, state, op, row)
			last := len(valid.ActionApplications) - 1
			switch mode {
			case "missing-creator":
				valid.ActionApplications = append(valid.ActionApplications[:0], valid.ActionApplications[1:]...)
			case "missing-applied":
				valid.ActionApplications = append(valid.ActionApplications[:1], valid.ActionApplications[2:]...)
			case "missing-retired":
				valid.ActionApplications = append(valid.ActionApplications[:2], valid.ActionApplications[3:]...)
			case "orphan":
				valid.ActionApplications = valid.ActionApplications[:last]
			case "edit-producer":
				valid.ActionApplications[0].Operation.TaskID = "edited-original"
			case "edit-time":
				valid.ActionApplications[0].VerifiedAt = valid.ActionApplications[0].VerifiedAt.DeepCopy()
				valid.ActionApplications[0].VerifiedAt.Time = valid.ActionApplications[0].VerifiedAt.Add(-time.Hour)
			case "edit-digest":
				valid.ActionApplications[0].Resource = strings.Replace(valid.ActionApplications[0].Resource, strings.Split(strings.TrimPrefix(valid.ActionApplications[0].Resource, siteHistoryPrefix), ":")[0], strings.Repeat("0", 64), 1)
			case "unknown-version":
				valid.ActionApplications[0].Resource = strings.Replace(valid.ActionApplications[0].Resource, ":v1:", ":v2:", 1)
			case "duplicate":
				valid.ActionApplications = append(valid.ActionApplications, *valid.ActionApplications[0].DeepCopy())
			case "borrowed-task":
				valid.ActionApplications[last].Operation.TaskID = valid.ActionApplications[0].Operation.TaskID
			case "reused-uuid":
				valid.ActionApplications[last].Resource = siteCreatedResource(&valid.ActionApplications[last].Operation, "area", siteOwnedTestUUID, strings.Repeat("f", 64))
			case "same-input":
				valid.ActionApplications[last].Operation.ConfigGeneration = 2
			case "foreign-owner":
				valid.ActionApplications[last].Operation.ConfigUID = "foreign"
			case "wrong-parent":
				valid.ActionApplications[last].Operation.ParentID = "foreign-parent"
			case "wrong-key":
				r := &valid.ActionApplications[last]
				r.Operation.Resource = strings.Replace(r.Operation.Resource, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
				r.Resource = siteCreatedResource(&r.Operation, "area", "22222222-2222-4222-8222-222222222222", strings.Repeat("f", 64))
			case "lost-parent":
				r := &valid.ActionApplications[last]
				r.Operation.Resource = strings.Replace(r.Operation.Resource, strings.Repeat("d", 32), strings.Repeat("7", 32), 1)
			case "oversized":
				valid.ActionApplications[0].Resource += strings.Repeat("x", 512)
			}
			if _, err := siteCreatedRecords(valid); err == nil {
				t.Fatal("invalid history admitted by creator reader")
			}
			if _, err := siteAppliedRecords(valid); err == nil {
				t.Fatal("invalid history admitted by applied reader")
			}
			if _, err := siteRetiredRecords(valid); err == nil {
				t.Fatal("invalid history admitted by retirement reader")
			}
		})
	}
}

func TestSiteRecreationArchiveAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "same-input", "old-controller", "foreign-owner", "wrong-parent", "reused-operation", "borrowed-task", "wrong-cohort", "wrong-phase", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			state, op, _ := siteRecreationFixture(t, false)
			switch mode {
			case "same-input":
				op.ConfigGeneration = 2
			case "old-controller":
				op.ControllerGeneration = 0
			case "foreign-owner":
				op.ConfigUID = "foreign"
			case "wrong-parent":
				op.ParentID = "foreign-parent"
			case "reused-operation":
				op.ID = state.ActionApplications[0].Operation.ID
			case "borrowed-task":
				op.TaskID = state.ActionApplications[0].Operation.TaskID
			case "wrong-cohort":
				op.Resource = op.Resource[:len(op.Resource)-64] + strings.Repeat("0", 64)
			case "wrong-phase":
				op.Phase = "Unknown"
			case "capacity":
				fillSiteRetirementHistory(state, false)
			}
			state.Operation = op.DeepCopy()
			before := state.DeepCopy()
			staged, err := siteArchivePredecessor(state, op)
			if (err == nil) != (mode == "valid") {
				t.Fatal("archive authority mismatch", mode, err)
			}
			if !reflect.DeepEqual(before, state) {
				t.Fatal("failed archive mutated predecessor state")
			}
			if staged != nil && mode != "valid" {
				t.Fatal("invalid archive exposed staged state")
			}
		})
	}
}

func TestSiteRecreationOlderHistoryFences(t *testing.T) {
	for _, mode := range []string{"valid", "older-task", "older-uuid", "configmap-successor"} {
		t.Run(mode, func(t *testing.T) {
			state, op, row := siteRecreationFixture(t, false)
			state = publishSiteRecreationFixture(t, state, op, row)
			appendSiteRetirementFixture(t, state, 4, "5", "retire-task-2", time.Now().Add(-20*time.Millisecond))
			state, next, last := stageSiteRecreationFixture(t, state, 5, "6", "recreate-task-2", "33333333-3333-4333-8333-333333333333", time.Now().Add(-10*time.Millisecond))
			state = publishSiteRecreationFixture(t, state, next, last)
			if mode == "older-task" || mode == "older-uuid" {
				cohorts, err := siteHistoryCohorts(state)
				if err != nil {
					t.Fatal(err)
				}
				newer := cohorts[strings.Repeat("5", 32)]
				older := cohorts[strings.Repeat("d", 32)]
				rows := append([]cvk.NetworkControllerConfigurationActionApplication(nil), newer.Rows...)
				for i := range rows {
					rows[i] = *rows[i].DeepCopy()
				}
				if mode == "older-task" {
					rows[0].Operation.TaskID = older.Creator.Operation.TaskID
				} else {
					rows[0].Resource = siteCreatedResource(&rows[0].Operation, "area", older.ID, strings.Repeat("f", 64))
					rows[1].Operation.TargetID = older.ID
				}
				parts := strings.Split(strings.TrimPrefix(rows[1].Operation.Resource, siteRetirementPrefix), ":")
				parts[4] = siteReceiptDigest(rows[0])
				rows[1].Operation.Resource = siteRetirementPrefix + strings.Join(parts, ":")
				digest := siteCohortDigest(rows)
				for i := range state.ActionApplications {
					existing := &state.ActionApplications[i]
					if strings.HasPrefix(existing.Resource, siteHistoryPrefix+newer.Digest+":") {
						for _, replacement := range rows {
							if replacement.Operation.ID == existing.Operation.ID {
								*existing = *replacement.DeepCopy()
								existing.Resource = siteHistoryPrefix + digest + ":" + replacement.Resource
								break
							}
						}
					}
				}
				current := &state.ActionApplications[len(state.ActionApplications)-1]
				current.Operation.Resource = strings.Replace(current.Operation.Resource, newer.Digest, digest, 1)
				// Recompute all public checksums: the chain validator must detect actual
				// older task/UUID reuse, not merely a stale cohort digest.
			}
			if mode == "configmap-successor" {
				current := &state.ActionApplications[len(state.ActionApplications)-1]
				current.Operation.Resource = strings.Replace(current.Operation.Resource, ":i:", ":c:", 1)
				current.Operation.SourceUID, current.Operation.SourceVersion = "new-source", "new-revision"
				current.Resource = siteCreatedResource(&current.Operation, "area", "33333333-3333-4333-8333-333333333333", strings.Repeat("f", 64))
			}
			_, err := siteCreatedRecords(state)
			if (err == nil) != (mode == "valid" || mode == "configmap-successor") {
				t.Fatal("older history/source transition fence mismatch", mode, err)
			}
		})
	}
}
