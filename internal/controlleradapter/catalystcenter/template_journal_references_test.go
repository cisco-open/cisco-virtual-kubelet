// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func templateJournalOperation(now time.Time) *cvk.NetworkControllerConfigurationOperation {
	at := metav1.NewTime(now.Add(-time.Minute))
	return &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "deleted-source", ConfigUID: "original-owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "template-deploy:target/target-v1/1/" + strings.Repeat("b", 64), Action: "Update", TargetID: "device", ParentID: "site", TargetSerial: "serial", Phase: "Unknown", SubmittedAt: &at}
}

func TestTemplateJournalReferences(t *testing.T) {
	for _, mode := range []string{"unknown-receipt", "submitting-receipt", "prepared-receipt", "reversed-preparation", "credential-history", "composite-private", "composite-version-alias", "empty", "unrelated", "prepared", "submitting", "unknown-taskless", "accepted", "verifying", "verified", "application", "atomic-application", "legacy-application", "composite-parent", "composite-member", "version-alias", "private-inputs", "removed-source-owner", "bad-resource", "bad-operation", "bad-phase", "missing-receipt", "bad-receipt", "missing-time", "future-time", "unpaired-source", "created-result", "wrong-section", "wrong-action", "bad-target", "bad-parent", "regular-graph", "missing-graph", "duplicate-graph", "foreign-graph-resource", "bad-application-key", "bad-application-section", "unverified-application", "future-application", "early-application", "duplicate-application", "duplicate-resource", "conflicting-operation", "duplicate-deployment", "uppercase-deployment-collision", "malformed-unrelated", "too-many-receipts"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			op := templateJournalOperation(now)
			state := &cvk.NetworkControllerConfigurationStatus{Operation: op}
			aliases := map[string]bool{"target": true, "target-v1": true}
			want := 1
			valid := true
			setAccepted := func() { op.Phase = "Accepted"; op.TaskID = deploymentFixtureID }
			setApplication := func() {
				op.Phase = "Verified"
				op.TaskID = deploymentFixtureID
				at := metav1.NewTime(now)
				state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: op.Resource, Operation: *op.DeepCopy(), VerifiedAt: &at}}
				state.Operation = nil
			}
			setGraph := func() {
				op.Resource = "template-composite-deploy:" + strings.Repeat("c", 64)
				op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1", Entries: []cvk.NetworkControllerConfigurationDependencyVersion{{ID: "parent", VersionID: "parent-v1", Version: 1}, {ID: "target", VersionID: "target-v1", Version: 1}}}
			}
			switch mode {
			case "unknown-receipt":
				setAccepted()
				op.Phase = "Unknown"
			case "submitting-receipt":
				setAccepted()
				op.Phase = "Submitting"
			case "prepared-receipt":
				setAccepted()
				op.Phase = "Prepared"
				valid = false
			case "reversed-preparation":
				at := metav1.NewTime(now)
				op.PreparedAt = &at
				valid = false
			case "credential-history":
				op.SSIDCredentialHistory = []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 1, Version: 1}}
				valid = false
			case "composite-private":
				setGraph()
				op.Resource += ":private-v1:" + strings.Repeat("d", 64) + ":" + strings.Repeat("e", 64)
			case "composite-version-alias":
				setGraph()
				aliases = map[string]bool{"target-v1": true}
			case "empty":
				state = nil
				want = 0
			case "unrelated":
				op.Resource = "pnp-claim:unrelated"
				want = 0
			case "prepared":
				op.Phase = "Prepared"
				op.SubmittedAt = nil
			case "submitting":
				op.Phase = "Submitting"
			case "unknown-taskless":
			case "accepted":
				setAccepted()
			case "verifying":
				setAccepted()
				op.Phase = "Verifying"
			case "verified":
				setAccepted()
				op.Phase = "Verified"
			case "application":
				setApplication()
			case "atomic-application":
				setApplication()
				state.Operation = op
				want = 2
			case "legacy-application":
				setApplication()
				state.ActionApplications[0].VerifiedAt = nil
			case "composite-parent":
				setGraph()
				aliases = map[string]bool{"parent": true}
			case "composite-member":
				setGraph()
			case "version-alias":
				aliases = map[string]bool{"target-v1": true}
			case "private-inputs":
				op.Resource += "/private-v1/" + strings.Repeat("d", 64) + "/" + strings.Repeat("e", 64)
			case "removed-source-owner":
				op.SourceUID = "deleted-configmap"
				op.SourceVersion = "old-version"
				state.Owners = nil
			case "bad-resource":
				op.Resource = "template-deploy:opaque"
				valid = false
			case "bad-operation":
				op.ID = "bad"
				valid = false
			case "bad-phase":
				op.Phase = "Finished"
				valid = false
			case "missing-receipt":
				op.Phase = "Accepted"
				valid = false
			case "bad-receipt":
				setAccepted()
				op.TaskID = "native-task-not-a-deployment"
				valid = false
			case "missing-time":
				setAccepted()
				op.SubmittedAt = nil
				valid = false
			case "future-time":
				at := metav1.NewTime(now.Add(time.Minute))
				op.SubmittedAt = &at
				valid = false
			case "unpaired-source":
				op.SourceUID = "pruned-source"
				valid = false
			case "created-result":
				op.CreatedResourceID = "foreign-result"
				valid = false
			case "wrong-section":
				op.Section = "templates"
				valid = false
			case "wrong-action":
				op.Action = "Delete"
				valid = false
			case "bad-target":
				op.TargetID = "../device"
				valid = false
			case "bad-parent":
				op.ParentID = "../site"
				valid = false
			case "regular-graph":
				setGraph()
				op.Resource = templateJournalOperation(now).Resource
				valid = false
			case "missing-graph":
				setGraph()
				op.DependencyVersions = nil
				valid = false
			case "duplicate-graph":
				setGraph()
				op.DependencyVersions.Entries[1] = op.DependencyVersions.Entries[0]
				valid = false
			case "foreign-graph-resource":
				setGraph()
				op.Resource = "unknown:resource"
				valid = false
			case "bad-application-key":
				setApplication()
				state.ActionApplications[0].Resource = "unknown"
				valid = false
			case "bad-application-section":
				setApplication()
				state.ActionApplications[0].Section = "templates"
				valid = false
			case "unverified-application":
				setApplication()
				state.ActionApplications[0].Operation.Phase = "Unknown"
				valid = false
			case "future-application":
				setApplication()
				at := metav1.NewTime(now.Add(time.Minute))
				state.ActionApplications[0].VerifiedAt = &at
				valid = false
			case "early-application":
				setApplication()
				at := metav1.NewTime(now.Add(-2 * time.Minute))
				state.ActionApplications[0].VerifiedAt = &at
				valid = false
			case "duplicate-application":
				setApplication()
				state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
				valid = false
			case "duplicate-resource":
				setApplication()
				copy := state.ActionApplications[0].DeepCopy()
				copy.Operation.ID = strings.Repeat("f", 32)
				state.ActionApplications = append(state.ActionApplications, *copy)
				valid = false
			case "conflicting-operation":
				setApplication()
				state.Operation = op.DeepCopy()
				state.Operation.TargetID = "replacement"
				valid = false
			case "duplicate-deployment", "uppercase-deployment-collision":
				setApplication()
				state.Operation = op.DeepCopy()
				state.Operation.ID = strings.Repeat("f", 32)
				state.Operation.Resource = strings.Replace(op.Resource, "target-v1/1", "target-v2/2", 1)
				if mode == "uppercase-deployment-collision" {
					state.Operation.TaskID = "abcdefab-1111-4111-8111-111111111111"
					state.ActionApplications[0].Operation.TaskID = strings.ToUpper(state.Operation.TaskID)
				}
				valid = false
			case "malformed-unrelated":
				op.Resource = "template-deploy:other/version/1/not-a-digest"
				valid = false
			case "too-many-receipts":
				state.ActionApplications = make([]cvk.NetworkControllerConfigurationActionApplication, 257)
				valid = false
			}
			before, _ := json.Marshal(state)
			// JSON reload models a fresh process, not independent native completion.
			var restored *cvk.NetworkControllerConfigurationStatus
			if err := json.Unmarshal(before, &restored); err != nil {
				t.Fatal(err)
			}
			got, err := templateJournalReferences(restored, aliases, now)
			if !valid {
				if err == nil || got != nil {
					t.Fatal("uncertain retained evidence became a reference-free view")
				}
				return
			}
			if err != nil || len(got) != want {
				t.Fatal("known journal references incorrect", err, len(got), want)
			}
			if want > 0 && (got[0].OperationID != strings.Repeat("a", 32) || got[0].Version != 1 || got[0].VersionID == "") {
				t.Fatal("lost immutable deployment binding")
			}
			if mode == "composite-member" && got[0].Position != 1 || mode == "composite-parent" && got[0].Position != 0 {
				t.Fatal("composite role lost")
			}
			raw, _ := json.Marshal(got)
			for _, private := range []string{"deleted-source", "original-owner", "serial", strings.Repeat("b", 64), strings.Repeat("d", 64), strings.Repeat("e", 64)} {
				if strings.Contains(string(raw), private) {
					t.Fatal("unneeded journal/input metadata escaped projection")
				}
			}
			after, _ := json.Marshal(restored)
			if string(before) != string(after) {
				t.Fatal("reference reading mutated journal")
			}
		})
	}
}

