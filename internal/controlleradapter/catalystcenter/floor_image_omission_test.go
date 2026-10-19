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
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFloorImageOmissionWitnessFences(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	want := floorImageIntent{floor: "Global/Lab/Building/Floor", filename: "maps/floor.pdf", digest: strings.Repeat("a", 64)}
	for _, mode := range []string{"valid", "valid-inline", "valid-source-rebind", "same-source", "old-source", "old-generation", "old-controller", "different-source-uid", "foreign-owner", "foreign-name", "unpaired-source", "reused-operation", "reused-task", "before-omission", "future-witness", "before-verification", "wrong-key", "unknown-version", "noncanonical", "missing-verification", "duplicate", "replaced-floor", "unverified", "created-resource", "effects", "prepared", "cancelled", "old-source-pair", "no-frame"} {
		t.Run(mode, func(t *testing.T) {
			submitted, verified := metav1.NewTime(now.Add(-20*time.Second)), metav1.NewTime(now.Add(-10*time.Second))
			row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: want.resource(), VerifiedAt: &verified, Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), Section: "sites", Resource: want.synchronousOperationResource(), Action: "Update", Phase: "Verified", ConfigName: "model", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, SourceUID: "source", SourceVersion: "1", TargetID: "floor", SubmittedAt: &submitted}}
			w := floorImageOmission{Key: want.resource(), Generation: 1, ControllerGeneration: 1, SourceUID: "source", SourceVersion: "2", Observed: now.Add(-5 * time.Second).Unix()}
			next := &cvk.NetworkControllerConfigurationOperation{ConfigName: "model", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, SourceUID: "source", SourceVersion: "3"}
			switch mode {
			case "valid-inline":
				row.Operation.SourceUID, row.Operation.SourceVersion = "", ""
				w.Generation, w.SourceUID, w.SourceVersion = 2, "", ""
				next.ConfigGeneration, next.SourceUID, next.SourceVersion = 3, "", ""
			case "valid-source-rebind":
				next.ConfigGeneration, next.SourceUID = 2, "replacement"
			case "same-source":
				next.SourceVersion = "2"
			case "old-source":
				next.SourceVersion = "1"
			case "old-generation":
				next.ConfigGeneration = 0
			case "old-controller":
				next.ControllerGeneration = 0
			case "different-source-uid":
				next.SourceUID = "replacement"
			case "foreign-owner":
				next.ConfigUID = "foreign"
			case "foreign-name":
				next.ConfigName = "foreign"
			case "unpaired-source":
				next.SourceVersion = ""
			case "reused-operation":
				next.ID = row.Operation.ID
			case "reused-task":
				row.Operation.Resource = want.operationResource()
				row.Operation.TaskID, next.TaskID = "task", "task"
			case "before-omission":
				next.SubmittedAt = &submitted
			case "future-witness":
				w.Observed = now.Add(time.Second).Unix()
			case "before-verification":
				w.Observed = submitted.Unix()
			case "wrong-key":
				w.Key = "floor-image:" + strings.Repeat("c", 64)
			case "missing-verification":
				row.VerifiedAt = nil
			case "replaced-floor":
				row.Operation.TargetID = "replacement"
			case "unverified":
				row.Operation.Phase = "Unknown"
			case "created-resource":
				row.Operation.CreatedResourceID = "foreign"
			case "effects":
				row.EffectReceipts = []string{"foreign effect"}
			case "prepared":
				row.Operation.PreparedAt = &submitted
			case "cancelled":
				row.Operation.CancellationRequestedAt = &submitted
			case "old-source-pair":
				row.Operation.SourceVersion = ""
			case "no-frame":
				next = nil
			}
			row.Resource = w.resource()
			if mode == "unknown-version" {
				row.Resource = strings.Replace(row.Resource, ":v1:", ":v2:", 1)
			}
			if mode == "noncanonical" {
				row.Resource += "="
			}
			rows := []cvk.NetworkControllerConfigurationActionApplication{row}
			if mode == "duplicate" {
				rows = append(rows, row)
			}
			applied, err := floorImageApplied(want, "floor", "model", "owner", rows, now)
			if applied {
				t.Fatal("omission became native completion")
			}
			if err == nil {
				err = floorImageNewDeclaration(want, rows, next, now)
			}
			if (err == nil) != strings.HasPrefix(mode, "valid") {
				t.Fatal("omission/source fence mismatch", mode, err)
			}
		})
	}
}

func TestFloorImageOmissionReintroduction(t *testing.T) {
	for _, mode := range []string{"omit", "null", "legacy", "selected-omit"} {
		t.Run(mode, func(t *testing.T) { exerciseFloorImageOmission(t, mode, nil) })
	}
}

func TestFloorImageOmissionFinalizationFences(t *testing.T) {
	for _, mode := range []string{"pending-operation", "foreign-owner", "stale-source", "replaced-floor", "cas-conflict"} {
		t.Run(mode, func(t *testing.T) { exerciseFloorImageOmission(t, mode, nil) })
	}
}

