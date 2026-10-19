// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPnPWorkflowReferenceInventory(t *testing.T) {
	for _, mode := range []string{"empty", "one", "paged", "repeat", "short-count", "changed-count", "missing-count", "null-count", "fractional-count", "negative-count", "too-large-count", "native-id", "equal-ids", "conflicting-ids", "bad-task-config", "bad-id", "bad-config", "private-values", "unavailable", "enveloped-list"} {
		t.Run(mode, func(t *testing.T) {
			var counts atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("inventory attempted a mutation")
				}
				if r.URL.Path == pnpWorkflowsPath+"/count" {
					seq := counts.Add(1)
					switch mode {
					case "missing-count":
						fmt.Fprint(w, `{}`)
						return
					case "null-count":
						fmt.Fprint(w, `{"response":null}`)
						return
					case "fractional-count":
						fmt.Fprint(w, `{"response":0.5}`)
						return
					}
					n := 1
					switch mode {
					case "empty":
						n = 0
					case "paged", "repeat":
						n = 501
					case "negative-count":
						n = -1
					case "too-large-count":
						n = 10001
					case "changed-count":
						if seq > 1 {
							n = 2
						}
					}
					fmt.Fprintf(w, `{"response":%d}`, n)
					return
				}
				if r.URL.Path != pnpWorkflowsPath || r.URL.Query().Get("limit") != "500" {
					t.Error("wrong workflow paging boundary")
					w.WriteHeader(400)
					return
				}
				if mode == "unavailable" {
					w.WriteHeader(503)
					return
				}
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				if offset != 0 && offset != 500 {
					t.Error("wrong zero-based workflow offset")
				}
				rows := []map[string]any{{"_id": "workflow", "configId": "target", "name": "private workflow name", "tasks": []any{map[string]any{"name": "private task", "configInfo": map[string]any{"configId": "target", "configFileUrl": "private URL", "configParameters": []any{map[string]any{"key": "private key", "value": "private value"}}}, "workItemList": []any{map[string]any{"command": "private command", "outputStr": "private output"}}}}}}
				if mode == "empty" || mode == "short-count" {
					rows = []map[string]any{}
				}
				if mode == "paged" || mode == "repeat" {
					rows = []map[string]any{}
					end := offset + 500
					if end > 501 {
						end = 501
					}
					if mode == "repeat" && offset > 0 {
						offset, end = 0, 500
					}
					for i := offset; i < end; i++ {
						rows = append(rows, map[string]any{"_id": fmt.Sprintf("workflow-%d", i)})
					}
				}
				switch mode {
				case "native-id":
					delete(rows[0], "_id")
					rows[0]["id"] = "workflow"
				case "equal-ids":
					rows[0]["id"] = "workflow"
				case "conflicting-ids":
					rows[0]["id"] = "other"
				case "bad-task-config":
					rows[0]["tasks"] = []any{map[string]any{"configInfo": map[string]any{"configId": "../foreign"}}}
				}
				if mode == "bad-id" {
					rows[0]["_id"] = "../foreign"
				}
				if mode == "bad-config" {
					rows[0]["configId"] = "../foreign"
				}
				if mode == "enveloped-list" {
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				json.NewEncoder(w).Encode(rows)
			})
			got, err := c.pnpWorkflows(context.Background())
			good := mode == "empty" || mode == "one" || mode == "paged" || mode == "private-values" || mode == "native-id" || mode == "equal-ids"
			if good {
				want := 1
				if mode == "empty" {
					want = 0
				}
				if mode == "paged" {
					want = 501
				}
				if err != nil || len(got) != want {
					t.Fatal("complete workflow inventory failed", err, len(got))
				}
				raw, _ := json.Marshal(got)
				if strings.Contains(string(raw), "private") {
					t.Fatal("private workflow values survived safe projection")
				}
			} else if err == nil || got != nil {
				t.Fatal("incomplete workflow inventory returned a usable view")
			}
		})
	}
}

