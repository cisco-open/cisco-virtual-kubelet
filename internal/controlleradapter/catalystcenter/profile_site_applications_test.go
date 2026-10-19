// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var profileSiteReceiptModes = []string{"complete", "accepted", "import", "legacy", "repair", "capacity", "replace-at-capacity", "drop-verified-row", "corrupt-row", "corrupt-commit", "settled-corrupt-row", "duplicate", "history-race", "alias", "site-replaced", "parent-replaced", "deselect"}

type profileSiteReceiptQualification struct {
	prune   *profileSitePruneQualification
	mode    string
	steps   int
	changed bool
	initial *cvk.NetworkControllerConfigurationActionApplication
	held    *cvk.NetworkControllerConfigurationOperation
}

func (q *profileSiteReceiptQualification) prepare(t *testing.T, a *adapter) {
	t.Helper()
	count := 0
	switch q.mode {
	case "capacity":
		count = 256
	case "replace-at-capacity", "prune-at-capacity":
		count = 255
	case "history-race":
		count = 1
	}
	if count == 0 {
		return
	}
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration == nil {
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
	}
	at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
	for i := 0; i < count; i++ {
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: fmt.Sprintf("unrelated-%d", i), VerifiedAt: &at, Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), ConfigName: "unrelated", ConfigUID: "unrelated", ConfigGeneration: 1, ControllerGeneration: 1, Section: "templates", Resource: "unrelated", Action: "Update", Phase: "Verified", TargetID: "unrelated", TaskID: "unrelated-task", SubmittedAt: &at}})
	}
	if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
}
func (q *profileSiteReceiptQualification) observe(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, tick func(), assigned map[string]bool, writes int, namespace, name string, model func([]string) []byte, nativePath, profileID, siteID *string) bool {
	t.Helper()
	if q.prune != nil {
		return q.prune.observe(t, a, config, tick, assigned, writes, namespace, name, model, nativePath, profileID, siteID)
	}
	q.steps++
	ctx := context.Background()
	state := journalState(t, a)
	if q.mode == "import" || q.mode == "capacity" {
		if q.steps < 7 {
			return false
		}
		rows, err := profileSiteApplicationRecords(state)
		if err != nil || len(rows) != 0 || writes != 0 || state.Operation != nil {
			t.Fatal("import/capacity minted an association receipt or effect", err)
		}
		if (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (q.mode == "import") {
			t.Fatal("incorrect import/capacity boundary")
		}
		return true
	}
	if q.held != nil {
		for range 5 {
			tick()
		}
		current := journalState(t, a)
		if !reflect.DeepEqual(current.Operation, q.held) || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || writes != 1 {
			t.Fatal("invalid application custody settled or replayed")
		}
		return true
	}
	if op := state.Operation; op != nil && op.Phase == "Accepted" {
		if q.mode == "accepted" {
			return false
		}
		if !profileSiteApplicationSelected(op) {
			t.Fatal("new association lacks versioned absent-before-write binding")
		}
		if q.mode == "legacy" && !q.changed {
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.Operation.Resource = namespace + tagMembershipIdentity("Global/A", name)
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			q.changed = true
		}
		if q.mode == "history-race" && !q.changed {
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.ActionApplications = nil
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			q.changed = true
		}
		if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
		return false
	}
	if state.Operation != nil && state.Operation.Phase == "Verifying" && q.mode == "history-race" {
		q.held = state.Operation.DeepCopy()
		return false
	}
	if op := state.Operation; op != nil && op.Phase == "Verified" {
		if q.mode == "legacy" {
			if profileSiteApplicationSelected(op) || len(state.ActionApplications) != 0 {
				t.Fatal("legacy task gained receipt authority")
			}
			return false
		}
		if !profileSiteApplicationPresent(state, op) {
			t.Fatal("Verified association missing atomic application seal")
		}
		switch q.mode {
		case "drop-verified-row", "corrupt-row", "corrupt-commit", "duplicate":
			q.held = op.DeepCopy()
			nc, _ := a.currentController(ctx)
			row := &nc.Status.Configuration.ActionApplications[len(nc.Status.Configuration.ActionApplications)-1]
			switch q.mode {
			case "drop-verified-row":
				nc.Status.Configuration.ActionApplications = nil
			case "corrupt-row":
				row.Operation.TaskID = "changed-task"
			case "corrupt-commit":
				parts := strings.Split(row.Resource, ":")
				parts[len(parts)-1] = strings.Repeat("f", 64)
				row.Resource = strings.Join(parts, ":")
			case "duplicate":
				duplicate := row.DeepCopy()
				parts := strings.Split(duplicate.Resource, ":")
				seal := parts[len(parts)-1]
				first := "0"
				if seal[0] == '0' {
					first = "1"
				}
				parts[len(parts)-1] = first + seal[1:]
				duplicate.Resource = strings.Join(parts, ":")
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, *duplicate)
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			return false
		}
		return false
	}
	if state.Operation != nil || writes == 0 {
		return false
	}
	rows, err := profileSiteApplicationRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	if q.mode == "legacy" {
		if len(rows) != 0 || writes != 1 {
			t.Fatal("legacy task adopted as owned application")
		}
		return true
	}
	if len(rows) != 1 {
		t.Fatal("association does not retain exactly one application")
	}
	var row cvk.NetworkControllerConfigurationActionApplication
	for _, r := range rows {
		row = r
	}
	if row.Operation.ParentID != "profile-id" || row.Operation.TargetID != "site-a" || !assigned["foreign-site"] {
		t.Fatal("application or foreign membership identity changed")
	}
	if q.initial == nil {
		q.initial = row.DeepCopy()
	}
	if !q.changed {
		switch q.mode {
		case "repair", "replace-at-capacity":
			delete(assigned, "site-a")
			q.changed = true
			return false
		case "alias":
			*nativePath = "Global/B"
			config.Spec.Source.Inline.Raw = model([]string{"Global/B"})
		case "site-replaced":
			*siteID = "replacement-site"
		case "parent-replaced":
			*profileID = "replacement-profile"
		case "deselect":
			config.Spec.Source.Inline.Raw = model(nil)
		case "settled-corrupt-row":
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.ActionApplications[0].Operation.TaskID = "changed-after-settlement"
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			for range 5 {
				tick()
			}
			current := journalState(t, a)
			_, err := profileSiteApplicationRecords(current)
			if err == nil || current.Operation != nil || writes != 1 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("historical row edit silently accepted")
			}
			return true
		default:
			for range 3 {
				tick()
			}
			if writes != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("receipt no-op failed")
			}
			return true
		}
		if q.mode == "alias" || q.mode == "deselect" {
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		q.changed = true
	}
	for range 6 {
		tick()
	}
	current := journalState(t, a)
	if q.mode == "repair" || q.mode == "replace-at-capacity" {
		// The caller's write count is sampled before these final reads; completion
		// is checked on the next tick with fresh counters.
		if row.Operation.ID == q.initial.Operation.ID {
			return false
		}
		if row.Operation.TaskID == q.initial.Operation.TaskID || row.Operation.Resource == q.initial.Operation.Resource || writes != 2 || q.mode == "replace-at-capacity" && len(current.ActionApplications) != 256 {
			t.Fatal("repair did not replace the single bounded latest application")
		}
		return true
	}
	if writes != 1 || current.Operation != nil || !reflect.DeepEqual(current.ActionApplications, state.ActionApplications) || (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (q.mode == "deselect") {
		t.Fatal("retained application silently migrated or was erased")
	}
	return true
}
func TestProfileSiteApplications(t *testing.T) {
	for _, wireless := range []bool{false, true} {
		for _, mode := range profileSiteReceiptModes {
			t.Run(fmt.Sprintf("wireless-%t/%s", wireless, mode), func(t *testing.T) {
				exerciseProfileSiteRecovery(t, wireless, "complete", nil, &profileSiteReceiptQualification{mode: mode})
			})
		}
	}
}
