// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var switchingProfileRecoveryModes = []string{"preflight-read-error", "known", "slash-name", "legacy-empty", "legacy-unbound", "project-replaced", "template-replaced", "taskless", "source", "owner", "child-failed", "child-pending", "tree-missing", "foreign-root", "mid-tree-inherited", "inherited-reordered", "accepted-child-failed", "accepted-child-pending"}

func TestSwitchingProfilePublicTaskRecovery(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, mode := range switchingProfileRecoveryModes {
			t.Run(fmt.Sprintf("update-%t-%s", update, mode), func(t *testing.T) { exerciseSwitchingProfileRecovery(t, update, mode, nil) })
		}
	}
	for _, mode := range []string{"profile-replaced", "mid-tree-profile"} {
		t.Run(mode, func(t *testing.T) { exerciseSwitchingProfileRecovery(t, true, mode, nil) })
	}
}

func exerciseSwitchingProfileRecovery(t *testing.T, update bool, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	name := "P"
	if mode == "slash-name" {
		name = "P/Interior"
	}
	profile := map[string]any{"name": name, "dayn_templates": []string{"Project#T"}}
	if mode == "legacy-empty" {
		delete(profile, "dayn_templates")
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{"switching": []any{profile}}}})
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw = raw
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	actual := networkProfile{}
	projectID, templateID := "project", "template"
	inherited := `{"key":"site.settings","counter":9007199254740992,"attribs":[{"key":"private","value":"fixture-private-value"},{"key":"other","value":"two"}]}`
	if update {
		actual = networkProfile{ID: "profile", Name: name, Namespace: "switching", Attributes: []networkProfileAttribute{}}
		if mode == "legacy-empty" {
			actual.Attributes = []networkProfileAttribute{{Key: "cli.templates", Attributes: []networkProfileTemplate{{Key: "template.id", Value: "template"}}}}
		}
	}
	writes, treeReads, observed := 0, 0, false
	refusalInjected, refusalRetired := false, false
	var beforeRefusal *cvk.NetworkControllerConfigurationStatus
	stamp := time.Now().UnixMilli()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if mode == "preflight-read-error" && !refusalRetired && r.Method == http.MethodGet && r.URL.Path == networkProfilesPath {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if state := nc.Status.Configuration; state != nil && state.Operation != nil && state.Operation.Phase == "Submitting" {
				refusalInjected = true
				beforeRefusal = state.DeepCopy()
				w.WriteHeader(503)
				return
			}
		}
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == templateProjectsPath:
			json.NewEncoder(w).Encode([]templateProject{{ID: projectID, Name: "Project"}})
		case r.URL.Path == templatesPath:
			json.NewEncoder(w).Encode([]templateSummary{{ID: templateID, ProjectID: projectID, Name: "T"}})
		case r.URL.Path == sitesPath:
			reply(sampleSites())
		case (r.Method == "POST" && r.URL.Path == networkProfilesPath) || (r.Method == "PUT" && r.URL.Path == networkProfilesPath+"/profile"):
			if update != (r.Method == "PUT") {
				t.Error("unexpected profile mutation action")
			}
			if json.NewDecoder(r.Body).Decode(&actual) != nil {
				t.Error("invalid profile payload")
			}
			actual.ID = "profile"
			if !update {
				actual.ID = switchingResultUUID
			}
			writes++
			stamp = time.Now().UnixMilli()
			reply(map[string]any{"taskId": "profile-task"})
		case r.URL.Path == networkProfilesPath:
			rows := []networkProfile{}
			if actual.ID != "" {
				rows = append(rows, actual)
			}
			reply(rows)
		case actual.ID != "" && r.URL.Path == networkProfilesPath+"/"+actual.ID:
			p := actual
			p.Attributes = nil
			for _, a := range actual.Attributes {
				if a.Key != "site.settings" {
					p.Attributes = append(p.Attributes, a)
				}
			}
			var attr networkProfileAttribute
			if json.Unmarshal([]byte(inherited), &attr) != nil {
				t.Error("invalid private fixture")
			}
			p.Attributes = append(p.Attributes, attr)
			reply(p)
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			if id != "profile-task" {
				t.Error("foreign task read")
			}
			if strings.HasSuffix(r.URL.Path, "/tree") {
				treeReads++
				if mode == "tree-missing" {
					w.WriteHeader(404)
					return
				}
			}
			root := map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false}
			if !update {
				root["serviceType"] = "Site Profile Service"
				root["progress"] = "Network Profile [" + switchingResultUUID + "] Successfully Created"
			}
			if mode == "foreign-root" {
				root["id"] = "other-task"
			}
			children := []map[string]any{}
			if strings.HasSuffix(mode, "child-failed") || strings.HasSuffix(mode, "child-pending") {
				child := map[string]any{"id": "child", "rootId": id, "parentId": id, "isError": strings.HasSuffix(mode, "child-failed"), "startTime": stamp, "endTime": stamp}
				if strings.HasSuffix(mode, "child-pending") {
					child["endTime"] = 0
				}
				children = append(children, child)
			}
			writeDefinitionTaskFixture(w, r, root, children...)
			if strings.HasSuffix(r.URL.Path, "/tree") {
				if mode == "mid-tree-inherited" {
					inherited = strings.Replace(inherited, "9007199254740992", "9007199254740993", 1)
					if treeReads%2 == 0 {
						inherited = strings.Replace(inherited, "9007199254740993", "9007199254740992", 1)
					}
				}
				if mode == "inherited-reordered" {
					inherited = `{"key":"site.settings","attribs":[{"key":"other","value":"two"},{"key":"private","value":"fixture-private-value"}],"counter":9007199254740992}`
				}
				if mode == "mid-tree-profile" {
					actual.ID = "replacement"
				}
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	a.client = c
	key := ctrlclient.ObjectKeyFromObject(config)
	accepted := strings.HasPrefix(mode, "accepted-")
	positive := mode == "preflight-read-error" || mode == "known" || mode == "slash-name" || mode == "legacy-empty" || mode == "inherited-reordered"
	for range 24 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if refusalInjected && !refusalRetired {
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			for _, condition := range config.Status.Conditions {
				if condition.Reason == "NativePreflightRefused" {
					if state.Operation != nil || writes != 0 || !reflect.DeepEqual(state.Owners, beforeRefusal.Owners) || !reflect.DeepEqual(state.ActionApplications, beforeRefusal.ActionApplications) {
						t.Fatal("preflight retirement changed custody or dispatched")
					}
					refusalRetired = true
				}
			}
		}
		if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !observed {
			observed = true
			if !strings.HasPrefix(state.Operation.Resource, "profiles/") || !validSwitchingProfileBinding(state.Operation.ParentID) {
				t.Fatal("original project binding missing")
			}
			if !accepted {
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "legacy-empty":
				nc.Status.Configuration.Operation.Resource = "profiles/" + name
				nc.Status.Configuration.Operation.ParentID = profileDependencyIdentity(networkProfile{})
			case "legacy-unbound":
				nc.Status.Configuration.Operation.Resource = "profiles/" + name
				nc.Status.Configuration.Operation.ParentID = strings.Repeat("a", 64)
			case "taskless":
				nc.Status.Configuration.Operation.TaskID = ""
			case "owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "profile-replaced":
				actual.ID = "replacement"
			case "project-replaced":
				projectID = "replacement-project"
			case "template-replaced":
				templateID = "replacement-template"
			case "source":
				if err := a.statusReader.Get(ctx, key, config); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(raw), name, "changed-profile", 1))
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(nc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "fixture-private-value") {
			t.Fatal("private inherited values entered journal")
		}
		var restored cvk.NetworkController
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Status().Update(ctx, &restored); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusReader.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if mode == "preflight-read-error" && (!refusalInjected || !refusalRetired) {
		t.Fatal("automatic preflight replan missing")
	}
	if !observed || writes != 1 {
		t.Fatal("original operation missing or replayed", writes)
	}
	if positive {
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil || treeReads == 0 {
			t.Fatal("original profile recovery did not converge", config.Status.Conditions)
		}
	} else {
		phase := "Unknown"
		if accepted {
			phase = "Accepted"
		}
		if state.Operation == nil || state.Operation.Phase != phase || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("uncertain profile operation released", config.Status.Conditions)
		}
		if (mode == "taskless" || mode == "legacy-unbound" || mode == "source" || mode == "owner" || mode == "project-replaced" || mode == "template-replaced" || mode == "profile-replaced") && treeReads != 0 {
			t.Fatal("unbound task tree read")
		}
	}
	expectedCreators := 0
	if !update && positive && mode != "legacy-empty" {
		expectedCreators = 1
	}
	if len(state.ActionApplications) != expectedCreators {
		t.Fatal("unexpected creator receipt count", len(state.ActionApplications), expectedCreators)
	}
	if expectedCreators == 1 {
		id, err := switchingCreatedTarget(state.ActionApplications[0])
		if err != nil || id != switchingResultUUID {
			t.Fatal("original Create result missing from receipt", err)
		}
	}
}
