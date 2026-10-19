// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func templateVersionCohortFixture(t *testing.T) (*templateInputComparison, *cvk.NetworkControllerConfigurationStatus, string, map[string]any) {
	t.Helper()
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64)}
	original := templateCreatorReceiptFixture("r")
	original.Operation.Resource += ":" + templateComparisonTag + ":" + comparison.keyID
	original.Resource = templateCreatorResultResource(original.Operation.Resource, templateOwnedTestUUID, strings.Repeat("b", 64))
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{original}}
	bodies := map[string]any{}
	entries := []any{}
	for i, id := range []string{"44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"} {
		op := original.Operation.DeepCopy()
		op.Action = "Update"
		op.TargetID = templateOwnedTestUUID
		op.PreparedAt = nil
		op.ID = strings.Repeat([]string{"c", "d"}[i], 32)
		op.Resource = templateApplyResource(original.Operation.Resource, "commit") + ":" + templateVersionOwnershipTag
		now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		op.SubmittedAt = &now
		body := map[string]any{"id": id, "name": "base", "projectId": op.ParentID, "version": json.Number([]string{"1", "2"}[i]), "templateContent": "synthetic private committed body", "versionComment": templateCommitComment("CVK NetAsCode configuration", op.ID), "unknownCounter": json.Number("9007199254740993")}
		entry := map[string]any{"id": id, "version": body["version"], "versionComment": body["versionComment"], "unknownMetadata": "private version metadata"}
		entries = append(entries, entry)
		mac, err := templateVersionBodyMAC(comparison, original.Operation.Resource, id, map[string]any{"definition": body, "metadata": entry})
		if err != nil {
			t.Fatal(err)
		}
		row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: templateAppliedVersionResult(op, original, strings.Repeat("e", 64), id, mac), Operation: *op, VerifiedAt: &now}
		if len(row.Resource) > 512 {
			t.Fatal("version receipt exceeds shared resource limit")
		}
		if err := templateReplaceApplied(state, row); err != nil {
			t.Fatal(err)
		}
		bodies[id] = body
	}
	return comparison, state, original.Operation.Resource, map[string]any{"committed": bodies, "metadata": []any{map[string]any{"templateId": templateOwnedTestUUID, "versionsInfo": entries}}}
}
func TestTemplateVersionCohortOwnership(t *testing.T) {
	for _, mode := range []string{"valid", "old-private-edit", "unknown-number-edit", "metadata-only-edit", "missing-metadata", "foreign-uncommitted", "external-version", "missing-native-version", "lost-history", "lost-creator", "duplicate-version", "duplicate-operation", "lost-operation-marker", "lost-result-marker", "history-kind-pruned", "key-change", "foreign-comment", "legacy-no-adoption"} {
		t.Run(mode, func(t *testing.T) {
			comparison, state, resource, footprint := templateVersionCohortFixture(t)
			bodies := footprint["committed"].(map[string]any)
			first := "44444444-4444-4444-8444-444444444444"
			switch mode {
			case "old-private-edit":
				bodies[first].(map[string]any)["templateContent"] = "externally edited old body"
			case "unknown-number-edit":
				bodies[first].(map[string]any)["unknownCounter"] = json.Number("9007199254740992")
			case "metadata-only-edit":
				groups := footprint["metadata"].([]any)
				entries := groups[0].(map[string]any)["versionsInfo"].([]any)
				entries[0].(map[string]any)["unknownMetadata"] = "external metadata edit"
			case "missing-metadata":
				delete(footprint, "metadata")
			case "foreign-uncommitted":
				groups := footprint["metadata"].([]any)
				group := groups[0].(map[string]any)
				group["versionsInfo"] = append(group["versionsInfo"].([]any), map[string]any{"id": "88888888-8888-4888-8888-888888888888", "version": json.Number("0")})
			case "external-version":
				id := "88888888-8888-4888-8888-888888888888"
				bodies[id] = map[string]any{"id": id, "name": "base", "projectId": state.ActionApplications[0].Operation.ParentID, "version": json.Number("3"), "versionComment": "external"}
				groups := footprint["metadata"].([]any)
				group := groups[0].(map[string]any)
				group["versionsInfo"] = append(group["versionsInfo"].([]any), map[string]any{"id": id, "version": json.Number("3"), "versionComment": "external"})
			case "missing-native-version":
				delete(bodies, first)
				groups := footprint["metadata"].([]any)
				group := groups[0].(map[string]any)
				group["versionsInfo"] = group["versionsInfo"].([]any)[1:]
			case "lost-history":
				state.ActionApplications = append(state.ActionApplications[:1], state.ActionApplications[2:]...)
			case "lost-creator":
				state.ActionApplications = state.ActionApplications[1:]
			case "duplicate-version":
				r := &state.ActionApplications[2]
				p := strings.Split(r.Resource, ":")
				p[len(p)-2] = first
				r.Resource = strings.Join(p, ":")
			case "duplicate-operation":
				state.ActionApplications[2].Operation.ID = state.ActionApplications[1].Operation.ID
			case "lost-operation-marker":
				state.ActionApplications[2].Operation.Resource = strings.TrimSuffix(state.ActionApplications[2].Operation.Resource, ":"+templateVersionOwnershipTag)
			case "lost-result-marker":
				state.ActionApplications[2].Resource = strings.Split(state.ActionApplications[2].Resource, ":"+templateVersionOwnershipTag+":")[0]
			case "history-kind-pruned":
				state.ActionApplications[1].Operation.Resource = strings.TrimSuffix(state.ActionApplications[1].Operation.Resource, ":"+templateVersionOwnershipTag)
			case "key-change":
				comparison.keyID = strings.Repeat("f", 64)
			case "foreign-comment":
				bodies[first].(map[string]any)["versionComment"] = templateCommitComment("CVK NetAsCode configuration", strings.Repeat("f", 32))
			case "legacy-no-adoption":
				state.ActionApplications = state.ActionApplications[:1]
			}
			before := state.DeepCopy()
			matched, err := templateVersionCohortMatches(comparison, state, resource, footprint)
			if mode == "valid" {
				if err != nil || !matched {
					t.Fatal("valid owned cohort refused", err)
				}
			} else if matched {
				t.Fatal("unowned or changed version acquired authority")
			}
			if (mode == "external-version" || mode == "missing-native-version" || mode == "lost-history" || mode == "legacy-no-adoption") && err != nil {
				t.Fatal("valid native evidence was mistaken for malformed input", err)
			}
			if !reflect.DeepEqual(before, state) {
				t.Fatal("comparison rewrote history")
			}
			raw, _ := json.Marshal(state)
			for _, private := range []string{"synthetic private committed body", strings.Repeat("k", 32), "9007199254740993", "private version metadata"} {
				if strings.Contains(string(raw), private) {
					t.Fatal("private version content leaked")
				}
			}
		})
	}
}
func TestTemplateVersionArchiveAndCapacity(t *testing.T) {
	comparison, state, resource, footprint := templateVersionCohortFixture(t)
	original := state.ActionApplications[0]
	history, err := templateAppliedHistoryRecords(state)
	if err != nil || len(history) != 1 {
		t.Fatal("first commit was not archived", err)
	}
	old := *history[0].DeepCopy()
	current, err := templateAppliedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	key, _, _, _ := templateApplyIdentity(&state.ActionApplications[2].Operation)
	latest := current[key]
	op := latest.Operation.DeepCopy()
	op.ID = strings.Repeat("f", 32)
	op.Resource = templateApplyResource(resource, "draft")
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	op.SubmittedAt = &now
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: templateAppliedResult(op, original, strings.Repeat("f", 64)), Operation: *op, VerifiedAt: &now}
	if err := templateReplaceApplied(state, row); err != nil {
		t.Fatal(err)
	}
	history, err = templateAppliedHistoryRecords(state)
	if err != nil || len(history) != 2 || !reflect.DeepEqual(history[0], old) {
		t.Fatal("draft update lost or changed committed lineage", err)
	}
	if ok, err := templateVersionCohortMatches(comparison, state, resource, footprint); err != nil || !ok {
		t.Fatal("draft update discarded version authority", err)
	}
	for len(state.ActionApplications) < 256 {
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "unrelated"})
	}
	// Replacing a non-version draft requires no additional slot.
	if err := templateApplyCapacity(state, op); err != nil {
		t.Fatal("bounded draft replacement unexpectedly grew history", err)
	}
	// A current strict version must be archived before its next replacement.
	strict := latest
	for i := range state.ActionApplications {
		if state.ActionApplications[i].Operation.ID == op.ID {
			state.ActionApplications[i] = strict
		}
	}
	// Remove the separately archived copy of that current version to avoid an
	// invalid duplicate fixture while retaining the full receipt count.
	for i := range state.ActionApplications {
		if strings.HasPrefix(state.ActionApplications[i].Resource, templateAppliedHistoryPrefix) && state.ActionApplications[i].Operation.ID == strict.Operation.ID {
			state.ActionApplications[i] = cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "unrelated"}
		}
	}
	before := state.DeepCopy()
	if err := templateApplyCapacity(state, op); err == nil {
		t.Fatal("version archive overflow was not refused")
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("capacity check changed durable history")
	}
}
func TestTemplateVersionCommentIdentity(t *testing.T) {
	id := strings.Repeat("a", 32)
	base := "CVK member versions " + strings.Repeat("b", 64)
	for _, comment := range []string{base, templateCommitComment(base, id)} {
		if !templateCommitCommentMatches(comment, base) {
			t.Fatal("supported comment rejected")
		}
	}
	for _, comment := range []string{base + " [cvk:foreign]", templateCommitComment(base, id) + " trailing", base + " [cvk:" + id, templateCommitComment("wrong", id)} {
		if templateCommitCommentMatches(comment, base) {
			t.Fatal("foreign comment matched dependencies")
		}
	}
	if templateVersionCommentOperation(base) != "" || templateVersionCommentOperation(templateCommitComment(base, id)) != id {
		t.Fatal("nonce did not bind original journal identity")
	}
}

var templateVersionPublicModes = []string{"known", "version-missing-nonce", "version-wrong-nonce", "version-noncanonical", "version-duplicate-nonce", "receipt-version-pruned", "receipt-version-noncanonical", "version-body-comment-omitted", "version-meta-missing"}

func TestTemplateVersionPublicOwnership(t *testing.T) {
	for _, stage := range []string{"commit", "composite-commit"} {
		for _, mode := range templateVersionPublicModes {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseTemplateNoDispatchReplan(t, stage, "applied-"+mode, nil) })
		}
	}
}
