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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const floorImageReportModel = `{"catalyst_center":{"sites":{"floors":[{"name":"Floor","parent_name":"Global/Lab/Building","floor_number":1,"rf_model":"Free Space","width":10,"length":15,"height":3,"units_of_measure":"meters","image_path":"maps/floor.pdf"}]}}}`

func TestFloorImageReportDoesNotClaimByteObservation(t *testing.T) {
	for _, mode := range []string{"unobservable", "geometry-drift", "missing-floor", "missing-asset", "invalid-asset", "null-image", "source-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r, config, _ := reportFixture(t)
			cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "images", Namespace: config.Namespace, UID: "source"}, Data: map[string]string{"model.json": floorImageReportModel, floorImageAssetsKey: `{"maps/floor.pdf":"floor-pdf"}`}, BinaryData: map[string][]byte{"floor-pdf": []byte("%PDF-1.7\nfixture")}}
			if mode == "null-image" {
				cm.Data["model.json"] = strings.Replace(floorImageReportModel, `"maps/floor.pdf"`, `null`, 1)
			}
			if mode == "missing-asset" {
				delete(cm.BinaryData, "floor-pdf")
			}
			if mode == "invalid-asset" {
				cm.BinaryData["floor-pdf"] = []byte("invalid")
			}
			if err := r.a.statusWriter.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline = nil
			config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
			if err := r.a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			reads := 0
			r.a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if req.Method != http.MethodGet || req.URL.Path != sitesPath {
					t.Error("image Report performed mutation or unsupported image fetch")
					w.WriteHeader(500)
					return
				}
				floor := Site{ID: "floor", Name: "Floor", Type: "floor", ParentID: "building", NameHierarchy: "Global/Lab/Building/Floor"}
				if err := json.Unmarshal([]byte(`{"floorNumber":1,"rfModel":"Free Space","width":10,"length":15,"height":3,"unitsOfMeasure":"meters"}`), &floor); err != nil {
					t.Error(err)
				}
				if mode == "geometry-drift" {
					width := 12.0
					floor.Width = &width
				}
				sites := sampleSites()
				if mode != "missing-floor" {
					sites = append(sites, floor)
				}
				if mode == "source-race" {
					cm.BinaryData["floor-pdf"] = []byte("%PDF-1.7\nchanged")
					if err := r.a.statusWriter.Update(ctx, cm); err != nil {
						t.Error(err)
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": sites})
			})
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			var published configapi.NetworkControllerConfig
			if err := r.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			if mode == "source-race" {
				if published.Status.ObservedGeneration != 0 {
					t.Fatal("stale image snapshot report published")
				}
				return
			}
			if mode == "missing-asset" || mode == "invalid-asset" {
				if published.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || reads != 0 {
					t.Fatal("invalid asset report contacted native API or appeared valid")
				}
				return
			}
			expected := configapi.NetworkControllerConfigPhaseWaiting
			if mode == "geometry-drift" || mode == "missing-floor" {
				expected = configapi.NetworkControllerConfigPhaseDrifted
			}
			if mode == "null-image" {
				expected = configapi.NetworkControllerConfigPhaseInSync
			}
			if published.Status.Phase != expected || len(published.Status.SectionStatus) != 1 {
				t.Fatal("unobservable image relabeled complete or hid real drift", mode, published.Status.Phase)
			}
			if mode != "null-image" && (published.Status.SectionStatus[0].ResourceCount != 2 || !strings.Contains(published.Status.SectionStatus[0].Message, "unverified")) {
				t.Fatal("missing explicit partial-observation status")
			}
			if journalState(t, r.a) != nil {
				t.Fatal("Report created ownership or image upload receipt")
			}
		})
	}
}
