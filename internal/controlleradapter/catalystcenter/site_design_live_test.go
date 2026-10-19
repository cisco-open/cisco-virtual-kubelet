// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func setupLivePrivateDesignSource(t *testing.T, ctx context.Context, h *liveSiteManifest) {
	t.Helper()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "private-site-design", Namespace: h.config.Namespace, UID: "live-private-site-design-source"}, Data: map[string]string{"model.json": string(h.config.Spec.Source.Inline.Raw)}}
	if err := h.a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	h.config.Spec.Source.Inline = nil
	h.config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
	if os.Getenv("CATC_AP_NATIVE_BASELINE") == "isolated-keyed" {
		if h.config.Annotations == nil {
			h.config.Annotations = map[string]string{}
		}
		h.config.Annotations[plannedAPBaselineAnnotation] = "true"
	}
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
}

type liveDesignStage struct{ resource, action string }

func runLivePrivateDesignStages(t *testing.T, ctx context.Context, h *liveSiteManifest, stages []liveDesignStage, optionalLast ...bool) int {
	return runLivePrivateDesignStagesObserved(t, ctx, h, stages, nil, optionalLast...)
}

func runLivePrivateDesignStagesObserved(t *testing.T, ctx context.Context, h *liveSiteManifest, stages []liveDesignStage, observe func(context.Context, *cvk.NetworkControllerConfigurationOperation), optionalLast ...bool) int {
	t.Helper()
	index := 0
	seen, verified := false, false
	limit := 180
	if os.Getenv("CATC_AP_TYPE_TRANSITION") == "isolated-compatible-models" || os.Getenv("CATC_AP_FLOOR_HANDOFF") == "isolated-original-task-effects" {
		limit = 24
	}
	loggedTasks := map[string]bool{}
	for step := 0; step < limit; step++ {
		restarted := *h.a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, h.a)
		if op := state.Operation; op != nil {
			if index >= len(stages) {
				t.Fatal("design emitted an unexpected additional operation", op.Action, "plannedAP", plannedAPResourceKey(op.Resource) != "")
			}
			want := stages[index]
			matches := op.Resource == want.resource
			if plannedAPResourceKey(want.resource) != "" {
				matches = plannedAPResourceKey(op.Resource) == plannedAPResourceKey(want.resource)
			}
			if validSitePath(want.resource) {
				matches = siteResourcePathDigest(op.Resource) == sitePathDigest(want.resource)
			}
			if !matches || op.Action != want.action || op.Section != "sites" || op.ConfigUID != string(h.config.UID) || op.ConfigGeneration != h.config.Generation {
				t.Fatal("design operation escaped exact expected sequence")
			}
			if !seen {
				t.Log("design stage", index+1, op.Action, "plannedAP", plannedAPResourceKey(op.Resource) != "")
			}
			seen = true
			verified = verified || op.Phase == "Verified"
			if observe != nil && op.Phase == "Verified" {
				observe(ctx, op)
			}
			if op.TaskID != "" && !loggedTasks[op.TaskID] {
				t.Log("design task", op.TaskID)
				loggedTasks[op.TaskID] = true
			}
			h.injectSiteTaskUncertainty(t, ctx)
		} else if seen {
			if !verified {
				t.Fatal("design operation released without Verified boundary")
			}
			index++
			seen, verified = false, false
		}
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
			t.Fatal(err)
		}
		if h.config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			complete := index == len(stages) || len(optionalLast) == 1 && optionalLast[0] && index == len(stages)-1
			if !complete || state.Operation != nil {
				t.Fatal("design declared convergence without every expected stage")
			}
			return index
		}
		if h.config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("design qualification failed; retain exact fixture")
		}
		select {
		case <-ctx.Done():
			t.Fatal("design qualification deadline; retain exact fixture")
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("design qualification did not converge; retain exact fixture")
	return 0
}

