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

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFloorImageExecutorLifecycle(t *testing.T) {
	for _, mode := range []string{"complete", "legacy-complete", "failed", "running", "wrong-task", "replaced-floor", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte(strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"floor.pdf"`, 1))
			_, _, desired, err := parseSitesWithFloorImages(raw, map[string][]byte{"floor.pdf": []byte("%PDF-1.7\nfixture")})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
			floorID := "floor"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					sites := append(sampleSites(), Site{ID: floorID, Name: "Floor", NameHierarchy: desired[0].floor, ParentID: "building", Type: "floor"})
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case "/dna/intent/api/v2/floors/floor/uploadImage":
					writes++
					if r.Method != "POST" {
						t.Error("wrong upload method")
					}
					fmt.Fprint(w, `{"response":{"taskId":"image-task"}}`)
				case taskPath + "image-task":
					task := map[string]any{"id": "image-task", "startTime": start.UnixMilli(), "endTime": start.Add(time.Second).UnixMilli(), "isError": false}
					switch mode {
					case "failed":
						task["isError"] = true
					case "running":
						delete(task, "endTime")
					case "wrong-task":
						task["id"] = "other"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": task})
				default:
					t.Error("unexpected API request")
					w.WriteHeader(404)
				}
			})
			e := &floorImageExecutor{client: c, desired: desired, configName: "model", configUID: "owner"}
			next, err := e.next(context.Background())
			if err != nil || next == nil || next.identity.target != "floor" || next.resource != desired[0].operationResource() {
				t.Fatal("no upload planned", err)
			}
			task, err := next.submit(context.Background())
			if err != nil || task.ID != "image-task" || writes != 1 {
				t.Fatal("upload receipt missing", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: next.resource, Action: next.action, Phase: "Verifying", ConfigName: "model", ConfigUID: "owner", TargetID: "floor", TaskID: task.ID, SubmittedAt: &metav1.Time{Time: start}}
			if mode == "legacy-complete" {
				op.Resource = desired[0].legacyOperationResource()
			}
			if mode == "replaced-floor" {
				floorID = "replacement"
			}
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			done, err := e.uploadCompleted(context.Background(), op)
			complete := mode == "complete" || mode == "legacy-complete"
			if done != complete || (err == nil) != (complete || mode == "running") {
				t.Fatalf("done=%v err=%v", done, err)
			}
			if complete {
				op.Phase = "Verified"
				verified := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				receipts := []cvk.NetworkControllerConfigurationActionApplication{{Section: "sites", Resource: desired[0].resource(), Operation: *op.DeepCopy(), VerifiedAt: &verified}}
				// A new executor sees retained completion and does not upload again.
				rebuilt := &floorImageExecutor{client: c, desired: desired, configName: "model", configUID: "owner", receipts: receipts}
				next, err = rebuilt.next(context.Background())
				if err != nil || next != nil {
					t.Fatal("completed upload replayed", err)
				}
				// Corrupting the payload without replacing its digest is rejected.
				desired[0].content[len(desired[0].content)-1] = 'X'
				if next, err = rebuilt.next(context.Background()); err == nil || next != nil {
					t.Fatal("payload digest mismatch accepted")
				}
			}
			if writes != 1 {
				t.Fatal("verification replayed upload")
			}
		})
	}
}
