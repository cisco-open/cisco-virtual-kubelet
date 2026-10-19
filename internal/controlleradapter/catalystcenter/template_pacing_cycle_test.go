// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Real public routing/journal with fake Kubernetes persistence and native HTTPS
// fixtures. Timestamp aging simulates elapsed time, not installed-actor proof.
func TestCompositeTemplatePublicSettlingSurvivesRestart(t *testing.T) {
	for _, mode := range []string{"settled", "source-changed", "lost-receipt", "member-race", "member-drift"} {
		t.Run(mode, func(t *testing.T) { testCompositeTemplatePublicSettling(t, mode) })
	}
}

func testCompositeTemplatePublicSettling(t *testing.T, mode string) {
	ctx := context.Background()
	a, config := journalFixture(t)
	raw := `{"catalyst_center":{"templates":{"projects":[{"name":"Lab","dayn_templates":[{"name":"combined","composite":true,"containing_templates":["base"],"language":"JINJA","software_type":"IOS-XE","device_types":[{"product_family":"Switches and Hubs"}]},{"name":"base","language":"JINJA","software_type":"IOS-XE","device_types":[{"product_family":"Switches and Hubs"}]}]}]}}}`
	intent, err := parseTemplateIntent([]byte(raw), map[string]string{"base.j2": "hostname lab"})
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	for _, d := range intent.definitions {
		if !d.identity.Composite {
			base = d.payload
		}
	}
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "template-model", UID: "template-source"}, Data: map[string]string{"model.yaml": raw, nacmodel.TemplateAssetsKey: `{"base.j2":"hostname lab"}`}}
	if err := a.statusWriter.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"templates"}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: model.Name, Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	projectID, baseID, combinedID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	writes := 0
	var combined, committed map[string]any
	comment := ""
	baseVersion := 1
	submitted := time.Now()
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(r.URL.Path, projectID, "project"), baseID, "base"), combinedID, "combined")
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			submitted = time.Now()
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			switch r.URL.Path {
			case templateProjectsPath + "/project/template":
				combined = body
				combined["id"] = combinedID
				combined["projectId"] = projectID
			case templateVersionsPath:
				encoded, _ := json.Marshal(combined)
				_ = json.Unmarshal(encoded, &committed)
				committed["id"] = "combined-v1"
				committed["version"] = "1"
				comment, _ = body["comments"].(string)
			default:
				t.Errorf("unexpected mutation %s", r.URL.Path)
			}
			if mode == "lost-receipt" {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			fmt.Fprintf(w, `{"response":{"taskId":"task-%d"}}`, writes)
			return
		}
		switch {
		case r.URL.Path == templateProjectsPath:
			fmt.Fprintf(w, `[{"id":%q,"name":"Lab"}]`, projectID)
		case r.URL.Path == templatesPath:
			rows := []any{map[string]any{"templateId": baseID, "name": "base", "projectId": projectID}}
			if combined != nil {
				rows = append(rows, map[string]any{"templateId": combinedID, "name": "combined", "projectId": projectID})
			}
			_ = json.NewEncoder(w).Encode(rows)
		case r.URL.Path == templateVersionsPath+"/base":
			if mode == "member-race" {
				state := journalState(t, a)
				if state.Operation != nil && state.Operation.Phase == "Submitting" {
					baseVersion = 2
				}
			}
			fmt.Fprintf(w, `[{"templateId":%q,"versionsInfo":[{"id":%q},{"id":"base-v%d","version":%d}]}]`, baseID, baseID, baseVersion, baseVersion)
		case r.URL.Path == templateVersionsPath+"/combined":
			rows := []any{map[string]any{"id": combinedID}}
			if committed != nil {
				rows = append(rows, map[string]any{"id": "combined-v1", "version": 1, "versionComment": comment})
			}
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"templateId": combinedID, "versionsInfo": rows}})
		case r.URL.Path == templatesPath+"/base" || strings.HasPrefix(r.URL.Path, templatesPath+"/base-v"):
			body := map[string]any{}
			for key, value := range base {
				body[key] = value
			}
			body["id"], body["projectId"] = baseID, projectID
			if strings.HasPrefix(r.URL.Path, templatesPath+"/base-v") {
				body["id"], body["version"] = fmt.Sprintf("base-v%d", baseVersion), fmt.Sprint(baseVersion)
			}
			_ = json.NewEncoder(w).Encode(body)
		case r.URL.Path == templatesPath+"/combined":
			_ = json.NewEncoder(w).Encode(combined)
		case r.URL.Path == templatesPath+"/combined-v1":
			_ = json.NewEncoder(w).Encode(committed)
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			data := ""
			if id == "task-1" {
				data = combinedID
			}
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "isError": false, "serviceType": "NCTP", "data": data, "startTime": submitted.UnixMilli(), "endTime": time.Now().UnixMilli()})
		default:
			t.Errorf("unexpected observation %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	evaluate := func() string {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		restarted := *a
		nc, err := restarted.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, handled, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, &configapi.NetworkControllerConfigStatus{})
		if !handled {
			t.Fatal("public compiler rejected model", reason)
		}
		return reason
	}
	memberRefusalQualified := false
	for stage := 1; stage <= 2; stage++ {
		if reason := evaluate(); reason != "DependencySettlingPending" {
			t.Fatal("no durable preparation", reason)
		}
		op := journalState(t, a).Operation.DeepCopy()
		if op.Phase != "Prepared" || op.PreparedAt == nil || op.TaskID != "" || op.SubmittedAt != nil || writes != stage-1 {
			t.Fatal("preparation mutated native state")
		}
		for range 3 {
			if reason := evaluate(); reason != "DependencySettlingPending" || journalState(t, a).Operation.ID != op.ID || writes != stage-1 {
				t.Fatal("restart lost settling boundary", reason)
			}
		}
		if mode == "member-drift" {
			base["templateContent"] = "! unrelated native edit"
			evaluate()
			if journalState(t, a).Operation != nil || writes != 0 {
				t.Fatal("dependency drift stranded preparation or submitted stale composite")
			}
			return
		}
		if mode == "source-changed" {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
				t.Fatal(err)
			}
			model.Data[nacmodel.TemplateAssetsKey] = `{"base.j2":"hostname changed"}`
			if err := a.statusWriter.Update(ctx, model); err != nil {
				t.Fatal(err)
			}
			evaluate()
			if journalState(t, a).Operation != nil || writes != 0 {
				t.Fatal("changed source submitted old preparation")
			}
			return
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.Operation.PreparedAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second).Add(-12 * time.Second)}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if mode == "member-race" && !memberRefusalQualified {
			if reason := evaluate(); reason != "NativePreflightRefused" || writes != 0 || journalState(t, a).Operation != nil {
				t.Fatal("proven unsubmitted member race was not retired", reason)
			}
			if reason := evaluate(); reason != "DependencySettlingPending" {
				t.Fatal("changed member did not start fresh settling", reason)
			}
			fresh := journalState(t, a).Operation.DeepCopy()
			if fresh == nil || fresh.ID == op.ID || fresh.Phase != "Prepared" || fresh.PreparedAt == nil || fresh.SubmittedAt != nil || writes != 0 {
				t.Fatal("replan reused a consumed operation or submitted before fresh settling")
			}
			for range 3 {
				if reason := evaluate(); reason != "DependencySettlingPending" || journalState(t, a).Operation.ID != fresh.ID || writes != 0 {
					t.Fatal("restart lost fresh member-version settling", reason)
				}
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			current.Status.Configuration.Operation.PreparedAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second).Add(-12 * time.Second)}
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			op = journalState(t, a).Operation.DeepCopy()
			memberRefusalQualified = true
		}
		if mode == "lost-receipt" {
			expectedWrites := 1
			if reason := evaluate(); reason != "SubmissionRecoveryRequired" || writes != expectedWrites || journalState(t, a).Operation.Phase != "Unknown" {
				t.Fatal("ambiguous receipt not retained", reason)
			}
			for range 3 {
				evaluate()
			}
			if writes != expectedWrites || journalState(t, a).Operation.ID != op.ID || journalState(t, a).Operation.Phase != "Unknown" {
				t.Fatal("ambiguous native submission replayed")
			}
			return
		}
		if reason := evaluate(); reason != "TaskAccepted" || writes != stage || journalState(t, a).Operation.ID != op.ID {
			t.Fatal("settled dependency did not submit exactly once", reason, writes)
		}
		for range 3 {
			evaluate()
		}
		if journalState(t, a).Operation != nil {
			t.Fatal("verified operation not retired")
		}
	}
	if reason := evaluate(); reason != "ApplyVerified" || writes != 2 {
		t.Fatal("completed model replayed", reason, writes)
	}
}