func qualifyLivePrivateSiteDesign(t *testing.T, ctx context.Context, h *liveSiteManifest, owned map[string]Site, wait func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	c := h.a.client
	state := journalState(t, h.a)
	creators, err := siteCreatedRecords(state)
	if err != nil || len(creators) != 3 {
		t.Fatal(err)
	}
	ref := h.config.Spec.Source.ConfigMapRef
	var cm corev1.ConfigMap
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: h.config.Namespace, Name: ref.Name}, &cm); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal([]byte(cm.Data[ref.Key]), &root) != nil {
		t.Fatal("invalid private design source")
	}
	floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	path := floor["parent_name"].(string) + "/" + floor["name"].(string)
	id := ""
	for key, site := range owned {
		if site.NameHierarchy == path {
			id = key
		}
	}
	if !validAPIID(id) {
		t.Fatal("private floor identity missing")
	}
	managedSelection := os.Getenv("CATC_SITE_MANAGED_SELECTION") == "isolated-design"
	selection := nacmodel.SiteSelectionOptions{ManagedSites: []string{path}, ManageSpecificSitesOnly: true}
	if managedSelection {
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
			t.Fatal(err)
		}
		h.config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		h.config.Spec.ModelSource.Exporter = SiteSelectionExporter
		h.config.Generation++
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
	}
	positions, err := c.plannedAPPositions(ctx, id)
	if err != nil || len(positions) != 0 {
		t.Fatal("private floor drawing not empty", err)
	}
	positionID := ""
	var expected plannedAPObservation
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	writes := &atomic.Int64{}
	rest.HTTPClient.Transport = livePlannedAPTrace{base: base, t: t, writes: writes}
	defer func() { rest.HTTPClient.Transport = base }()
	t.Cleanup(func() {
		if !*settled || positionID == "" {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		*settled = false
		current, err := c.plannedAPPositions(cleanup, id)
		if err != nil || len(current) != 1 || !reflect.DeepEqual(current[expected.Name], expected) {
			t.Error("private planned-AP cleanup scope changed")
			return
		}
		at := time.Now()
		raw, err := c.mutate(cleanup, http.MethodDelete, plannedAPPositionsPath(id)+"/"+positionID, nil)
		if err != nil {
			t.Error("private planned-AP cleanup uncertain; no replay", err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("private planned-AP cleanup missing task")
			return
		}
		if err := wait(cleanup, receipt.Response, at); err != nil {
			t.Error(err)
			return
		}
		current, err = c.plannedAPPositions(cleanup, id)
		if err != nil || len(current) != 0 {
			t.Error("private planned-AP cleanup not verified")
			return
		}
		*settled = true
		t.Log("exact private planned-AP UUID removed before hierarchy cleanup")
	})
	entry := map[string]any{"name": "CVK-Private-Planned-AP", "ap_type": "AP9130I", "position_x": 4.5, "position_y": 6.0, "position_z": 4.0, "mac_address": "02:00:00:00:00:01", "radios": []any{map[string]any{"band": 5.0, "channel": 36.0, "tx_power": 12.0, "antenna_name": "Internal-9130-5GHz", "antenna_azimuth": 90.0, "antenna_elevation": -10.0}}}
	typeTransition := os.Getenv("CATC_AP_TYPE_TRANSITION") == "isolated-compatible-models"
	if typeTransition {
		entry["ap_type"] = "APIW6300DC"
		entry["position_z"] = 10.0
		entry["radios"].([]any)[0].(map[string]any)["antenna_name"] = "AIR-ANT2547V-N-5GHz"
	}
	floor["planned_access_points"] = []any{entry}
	floor["image_path"] = "maps/cvk-private-floor.png"
	cm.Data[floorImageAssetsKey] = `{"maps/cvk-private-floor.png":"floor-png"}`
	cm.BinaryData = map[string][]byte{"floor-png": liveFloorImagePNG(t, 96)}
	save := func() {
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("CATC_SITE_YAML_SOURCE") == "isolated-design" {
			raw = resolveYAMLSiteDesignFixture(t, raw)
		}
		if os.Getenv("CATC_SITE_MERGED_SOURCE") == "isolated-design" {
			raw = mergeSiteDesignFixture(t, raw)
		}
		if os.Getenv("CATC_SITE_DESIGN_DEFAULTS") == "isolated-configmap-design" {
			cm.Data[ref.Key] = resolveSiteDesignDefaultFixture(t, string(raw), &cm)
		} else {
			cm.Data[ref.Key] = string(raw)
		}
		if managedSelection {
			cm.Data[ref.Key] = string(encodeSiteSelectionFixture(t, []byte(cm.Data[ref.Key]), selection))
		}
		if err := h.a.statusWriter.Update(ctx, &cm); err != nil {
			t.Fatal(err)
		}
	}
	intents := func() (floorImageIntent, plannedAPIntent) {
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		_, ap, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
		if err != nil || len(ap) != 1 || len(images) != 1 {
			t.Fatal("private composed source did not compile", err)
		}
		return images[0], ap[0]
	}
	save()
	image, ap := intents()
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{image.synchronousOperationResource(), "Update"}, {plannedAPResource(ap), "Create"}})
	positions, err = c.plannedAPPositions(ctx, id)
	if err != nil || len(positions) != 1 {
		t.Fatal("private AP native result incomplete", err)
	}
	expected = positions[entry["name"].(string)]
	positionID = expected.ID
	t.Log("isolated AP UUID", positionID, "floor UUID", id)
	if !validAPIID(positionID) {
		t.Fatal("private AP UUID missing")
	}
	nativeRows, err := c.plannedAPNativePositions(ctx, id)
	if err != nil || len(nativeRows) != 1 {
		t.Fatal("native AP footprint unavailable", err)
	}
	expectedNative := nativeRows[expected.Name].native
	var extraExpectedNative map[string]any
	for _, radio := range expectedNative["radios"].([]any) {
		antenna := radio.(map[string]any)["antenna"].(map[string]any)
		_, present := antenna["elevationAccelerometer"]
		if _, ok := antenna["elevationAccelerometer"].(bool); !ok && (present || !typeTransition) {
			t.Fatal("qualified native antenna flag missing or changed type")
		}
	}
	checkNative := func() {
		t.Helper()
		rows, err := c.plannedAPNativePositions(ctx, id)
		count := 1
		if extraExpectedNative != nil {
			count++
		}
		if err != nil || len(rows) != count {
			t.Fatal("native AP footprint changed scope", err)
		}
		before, _ := json.Marshal(expectedNative)
		after, _ := json.Marshal(rows[expected.Name].native)
		if !bytes.Equal(before, after) {
			t.Fatal("AP edit changed unselected complete native state; retain exact fixture")
		}
		if extraExpectedNative != nil {
			before, _ := json.Marshal(extraExpectedNative)
			after, _ := json.Marshal(rows[extraExpectedNative["name"].(string)].native)
			if !bytes.Equal(before, after) {
				t.Fatal("additional AP changed complete native state; retain exact fixture")
			}
		}
	}
	*settled = true
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
		t.Fatal(err)
	}
	cm.BinaryData["floor-png"] = liveFloorImagePNG(t, 160)
	retainedSource := os.Getenv("CATC_AP_NATIVE_BASELINE") == "isolated-keyed"
	if retainedSource {
		delete(floor, "planned_access_points")
		save()
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		_, aps, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
		if err != nil || len(aps) != 0 || len(images) != 1 {
			t.Fatal("retained AP image source did not compile", err)
		}
		image = images[0]
	} else {
		save()
		image, _ = intents()
	}
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{image.synchronousOperationResource(), "Update"}})
	checkNative()
	*settled = true
	if retainedSource {
		floor["planned_access_points"] = []any{entry}
		t.Log("keyed AP omitted from source under Retain; synchronous image Update completed with unchanged complete native AP state and no AP mutation")
	}
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
		t.Fatal(err)
	}
	if typeTransition {
		entry["ap_type"] = "APIW6300AC"
	} else {
		entry["position_x"] = 5.5
	}
	save()
	_, ap = intents()
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{plannedAPResource(ap), "Update"}})
	if typeTransition {
		expectedNative["type"] = "APIW6300AC"
	} else {
		expectedNative["position"].(map[string]any)["x"] = json.Number("5.5")
	}
	checkNative()
	positions, err = c.plannedAPPositions(ctx, id)
	if err != nil || len(positions) != 1 || positions[expected.Name].ID != positionID {
		t.Fatal("private AP update changed identity", err)
	}
	expected = positions[expected.Name]
	*settled = true
	if os.Getenv("CATC_AP_NATIVE_BASELINE") == "isolated-keyed" {
		before := journalState(t, h.a)
		if os.Getenv("CATC_AP_FLOOR_COHORT") == "isolated-two-aps" {
			qualifyLiveFloorAPCohort(t, ctx, h, &cm, floor, entry, ap, id, path, expectedNative, &extraExpectedNative, save, checkNative, wait, settled, writes)
			return
		}
		if os.Getenv("CATC_AP_FLOOR_HANDOFF") == "isolated-original-task-effects" {
			h.config.Annotations[siteFloorHandoffAnnotation] = "true"
			if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
				t.Fatal(err)
			}
			originalAnchors, err := plannedAPIdentityRecords(before)
			if err != nil || len(originalAnchors) != 1 {
				t.Fatal("handoff anchor missing", err)
			}
			previousFloor := ""
			changes := []map[string]any{{"width": 14.0}, {"width": 16.0}}
			dimensions := os.Getenv("CATC_AP_FLOOR_DIMENSIONS") == "isolated-all-axes"
			if dimensions {
				changes = []map[string]any{
					{"length": 14.0},
					{"width": 16.0, "length": 18.0},
					{"height": 4.0},
					{"width": 60.0, "length": 70.0, "height": 12.0, "units_of_measure": "feet"},
				}
			}
			for _, change := range changes {
				if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
					t.Fatal(err)
				}
				for key, value := range change {
					floor[key] = value
				}
				save()
				*settled = false
				stages := []liveDesignStage{{path, "Update"}}
				heightOnly := len(change) == 1 && change["height"] != nil
				if !heightOnly {
					stages = append(stages, liveDesignStage{plannedAPResource(ap), "Update"})
				}
				runLivePrivateDesignStages(t, ctx, h, stages)
				checkNative()
				current := journalState(t, h.a)
				checkpoints, err := plannedAPFloorAuthorities(current)
				if err != nil || len(checkpoints) != 1 {
					t.Fatal("native handoff checkpoint missing", err)
				}
				latest, err := plannedAPAppliedRecords(current)
				if err != nil || len(latest) != 1 || len(current.ActionApplications) != 7 {
					t.Fatal("native handoff receipts unbounded", err)
				}
				for key, checkpoint := range checkpoints {
					parts, ok := plannedAPPrivateParts(latest[key].Operation.Resource)
					if !ok || len(parts) != 6 || !heightOnly && parts[5] != checkpoint.floor.Operation.ID || checkpoint.floor.Operation.ID == previousFloor {
						t.Fatal("native correction lost current floor authority")
					}
					previousFloor = checkpoint.floor.Operation.ID
				}
				anchors, err := plannedAPIdentityRecords(current)
				if err != nil || !reflect.DeepEqual(anchors, originalAnchors) {
					t.Fatal("handoff changed original AP observation", err)
				}
				*settled = true
			}
			for i := 0; i < 3; i++ {
				runLivePrivateDesignStages(t, ctx, h, nil)
			}
			wantWrites, wantRecoveries := int64(8), 10
			if dimensions {
				wantWrites, wantRecoveries = 11, 13
			}
			if writes.Load() != wantWrites || len(h.recovered) != wantRecoveries {
				t.Fatal("native handoff task/write counts changed", writes.Load(), len(h.recovered))
			}
			t.Log("original-task floor effect publications and AP corrections verified", "changes", len(changes), "recoveries", wantRecoveries, "designWrites", wantWrites, "boundedRows", 7, "allAxes", dimensions, "immutable anchor and complete native AP preservation")
			return
		}
		anchors, err := plannedAPIdentityRecords(before)
		if err != nil || len(anchors) != 1 {
			t.Fatal("keyed AP anchor missing", err)
		}
		latest, err := plannedAPAppliedRecords(before)
		if err != nil || len(latest) != 1 {
			t.Fatal("keyed AP applied row missing", err)
		}
		for _, r := range anchors {
			if r.keyID == "" || r.mac == "" {
				t.Fatal("keyed native AP fingerprint absent")
			}
		}
		originalWidth := floor["width"]
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
			t.Fatal(err)
		}
		floor["width"] = 14.0
		save()
		*settled = false
		for i := 0; i < 3; i++ {
			restarted := *h.a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
			t.Fatal(err)
		}
		if writes.Load() != 4 || journalState(t, h.a).Operation != nil || h.config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("keyed geometry edit escaped the pending handoff fence")
		}
		floor["width"] = originalWidth
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
			t.Fatal(err)
		}
		if typeTransition {
			entry["ap_type"] = "APIW6300DC"
		} else {
			entry["position_x"] = 6.5
		}
		save()
		_, ap = intents()
		runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{plannedAPResource(ap), "Update"}})
		if typeTransition {
			expectedNative["type"] = "APIW6300DC"
		} else {
			expectedNative["position"].(map[string]any)["x"] = json.Number("6.5")
		}
		checkNative()
		positions, err = c.plannedAPPositions(ctx, id)
		if err != nil || len(positions) != 1 || positions[expected.Name].ID != positionID {
			t.Fatal("second keyed AP update lost identity", err)
		}
		expected = positions[expected.Name]
		for i := 0; i < 3; i++ {
			runLivePrivateDesignStages(t, ctx, h, nil)
		}
		after := journalState(t, h.a)
		retained, err := plannedAPIdentityRecords(after)
		if err != nil || !reflect.DeepEqual(anchors, retained) {
			t.Fatal("keyed AP update changed original observation", err)
		}
		current, err := plannedAPAppliedRecords(after)
		if err != nil || len(current) != 1 || len(after.ActionApplications) != 7 || len(before.ActionApplications) != 7 {
			t.Fatal("keyed AP latest rows were not bounded", err)
		}
		for key, row := range current {
			if row.Operation.ID == latest[key].Operation.ID {
				t.Fatal("keyed AP current row was not replaced")
			}
		}
		if writes.Load() != 5 || len(h.recovered) != 7 {
			t.Fatal("keyed AP writes/recoveries were replayed or lost")
		}
		*settled = true
		if typeTransition {
			t.Log("native AP type-only forward/reverse transition preserved AP/radio UUIDs, coordinates, MAC and complete unselected native state")
		}
		t.Log("opt-in keyed AP observation and two original-task Updates verified; five design mutations, seven acknowledged task recoveries, immutable anchor, one bounded latest row; geometry handoff refused with zero native effects")
		return
	}
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
		t.Fatal(err)
	}
	var effectBefore plannedAPState
	effectContract := os.Getenv("CATC_AP_GEOMETRY_CONTRACT") == "isolated-floor-scaling"
	if effectContract {
		actualSites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, observed := range actualSites {
			if observed.ID == id {
				effectBefore = plannedAPState{want: ap, floorID: id, floor: observed, actual: expected, native: expectedNative}
			}
		}
		if effectBefore.floor.ID != id {
			t.Fatal("geometry contract original floor missing")
		}
	}
	floor["width"] = 14.0
	save()
	*settled = false
	// A native geometry edit may move existing drawing coordinates. Permit one
	// exact same-AP correction, then require full readback and stable no-op passes.
	observedEffect, effectMismatch := false, false
	observeEffect := func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) {
		if !effectContract || observedEffect || siteResourcePathDigest(op.Resource) != sitePathDigest(path) {
			return
		}
		observedEffect = true
		actualSites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var updated Site
		for _, row := range actualSites {
			if row.ID == id {
				updated = row
			}
		}
		rows, err := c.plannedAPNativePositions(ctx, id)
		if err != nil || len(rows) != 1 || rows[expected.Name].actual.ID != positionID {
			t.Fatal("geometry contract AP observation changed identity", err)
		}
		predicted, err := plannedAPFloorEffect(effectBefore, updated)
		if err != nil {
			t.Fatal("geometry expected-state calculation refused", err)
		}
		actual := plannedAPState{want: ap, floorID: id, floor: updated, actual: rows[expected.Name].actual, native: rows[expected.Name].native}
		beforeView, err := plannedAPBaselineView(predicted)
		if err != nil {
			t.Fatal(err)
		}
		afterView, err := plannedAPBaselineView(actual)
		if err != nil {
			t.Fatal(err)
		}
		effectMismatch = !bytes.Equal(beforeView, afterView)
		t.Log("original floor task verified before AP correction; complete predicted native effect matches", !effectMismatch)
		// Only synthetic fixture coordinates are emitted, never native bodies,
		// private values or keyed fingerprints.
		t.Log("fixture AP coordinates before", *effectBefore.actual.Position.X, *effectBefore.actual.Position.Y, *effectBefore.actual.Position.Z,
			"predicted", *predicted.actual.Position.X, *predicted.actual.Position.Y, *predicted.actual.Position.Z,
			"observed", *actual.actual.Position.X, *actual.actual.Position.Y, *actual.actual.Position.Z)
	}
	finalStages := runLivePrivateDesignStagesObserved(t, ctx, h, []liveDesignStage{{path, "Update"}, {plannedAPResource(ap), "Update"}}, observeEffect, true)
	positions, err = c.plannedAPPositions(ctx, id)
	if err != nil || len(positions) != 1 || !reflect.DeepEqual(positions[expected.Name], expected) {
		t.Fatal("geometry edit did not preserve the declared AP and native identities", err)
	}
	for i := 0; i < 3; i++ {
		runLivePrivateDesignStages(t, ctx, h, nil)
	}
	checkNative()
	t.Log("complete native AP footprint, exact identities and observed antenna sensor flag preserved across image, AP and geometry edits")
	*settled = true
	if effectContract && (!observedEffect || effectMismatch) {
		t.Error("floor effect contract not established; protected geometry handoff must remain fenced")
	}
	state = journalState(t, h.a)
	after, err := siteCreatedRecords(state)
	if err != nil || !reflect.DeepEqual(creators, after) {
		t.Fatal("design composition changed creator evidence", err)
	}
	identities, err := plannedAPIdentityRecords(state)
	if err != nil || len(identities) != 1 || identities[plannedAPResource(ap)].id != positionID {
		t.Fatal("design lost its exact observed AP identity", err)
	}
	if writes.Load() != int64(4+finalStages) || len(state.ActionApplications) != 6 {
		t.Fatal("design replayed or lost bounded receipts", writes.Load(), len(state.ActionApplications))
	}
	if os.Getenv("CATC_SITE_TASK_RECOVERY") == "isolated-acknowledged-tasks" && len(h.recovered) != 6+finalStages {
		t.Fatal("site/design retained-task recovery incomplete")
	}
	for _, row := range state.ActionApplications {
		if strings.HasPrefix(row.Operation.Resource, "floor-image-sync:") && row.Operation.TaskID != "" {
			t.Fatal("synchronous image invented a task")
		}
	}
	if managedSelection {
		before := journalState(t, h.a).DeepCopy()
		for _, options := range []nacmodel.SiteSelectionOptions{{ManageGlobalSettings: true}, selection} {
			if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&cm), &cm); err != nil {
				t.Fatal(err)
			}
			doc, err := decodeSiteSelectionDocument([]byte(cm.Data[ref.Key]))
			if err != nil {
				t.Fatal(err)
			}
			cm.Data[ref.Key] = string(encodeSiteSelectionFixture(t, doc.Model, options))
			if err := h.a.statusWriter.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
			runLivePrivateDesignStages(t, ctx, h, nil)
			after := journalState(t, h.a)
			if !reflect.DeepEqual(before, after) || writes.Load() != int64(4+finalStages) {
				beforeJSON, _ := json.Marshal(before)
				afterJSON, _ := json.Marshal(after)
				diagnostic, _ := json.MarshalIndent(map[string]any{"before": before, "after": after, "expectedWrites": 4 + finalStages, "observedWrites": writes.Load(), "sameJSON": bytes.Equal(beforeJSON, afterJSON)}, "", "  ")
				if err := os.WriteFile(filepath.Join(filepath.Dir(h.a.intentSecretPath), "selection-idle-diagnostic.json"), diagnostic, 0600); err != nil {
					t.Fatal(err)
				}
				t.Fatalf("scope-only check: same journal=%t same JSON=%t writes=%d expected=%d", reflect.DeepEqual(before, after), bytes.Equal(beforeJSON, afterJSON), writes.Load(), 4+finalStages)
			}
		}
		t.Log("selected floor-only design and empty Global scope retained all three site creators under Delete policy; re-selection emitted no native writes")
	}
	t.Log("private floor/image/AP composition: two synchronous image uploads, AP create/update and later keyed geometry update; exact mutations", writes.Load(), "geometry AP corrections", finalStages-1, "unchanged creators and bounded image/latest receipts")
}
