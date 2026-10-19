// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var floorHandoffModes = []string{"keyed-handoff-private", "keyed-handoff-recover-private", "image-keyed-handoff-private", "keyed-handoff-multiple-recover-private", "keyed-handoff-unowned-private", "keyed-handoff-disable-private", "keyed-handoff-result-drift-private", "keyed-handoff-ignored-private", "keyed-handoff-missing-task-private"}

func TestPlannedAPFloorHandoffManifest(t *testing.T) {
	for _, mode := range floorHandoffModes {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func TestFloorHandoffPredictsObservableDimensions(t *testing.T) {
	_, before, _ := plannedAPKeyedFixture(t)
	old := plannedAPTestFloor(before.floorID, before.want.floor)
	w, l, h := 41.51575, 49.616142, 10.246063
	old.Width, old.Length, old.Height = &w, &l, &h
	number, rf := 1.0, "Free Space"
	old.FloorNumber, old.RFModel = &number, &rf
	before.floor = old
	x, z := 5.5, 4.0
	before.actual.Position.X, before.actual.Position.Z = &x, &z
	before.native["position"].(map[string]any)["x"] = 5.5
	before.native["position"].(map[string]any)["z"] = 4.0
	want := desiredSite{kind: "floor", path: before.want.floor, fields: map[string]any{"floor_number": 1.0, "rf_model": "Free Space", "width": 14.0, "length": 15.123, "height": 3.123, "units_of_measure": "meters"}}
	mutation, err := nextSiteMutation([]desiredSite{want}, append(sampleSites(), old))
	if err != nil || mutation == nil {
		t.Fatal(err)
	}
	request := string(mutation.body)
	future, err := siteFloorFuture(append(sampleSites(), old), mutation)
	if err != nil || *future.UnitsOfMeasure != "feet" || *future.Width != 45.93176 || *future.Length != 49.616142 || *future.Height != 10.246063 {
		t.Fatal("observable dimension projection differs from measured native shape", err)
	}
	projected, err := plannedAPFloorEffect(before, future)
	if err != nil || *projected.actual.Position.X != 6.0850325 || *projected.actual.Position.Y != 6 || *projected.actual.Position.Z != 4 {
		t.Fatal("prebound request prediction misses the native readback effect", err)
	}
	if string(mutation.body) != request || *old.Width != 41.51575 || *old.UnitsOfMeasure != "feet" {
		t.Fatal("prediction mutated the source request or original floor")
	}
	var body map[string]any
	json.Unmarshal(mutation.body, &body)
	if body["unitsOfMeasure"] != "meters" || body["width"] != 14.0 {
		t.Fatal("prediction rewrote native write semantics")
	}
}

func TestFloorHandoffPreservesUnchangedWidthDuringLengthEdit(t *testing.T) {
	_, before, _ := plannedAPKeyedFixture(t)
	old := plannedAPTestFloor(before.floorID, before.want.floor)
	w, l, h := 41.51575, 49.616142, 10.246063
	old.Width, old.Length, old.Height = &w, &l, &h
	number, rf := 1.0, "Free Space"
	old.FloorNumber, old.RFModel = &number, &rf
	x, y, z := 5.5, 6.0, 4.0
	before.actual.Position.X, before.actual.Position.Y, before.actual.Position.Z = &x, &y, &z
	before.native["position"] = map[string]any{"x": json.Number("5.5"), "y": json.Number("6.0"), "z": json.Number("4.0")}
	before.floor = old
	want := desiredSite{kind: "floor", path: before.want.floor, fields: map[string]any{"floor_number": 1.0, "rf_model": "Free Space", "width": 12.654, "length": 14.0, "height": 3.123, "units_of_measure": "meters"}}
	mutation, err := nextSiteMutation([]desiredSite{want}, append(sampleSites(), old))
	if err != nil || mutation == nil {
		t.Fatal(err)
	}
	future, err := siteFloorFuture(append(sampleSites(), old), mutation)
	if err != nil || *future.Width != w || *future.Length != 45.93176 || *future.Height != h {
		t.Fatal("native conversion changed an unchanged extent", err)
	}
	projected, err := plannedAPFloorEffect(before, future)
	if err != nil || *projected.actual.Position.X != 5.5 || *projected.actual.Position.Y != 5.554454 || *projected.actual.Position.Z != 4 {
		t.Fatal("length-only native effect differs from measured coordinates", err)
	}
	if projected.native["position"].(map[string]any)["x"] != json.Number("5.5") || projected.native["position"].(map[string]any)["z"] != json.Number("4.0") {
		t.Fatal("untouched raw coordinates changed")
	}
}

func TestFloorHandoffCapacityBoundsAndImmutability(t *testing.T) {
	for _, mode := range []string{"bounded", "existing-latest", "count", "correction-headroom", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			state, row := siteFloorEffectsFixture(t)
			state.ActionApplications = state.ActionApplications[:2]
			op := row.Operation.DeepCopy()
			op.Phase = "Submitting"
			op.TaskID = ""
			count := 0
			if mode == "existing-latest" {
				anchor := state.ActionApplications[1]
				base, _ := plannedAPPrivateParts(anchor.Operation.Resource)
				previous := anchor.Operation.DeepCopy()
				previous.ID, previous.Action, previous.TargetID = strings.Repeat("b", 32), "Update", plannedAPIdentityTestUUID
				previous.Resource = plannedAPPrivateUpdatePrefix + strings.Join(base, ":") + ":" + siteReceiptDigest(anchor) + ":" + strings.Repeat("d", 64)
				records, err := plannedAPIdentityRecords(state)
				if err != nil {
					t.Fatal(err)
				}
				state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPAppliedResult(previous, records[plannedAPResourceKey(previous.Resource)]), Operation: *previous, VerifiedAt: anchor.VerifiedAt.DeepCopy()})
				count = 252
			}
			if mode == "count" {
				count = 254
			}
			if mode == "correction-headroom" {
				count = 253
			}
			if mode == "bytes" {
				count = 150
			}
			for i := 0; i < count; i++ {
				q := strings.Repeat("q", 128)
				other := cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), ConfigName: strings.Repeat("q", 253), ConfigUID: q, ConfigGeneration: 1, ControllerGeneration: 1, SourceUID: q, SourceVersion: q, Section: "other", Resource: strings.Repeat("q", 512), TargetID: q, ParentID: q, TaskID: q, Action: "Update", Phase: "Verified"}
				resource := fmt.Sprintf("%03d", i) + strings.Repeat("q", 509)
				if mode == "existing-latest" {
					other.ConfigName = "config"
					other.ConfigUID = "other"
					other.SourceUID = ""
					other.SourceVersion = ""
					other.Resource = "other"
					other.TargetID = ""
					other.ParentID = ""
					other.TaskID = ""
					resource = fmt.Sprintf("other-%d", i)
				}
				state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "other", Resource: resource, Operation: other})
			}
			before, _ := json.Marshal(state)
			if mode == "bytes" && len(before) <= 256<<10 {
				t.Fatal("byte-budget fixture is not above the intended boundary")
			}
			original, _ := json.Marshal(op)
			err := siteApplyCapacity(state, op)
			if (err == nil) != (mode == "bounded" || mode == "existing-latest") {
				t.Fatal("floor effect capacity did not enforce count/bytes", err)
			}
			after, _ := json.Marshal(state)
			operation, _ := json.Marshal(op)
			if string(before) != string(after) || string(original) != string(operation) {
				t.Fatal("capacity reservation rewrote original prebound proof or state")
			}
		})
	}
}