func TestTemplatePnPReferences(t *testing.T) {
	for _, mode := range []string{"library-task", "library-task-version", "library-task-duplicate", "library-task-two-aliases", "main-task", "system-task", "reset-task", "task-private-only", "late-task", "bad-task-config", "empty", "library-base", "library-version", "main", "system", "reset", "all-instances", "parameters", "parameters-version", "parameters-duplicate", "parameters-missing-id", "parameters-private-only", "late-parameters", "completed", "unrelated-private-text", "library-link", "dangling-link", "partial-inline-link", "missing-instance-id", "bad-instance-config", "late-library", "late-instance", "device-count-change", "workflow-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			var pages, deviceCounts atomic.Int32
			hasLibrary := strings.HasPrefix(mode, "library-task") || mode == "library-base" || mode == "library-version" || mode == "library-link" || mode == "late-library"
			hasDevice := !strings.HasPrefix(mode, "library-task") && mode != "empty" && mode != "library-base" && mode != "library-version" && mode != "late-library" && mode != "workflow-unavailable"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("reference reader attempted a mutation")
				}
				switch r.URL.Path {
				case templatesPath + "/target", templatesPath + "/target-v1":
					id := "target"
					if strings.HasSuffix(r.URL.Path, "-v1") {
						id = "target-v1"
					}
					body := map[string]any{"id": id, "name": "target", "projectId": "project", "composite": false, "templateContent": "private template"}
					if id != "target" {
						body["version"] = "1"
					}
					json.NewEncoder(w).Encode(body)
				case templateVersionsPath + "/target":
					fmt.Fprint(w, `[{"templateId":"target","versionsInfo":[{"id":"target"},{"id":"target-v1","version":"1"}]}]`)
				case pnpWorkflowsPath + "/count":
					n := 0
					if hasLibrary {
						n = 1
					}
					fmt.Fprintf(w, `{"response":%d}`, n)
				case pnpWorkflowsPath:
					iteration := pages.Add(1)
					rows := []map[string]any{}
					if mode == "workflow-unavailable" {
						w.WriteHeader(503)
						return
					}
					if hasLibrary {
						config := "target"
						if mode == "library-version" {
							config = "target-v1"
						}
						if mode == "late-library" && iteration == 1 {
							config = "unrelated"
						}
						flow := map[string]any{"id": "library", "configId": config}
						if strings.HasPrefix(mode, "library-task") {
							delete(flow, "configId")
							if mode == "library-task-version" {
								config = "target-v1"
							}
							task := map[string]any{"configInfo": map[string]any{"configId": config, "configParameters": []any{map[string]any{"key": "private", "value": "private secret"}}}}
							tasks := []any{task}
							if mode == "library-task-duplicate" {
								tasks = append(tasks, task)
								flow["configId"] = config
							}
							if mode == "library-task-two-aliases" {
								tasks = append(tasks, map[string]any{"configInfo": map[string]any{"configId": "target-v1"}})
							}
							flow["tasks"] = tasks
						}
						rows = append(rows, flow)
					}
					json.NewEncoder(w).Encode(rows)
				case pnpDevicesPath + "/count":
					seq := deviceCounts.Add(1)
					n := 0
					if hasDevice {
						n = 1
					}
					if mode == "device-count-change" && seq == 2 {
						n = 2
					}
					fmt.Fprintf(w, `{"response":%d}`, n)
				case pnpDevicesPath:
					rows := []map[string]any{}
					if hasDevice {
						row := pnpFixture("device", "FOC12345678")
						row["workflow"] = nil
						flow := map[string]any{"_id": "instance", "configId": "target", "state": "Completed", "tasks": []any{map[string]any{"workItemList": []any{map[string]any{"command": "private command target-v1", "outputStr": "private output"}}}}}
						switch mode {
						case "parameters", "parameters-version", "parameters-duplicate", "parameters-missing-id", "parameters-private-only", "late-parameters":
							configID := "target"
							if mode == "parameters-version" {
								configID = "target-v1"
							}
							if mode == "parameters-private-only" || mode == "late-parameters" && pages.Load() == 1 {
								configID = "unrelated"
							}
							entry := map[string]any{"configId": configID, "configParameters": []any{map[string]any{"key": "password", "value": "private secret target-v1"}}}
							configs := []any{entry}
							if mode == "parameters-duplicate" {
								configs = append(configs, entry)
							}
							if mode == "parameters-missing-id" {
								delete(entry, "configId")
							}
							row["workflowParameters"] = map[string]any{"configList": configs, "topOfStackSerialNumber": "private stack identity", "licenseLevel": "private license"}
						case "main-task", "system-task", "reset-task", "task-private-only", "late-task", "bad-task-config":
							delete(flow, "configId")
							config := "target"
							if mode == "task-private-only" || mode == "late-task" && pages.Load() == 1 {
								config = "unrelated"
							}
							if mode == "bad-task-config" {
								config = "../foreign"
							}
							flow["tasks"] = []any{map[string]any{"configInfo": map[string]any{"configId": config, "configParameters": []any{map[string]any{"key": "private", "value": "private secret target"}}, "configFileUrl": "private URL"}}}
							key := "workflow"
							if mode == "system-task" {
								key = "systemWorkflow"
							}
							if mode == "reset-task" {
								key = "systemResetWorkflow"
							}
							row[key] = flow
						case "main", "completed", "device-count-change":
							row["workflow"] = flow
						case "system":
							row["systemWorkflow"] = flow
						case "reset":
							row["systemResetWorkflow"] = flow
						case "all-instances":
							row["workflow"], row["systemWorkflow"], row["systemResetWorkflow"] = flow, flow, flow
						case "unrelated-private-text":
							flow["configId"] = "unrelated"
							row["workflow"] = flow
						case "library-link":
							row["deviceInfo"].(map[string]any)["workflowId"] = "library"
						case "dangling-link":
							row["deviceInfo"].(map[string]any)["workflowId"] = "unknown"
						case "partial-inline-link":
							row["deviceInfo"].(map[string]any)["workflowId"] = "unknown"
							row["workflow"] = map[string]any{}
						case "missing-instance-id":
							delete(flow, "_id")
							row["workflow"] = flow
						case "bad-instance-config":
							flow["configId"] = "../foreign"
							row["workflow"] = flow
						case "late-instance":
							if pages.Load() == 1 {
								flow["configId"] = "unrelated"
							}
							row["workflow"] = flow
						}
						rows = append(rows, row)
					}
					json.NewEncoder(w).Encode(rows)
				default:
					t.Error("unexpected PnP reference request")
					w.WriteHeader(404)
				}
			})
			got, err := c.stableTemplatePnPReferences(context.Background(), templateSummary{ID: "target", Name: "target", ProjectID: "project"})
			positive := strings.HasPrefix(mode, "library-task") || mode == "main-task" || mode == "system-task" || mode == "reset-task" || mode == "task-private-only" || mode == "empty" || mode == "library-base" || mode == "library-version" || mode == "main" || mode == "system" || mode == "reset" || mode == "all-instances" || mode == "parameters" || mode == "parameters-version" || mode == "parameters-duplicate" || mode == "parameters-private-only" || mode == "completed" || mode == "unrelated-private-text" || mode == "library-link"
			if positive {
				want := 1
				if mode == "empty" || mode == "task-private-only" || mode == "unrelated-private-text" || mode == "parameters-private-only" {
					want = 0
				}
				if mode == "library-task-two-aliases" {
					want = 2
				}
				if mode == "all-instances" {
					want = 3
				}
				if err != nil || len(got) != want {
					t.Fatal("complete PnP references refused", err, len(got))
				}
				raw, _ := json.Marshal(got)
				for _, value := range []string{"private", "FOC12345678", "C9300-48P", "Completed"} {
					if strings.Contains(string(raw), value) {
						t.Fatal("private or unrelated inventory data entered reference output")
					}
				}
			} else if err == nil || got != nil {
				t.Fatal("uncertain or changing PnP view became a stable reference set")
			}
		})
	}
}
