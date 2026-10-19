// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFloorImageAtomicJournal(t *testing.T) {
	for _, mode := range []string{"complete", "synchronous-complete", "pruned", "failed-task", "running-task", "replaced-floor", "stale-source", "foreign-owner", "generic-bypass"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			cm := floorImageAssetFixture()
			cm.ObjectMeta = metav1.ObjectMeta{Name: "images", Namespace: config.Namespace, UID: "source"}
			cm.Data["model.yaml"] = strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"maps/floor.pdf"`, 1)
			if err := a.statusWriter.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline = nil
			config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			_, _, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
			if err != nil {
				t.Fatal(err)
			}
			identity := configurationResourceIdentity{target: "floor"}
			resource := images[0].operationResource()
			if mode == "synchronous-complete" {
				resource = images[0].synchronousOperationResource()
			}
			op, err := a.prepareConfiguration(ctx, config, source, "sites", resource, "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.startConfiguration(ctx, config, source, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			if mode == "synchronous-complete" {
				err = a.recordConfigurationCompleted(ctx, op.ID)
			} else {
				err = a.advanceConfiguration(ctx, op.ID, "Accepted", "image-task")
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode != "synchronous-complete" {
				err = a.advanceConfiguration(ctx, op.ID, "Verifying", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			start := nc.Status.Configuration.Operation.SubmittedAt.Time
			if mode == "foreign-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stale-source" {
				cm.BinaryData["floor-pdf"] = []byte("%PDF-1.7\nchanged")
				if err = a.statusWriter.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
			}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" && r.URL.Path != authPath {
					t.Error("receipt performed mutation")
				}
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					id := "floor"
					if mode == "replaced-floor" {
						id = "replacement"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), Site{ID: id, Name: "Floor", NameHierarchy: images[0].floor, Type: "floor", ParentID: "building"})})
				case taskPath + "image-task":
					task := map[string]any{"id": "image-task", "startTime": start.UnixMilli(), "endTime": time.Now().UnixMilli(), "isError": mode == "failed-task"}
					if mode == "running-task" {
						delete(task, "endTime")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": task})
				default:
					t.Error("unexpected request")
					w.WriteHeader(404)
				}
			})
			before := journalState(t, a).DeepCopy()
			if mode == "pruned" {
				a.statusWriter = pruningActionReceiptClient{Client: a.statusWriter}
			}
			if mode == "generic-bypass" {
				err = a.advanceConfiguration(ctx, op.ID, "Verified", "")
			} else {
				err = a.recordFloorImageApplication(ctx, config, source, op.ID)
			}
			state := journalState(t, a)
			switch mode {
			case "complete", "synchronous-complete":
				if err != nil || state.Operation.Phase != "Verified" || !floorImageOperationReceiptPresent(state, state.Operation) {
					t.Fatal("receipt not committed atomically", err)
				}
				if err = a.finishConfiguration(ctx, op.ID); err != nil {
					t.Fatal("verified operation not released", err)
				}
				if len(journalState(t, a).ActionApplications) != 1 || journalState(t, a).Operation != nil {
					t.Fatal("receipt lost on release")
				}
			case "pruned":
				if err == nil || state.Operation.Phase != "Verified" {
					t.Fatal("pruning not detected", err)
				}
				if err = a.finishConfiguration(ctx, op.ID); err == nil {
					t.Fatal("operation released without receipt")
				}
			default:
				if err == nil || !reflect.DeepEqual(before, state) {
					t.Fatal("invalid verification changed journal", err)
				}
			}
		})
	}
}
