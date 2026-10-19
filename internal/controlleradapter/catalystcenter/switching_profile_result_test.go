// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const switchingResultUUID = "4ad1552c-78b7-4c01-bb88-7a8b15e78352"

func TestSwitchingProfileCreateResultGrammar(t *testing.T) {
	now := time.Now()
	submitted := now.Add(-time.Second)
	for _, mode := range []string{"known", "wrong-service", "wrong-task", "wrong-action", "leading", "trailing", "newline", "upper-uuid", "nil-uuid", "braced-uuid", "extra-uuid", "json-progress", "object-progress", "missing-progress", "missing-start", "missing-error", "stale", "future-end", "pending", "failed", "invalid-task", "future-submission"} {
		t.Run(mode, func(t *testing.T) {
			progress := "Network Profile [" + switchingResultUUID + "] Successfully Created"
			task, when := "task", submitted
			body := map[string]any{"id": task, "serviceType": "Site Profile Service", "progress": progress, "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}
			switch mode {
			case "wrong-service":
				body["serviceType"] = "NCIP"
			case "wrong-task":
				body["id"] = "other"
			case "wrong-action":
				body["progress"] = strings.Replace(progress, "Created", "Updated", 1)
			case "leading":
				body["progress"] = " " + progress
			case "trailing":
				body["progress"] = progress + " OTHER"
			case "newline":
				body["progress"] = progress + "\n"
			case "upper-uuid":
				body["progress"] = strings.Replace(progress, switchingResultUUID, strings.ToUpper(switchingResultUUID), 1)
			case "nil-uuid":
				body["progress"] = "Network Profile [00000000-0000-0000-0000-000000000000] Successfully Created"
			case "braced-uuid":
				body["progress"] = "Network Profile [{" + switchingResultUUID + "}] Successfully Created"
			case "extra-uuid":
				body["progress"] = "Network Profile [" + switchingResultUUID + " " + switchingResultUUID + "] Successfully Created"
			case "json-progress":
				body["progress"] = `{"id":"` + switchingResultUUID + `"}`
			case "object-progress":
				body["progress"] = map[string]string{"id": switchingResultUUID}
			case "missing-progress":
				delete(body, "progress")
			case "missing-start":
				delete(body, "startTime")
			case "missing-error":
				delete(body, "isError")
			case "stale":
				body["startTime"] = submitted.Add(-time.Hour).UnixMilli()
			case "future-end":
				body["endTime"] = now.Add(time.Hour).UnixMilli()
			case "pending":
				body["endTime"] = 0
			case "failed":
				body["isError"] = true
			case "invalid-task":
				task = "../task"
			case "future-submission":
				when = now.Add(time.Hour)
			}
			raw, _ := json.Marshal(map[string]any{"response": body})
			id, done, err := decodeSwitchingProfileCreateResult(raw, task, when, now)
			if mode == "known" {
				if err != nil || !done || id != switchingResultUUID {
					t.Fatal("known result refused", id, done, err)
				}
			} else if mode == "pending" {
				if err != nil || done || id != "" {
					t.Fatal("pending result inferred")
				}
			} else if err == nil || done || id != "" {
				t.Fatal("unbound result accepted")
			}
		})
	}
}

func TestSwitchingProfileCreatedResultNativeBinding(t *testing.T) {
	for _, mode := range []string{"known", "missing", "replacement", "wrong-name", "wrong-namespace", "drift", "project-replaced", "template-replaced", "child-failed", "child-pending", "tree-missing", "task-changed", "native-replaced-between", "inherited-changed", "inherited-reordered", "update", "target", "credentials", "physical", "taskless", "undeclared", "wrong-owner-envelope"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			submitted := metav1.NewTime(now.Add(-time.Second))
			project, template, id, name, namespace := "project", "template", switchingResultUUID, "Lab", "switching"
			if mode == "missing" {
				id = ""
			}
			if mode == "replacement" {
				id = "7ecf67b6-e218-441e-abbe-99603d7de91c"
			}
			if mode == "wrong-name" {
				name = "Other"
			}
			if mode == "wrong-namespace" {
				namespace = "wireless"
			}
			taskReads, reads, mutations := 0, 0, 0
			inherited := `{"key":"site.settings","counter":9007199254740992,"attribs":[{"key":"a","value":"fixture-sensitive"},{"key":"b","value":"two"}]}`
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" && r.URL.Path != authPath {
					mutations++
					w.WriteHeader(500)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch {
				case r.URL.Path == authPath:
					w.Write([]byte(`{"Token":"test"}`))
				case r.URL.Path == templateProjectsPath:
					json.NewEncoder(w).Encode([]templateProject{{ID: project, Name: "Project"}})
				case r.URL.Path == templatesPath:
					json.NewEncoder(w).Encode([]templateSummary{{ID: template, ProjectID: project, Name: "T"}})
				case r.URL.Path == networkProfilesPath:
					rows := []networkProfile{}
					if id != "" {
						rows = append(rows, networkProfile{ID: id, Name: name, Namespace: namespace})
					}
					reply(rows)
				case id != "" && r.URL.Path == networkProfilesPath+"/"+id:
					reads++
					var attr networkProfileAttribute
					if json.Unmarshal([]byte(inherited), &attr) != nil {
						t.Error("bad private fixture")
					}
					membership := "template"
					if mode == "drift" {
						membership = "other-template"
					}
					reply(networkProfile{ID: id, Name: name, Namespace: namespace, Attributes: []networkProfileAttribute{{Key: "cli.templates", Attributes: []networkProfileTemplate{{Key: "template.id", Value: membership}}}, attr}})
				case strings.HasPrefix(r.URL.Path, taskPath):
					tree := strings.HasSuffix(r.URL.Path, "/tree")
					if tree && mode == "tree-missing" {
						w.WriteHeader(404)
						return
					}
					if !tree {
						taskReads++
					}
					result := switchingResultUUID
					if taskReads == 2 && mode == "task-changed" {
						result = "7ecf67b6-e218-441e-abbe-99603d7de91c"
					}
					root := map[string]any{"id": "task", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "Site Profile Service", "progress": "Network Profile [" + result + "] Successfully Created"}
					children := []map[string]any{}
					if mode == "child-failed" || mode == "child-pending" {
						child := map[string]any{"id": "child", "rootId": "task", "parentId": "task", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": mode == "child-failed"}
						if mode == "child-pending" {
							child["endTime"] = 0
						}
						children = append(children, child)
					}
					writeDefinitionTaskFixture(w, r, root, children...)
					if !tree && taskReads == 2 {
						if mode == "native-replaced-between" {
							id = "7ecf67b6-e218-441e-abbe-99603d7de91c"
						}
						if mode == "inherited-changed" {
							inherited = strings.Replace(inherited, "9007199254740992", "9007199254740993", 1)
						}
						if mode == "inherited-reordered" {
							inherited = `{"attribs":[{"key":"b","value":"two"},{"key":"a","value":"fixture-sensitive"}],"counter":9007199254740992,"key":"site.settings"}`
						}
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &networkProfileConfiguration{}
			wants := []switchingProfileIntent{{name: "Lab", dayn: []string{"Project#T"}}}
			executor := &switchingProfileExecutor{client: c, desired: wants}
			e.parts = []configurationPart{{name: "profiles", executor: executor}}
			desired, err := resolvedSwitchingProfile(wants[0], map[string]templateProject{"Project": {ID: "project", Name: "Project"}}, map[string]templateSummary{"project#T": {ID: "template", ProjectID: "project", Name: "T"}})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := boundSwitchingProfileDependency(wants[0], desired, map[string]templateProject{"Project": {ID: "project", Name: "Project"}}, map[string]templateSummary{"project#T": {ID: "template", ProjectID: "project", Name: "T"}})
			if err != nil {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "network_profiles", Resource: "profiles/Lab", Action: "Create", Phase: "Verifying", ParentID: binding, TaskID: "task", SubmittedAt: &submitted, ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1}
			switch mode {
			case "project-replaced":
				project = "new-project"
			case "template-replaced":
				template = "new-template"
			case "update":
				op.Action = "Update"
				op.TargetID = id
			case "target":
				op.TargetID = id
			case "credentials":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{Path: "secret", Version: 1}}
			case "physical":
				op.TargetSerial = "SERIAL"
			case "taskless":
				op.TaskID = ""
			case "undeclared":
				op.Resource = "profiles/Other"
			case "wrong-owner-envelope":
				op.ConfigUID = ""
			}
			before := op.DeepCopy()
			got, done, err := e.verifySwitchingProfileCreatedResult(ctx, op, now)
			positive := mode == "known" || mode == "inherited-reordered"
			if positive {
				if err != nil || !done || got != switchingResultUUID {
					t.Fatal("known native result refused", got, done, err)
				}
			} else if done || got != "" {
				t.Fatal("uncorrelated native result accepted")
			}
			a, _ := json.Marshal(op)
			b, _ := json.Marshal(before)
			if string(a) != string(b) || mutations != 0 {
				t.Fatal("read-only verifier changed operation or dispatched")
			}
			if err != nil && strings.Contains(err.Error(), "fixture-sensitive") {
				t.Fatal("private native value exposed")
			}
			if positive && reads < 2 {
				t.Fatal("native result lacked stable repeated readback")
			}
		})
	}
}
