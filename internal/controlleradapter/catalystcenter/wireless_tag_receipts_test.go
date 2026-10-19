// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func configureWirelessTagComparisonFixture(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) (string, []byte) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: "comparison", Name: "wireless-tag-baseline", Key: "key"}}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Use: "PrivateComparisonKey", Section: "network_profiles", Path: wirelessTagComparisonScope, Source: "comparison"}}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = t.TempDir()
	relative, err := contract.IntentSecretRelativePath(contract.IntentSecretPathInput{ConfigName: config.Name, Section: "network_profiles", JSONPointer: wirelessTagComparisonScope, SourceAlias: "comparison", SecretName: "wireless-tag-baseline", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.intentSecretPath, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encoded := []byte(hex.EncodeToString(key))
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path, encoded
}

var wirelessTagReceiptModes = []string{"complete", "import", "capacity", "legacy", "legacy-private", "drop-verified", "edit-verified", "drop-effect", "history-race", "producer-changed", "key-removed", "key-rotated", "native-edit", "uuid-replaced", "alias", "receipt-edited", "replace-at-capacity", "child-failed", "tree-missing", "lost-ack", "managed-drift", "source", "owner"}

type wirelessTagReceiptQualification struct {
	mode, keyPath           string
	key                     []byte
	stage, steps            int
	recreateEpoch           int
	deletes                 int
	tagDeletes, pruneWrites int
	pruned                  []wirelessTagPruneFixtureDeletion
	changed                 bool
	held                    *cvk.NetworkControllerConfigurationOperation
	writes                  int
	retained                []cvk.NetworkControllerConfigurationActionApplication
}

func (q *wirelessTagReceiptQualification) seed(t *testing.T, a *adapter) {
	if q.mode != "capacity" {
		return
	}
	q.fill(t, a, 254)
}
func (q *wirelessTagReceiptQualification) fill(t *testing.T, a *adapter, count int) {
	t.Helper()
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration == nil {
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
	}
	at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
	for i := len(nc.Status.Configuration.ActionApplications); i < count; i++ {
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: fmt.Sprintf("other-%d", i), VerifiedAt: &at, Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), Section: "templates", Resource: "other", Action: "Update", Phase: "Verified", TaskID: "other-task", TargetID: "other", ConfigName: "other", ConfigUID: "other", ConfigGeneration: 1, ControllerGeneration: 1, SubmittedAt: &at}})
	}
	if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
}
func (q *wirelessTagReceiptQualification) seedImports(tags map[string][]map[string]any, cohort bool) {
	tags["siteTags"] = []map[string]any{{"siteTagId": "import-site", "siteTagName": "ST", "siteIds": []any{"lab-site"}, "apProfileName": "AP", "flexProfileName": "default-flex-profile"}}
	tags["policyTags"] = []map[string]any{{"policyTagId": "import-policy", "policyTagName": "PT", "siteIds": []any{"lab-site"}, "apZones": []any{"default-zone"}}}
	if cohort {
		tags["policyTags"] = []map[string]any{{"policyTagId": "import-policy-1", "policyTagName": "PT1", "siteIds": []any{"lab-site"}, "apZones": []any{"Z1"}}, {"policyTagId": "import-policy-2", "policyTagName": "PT2", "siteIds": []any{"lab-site"}, "apZones": []any{"Z2"}}}
	}
}
func (q *wirelessTagReceiptQualification) observe(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, tick func(), recovery *wirelessProfileRecoveryQualification, profiles []map[string]any, tags map[string][]map[string]any, writes, tagWrites int, model func(string) []byte, cohort bool) bool {
	t.Helper()
	q.steps++
	ctx := context.Background()
	state := journalState(t, a)
	if q.held != nil {
		for range 4 {
			tick()
		}
		current := journalState(t, a)
		if !reflect.DeepEqual(current.Operation, q.held) || tagWrites != q.writes || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("private tag uncertainty advanced or replayed")
		}
		if q.mode == "producer-changed" {
			for _, row := range current.ActionApplications {
				if row.Operation.ID == q.held.ID {
					t.Fatal("altered producer set published a receipt")
				}
			}
		}
		return true
	}
	if recovery.interrupted != nil {
		for range 5 {
			tick()
		}
		recovery.assertRetained(t, a, c, writes)
		if q.pruning() {
			if q.stage != 2 || q.tagDeletes != 1 || tagWrites != q.pruneWrites || !wirelessTagPruneSelected(recovery.interrupted.Resource) {
				t.Fatal("uncertain tag deletion advanced or replayed")
			}
			return true
		}
		if strings.HasPrefix(q.mode, "membership-") {
			expected := 5
			if cohort {
				expected = 6
			}
			if q.stage != 2 || tagWrites != expected || !wirelessTagMembershipSelected(recovery.interrupted.Resource) {
				t.Fatal("membership recovery did not retain exactly the original scoped Update")
			}
		}
		return true
	}
	if q.pruning() && q.stage == 2 {
		return q.observePruning(t, a, c, tick, recovery, tags, tagWrites, model, cohort)
	}
	op := state.Operation
	if op != nil && wirelessTagPrivateSelected(op.Resource) && op.Phase == "Unknown" && !q.changed {
		if (q.mode == "legacy" || q.mode == "legacy-private" && q.stage == 0) && wirelessTagBindingMode(op.Resource) != "" {
			nc, _ := a.currentController(ctx)
			legacy := legacyWirelessTagFixtureResource(t, a, c, op)
			if q.mode == "legacy-private" {
				parts := strings.Split(op.Resource, ":")
				ns := strings.SplitN(legacy, "/", 2)[0] + "/"
				legacy = ns + wirelessTagPrivatePrefix + strings.TrimPrefix(legacy, ns) + ":" + strings.Join(parts[len(parts)-3:], ":")
			}
			nc.Status.Configuration.Operation.Resource = legacy
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			return false
		}
		if q.mode == "producer-changed" {
			nc, _ := a.currentController(ctx)
			parts := strings.Split(nc.Status.Configuration.Operation.Resource, ":")
			parts[len(parts)-3] = strings.Repeat("f", 64)
			nc.Status.Configuration.Operation.Resource = strings.Join(parts, ":")
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			q.changed = true
			return false
		}
		if q.mode == "history-race" {
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigName: "other", ConfigUID: "other"})
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			q.changed = true
			return false
		}
	}
	if op != nil && wirelessTagPrivateSelected(op.Resource) && (op.Phase == "Verifying" || op.Phase == "Unknown") && q.changed && (q.mode == "history-race" || q.mode == "producer-changed") {
		q.held = op.DeepCopy()
		q.writes = tagWrites
		return false
	}
	if op != nil && wirelessTagPrivateSelected(op.Resource) && op.Phase == "Verified" && !q.changed {
		if !wirelessTagReceiptPresent(state, op) {
			t.Fatal("private Verified lacks complete atomic receipt")
		}
		switch q.mode {
		case "drop-verified", "edit-verified", "drop-effect":
			nc, _ := a.currentController(ctx)
			index := -1
			for i, row := range nc.Status.Configuration.ActionApplications {
				if row.Operation.ID == op.ID {
					index = i
				}
			}
			if index < 0 {
				t.Fatal("missing stored task")
			}
			q.held = op.DeepCopy()
			q.writes = tagWrites
			q.changed = true
			switch q.mode {
			case "drop-verified":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications[:index], nc.Status.Configuration.ActionApplications[index+1:]...)
			case "edit-verified":
				nc.Status.Configuration.ActionApplications[index].Operation.TaskID = "edited-task"
			case "drop-effect":
				nc.Status.Configuration.ActionApplications[index].EffectReceipts = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			return false
		}
	}
	if q.mode == "capacity" {
		if q.steps < 15 {
			return false
		}
		if tagWrites != 0 || state.Operation != nil || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("complete tag cohort capacity was not fenced")
		}
		return true
	}
	if strings.HasPrefix(q.mode, "membership-") && q.stage == 2 && q.mode != "membership-complete" && q.mode != "membership-zone-complete" && q.mode != "membership-compose-complete" {
		if q.steps < 12 {
			return false
		}
		expected := 4
		if cohort {
			expected = 5
		}
		if (q.mode == "membership-preflight-consumer" || q.mode == "membership-compose-preflight-consumer" || q.mode == "membership-compose-late-foreign") && !q.changed {
			t.Fatal("submission consumer injection was never exercised")
		}
		if q.mode == "membership-post-task-consumer" {
			expected++
			if op == nil || op.TaskID == "" {
				t.Fatal("post-task consumer released original task")
			}
			for _, row := range state.ActionApplications {
				if row.Operation.ID == op.ID {
					t.Fatal("unsafe post-task receipt published")
				}
			}
		}
		if q.mode == "membership-compose-post-task-foreign" {
			expected++
		}
		if (q.mode == "membership-compose-preflight-consumer" || q.mode == "membership-compose-late-foreign") && op != nil {
			t.Fatal("proven dependency preflight refusal did not retire its unsubmitted operation")
		}
		if q.mode == "membership-compose-parent-change" && writes != expected+4 {
			t.Fatal("ordinary parent change bypassed reference-blocked cohort")
		}
		if q.composition() && q.deletes != 0 {
			t.Fatal("blocked dependency cohort unassigned a site")
		}
		if tagWrites != expected || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unsafe membership removal advanced")
		}
		return true
	}
	if op != nil || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		return false
	}
	records, err := wirelessTagRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	if q.mode == "import" || q.mode == "legacy" {
		if len(records) != 0 || q.mode == "import" && tagWrites != 0 {
			t.Fatal("import or legacy task acquired tag ownership")
		}
		return true
	}
	if q.stage == 0 {
		if len(records) != 2 || tagWrites != 2 {
			t.Fatal("creation should retain one row per original task")
		}
		members := 0
		for _, row := range records {
			if row.Operation.Action != "Create" {
				t.Fatal("initial baseline is not creation-bound")
			}
			members += len(row.EffectReceipts)
			if q.mode == "legacy-private" && wirelessTagBindingMode(row.Operation.Resource) != "" {
				t.Fatal("legacy Create evidence was silently upgraded")
			}
		}
		expected := 2
		if cohort {
			expected = 3
		}
		if members != expected {
			t.Fatal("incomplete cohort member set")
		}
		encoded, _ := json.Marshal(state)
		if strings.Contains(string(encoded), string(q.key)) {
			t.Fatal("comparison key leaked into public journal")
		}
		// A raw read-only compiler may verify attributes from the retained
		// selector, but cannot publish or release private receipts without a key.
		readonly, err := a.client.compileNetworkProfileConfiguration(c.Spec.Source.Inline.Raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range records {
			if done, err := readonly.verify(ctx, &row.Operation); err != nil || !done {
				t.Fatal("private selector readback failed", err)
			}
		}
		q.retained = state.DeepCopy().ActionApplications
		if q.mode == "membership-prune-created-only" || q.mode == "membership-prune-capacity" {
			q.startPruning(t, a, c, tags, tagWrites)
			return false
		}
		switch q.mode {
		case "key-removed":
			c.Spec.SecretRefs = nil
			c.Generation++
			if err := a.statusWriter.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
		case "key-rotated":
			if err := os.WriteFile(q.keyPath, []byte(strings.Repeat("b", 64)), 0600); err != nil {
				t.Fatal(err)
			}
		case "native-edit":
			tags["siteTags"][0]["apProfileName"] = "AP-OTHER"
		case "uuid-replaced":
			tags["siteTags"][0]["siteTagId"] = "replacement-id"
		case "alias":
			tags["siteTags"][0]["siteTagName"] = "ST-renamed"
			c.Spec.Source.Inline.Raw = []byte(strings.Replace(string(c.Spec.Source.Inline.Raw), `"name":"ST"`, `"name":"ST-renamed"`, 1))
			c.Generation++
			if err := a.statusWriter.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
		case "receipt-edited":
			nc, _ := a.currentController(ctx)
			for i, row := range nc.Status.Configuration.ActionApplications {
				if wirelessTagPrivateSelected(row.Operation.Resource) {
					nc.Status.Configuration.ActionApplications[i].EffectReceipts[0] = strings.Replace(row.EffectReceipts[0], ":", ":edited-", 1)
					break
				}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		default:
			q.stage = 1
			c.Spec.Source.Inline.Raw = model("WLAN2")
			c.Generation++
			if err := a.statusWriter.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			return false
		}
		for range 4 {
			tick()
		}
		if journalState(t, a).Operation != nil || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || tagWrites != 2 {
			t.Fatal("changed private custody accepted")
		}
		return true
	}
	expectedRows, expectedWrites := 4, 4
	if cohort {
		expectedRows, expectedWrites = 5, 5
	}
	compareWrites := expectedWrites
	if q.stage == 2 {
		if strings.HasPrefix(q.mode, "membership-") {
			compareWrites += 2
			if cohort {
				compareWrites++
			}
		} else {
			compareWrites++
		}
	}
	if len(records) != expectedRows || tagWrites != compareWrites {
		t.Fatal("latest applications lost creators or duplicated updates")
	}
	if _, err := wirelessTagLatest(records); err != nil {
		t.Fatal(err)
	}
	if q.mode == "legacy-private" && q.stage == 1 {
		latest, _ := wirelessTagLatest(records)
		for _, row := range latest {
			if row.row.Operation.Action != "Update" || wirelessTagBindingMode(row.row.Operation.Resource) != "write" {
				t.Fatal("verified new Update failed to replace legacy latest proof")
			}
		}
	}
	if q.pruning() && q.stage == 1 {
		q.startPruning(t, a, c, tags, tagWrites)
		switch q.mode {
		case "membership-prune-lost-ack", "membership-prune-child-failed", "membership-prune-tree-missing", "membership-prune-source", "membership-prune-owner":
			recovery.mode = strings.TrimPrefix(q.mode, "membership-prune-")
		}
		return false
	}
	if strings.HasPrefix(q.mode, "membership-") && q.stage == 1 {
		q.startMembershipRemoval(t, a, c)
		switch q.mode {
		case "membership-lost-ack", "membership-child-failed", "membership-tree-missing", "membership-source", "membership-owner", "membership-compose-lost-ack", "membership-compose-child-failed", "membership-compose-source", "membership-compose-owner":
			recovery.mode = strings.TrimPrefix(strings.TrimPrefix(q.mode, "membership-"), "compose-")
		}
		return false
	}
	if q.mode == "replace-at-capacity" && q.stage == 1 {
		q.stage = 2
		q.fill(t, a, 256)
		var raw map[string]any
		if json.Unmarshal(c.Spec.Source.Inline.Raw, &raw) != nil {
			t.Fatal("invalid source")
		}
		p := raw["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)[0].(map[string]any)
		p["site_tags"].([]any)[0].(map[string]any)["ap_profile_name"] = "AP-OTHER"
		c.Spec.Source.Inline.Raw, _ = json.Marshal(raw)
		c.Generation++
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		return false
	}
	if q.stage == 2 && !strings.HasPrefix(q.mode, "membership-") {
		if tagWrites != expectedWrites+1 || len(state.ActionApplications) != 256 {
			t.Fatal("latest slot was not replaced at capacity")
		}
	}
	if q.composition() {
		if q.deletes != 1 {
			t.Fatal("combined source did not remove exactly one association")
		}
		rows, err := profileSiteApplicationRecords(state)
		if err != nil {
			t.Fatal(err)
		}
		deleted := 0
		for _, row := range rows {
			if row.Operation.Action == "Delete" {
				if row.Operation.TargetID != "lab2-site" {
					t.Fatal("wrong native association tombstone")
				}
				deleted++
			}
		}
		if deleted != 1 || len(rows) != 2 {
			t.Fatal("combined lifecycle lost association evidence")
		}
	}
	for range 3 {
		tick()
	}
	if journalState(t, a).Operation != nil || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("private tag no-op did not converge")
	}
	return true
}

// Reconstruct a genuine old selector from authoritative fixture observations.
// Merely stripping v4's envelope would leave a new dependency digest behind.
func legacyWirelessTagFixtureResource(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) string {
	t.Helper()
	e, err := a.client.compileNetworkProfileConfiguration(c.Spec.Source.Inline.Raw)
	if err != nil {
		t.Fatal(err)
	}
	kind, key, _ := networkProfileMetadataResource(op.Resource)
	ns := strings.SplitN(op.Resource, "/", 2)[0] + "/"
	for _, tags := range e.tagExecutors {
		if tags.policy != (kind != "site-tag") {
			continue
		}
		rows, err := tags.observe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if kind == "policy-cohort" {
			resource, _, err := wirelessPolicyCohortResource(policyRowsForProfile(rows, op.ParentID))
			if err != nil {
				t.Fatal(err)
			}
			return ns + resource
		}
		for _, row := range rows {
			logical, _ := wirelessTagLogical(row.resource)
			if logical == key && row.profileID == op.ParentID {
				return ns + row.resource
			}
		}
	}
	t.Fatal("legacy fixture target missing")
	return ""
}
func exerciseWirelessTagReceipts(t *testing.T, cohort bool, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	baseMode, target := "complete", "all"
	switch mode {
	case "child-failed", "tree-missing", "lost-ack", "managed-drift", "source", "owner":
		baseMode, target = mode, "site-tag"
	}
	qualifyWirelessProfileManifestRestart(t, cohort, "", &wirelessProfileRecoveryQualification{mode: baseMode, target: target, install: install, receipts: &wirelessTagReceiptQualification{mode: mode}})
}
func TestWirelessTagReceipts(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		for _, mode := range wirelessTagReceiptModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) { exerciseWirelessTagReceipts(t, cohort, mode, nil) })
		}
	}
}
