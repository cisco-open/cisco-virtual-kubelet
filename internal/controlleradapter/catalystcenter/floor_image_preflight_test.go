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

func TestFloorImagePreflightBeforeGeometry(t *testing.T) {
	for _, mode := range []string{"new-floor", "null-image", "missing-asset", "bad-image", "missing-floor-receipt", "capacity", "invalid-ap"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			model := strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"maps/floor.pdf"`, 1)
			assets := map[string][]byte{"maps/floor.pdf": []byte("%PDF-1.7\nfixture")}
			if mode == "null-image" {
				model = strings.Replace(model, `"image_path":"maps/floor.pdf"`, `"image_path":null`, 1)
			}
			if mode == "missing-asset" {
				assets = nil
			}
			if mode == "bad-image" {
				assets["maps/floor.pdf"] = []byte("invalid")
			}
			if mode == "invalid-ap" {
				model = strings.Replace(model, `"radios":[{`, `"unsupported_radios":[{`, 1)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
			if mode == "missing-floor-receipt" {
				_, _, images, err := parseSitesWithFloorImages([]byte(model), assets)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "sites", Resource: images[0].resource()}}
			}
			if mode == "capacity" {
				for i := 0; i < 256; i++ {
					nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprintf("existing-%d", i)})
				}
			}
			requests := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == plannedAPCatalogPath {
					fmt.Fprint(w, plannedAPCatalogFixture)
					return
				}
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				requests++
				if r.Method != "GET" || r.URL.Path != sitesPath {
					t.Error("preflight mutated or queried a nonexistent floor")
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
			})
			plan, err := a.compileSiteImageApply(nc, config, &configSourceSnapshot{raw: []byte(model), floorImages: assets})
			if mode == "bad-image" || mode == "missing-asset" || mode == "invalid-ap" {
				if err == nil || requests != 0 {
					t.Fatal("invalid source accepted or read appliance", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := plan.executor.next(ctx)
			valid := mode == "new-floor" || mode == "null-image"
			if valid {
				if err != nil || next == nil || next.resource != "Global/Lab/Building/Floor" || next.action != "Create" {
					t.Fatal("valid geometry blocked", err)
				}
				if mode == "null-image" && plan.executor.count() != 2 {
					t.Fatal("null image counted as a mutation")
				}
			} else if err == nil || next != nil {
				t.Fatal("image evidence/capacity failure allowed earlier geometry")
			}
		})
	}
}

func TestFloorImageReleaseRejectsAlteredEvidence(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: "floor-image:" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64), Phase: "Verified", Action: "Update", ConfigName: "model", ConfigUID: "owner", TaskID: "task", TargetID: "floor", SubmittedAt: &now}
	key, _ := floorImageReceiptKey(op.Resource)
	for _, mode := range []string{"exact", "changed-operation", "duplicate", "missing-time", "future-time"} {
		state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "sites", Resource: key, Operation: *op.DeepCopy(), VerifiedAt: now.DeepCopy()}}}
		switch mode {
		case "changed-operation":
			state.ActionApplications[0].Operation.TaskID = "other"
		case "duplicate":
			state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
		case "missing-time":
			state.ActionApplications[0].VerifiedAt = nil
		case "future-time":
			state.ActionApplications[0].VerifiedAt = &metav1.Time{Time: time.Now().Add(time.Hour)}
		}
		if floorImageOperationReceiptPresent(state, &op) != (mode == "exact") {
			t.Fatalf("unexpected release evidence: %s", mode)
		}
	}
}