func TestRetainedTemplateDeploymentReferences(t *testing.T) {
	for _, mode := range []string{"composite-member", "same", "empty", "deleted-source", "source-edited", "owner-removed", "late-journal", "journal-cleared", "late-version-alias", "target-replaced", "unavailable-history", "controller-paused"} {
		t.Run(mode, func(t *testing.T) { exerciseRetainedTemplateReferences(t, mode, nil) })
	}
}

func exerciseRetainedTemplateReferences(t *testing.T, mode string, wire func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	if wire != nil {
		wire(a, config)
	}
	now := time.Now().UTC().Truncate(time.Second)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := templateJournalOperation(now)
	op.ConfigName = config.Name
	op.ConfigUID = string(config.UID)
	op.ConfigGeneration = config.Generation
	op.ControllerGeneration = nc.Generation
	if mode == "composite-member" {
		op.Resource = "template-composite-deploy:" + strings.Repeat("c", 64)
		op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1", Entries: []cvk.NetworkControllerConfigurationDependencyVersion{{ID: "parent", VersionID: "parent-v1", Version: 1}, {ID: "target", VersionID: "target-v1", Version: 1}}}
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
	if mode == "owner-removed" {
		nc.Status.Configuration.Owners = nil
	}
	if mode == "empty" {
		nc.Status.Configuration = nil
	}
	if mode == "controller-paused" {
		nc.Spec.Paused = true
		if err := a.statusWriter.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if mode == "deleted-source" {
		if err := a.statusWriter.Delete(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "source-edited" {
		config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"sites":[]}}`)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	var reads atomic.Int32
	var versionReads atomic.Int32
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("reference observation performed a mutation")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == templateVersionsPath+"/target" {
			versionRead := versionReads.Add(1)
			if mode == "unavailable-history" {
				w.WriteHeader(503)
				return
			}
			versions := []any{map[string]any{"id": "target"}, map[string]any{"id": "target-v1", "version": "1"}}
			if mode == "late-version-alias" && versionRead > 1 {
				versions = append(versions, map[string]any{"id": "target-v2", "version": "2"})
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"templateId": "target", "versionsInfo": versions}})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, templatesPath+"/")
		if id == "target" {
			read := reads.Add(1)
			if read == 1 && (mode == "late-journal" || mode == "journal-cleared") {
				fresh, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if mode == "late-journal" {
					fresh.Status.Configuration.Operation.Phase = "Submitting"
				} else {
					fresh.Status.Configuration.Operation = nil
				}
				if err := a.statusWriter.Status().Update(ctx, fresh); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
			}
		}
		body := map[string]any{"id": id, "name": "target", "projectId": "project", "composite": false, "templateContent": "private CLI"}
		if id != "target" {
			body["version"] = strings.TrimPrefix(id, "target-v")
		}
		if mode == "target-replaced" {
			body["id"] = "replacement"
		}
		json.NewEncoder(w).Encode(body)
	})
	refs, err := a.retainedTemplateDeploymentReferences(ctx, templateSummary{ID: "target", ProjectID: "project", Name: "target"})
	bad := mode == "late-journal" || mode == "journal-cleared" || mode == "late-version-alias" || mode == "target-replaced" || mode == "unavailable-history" || mode == "controller-paused"
	if bad {
		if err == nil || refs != nil {
			t.Fatal("changing/unknown evidence returned a usable view")
		}
		return
	}
	want := 1
	if mode == "empty" {
		want = 0
	}
	if err != nil || len(refs) != want {
		t.Fatal("retained references lost across source/owner changes", err, len(refs))
	}
}
