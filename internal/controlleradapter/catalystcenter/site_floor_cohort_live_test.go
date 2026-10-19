// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Qualify cohort membership, post-checkpoint creation, Retain/source omission
// and independent later checkpoint consumption through the production planner.
func qualifyLiveFloorAPCohort(t *testing.T, ctx context.Context, h *liveSiteManifest, cm *corev1.ConfigMap, floor, entry map[string]any, ap plannedAPIntent, id, path string, mainNative map[string]any, extraNative *map[string]any, save, checkNative func(), wait func(context.Context, Task, time.Time) error, settled *bool, writes *atomic.Int64) {
	t.Helper()
	c := h.a.client
	h.config.Annotations[siteFloorHandoffAnnotation] = "true"
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
			t.Fatal(err)
		}
	}
	firstAnchors, err := plannedAPIdentityRecords(journalState(t, h.a))
	if err != nil || len(firstAnchors) != 1 {
		t.Fatal("cohort initial anchor", err)
	}
	refresh()
	floor["width"] = 14.0
	save()
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{path, "Update"}, {plannedAPResource(ap), "Update"}})
	checkNative()
	*settled = true
	initialFloors, err := plannedAPFloorAuthorities(journalState(t, h.a))
	if err != nil || len(initialFloors) != 1 {
		t.Fatal("first cohort checkpoint", err)
	}
	var firstEpoch string
	for _, record := range initialFloors {
		firstEpoch = record.floor.Operation.ID
	}
	raw, _ := json.Marshal(entry)
	var second map[string]any
	if json.Unmarshal(raw, &second) != nil {
		t.Fatal("second AP intent")
	}
	second["name"], second["position_x"], second["position_y"], second["mac_address"] = "CVK-Private-Planned-AP-2", 8.0, 7.0, "02:00:00:00:00:02"
	floor["planned_access_points"] = []any{entry, second}
	refresh()
	save()
	loadAP := func() plannedAPIntent {
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		_, aps, _, err := parseSitesWithFloorImages(source.raw, source.floorImages)
		if err != nil || len(aps) != 2 {
			t.Fatal("cohort model", err)
		}
		for _, want := range aps {
			if want.payload["name"] == second["name"] {
				return want
			}
		}
		t.Fatal("second AP intent absent")
		return plannedAPIntent{}
	}
	ap2 := loadAP()
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{plannedAPResource(ap2), "Create"}})
	rows, err := c.plannedAPNativePositions(ctx, id)
	if err != nil || len(rows) != 2 {
		t.Fatal("post-checkpoint AP creation", err)
	}
	other := rows[second["name"].(string)]
	if !canonicalTemplateUUID(other.actual.ID) {
		t.Fatal("second AP observed UUID")
	}
	otherExpected := other.actual
	*extraNative = other.native
	// Registered after the primary AP cleanup: delete only this exact AP first,
	// verify one unchanged primary remains, then let the existing cleanup run.
	t.Cleanup(func() {
		if !*settled {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		*settled = false
		current, err := c.plannedAPNativePositions(cleanup, id)
		if err != nil || len(current) != 2 {
			t.Error("cohort cleanup membership changed", err)
			return
		}
		for _, expected := range []map[string]any{mainNative, *extraNative} {
			before, _ := json.Marshal(expected)
			after, _ := json.Marshal(current[expected["name"].(string)].native)
			if !bytes.Equal(before, after) {
				t.Error("cohort cleanup complete state changed")
				return
			}
		}
		if !reflect.DeepEqual(current[otherExpected.Name].actual, otherExpected) {
			t.Error("cohort cleanup UUID/state changed")
			return
		}
		at := time.Now()
		raw, err := c.mutate(cleanup, http.MethodDelete, plannedAPPositionsPath(id)+"/"+otherExpected.ID, nil)
		if err != nil {
			t.Error("cohort cleanup uncertain; no replay", err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("cohort cleanup missing original receipt")
			return
		}
		if err := wait(cleanup, receipt.Response, at); err != nil {
			t.Error(err)
			return
		}
		current, err = c.plannedAPNativePositions(cleanup, id)
		if err != nil || len(current) != 1 || current[otherExpected.Name].actual.ID != "" {
			t.Error("second AP cleanup not verified", err)
			return
		}
		*extraNative = nil
		*settled = true
		t.Log("exact second AP UUID removed; unchanged primary remains for existing cleanup")
	})
	checkNative()
	*settled = true
	state := journalState(t, h.a)
	floors, err := plannedAPFloorAuthorities(state)
	if err != nil || len(floors) != 1 {
		t.Fatal("new AP changed historical checkpoint", err)
	}
	anchors, err := plannedAPIdentityRecords(state)
	if err != nil || len(anchors) != 2 {
		t.Fatal("second AP observation missing", err)
	}
	for key, original := range firstAnchors {
		if !reflect.DeepEqual(anchors[key], original) {
			t.Fatal("new AP changed original anchor")
		}
	}
	second["position_x"] = 9.0
	refresh()
	save()
	ap2 = loadAP()
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{plannedAPResource(ap2), "Update"}})
	(*extraNative)["position"].(map[string]any)["x"] = json.Number("9.0")
	current, err := c.plannedAPPositions(ctx, id)
	if err != nil || len(current) != 2 || current[otherExpected.Name].ID != otherExpected.ID {
		t.Fatal("second AP edit changed identity", err)
	}
	otherExpected = current[otherExpected.Name]
	checkNative()
	*settled = true
	latest, err := plannedAPAppliedRecords(journalState(t, h.a))
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := plannedAPPrivateParts(latest[plannedAPResource(ap2)].Operation.Resource)
	if !ok || len(parts) != 5 {
		t.Fatal("AP created after checkpoint did not use its own baseline")
	}
	stableOther := *extraNative
	// Omit the second AP from intent during the next floor resize. Retain still
	// includes it in preflight, effect publication and private baseline checking.
	floor["planned_access_points"] = []any{entry}
	floor["width"] = 16.0
	refresh()
	save()
	source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
	if err != nil {
		t.Fatal(err)
	}
	desired, _, _, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := nextSiteMutation(desired, sites)
	if err != nil {
		t.Fatal(err)
	}
	future, err := siteFloorFuture(sites, mutation)
	if err != nil {
		t.Fatal(err)
	}
	var old Site
	for _, site := range sites {
		if site.ID == id {
			old = site
		}
	}
	predicted, err := plannedAPFloorEffect(plannedAPState{want: ap2, floorID: id, floor: old, actual: otherExpected, native: stableOther}, future)
	if err != nil {
		t.Fatal(err)
	}
	*settled = false
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{path, "Update"}, {plannedAPResource(ap), "Update"}})
	*extraNative = predicted.native
	checkNative()
	state = journalState(t, h.a)
	floors, err = plannedAPFloorAuthorities(state)
	if err != nil || len(floors) != 2 || len(state.ActionApplications) != 9 {
		t.Fatal("retained cohort checkpoint incomplete/unbounded", err)
	}
	latest, err = plannedAPAppliedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	for key, checkpoint := range floors {
		if checkpoint.floor.Operation.ID == firstEpoch {
			t.Fatal("floor checkpoint epoch did not advance")
		}
		parts, ok := plannedAPPrivateParts(latest[key].Operation.Resource)
		if key == plannedAPResource(ap2) {
			if !ok || len(parts) != 5 {
				t.Fatal("omitted retained AP was incorrectly corrected")
			}
		} else if !ok || len(parts) != 6 || parts[5] != checkpoint.floor.Operation.ID {
			t.Fatal("primary AP did not independently consume cohort checkpoint")
		}
	}
	floor["planned_access_points"] = []any{entry, second}
	refresh()
	save()
	ap2 = loadAP()
	runLivePrivateDesignStages(t, ctx, h, []liveDesignStage{{plannedAPResource(ap2), "Update"}})
	*extraNative = stableOther
	checkNative()
	*settled = true
	for i := 0; i < 3; i++ {
		runLivePrivateDesignStages(t, ctx, h, nil)
	}
	state = journalState(t, h.a)
	after, err := plannedAPIdentityRecords(state)
	if err != nil || !reflect.DeepEqual(anchors, after) || len(state.ActionApplications) != 9 || writes.Load() != 11 || len(h.recovered) != 13 {
		t.Fatal("cohort lifecycle changed anchors, grew rows or replayed", err, writes.Load(), len(h.recovered))
	}
	latest, err = plannedAPAppliedRecords(state)
	if err != nil || len(latest) != 2 {
		t.Fatal("cohort latest rows", err)
	}
	for key, row := range latest {
		parts, ok := plannedAPPrivateParts(row.Operation.Resource)
		if !ok || len(parts) != 6 || parts[5] != floors[key].floor.Operation.ID {
			t.Fatal("cohort final independent authority")
		}
	}
	t.Log("two-AP native cohort: post-checkpoint create/edit, retained source omission during resize and independent reintroduction correction; thirteen original-task recoveries, eleven design writes, nine bounded rows, unchanged complete native state and anchors")
}
