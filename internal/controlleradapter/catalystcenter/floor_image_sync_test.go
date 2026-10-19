// Copyright © 2026 Cisco Systems Inc.
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

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFloorImageSynchronousUploadReceipt(t *testing.T) {
	for _, mode := range []string{"complete", "group-complete", "wrong-group", "wrong-floor", "opaque-hierarchy", "missing-image", "task-envelope", "wrong-status", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				writes++
				if r.Method != http.MethodPost || r.URL.Path != "/dna/intent/api/v2/floors/floor/uploadImage" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
					t.Error("wrong upload transport")
				}
				status := http.StatusCreated
				if mode == "wrong-status" {
					status = http.StatusOK
				}
				if mode == "unauthorized" {
					status = http.StatusUnauthorized
				}
				w.WriteHeader(status)
				body := `{"groupInstanceUuid":"floor","hierarchyName":"Global/Lab/Building/Floor","imageInfo":{"imageName":"floor.pdf"}}`
				if mode == "group-complete" {
					body = `{"groupInstanceUuid":"floor","hierarchyName":null,"imageInfo":{"imageName":"floor.pdf"}}`
				}
				if mode == "wrong-group" {
					body = `{"groupInstanceUuid":"other","imageInfo":{"imageName":"floor.pdf"}}`
				}
				if mode == "wrong-floor" {
					body = strings.Replace(body, `"floor"`, `"other"`, 1)
				}
				if mode == "opaque-hierarchy" {
					body = strings.Replace(body, "Building/Floor", "Building/Other", 1)
				}
				if mode == "missing-image" {
					body = `{"instanceUuid":"floor","hierarchyName":"Global/Lab/Building/Floor","imageInfo":{}}`
				}
				if mode == "task-envelope" {
					body = `{"response":{"taskId":"task"}}`
				}
				fmt.Fprint(w, body)
			})
			err := c.uploadFloorImageCompleted(context.Background(), "floor", "Global/Lab/Building/Floor", "floor.pdf", []byte("%PDF-1.7\nfixture"))
			if (err == nil) != (mode == "complete" || mode == "group-complete" || mode == "opaque-hierarchy") || writes != 1 {
				t.Fatal("bad synchronous confirmation or replay", mode, err)
			}
		})
	}
}

func TestFloorImageSynchronousCompletionRejectsMalformedJournal(t *testing.T) {
	for _, mode := range []string{"missing-time", "zero-time", "future-time", "created-id", "group", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			_, _, images, err := parseSitesWithFloorImages([]byte(floorImageReportModel), map[string][]byte{"maps/floor.pdf": []byte("%PDF-1.7\nfixture")})
			if err != nil {
				t.Fatal(err)
			}
			at := metav1.NewTime(time.Now().Add(-time.Minute))
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: images[0].synchronousOperationResource(), Phase: "Verifying", Action: "Update", ConfigName: "model", ConfigUID: "owner", TargetID: "floor", SubmittedAt: &at}
			switch mode {
			case "missing-time":
				op.SubmittedAt = nil
			case "zero-time":
				op.SubmittedAt = &metav1.Time{}
			case "future-time":
				future := metav1.NewTime(time.Now().Add(time.Hour))
				op.SubmittedAt = &future
			case "created-id":
				op.CreatedResourceID = "other"
			case "group":
				op.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
			case "cancel":
				op.CancellationRequestedAt = &at
			}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reads++
				t.Error("malformed journal queried native API")
				w.WriteHeader(500)
			})
			e := &floorImageExecutor{client: c, desired: images, configName: "model", configUID: "owner", synchronous: true}
			if done, err := e.uploadCompleted(context.Background(), op); done || err == nil || reads != 0 {
				t.Fatal("malformed synchronous evidence accepted", mode, err)
			}
		})
	}
}

func TestFloorImageSynchronousExecutorRefusesReplacedFloor(t *testing.T) {
	ctx := context.Background()
	_, _, images, err := parseSitesWithFloorImages([]byte(floorImageReportModel), map[string][]byte{"maps/floor.pdf": []byte("%PDF-1.7\nfixture")})
	if err != nil {
		t.Fatal(err)
	}
	floorID := "floor"
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != sitesPath {
			writes++
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), Site{ID: floorID, Name: "Floor", NameHierarchy: images[0].floor, ParentID: "building", Type: "floor"})})
	})
	e := &floorImageExecutor{client: c, desired: images, configName: "model", configUID: "owner", synchronous: true}
	write, err := e.next(ctx)
	if err != nil || write == nil || write.submitCompleted == nil {
		t.Fatal("synchronous plan", err)
	}
	floorID = "replacement"
	if err := write.submitCompleted(ctx); err == nil || writes != 0 {
		t.Fatal("stale floor identity received image")
	}
}

func TestFloorImageResponseKindCannotInferPrunedTask(t *testing.T) {
	for _, mode := range []string{"legacy-task", "legacy-pruned", "sync", "sync-task"} {
		resource := "floor-image:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
		task := "task"
		if strings.HasPrefix(mode, "sync") {
			resource = strings.Replace(resource, "floor-image:", "floor-image-sync:", 1)
			task = ""
		}
		if mode == "legacy-pruned" {
			task = ""
		}
		if mode == "sync-task" {
			task = "task"
		}
		op := &cvk.NetworkControllerConfigurationOperation{Resource: resource, TaskID: task}
		if floorImageCompletionKindMatches(op) != (mode == "legacy-task" || mode == "sync") {
			t.Fatal("response kind inferred from missing or foreign task", mode)
		}
		key, err := floorImageReceiptKey(resource)
		if err != nil || key != "floor-image:"+strings.Repeat("a", 64) {
			t.Fatal("completion dialect changed stable floor ownership")
		}
	}
}
