// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const featureResultUUID = "b81aaf99-cdcd-4932-8148-49244eff1c46"

func TestWirelessFeatureTaskResultBinding(t *testing.T) {
	now := time.Now()
	submitted := now.Add(-time.Second)
	for _, action := range []string{"Create", "Update", "Delete"} {
		for _, mode := range []string{"known", "missing-data", "object-data", "opaque-data", "upper-data", "nil-data", "wrong-service", "wrong-name", "wrong-action", "wrong-target", "wrong-task", "stale-time", "failed", "pending", "future-reference", "invalid-name", "missing-target"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ref := wirelessFeatureTaskReference{ID: "task-id", Name: "CVK_TEST", Action: action, Submitted: submitted}
				if action != "Create" {
					ref.Target = featureResultUUID
				}
				verb := map[string]string{"Create": "created", "Update": "updated", "Delete": "deleted"}[action]
				progress := "Successfully " + verb + " model config design with name CVK_TEST"
				if action == "Delete" {
					progress = "Successfully deleted model config design with id " + featureResultUUID
				}
				body := map[string]any{"id": "task-id", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "NCNC", "progress": progress, "data": featureResultUUID}
				switch mode {
				case "missing-data":
					delete(body, "data")
				case "object-data":
					body["data"] = map[string]string{"id": featureResultUUID}
				case "opaque-data":
					body["data"] = "created " + featureResultUUID
				case "upper-data":
					body["data"] = strings.ToUpper(featureResultUUID)
				case "nil-data":
					body["data"] = "00000000-0000-0000-0000-000000000000"
				case "wrong-service":
					body["serviceType"] = "NCIP"
				case "wrong-name":
					body["progress"] = progress + " OTHER"
				case "wrong-action":
					body["progress"] = "Successfully deleted model config design with id " + featureResultUUID
					if action == "Delete" {
						body["progress"] = "Successfully created model config design with name CVK_TEST"
					}
				case "wrong-target":
					if action == "Create" {
						ref.Target = featureResultUUID
					} else {
						ref.Target = "c01658ad-8573-4368-b91a-71aa2d666239"
					}
				case "wrong-task":
					body["id"] = "other-task"
				case "stale-time":
					body["startTime"] = submitted.Add(-time.Hour).UnixMilli()
				case "failed":
					body["isError"] = true
				case "pending":
					body["endTime"] = 0
				case "future-reference":
					ref.Submitted = now.Add(time.Hour)
				case "invalid-name":
					ref.Name = "bad/name"
				case "missing-target":
					if action == "Create" {
						ref.Action = "Delete"
					} else {
						ref.Target = ""
					}
				}
				raw, err := json.Marshal(map[string]any{"response": body})
				if err != nil {
					t.Fatal(err)
				}
				id, done, err := decodeWirelessFeatureTaskResult(raw, ref, now)
				if mode == "known" {
					if err != nil || !done || id != featureResultUUID {
						t.Fatal("qualified result refused", id, done, err)
					}
				} else if mode == "pending" {
					if err != nil || done || id != "" {
						t.Fatal("pending result inferred", id, done, err)
					}
				} else if err == nil || done || id != "" {
					t.Fatal("unbound result accepted", id, done, err)
				}
			})
		}
	}
}

func TestWirelessFeatureCreatedResultRequiresNativeUUIDAndFields(t *testing.T) {
	for _, mode := range []string{"known", "missing", "replacement", "system", "drift", "other-kind"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			submitted := now.Add(-time.Second)
			ref := wirelessFeatureTaskReference{ID: "create-task", Name: "CVK_TEST", Action: "Create", Submitted: submitted}
			want := wirelessFRA{Name: ref.Name, Attributes: wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"}}
			actual := want
			actual.ID = featureResultUUID
			if mode == "replacement" {
				actual.ID = "c01658ad-8573-4368-b91a-71aa2d666239"
			}
			if mode == "drift" {
				actual.Attributes.Interval = 6
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet && r.URL.Path != authPath {
					t.Error("result verifier mutated native state")
				}
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case taskPath + ref.ID:
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": ref.ID, "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "NCNC", "progress": "Successfully created model config design with name CVK_TEST", "data": featureResultUUID}})
				case wirelessFeatureSummaryPath:
					rows := []wirelessFeatureInstance{}
					system := mode == "system"
					if mode != "missing" && mode != "other-kind" {
						rows = append(rows, wirelessFeatureInstance{ID: actual.ID, Name: actual.Name, System: &system})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": wirelessFRAKind, "count": len(rows), "instances": rows}}})
				case wirelessFRAPath + "/" + actual.ID:
					json.NewEncoder(w).Encode(map[string]any{"response": actual})
				default:
					t.Errorf("unexpected result GET %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			id, done, err := verifyWirelessFeatureCreatedResult(context.Background(), c, wirelessFRAKind, ref, want, c.wirelessFRARead, wirelessFRAMatches, now)
			if mode == "known" {
				if err != nil || !done || id != featureResultUUID {
					t.Fatal("created result refused", id, done, err)
				}
			} else if done || id != "" {
				t.Fatal("unproven create adopted", id, done, err)
			}
		})
	}
}