func exerciseFloorImageOmission(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	selected := mode == "selected-omit"
	if selected {
		mode = "omit"
	}
	ctx := context.Background()
	a, config := journalFixture(t)
	if install != nil {
		install(a, config)
	}
	cm := floorImageAssetFixture()
	cm.ObjectMeta = metav1.ObjectMeta{Name: "image-lifetime", Namespace: config.Namespace}
	if install == nil {
		cm.UID = "source"
	}
	var model map[string]any
	if json.Unmarshal([]byte(plannedAPFixture), &model) != nil {
		t.Fatal("invalid fixture")
	}
	floor := model["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	delete(floor, "planned_access_points")
	floor["image_path"] = "maps/floor.pdf"
	raw, _ := json.Marshal(model)
	if selected {
		raw = encodeSiteSelectionFixture(t, raw, nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab/Building/Floor"}, ManageSpecificSitesOnly: true})
	}
	cm.Data["model.yaml"] = string(raw)
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"sites"}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	width, length, height, number := 10.0, 15.0, 3.0, 1.0
	rf, units := "Free Space", "meters"
	uploads := 0
	floorID := "floor"
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), Site{ID: floorID, Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", Type: "floor", ParentID: "building", Width: &width, Length: &length, Height: &height, FloorNumber: &number, RFModel: &rf, UnitsOfMeasure: &units})})
		case "/dna/intent/api/v2/floors/floor/uploadImage":
			if r.Method != "POST" {
				t.Error("unexpected image method")
			}
			uploads++
			w.WriteHeader(201)
			fmt.Fprint(w, `{"groupInstanceUuid":"floor","imageInfo":{"imageName":"floor.pdf"}}`)
		default:
			t.Error("unexpected image lifetime API", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	tick := func() {
		t.Helper()
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
	}
	settle := func(omitted bool) {
		t.Helper()
		for range 60 {
			tick()
			state := journalState(t, a)
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil || len(state.ActionApplications) != 1 {
				continue
			}
			_, w, err := floorImageRowKey(state.ActionApplications[0], time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if (w != nil) == omitted {
				return
			}
		}
		t.Fatal("image declaration did not settle", config.Status.Conditions, journalState(t, a).Operation)
	}
	write := func() {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(model)
		if selected {
			raw = encodeSiteSelectionFixture(t, raw, nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab/Building/Floor"}, ManageSpecificSitesOnly: true})
		}
		cm.Data["model.yaml"] = string(raw)
		if err := a.statusWriter.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	settle(false)
	if uploads != 1 {
		t.Fatal("initial image upload count", uploads)
	}
	if mode != "omit" && mode != "null" && mode != "legacy" {
		delete(floor, "image_path")
		write()
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "pending-operation" {
			nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
			nc.Status.Configuration.Operation.ID = strings.Repeat("c", 32)
			nc.Status.Configuration.Operation.Phase = "Unknown"
		}
		if mode == "foreign-owner" {
			nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
		}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		plan, err := a.compileSiteImageApply(nc, config, source)
		if err != nil {
			t.Fatal(err)
		}
		before := journalState(t, a).DeepCopy()
		if mode == "stale-source" {
			cm.BinaryData["floor-pdf"] = []byte("%PDF-1.7\nnew source version")
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "replaced-floor" {
			floorID = "replacement"
		}
		if mode == "cas-conflict" {
			a.statusWriter = &noDispatchConflictClient{Client: a.statusWriter, always: true}
		}
		if err := plan.finalize(ctx); err == nil {
			t.Fatal("unsafe omission finalization accepted")
		}
		if uploads != 1 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("refused finalization changed journal or submitted native work")
		}
		return
	}
	if mode == "legacy" {
		nc, _ := a.currentController(ctx)
		row := &nc.Status.Configuration.ActionApplications[0]
		row.Operation.Resource = strings.Replace(row.Operation.Resource, "floor-image-sync:v2:", "floor-image-sync:", 1)
		row.Operation.Resource = row.Operation.Resource[:strings.LastIndex(row.Operation.Resource, ":")]
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	for cycle := 0; cycle < 2; cycle++ {
		before := journalState(t, a).ActionApplications[0].DeepCopy()
		if mode == "null" {
			floor["image_path"] = nil
		} else {
			delete(floor, "image_path")
		}
		write()
		settle(true)
		retained := journalState(t, a).ActionApplications[0]
		if uploads != cycle+1 || !reflect.DeepEqual(before.Operation, retained.Operation) || !reflect.DeepEqual(before.VerifiedAt, retained.VerifiedAt) {
			t.Fatal("omission changed acknowledgement or submitted native work")
		}
		floor["image_path"] = "maps/floor.pdf"
		write()
		settle(false)
		after := journalState(t, a).ActionApplications[0]
		if uploads != cycle+2 || after.Operation.ID == before.Operation.ID || after.Operation.SourceVersion != cm.ResourceVersion || after.Operation.SourceUID != string(cm.UID) {
			t.Fatal("reintroduction did not use one new source-bound upload")
		}
		for range 3 {
			tick()
		}
		if uploads != cycle+2 || len(journalState(t, a).ActionApplications) != 1 {
			t.Fatal("reintroduced image replayed or grew receipt ledger")
		}
	}
	t.Log("two source-only omit/reintroduce cycles retained original proof while omitted and replaced the same receipt slot with one new upload per declaration")
}
