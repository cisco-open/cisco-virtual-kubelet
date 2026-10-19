// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var templateAppliedStages = []string{"project-update", "draft-update", "commit", "composite-draft", "composite-commit"}
var templateAppliedModes = []string{"known", "key-change-during-proof", "key-change-restore", "native-state-race", "wrong-declared-body", "source-during-proof", "owner-during-proof", "receipt-conflict", "receipt-lost-ack", "receipt-pruned", "source-fields-pruned", "baseline-pruned", "baseline-wrong-key"}

func TestTemplateAppliedPublicFlow(t *testing.T) {
	for _, stage := range templateAppliedStages {
		for _, mode := range templateAppliedModes {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseTemplateNoDispatchReplan(t, stage, "applied-"+mode, nil) })
		}
	}
}
func TestTemplateAppliedRecordEnvelope(t *testing.T) {
	for _, kind := range []string{"p", "r", "c"} {
		t.Run(kind, func(t *testing.T) {
			original := templateCreatorReceiptFixture(kind)
			original.Operation.Resource += ":" + templateComparisonTag + ":" + strings.Repeat("b", 64)
			original.Resource = templateCreatorResultResource(original.Operation.Resource, templateOwnedTestUUID, strings.Repeat("c", 64))
			stage := "draft"
			if kind == "p" {
				stage = "project"
			}
			op := original.Operation.DeepCopy()
			op.ID = strings.Repeat("d", 32)
			op.Resource = templateApplyResource(op.Resource, stage)
			op.Action = "Update"
			op.TargetID = templateOwnedTestUUID
			now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			op.SubmittedAt = &now
			row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: templateAppliedResult(op, original, strings.Repeat("e", 64)), Operation: *op, VerifiedAt: &now}
			modes := []string{"valid", "missing-creator", "wrong-uuid", "wrong-parent", "wrong-owner", "wrong-key", "missing-mac", "wrong-lineage", "stripped-mode", "duplicate", "future", "old-generation", "wrong-section", "unverified"}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{*original.DeepCopy(), *row.DeepCopy()}}
					r := &state.ActionApplications[1]
					switch mode {
					case "missing-creator":
						state.ActionApplications = state.ActionApplications[1:]
					case "wrong-uuid":
						r.Operation.TargetID = "88888888-8888-4888-8888-888888888888"
					case "wrong-parent":
						r.Operation.ParentID = "88888888-8888-4888-8888-888888888888"
					case "wrong-owner":
						r.Operation.ConfigUID = "foreign"
					case "wrong-key":
						r.Operation.Resource = strings.Replace(r.Operation.Resource, strings.Repeat("b", 64), strings.Repeat("f", 64), 1)
					case "missing-mac":
						r.Resource = r.Resource[:len(r.Resource)-65]
					case "wrong-lineage":
						state.ActionApplications[0].Operation.ID = strings.Repeat("f", 32)
					case "stripped-mode":
						r.Operation.SourceUID = ""
						r.Operation.SourceVersion = ""
					case "duplicate":
						state.ActionApplications = append(state.ActionApplications, *r.DeepCopy())
					case "future":
						r.VerifiedAt = &metav1.Time{Time: time.Now().Add(time.Hour)}
					case "old-generation":
						r.Operation.ConfigGeneration = 0
					case "wrong-section":
						r.Section = "sites"
					case "unverified":
						r.Operation.Phase = "Verifying"
					}
					before := state.DeepCopy()
					got, err := templateAppliedRecords(state)
					if mode == "valid" {
						if err != nil || len(got) != 1 || !templateAppliedReceiptPresent(state, op) {
							t.Fatal("valid applied receipt refused", err)
						}
					} else if err == nil {
						t.Fatal("invalid applied authority accepted")
					}
					if !reflect.DeepEqual(before, state) {
						t.Fatal("parser mutated history")
					}
				})
			}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{original}}
			if err := templateReplaceApplied(state, row); err != nil {
				t.Fatal(err)
			}
			next := *row.DeepCopy()
			next.Operation.ID = strings.Repeat("f", 32)
			next.Resource = templateAppliedResult(&next.Operation, original, strings.Repeat("f", 64))
			if err := templateReplaceApplied(state, next); err != nil || len(state.ActionApplications) != 2 || !reflect.DeepEqual(state.ActionApplications[0], original) {
				t.Fatal("replacement changed creator or grew history", err)
			}
			raw, _ := json.Marshal(state)
			if len(raw) == 0 {
				t.Fatal("empty state")
			}
		})
	}
}

func TestTemplateAppliedCapacity(t *testing.T) {
	original := templateCreatorReceiptFixture("r")
	original.Operation.Resource += ":" + templateComparisonTag + ":" + strings.Repeat("b", 64)
	original.Resource = templateCreatorResultResource(original.Operation.Resource, templateOwnedTestUUID, strings.Repeat("c", 64))
	op := original.Operation.DeepCopy()
	op.ID = strings.Repeat("d", 32)
	op.Resource = templateApplyResource(op.Resource, "commit")
	op.Action = "Update"
	op.TargetID = templateOwnedTestUUID
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	op.SubmittedAt = &now
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: templateAppliedResult(op, original, strings.Repeat("e", 64)), Operation: *op, VerifiedAt: &now}
	for _, mode := range []string{"new-at-limit", "replace-at-limit", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{original}}
			if mode == "replace-at-limit" {
				state.ActionApplications = append(state.ActionApplications, row)
			}
			for i := 0; ; i++ {
				unrelated := *row.DeepCopy()
				unrelated.Resource = "commit:" + strings.Repeat("p", 230) + "#" + strings.Repeat("t", 230) + fmt.Sprint(i)
				unrelated.Operation.Resource = unrelated.Resource
				unrelated.Operation.ConfigName = strings.Repeat("c", 230)
				unrelated.Operation.TaskID = strings.Repeat("t", 128)
				if mode != "bytes" {
					unrelated.Resource = "unrelated-" + fmt.Sprint(i)
					unrelated.Operation = cvk.NetworkControllerConfigurationOperation{Resource: unrelated.Resource}
				}
				trial := append(append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...), unrelated)
				raw, _ := json.Marshal(trial)
				if mode == "bytes" && len(raw) > 256<<10 || mode != "bytes" && len(trial) > 256 {
					break
				}
				state.ActionApplications = trial
			}
			before := state.DeepCopy()
			err := templateApplyCapacity(state, op)
			if mode == "replace-at-limit" {
				if err != nil {
					t.Fatal("replacement incorrectly requires another slot", err)
				}
			} else if err == nil {
				t.Fatal("capacity overflow was not refused")
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("capacity check mutated history")
			}
		})
	}
}