func testPlannedAPFloorHandoffStages(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, placements *[]plannedAPObservation, writes *[]string, mode string, tick, converge func()) {
	t.Helper()
	ctx := context.Background()
	editSource := func(edit func(map[string]any)) {
		t.Helper()
		raw := config.Spec.Source.Inline
		var cm corev1.ConfigMap
		var input []byte
		ref := config.Spec.Source.ConfigMapRef
		if ref != nil {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: ref.Name}, &cm); err != nil {
				t.Fatal(err)
			}
			input = []byte(cm.Data[ref.Key])
		} else {
			input = raw.Raw
		}
		var root map[string]any
		if json.Unmarshal(input, &root) != nil {
			t.Fatal("handoff model")
		}
		floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
		edit(floor)
		output, _ := json.Marshal(root)
		if ref != nil {
			cm.Data[ref.Key] = string(output)
			if err := a.statusWriter.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline.Raw = output
			config.Generation++
		}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	n := 1
	if strings.Contains(mode, "multiple") {
		editSource(func(floor map[string]any) {
			aps := floor["planned_access_points"].([]any)
			raw, _ := json.Marshal(aps[0])
			var other map[string]any
			json.Unmarshal(raw, &other)
			other["name"], other["position_x"], other["mac_address"] = "AP2", 8.0, "02:00:00:00:00:02"
			floor["planned_access_points"] = append(aps, other)
		})
		converge()
		n = 2
	}
	before := len(*writes)
	anchorsBefore, err := plannedAPIdentityRecords(journalState(t, a))
	if err != nil || len(anchorsBefore) != n {
		t.Fatal("handoff original anchors", err)
	}
	setWidth := func(width float64) {
		config.Annotations[siteFloorHandoffAnnotation] = "true"
		editSource(func(floor map[string]any) { floor["width"] = width })
	}
	if strings.Contains(mode, "unowned") {
		raw, _ := json.Marshal((*placements)[0])
		var other plannedAPObservation
		json.Unmarshal(raw, &other)
		other.ID, other.Name = "33333333-3333-4333-8333-333333333333", "unowned"
		other.Radios[0].ID = "unowned-radio"
		*placements = append(*placements, other)
	}
	setWidth(12)
	negative := strings.Contains(mode, "unowned") || strings.Contains(mode, "disable") || strings.Contains(mode, "result-drift") || strings.Contains(mode, "ignored") || strings.Contains(mode, "missing-task")
	if negative {
		for i := 0; i < 12; i++ {
			tick()
		}
		state := journalState(t, a)
		pre := strings.Contains(mode, "unowned") || strings.Contains(mode, "disable")
		want := before + 1
		if pre {
			want = before
		}
		if len(*writes) != want || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("handoff refusal mutated/replayed or declared convergence", len(*writes), want)
		}
		if pre {
			if state.Operation != nil || strings.Contains(mode, "disable") && config.Annotations[siteFloorHandoffAnnotation] != "false" {
				phase := "none"
				if state.Operation != nil {
					phase = state.Operation.Phase
				}
				reasons := []string{}
				for _, c := range config.Status.Conditions {
					reasons = append(reasons, c.Reason)
				}
				t.Fatal("unowned/disabled handoff left a journal operation or lost opt-out", phase, reasons)
			}
		} else {
			phase := "Verifying"
			if strings.Contains(mode, "missing-task") {
				phase = "Unknown"
			}
			if state.Operation == nil || state.Operation.Phase != phase {
				t.Fatal("uncertain handoff lost its fence")
			}
			rows, err := siteAppliedRecords(state)
			if err != nil || len(rows) != 0 {
				t.Fatal("mismatched floor effects published", err)
			}
		}
		return
	}
	converge()
	first, err := plannedAPFloorAuthorities(journalState(t, a))
	if err != nil || len(first) != n || len(*writes) != before+1+n {
		t.Fatal("first automatic floor/AP handoff failed", err)
	}
	var firstID string
	for _, r := range first {
		firstID = r.floor.Operation.ID
	}
	setWidth(14)
	converge()
	for i := 0; i < 3; i++ {
		tick()
	}
	state := journalState(t, a)
	latest, err := plannedAPFloorAuthorities(state)
	if err != nil || len(latest) != n {
		t.Fatal(err)
	}
	for _, r := range latest {
		if r.floor.Operation.ID == firstID {
			t.Fatal("later floor checkpoint was not advanced")
		}
	}
	anchors, err := plannedAPIdentityRecords(state)
	wantRows := 2 + 2*n
	if strings.HasPrefix(mode, "image") {
		wantRows++
	}
	if err != nil || !reflect.DeepEqual(anchorsBefore, anchors) || len(*writes) != before+2*(1+n) || len(state.ActionApplications) != wantRows || *(*placements)[0].Position.X != 4.5 || n == 2 && *(*placements)[1].Position.X != 8 {
		t.Fatal("handoff lost desired coordinates/anchors, grew receipts or replayed", err)
	}
}
