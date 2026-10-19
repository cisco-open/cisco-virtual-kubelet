// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPlannedAPManifestFloorThenPlacement(t *testing.T) {
	for _, mode := range []string{"normal", "ambiguous", "optional", "image", "image-ambiguous"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}
func testPlannedAPManifestFloorThenPlacement(t *testing.T, mode string, install ...func(*adapter, *configapi.NetworkControllerConfig)) {
	private := strings.HasSuffix(mode, "-private")
	mode = strings.TrimSuffix(mode, "-private")
	recoverTasks := strings.Contains(mode, "recover")
	keyed := strings.Contains(mode, "keyed")
	typeMode := strings.Contains(mode, "model-type")
	refuseOnce := strings.Contains(mode, "refuse")
	injectedRefusal, sawRefusal := false, false
	ambiguous := mode == "ambiguous"
	imageMode := strings.HasPrefix(mode, "image")
	var cm *corev1.ConfigMap
	model := plannedAPFixture
	if mode == "optional" {
		model = strings.Replace(model, `"position_z":2,`, "", 1)
		model = strings.Replace(model, `"band":5,`, "", 1)
		model = strings.Replace(model, `"mac_address":"AA-BB-CC-DD-EE-FF",`, "", 1)
	}
	if imageMode {
		model = strings.Replace(model, `"floor_number":1`, `"floor_number":1,"image_path":"maps/floor.pdf"`, 1)
	}
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"sites"}
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(model)}
	if imageMode {
		cm = floorImageAssetFixture()
		cm.ObjectMeta = metav1.ObjectMeta{Name: "images", Namespace: config.Namespace, UID: "images"}
		cm.Data["model.yaml"] = model
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline = nil
		config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
	}
	if strings.Contains(mode, "yaml-") {
		model = string(resolveYAMLSiteDesignFixture(t, []byte(model)))
		if cm != nil {
			cm.Data["model.yaml"] = model
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline.Raw = []byte(model)
		}
	}
	if strings.Contains(mode, "merged") {
		model = string(mergeSiteDesignFixture(t, []byte(model)))
		if cm != nil {
			cm.Data["model.yaml"] = model
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline.Raw = []byte(model)
		}
	}
	if strings.Contains(mode, "defaults") {
		model = resolveSiteDesignDefaultFixture(t, model, cm)
		if cm != nil {
			cm.Data["model.yaml"] = model
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline.Raw = []byte(model)
		}
		config.Spec.ModelSource.Exporter = SiteDefaultsExporter
	}
	if strings.Contains(mode, "selected") {
		model = string(encodeSiteSelectionFixture(t, []byte(model), nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab/Building/Floor"}, ManageSpecificSitesOnly: true}))
		if cm != nil {
			cm.Data["model.yaml"] = model
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline.Raw = []byte(model)
		}
		config.Spec.ModelSource.Exporter = SiteSelectionExporter
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if private {
		configureSiteNativeComparisonFixture(t, a, config)
	}
	if keyed {
		if !private {
			t.Fatal("keyed AP fixture requires existing private site key")
		}
		if config.Annotations == nil {
			config.Annotations = map[string]string{}
		}
		config.Annotations[plannedAPBaselineAnnotation] = "true"
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	if len(install) > 0 {
		install[0](a, config)
		if keyed {
			config.Annotations = map[string]string{plannedAPBaselineAnnotation: "true"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		if cm != nil {
			cm.ObjectMeta = metav1.ObjectMeta{Name: cm.Name, Namespace: config.Namespace}
			if err := a.statusWriter.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
	}
	sites := sampleSites()
	placements := []plannedAPObservation{}
	writes := []string{}
	lateNative := false
	keyedResultDrift := false
	retainedCheck, retainedDispatchChanged := false, false
	handoffDisabled := false
	publicationReads, sawUnstablePublication := 0, false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == plannedAPCatalogPath:
			catalog := plannedAPCatalogFixture
			if typeMode {
				catalog = plannedAPTypeCatalogFixture
				if strings.Contains(mode, "incompatible") {
					catalog = strings.Replace(catalog, `"C9130AXI-ALT","antennaPatterns":[{"band":"5","names":["Internal"]}`, `"C9130AXI-ALT","antennaPatterns":[{"band":"5","names":["Other"]}`, 1)
				}
			}
			fmt.Fprint(w, catalog)
		case r.URL.Path == sitesPath:
			if strings.Contains(mode, "handoff-disable") && !handoffDisabled {
				state := journalState(t, a)
				if state.Operation != nil && state.Operation.Phase == "Submitting" {
					if _, ok := sitePrivateFloorUpdateParts(state.Operation.Resource); ok {
						var current configapi.NetworkControllerConfig
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
							t.Fatal(err)
						}
						current.Annotations[siteFloorHandoffAnnotation] = "false"
						if err := a.statusWriter.Update(ctx, &current); err != nil {
							t.Fatal(err)
						}
						handoffDisabled = true
					}
				}
			}
			if refuseOnce && !injectedRefusal {
				state := journalState(t, a)
				if state != nil && state.Operation != nil && state.Operation.Phase == "Submitting" && siteDesignOperation(state.Operation) {
					injectedRefusal = true
					w.WriteHeader(503)
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": sites})
		case strings.HasPrefix(r.URL.Path, taskPath):
			now := time.Now().UnixMilli()
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "startTime": now, "endTime": now, "isError": false, "data": siteOwnedTestUUID})
		case r.URL.Path == "/dna/intent/api/v2/floors" && r.Method == http.MethodPost:
			var floor Site
			if json.NewDecoder(r.Body).Decode(&floor) != nil || floor.ParentID != "building" {
				t.Error("unresolved floor")
			}
			floor.ID = siteOwnedTestUUID
			floor.Type = "floor"
			floor.NameHierarchy = "Global/Lab/Building/Floor"
			sites = append(sites, floor)
			writes = append(writes, "floor")
			fmt.Fprint(w, `{"response":{"taskId":"floor-task"}}`)
		case r.URL.Path == "/dna/intent/api/v2/floors/"+siteOwnedTestUUID && r.Method == http.MethodPut:
			old := sites[len(sites)-1]
			var updated Site
			if json.NewDecoder(r.Body).Decode(&updated) != nil {
				t.Fatal("handoff floor payload")
			}
			updated.ID, updated.Type, updated.NameHierarchy = old.ID, old.Type, old.NameHierarchy
			var err error
			updated, err = siteFloorObservable(updated)
			if err != nil {
				t.Fatal("native floor readback fixture", err)
			}
			if !strings.Contains(mode, "handoff-ignored") {
				for i := range placements {
					raw, _ := json.Marshal(placements[i])
					value, err := decodeExactNativeJSON(raw)
					if err != nil {
						t.Fatal(err)
					}
					projected, err := plannedAPFloorEffect(plannedAPState{floorID: old.ID, floor: old, actual: placements[i], native: value.(map[string]any)}, updated)
					if err != nil {
						t.Fatal("native floor effect fixture", err)
					}
					placements[i] = projected.actual
				}
			}
			sites[len(sites)-1] = updated
			writes = append(writes, "floor-update")
			if strings.Contains(mode, "handoff-result-drift") {
				keyedResultDrift = true
			}
			if strings.Contains(mode, "handoff-missing-task") {
				fmt.Fprint(w, `{}`)
			} else {
				fmt.Fprintf(w, `{"response":{"taskId":"floor-update-task-%d"}}`, len(writes))
			}
		case r.URL.Path == "/dna/intent/api/v2/floors/"+siteOwnedTestUUID+"/uploadImage":
			if len(sites) != 4 || r.Method != http.MethodPost {
				t.Error("image before floor or wrong method")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			payload, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(payload, cm.BinaryData["floor-pdf"]) || part.FileName() != "floor.pdf" {
				t.Error("incorrect image bytes")
			}
			writes = append(writes, "image")
			if mode == "image-ambiguous" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"groupInstanceUuid":"`+siteOwnedTestUUID+`","imageInfo":{"imageName":"floor.pdf"}}`)
			}
		case r.URL.Path == plannedAPPositionsPath(siteOwnedTestUUID):
			if retainedCheck && strings.Contains(mode, "retained-dispatch") && !retainedDispatchChanged {
				state := journalState(t, a)
				if state.Operation != nil && state.Operation.Phase == "Submitting" {
					v := 9.0
					placements[0].Position.X = &v
					retainedDispatchChanged = true
				}
			}
			if (strings.Contains(mode, "native-") || keyedResultDrift) && len(placements) > 0 {
				state := journalState(t, a)
				if strings.Contains(mode, "native-publish-late") && state.Operation != nil && state.Operation.Phase == "Verifying" && plannedAPObservationSelected(state.Operation) {
					publicationReads++
					if publicationReads >= 2 {
						lateNative = true
					}
				}
				if strings.Contains(mode, "native-late") && state.Operation != nil && state.Operation.Phase == "Submitting" && state.Operation.Action == "Update" && plannedAPResourceKey(state.Operation.Resource) != "" {
					lateNative = true
				}
				raw, _ := json.Marshal(placements)
				value, err := decodeExactNativeJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				rows := value.([]any)
				for _, value := range rows {
					row := value.(map[string]any)
					if strings.Contains(mode, "native-unknown") || lateNative || keyedResultDrift {
						row["privateNativeState"] = "not-for-status"
					}
					if strings.Contains(mode, "native-sensor") {
						row["radios"].([]any)[0].(map[string]any)["antenna"].(map[string]any)["elevationAccelerometer"] = false
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": placements})
		case r.URL.Path == plannedAPPositionsPath(siteOwnedTestUUID)+"/bulk" || r.URL.Path == plannedAPPositionsPath(siteOwnedTestUUID)+"/bulkChange":
			if len(sites) != 4 || r.Method != http.MethodPost {
				t.Error("placement before floor")
			}
			if imageMode {
				state := journalState(t, a)
				expectedReceipts := 2
				if len(placements) != 0 {
					expectedReceipts++
				}
				if keyed {
					applied, err := plannedAPAppliedRecords(state)
					if err != nil {
						t.Fatal(err)
					}
					expectedReceipts += len(applied)
				}
				siteRows, err := siteAppliedRecords(state)
				if err != nil {
					t.Fatal(err)
				}
				expectedReceipts += len(siteRows)
				if len(state.ActionApplications) != expectedReceipts || state.ActionApplications[1].Operation.Phase != "Verified" {
					t.Error("placement before retained image completion")
				}
			}
			var batch []plannedAPObservation
			if json.NewDecoder(r.Body).Decode(&batch) != nil || len(batch) != 1 {
				t.Error("invalid placement request")
				w.WriteHeader(400)
				return
			}
			creating := r.URL.Path == plannedAPPositionsPath(siteOwnedTestUUID)+"/bulk"
			index := -1
			if !creating {
				for i := range placements {
					if placements[i].ID == batch[0].ID {
						index = i
					}
				}
				if index < 0 {
					t.Error("update targeted a missing AP")
					w.WriteHeader(404)
					return
				}
			}
			if creating {
				if r.URL.Path != plannedAPPositionsPath(siteOwnedTestUUID)+"/bulk" {
					t.Error("wrong create route")
				}
				batch[0].ID = "22222222-2222-4222-8222-222222222222"
				batch[0].Radios[0].ID = "radio-id"
				if len(placements) > 0 {
					batch[0].ID = "33333333-3333-4333-8333-333333333333"
					batch[0].Radios[0].ID = "radio-id-2"
				}
			} else {
				if typeMode && strings.Contains(mode, "ignored") {
					batch[0].Type = placements[index].Type
				}
				if strings.Contains(mode, "keyed-result-drift") {
					keyedResultDrift = true
				}
				if batch[0].ID != placements[index].ID || batch[0].Radios[0].ID != placements[index].Radios[0].ID {
					t.Error("update changed identity")
				}
				batch[0].Radios[0].Bands = placements[index].Radios[0].Bands
			}
			if mode == "optional" && len(placements) == 0 {
				if batch[0].MAC != nil || batch[0].Position.Z != nil || len(batch[0].Radios[0].Bands) != 0 {
					t.Error("optional defaults fabricated")
				}
				var defaults plannedAPObservation
				json.Unmarshal([]byte(plannedAPObservedFixture), &defaults)
				batch[0].MAC = defaults.MAC
				batch[0].Position.Z = defaults.Position.Z
				batch[0].Radios[0].Bands = defaults.Radios[0].Bands
			}
			if creating {
				placements = append(placements, batch[0])
			} else {
				placements[index] = batch[0]
			}
			writes = append(writes, "placement")
			if ambiguous {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				fmt.Fprintf(w, `{"response":{"taskId":"placement-task-%d"}}`, len(writes))
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	recovered := map[string]bool{}
	tick := func() {
		t.Helper()
		state := journalState(t, a)
		floorRecovery := false
		if state != nil && state.Operation != nil && strings.Contains(mode, "handoff") {
			_, floorRecovery = sitePrivateFloorUpdateParts(state.Operation.Resource)
		}
		if recoverTasks && state != nil && state.Operation != nil && (plannedAPResourceKey(state.Operation.Resource) != "" || floorRecovery) && state.Operation.Phase == "Accepted" && !recovered[state.Operation.ID] {
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			recovered[state.Operation.ID] = true
		}
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(mode, "native-publish-late") && publicationReads == 2 {
			state := journalState(t, a)
			if state.Operation == nil || state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 1 || len(writes) != 2 {
				t.Fatal("unstable complete native observation was published or replayed")
			}
			sawUnstablePublication = true
		}
		if (refuseOnce || strings.Contains(mode, "native-late")) && !sawRefusal {
			for _, condition := range config.Status.Conditions {
				if condition.Reason == "NativePreflightRefused" {
					expected := 1
					if strings.Contains(mode, "native-late") {
						expected = 2
					}
					if len(writes) != expected || journalState(t, a).Operation != nil {
						t.Fatal("design refusal emitted a native mutation or lost retirement")
					}
					sawRefusal = true
				}
			}
		}
	}
	converge := func() {
		t.Helper()
		for i := 0; i < 24; i++ {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("placement manifest failed to converge", config.Status.Conditions)
	}
	if ambiguous || mode == "image-ambiguous" {
		for i := 0; i < 20; i++ {
			tick()
		}
		state := journalState(t, a)
		if len(writes) != 2 || state.Operation == nil || state.Operation.Phase != "Unknown" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("uncertain placement was replayed or declared converged")
		}
		return
	}

	converge()
	if strings.Contains(mode, "handoff") {
		testPlannedAPFloorHandoffStages(t, a, config, &placements, &writes, mode, tick, converge)
		wantRecovered := 5
		if strings.Contains(mode, "multiple") {
			wantRecovered = 8
		}
		if recoverTasks && len(recovered) != wantRecovered {
			t.Fatal("handoff did not recover original AP create, two floor tasks and two AP corrections")
		}
		return
	}
	if strings.Contains(mode, "floor-consumer") {
		testPlannedAPFloorConsumerStages(t, a, config, sites, &placements, &writes, mode, tick, converge)
		if recoverTasks && len(recovered) != 4 {
			t.Fatal("floor consumer did not recover the original create and three correction tasks")
		}
		return
	}
	if strings.Contains(mode, "native-publish-late") && !sawUnstablePublication {
		t.Fatal("publication did not exercise full native reobservation")
	}
	identityBefore, err := plannedAPIdentityRecords(journalState(t, a))
	if err != nil || len(identityBefore) != 1 {
		t.Fatal("completed AP did not retain exactly one observed identity", err)
	}
	if keyed {
		for _, r := range identityBefore {
			if r.keyID == "" || r.mac == "" {
				t.Fatal("keyed observed AP baseline missing")
			}
		}
		if strings.Contains(mode, "keyed-retained") {
			if cm == nil {
				t.Fatal("retained mutation fixture needs a synchronous image writer")
			}
			before := len(writes)
			var root map[string]any
			json.Unmarshal([]byte(cm.Data["model.yaml"]), &root)
			floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
			delete(floor, "planned_access_points")
			raw, _ := json.Marshal(root)
			cm.Data["model.yaml"] = string(raw)
			cm.BinaryData["floor-pdf"] = append(cm.BinaryData["floor-pdf"], []byte("\n% retained AP qualification\n")...)
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
			snapshot, _ := json.Marshal(placements)
			retainedCheck = true
			switch {
			case strings.Contains(mode, "retained-drift"):
				v := 9.0
				placements[0].Position.X = &v
			case strings.Contains(mode, "retained-missing"):
				placements = nil
			case strings.Contains(mode, "retained-replaced"):
				placements[0].ID = "33333333-3333-4333-8333-333333333333"
			case strings.Contains(mode, "retained-renamed"):
				placements[0].Name = "Renamed"
			}
			if !strings.Contains(mode, "retained-same") {
				sawCertifiedRefusal := false
				for i := 0; i < 4; i++ {
					tick()
					for _, condition := range config.Status.Conditions {
						sawCertifiedRefusal = sawCertifiedRefusal || condition.Reason == "NativePreflightRefused"
					}
				}
				if len(writes) != before || journalState(t, a).Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("removed AP private boundary bypassed by synchronous image mutation")
				}
				if strings.Contains(mode, "retained-dispatch") && (!retainedDispatchChanged || !sawCertifiedRefusal) {
					t.Fatal("removed AP late change did not retire the proven undispatched journal")
				}
				json.Unmarshal(snapshot, &placements)
			}
			converge()
			if len(writes) != before+1 || len(placements) != 1 {
				t.Fatal("retained AP was replayed, deleted, or blocked the unchanged image write")
			}
			after, err := plannedAPIdentityRecords(journalState(t, a))
			if err != nil || !reflect.DeepEqual(identityBefore, after) {
				t.Fatal("source removal modified the original AP observation", err)
			}
			return
		}
		if strings.Contains(mode, "keyed-drift") || strings.Contains(mode, "keyed-geometry") || strings.Contains(mode, "keyed-disable") || strings.Contains(mode, "keyed-pruned") {
			before := len(writes)
			switch {
			case strings.Contains(mode, "keyed-drift"):
				value := 6.0
				placements[0].Position.X = &value
			case strings.Contains(mode, "keyed-geometry"):
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `"width":10`, `"width":11`, 1))
				if strings.Contains(mode, "removed") {
					var root map[string]any
					json.Unmarshal(config.Spec.Source.Inline.Raw, &root)
					floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
					delete(floor, "planned_access_points")
					config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
				}
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case strings.Contains(mode, "keyed-disable"):
				config.Generation++
				config.Annotations[plannedAPBaselineAnnotation] = "false"
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case strings.Contains(mode, "keyed-pruned"):
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for i, r := range nc.Status.Configuration.ActionApplications {
					if strings.HasPrefix(r.Resource, plannedAPPrivateIdentityPrefix) {
						nc.Status.Configuration.ActionApplications[i].Resource = "pruned-result-marker"
					}
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 4; i++ {
				tick()
			}
			if len(writes) != before || journalState(t, a).Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("keyed AP baseline drift/downgrade/geometry handoff was bypassed")
			}
			return
		}
	}
	if strings.Contains(mode, "identity-") {
		before := len(writes)
		switch {
		case strings.Contains(mode, "identity-replaced"):
			placements[0].ID = "33333333-3333-4333-8333-333333333333"
		case strings.Contains(mode, "identity-missing"):
			placements = nil
		case strings.Contains(mode, "identity-floor"):
			sites[len(sites)-1].ID = "33333333-3333-4333-8333-333333333333"
		case strings.Contains(mode, "identity-pruned"):
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i, r := range nc.Status.Configuration.ActionApplications {
				if strings.HasPrefix(r.Resource, plannedAPIdentityPrefix) {
					nc.Status.Configuration.ActionApplications[i].Resource = "pruned-result-marker"
				}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 3; i++ {
			tick()
		}
		if len(writes) != before || journalState(t, a).Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("observed identity fence allowed replacement, recreation or false convergence")
		}
		return
	}
	expected := 2
	if imageMode {
		expected = 3
		if writes[1] != "image" {
			t.Fatal("image not ordered before placement")
		}
	}
	if len(writes) != expected || writes[0] != "floor" || writes[expected-1] != "placement" {
		t.Fatal("incorrect order or duplicate write")
	}
	config.Generation++
	nextModel := strings.Replace(model, `"position_x":4.5`, `"position_x":8`, 1)
	if typeMode {
		nextModel = strings.Replace(model, `"ap_type":"C9130AXI"`, `"ap_type":"C9130AXI-ALT"`, 1)
		if strings.Contains(mode, "omitted") || strings.Contains(mode, "incompatible") {
			var root map[string]any
			json.Unmarshal([]byte(nextModel), &root)
			ap := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)["planned_access_points"].([]any)[0].(map[string]any)
			delete(ap, "radios")
			raw, _ := json.Marshal(root)
			nextModel = string(raw)
		}
	}
	if imageMode {
		cm.Data["model.yaml"] = nextModel
		if err := a.statusWriter.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
	} else {
		config.Spec.Source.Inline.Raw = []byte(nextModel)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if typeMode && (strings.Contains(mode, "ignored") || strings.Contains(mode, "incompatible")) {
		before := len(writes)
		for i := 0; i < 6; i++ {
			tick()
		}
		state := journalState(t, a)
		if strings.Contains(mode, "ignored") {
			if len(writes) != before+1 || state.Operation == nil || state.Operation.Phase != "Verifying" {
				t.Fatal("ignored model update was replayed or released")
			}
		} else if len(writes) != before || state.Operation != nil {
			t.Fatal("incompatible retained antenna was dispatched")
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unsupported/ignored type declared converged")
		}
		return
	}
	if strings.Contains(mode, "keyed-result-drift") {
		before := len(writes)
		for i := 0; i < 6; i++ {
			tick()
		}
		state := journalState(t, a)
		if len(writes) != before+1 || state.Operation == nil || state.Operation.Phase != "Verifying" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unexpected native result was published or replayed")
		}
		latest, err := plannedAPAppliedRecords(state)
		if err != nil || len(latest) != 0 {
			t.Fatal("unrelated native mutation became a trusted applied baseline", err)
		}
		anchors, err := plannedAPIdentityRecords(state)
		if err != nil || !reflect.DeepEqual(identityBefore, anchors) {
			t.Fatal("uncertain result changed original keyed baseline", err)
		}
		raw, _ := json.Marshal(state)
		if bytes.Contains(raw, []byte("not-for-status")) {
			t.Fatal("private native state escaped in failed completion")
		}
		return
	}
	if strings.Contains(mode, "native-unknown") || strings.Contains(mode, "native-late") || strings.Contains(mode, "native-publish-late") {
		before := len(writes)
		for i := 0; i < 6; i++ {
			tick()
		}
		if len(writes) != before || journalState(t, a).Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unmapped native state was overwritten or falsely converged")
		}
		if strings.Contains(mode, "native-late") && !sawRefusal {
			t.Fatal("late raw drift did not retire its proven no-dispatch operation")
		}
		raw, _ := json.Marshal(journalState(t, a))
		if bytes.Contains(raw, []byte("not-for-status")) || bytes.Contains(raw, []byte("privateNativeState")) {
			t.Fatal("private native attributes escaped into status")
		}
		return
	}
	converge()
	for i := 0; i < 3; i++ {
		tick()
	}
	positionOK := *placements[0].Position.X == 8
	if typeMode {
		positionOK = *placements[0].Position.X == 4.5 && placements[0].Type == plannedAPAlternateType
	}
	if len(writes) != expected+1 || !positionOK || journalState(t, a).Operation != nil {
		t.Fatal("update replayed or journal not settled")
	}
	identityAfter, err := plannedAPIdentityRecords(journalState(t, a))
	if err != nil || !reflect.DeepEqual(identityBefore, identityAfter) {
		t.Fatal("AP update changed its retained initial identity", err)
	}
	if recoverTasks && len(recovered) != 2 {
		t.Fatal("declared planned-AP tasks did not recover without resubmission")
	}
	if refuseOnce && !sawRefusal {
		t.Fatal("design did not exercise certified automatic no-dispatch retirement")
	}
	if private {
		state := journalState(t, a)
		creators, err := siteCreatedRecords(state)
		if err != nil || len(creators) != 1 {
			t.Fatal("private floor creator lineage lost", err)
		}
		for _, record := range creators {
			if record.keyID == "" || record.mac == "" {
				t.Fatal("private floor baseline missing")
			}
		}
	}
	if imageMode {
		cm.BinaryData["floor-pdf"] = []byte("%PDF-1.7\nsecond image")
		if err := a.statusWriter.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
		// Source-only changes must trigger a new image upload without replaying APs.
		tick()
		converge()
		for i := 0; i < 3; i++ {
			tick()
		}
		if len(writes) != expected+2 || writes[len(writes)-1] != "image" {
			t.Fatal("image revision failed or replayed other resources")
		}
	}
	if keyed {
		before := journalState(t, a)
		prior, err := plannedAPAppliedRecords(before)
		if err != nil || len(prior) != 1 {
			t.Fatal("keyed Update did not retain one latest row", err)
		}
		config.Generation++
		if imageMode {
			if typeMode {
				cm.Data["model.yaml"] = strings.Replace(cm.Data["model.yaml"], `"ap_type":"C9130AXI-ALT"`, `"ap_type":"C9130AXI"`, 1)
			} else {
				cm.Data["model.yaml"] = strings.Replace(cm.Data["model.yaml"], `"position_x":8`, `"position_x":9`, 1)
			}
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		} else {
			if typeMode {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"ap_type":"C9130AXI-ALT"`, `"ap_type":"C9130AXI"`, 1))
			} else {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"position_x":8`, `"position_x":9`, 1))
			}
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		converge()
		for i := 0; i < 3; i++ {
			tick()
		}
		after := journalState(t, a)
		latest, err := plannedAPAppliedRecords(after)
		if err != nil || len(latest) != 1 || len(before.ActionApplications) != len(after.ActionApplications) {
			t.Fatal("latest AP baseline grew or was lost", err)
		}
		identities, err := plannedAPIdentityRecords(after)
		if err != nil || !reflect.DeepEqual(identityBefore, identities) {
			t.Fatal("latest replacement changed original keyed observation", err)
		}
		for key, row := range latest {
			if row.Operation.ID == prior[key].Operation.ID {
				t.Fatal("second keyed update did not replace current applied state")
			}
		}
		positionOK := *placements[0].Position.X == 9
		if typeMode {
			positionOK = *placements[0].Position.X == 4.5 && placements[0].Type == "C9130AXI"
		}
		if !positionOK || after.Operation != nil {
			t.Fatal("second keyed update failed or replayed")
		}
	}

	if strings.Contains(mode, "selected") {
		before := journalState(t, a).DeepCopy()
		countBefore := len(writes)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for _, options := range []nacmodel.SiteSelectionOptions{{ManageGlobalSettings: true}, {ManagedSites: []string{"Global/Lab/Building/Floor"}, ManageSpecificSitesOnly: true}} {
			var raw []byte
			if imageMode {
				raw = []byte(cm.Data["model.yaml"])
			} else {
				raw = config.Spec.Source.Inline.Raw
			}
			doc, err := decodeSiteSelectionDocument(raw)
			if err != nil {
				t.Fatal(err)
			}
			updated := encodeSiteSelectionFixture(t, doc.Model, options)
			if imageMode {
				cm.Data["model.yaml"] = string(updated)
				if err := a.statusWriter.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
			} else {
				config.Spec.Source.Inline.Raw = updated
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			converge()
			if !reflect.DeepEqual(before, journalState(t, a)) || len(writes) != countBefore {
				t.Fatal("scope exclusion/re-selection changed retained image proof or replayed native writes")
			}
		}
	}

}

func TestPrivateSiteDesignManifestComposition(t *testing.T) {
	for _, mode := range []string{"normal-private", "optional-private", "image-private", "recover-private", "image-recover-private", "ambiguous-private", "image-ambiguous-private", "image-refuse-private", "recover-refuse-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func TestPlannedAPObservedIdentityManifest(t *testing.T) {
	for _, mode := range []string{"identity-replaced", "identity-missing", "identity-floor", "identity-pruned", "identity-replaced-private", "identity-missing-private", "identity-floor-private", "identity-pruned-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func TestPlannedAPNativeStateManifest(t *testing.T) {
	for _, mode := range []string{"native-unknown", "native-late", "native-sensor", "native-publish-late", "native-unknown-private", "native-late-private", "native-sensor-private", "native-publish-late-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func TestPlannedAPKeyedBaselineManifest(t *testing.T) {
	for _, mode := range []string{"keyed-private", "keyed-recover-private", "keyed-recover-refuse-private", "image-keyed-private", "keyed-native-sensor-private", "keyed-native-late-private", "keyed-drift-private", "keyed-geometry-private", "keyed-geometry-removed-private", "keyed-disable-private", "keyed-pruned-private", "keyed-result-drift-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func TestPlannedAPTypeManifest(t *testing.T) {
	for _, mode := range []string{"model-type", "model-type-recover", "model-type-omitted", "model-type-ignored", "model-type-incompatible", "model-type-keyed-private", "model-type-keyed-recover-private", "image-model-type-keyed-private", "model-type-keyed-ignored-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func TestPlannedAPRetainedManifest(t *testing.T) {
	for _, mode := range []string{"same", "drift", "missing", "replaced", "renamed", "dispatch"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, "image-keyed-retained-"+mode+"-private")
		})
	}
}

func TestPlannedAPIncompleteIntentBlocksSiteCreation(t *testing.T) {
	var model map[string]any
	if json.Unmarshal([]byte(plannedAPFixture), &model) != nil {
		t.Fatal("invalid fixture")
	}
	floor := model["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	delete(floor["planned_access_points"].([]any)[0].(map[string]any), "radios")
	raw, _ := json.Marshal(model)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == plannedAPCatalogPath {
			fmt.Fprint(w, plannedAPCatalogFixture)
			return
		}
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path != sitesPath || r.Method != http.MethodGet {
			t.Error("unexpected mutation or AP read before floor")
		}
		json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
	})
	e, err := c.compileConfigurationExecutor(raw, "sites")
	if err != nil {
		t.Fatal(err)
	}
	if next, err := e.next(context.Background()); err == nil || next != nil {
		t.Fatal("incomplete AP intent permitted floor creation")
	}
}
