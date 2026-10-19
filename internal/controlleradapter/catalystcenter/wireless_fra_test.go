// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

const wirelessFRAModel = `{"catalyst_center":{"feature_templates":{"wireless":{"rrm_fra":[{"name":"CVK_LAB","radio_band":"2.4GHz_5GHz","fra_interval":3}]}}}}`

func TestWirelessFRAManifestRestartUpdate(t *testing.T) {
	exerciseWirelessFRAManifestRestartUpdate(t, nil)
}

func exerciseWirelessFRAManifestRestartUpdate(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	config.Spec.Source.Inline.Raw = []byte(wirelessFRAModel)
	if install != nil {
		install(a, config)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []wirelessFRA{}
	writes := 0
	stamp := int64(0)
	verb := "created"
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessFeatureSummaryPath && r.Method == "GET":
			instances := []wirelessFeatureInstance{}
			flag := false
			for _, row := range rows {
				instances = append(instances, wirelessFeatureInstance{ID: row.ID, Name: row.Name, System: &flag})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": wirelessFRAKind, "count": len(instances), "instances": instances}}})
		case r.URL.Path == wirelessFRAPath+"/"+featureTestFRAUUID && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows[0]})
		case (r.URL.Path == wirelessFRAPath && r.Method == "POST") || (r.URL.Path == wirelessFRAPath+"/"+featureTestFRAUUID && r.Method == "PUT"):
			var body wirelessFRA
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "" || body.Name != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			verb = "created"
			if r.Method == http.MethodPut {
				verb = "updated"
			}
			stamp = time.Now().UnixMilli()
			body.ID = featureTestFRAUUID
			rows = []wirelessFRA{body}
			fmt.Fprint(w, `{"response":{"taskId":"fra-task"}}`)
		case r.URL.Path == taskPath+"fra-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "fra-task", "isError": false, "startTime": stamp, "endTime": stamp, "serviceType": "NCNC", "data": featureTestFRAUUID, "progress": "Successfully " + verb + " model config design with name CVK_LAB"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range wirelessFRAQualificationStates() {
		config.Generation++
		band := "2.4GHz_5GHz"
		if state.Band == "5GHZ_6GHZ" {
			band = "5GHz_6GHz"
		}
		desired := map[string]any{"name": "CVK_LAB", "radio_band": band, "fra_interval": state.Interval, "fra_freeze": state.Freeze, "fra_status": state.Status}
		if state.Sensitivity != "" {
			desired["fra_sensitivity"] = state.Sensitivity
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"feature_templates": map[string]any{"wireless": map[string]any{"rrm_fra": []any{desired}}}}})
		if err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline.Raw = raw
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("not converged", config.Status)
		}
		tick()
	}
	if writes != len(wirelessFRAQualificationStates()) || len(rows) != 1 || rows[0].Attributes.Interval != 3 || rows[0].Attributes.Band != "2_4GHZ_5GHZ" {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessFRA(config.Spec.Source.Inline.Raw)
	e := &wirelessFRAExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "rrm-fra:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != errConfigurationTransition || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].Attributes.Interval = 24
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"feature_templates"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessFRAModel(t *testing.T) {
	p, err := parseWirelessFRA([]byte(wirelessFRAModel))
	if err != nil || len(p) != 1 {
		t.Fatal(err)
	}
	if p[0].Attributes.Sensitivity != "Medium" || p[0].Attributes.Band != "2_4GHZ_5GHZ" || p[0].Attributes.Freeze || p[0].Attributes.Status {
		t.Fatal("pinned defaults")
	}
	for _, pair := range [][2]string{{`"fra_interval":3`, `"fra_interval":25`}, {`"fra_interval":3`, `"fra_interval":"3"`}, {`"name":"CVK_LAB"`, `"name":"bad/name"`}, {`"name":"CVK_LAB"`, `"name":"CVK_LAB","fra_freeze":null`}, {`"name":"CVK_LAB"`, `"name":"CVK_LAB","unlocked_attributes":[]`}} {
		if _, err := parseWirelessFRA([]byte(strings.Replace(wirelessFRAModel, pair[0], pair[1], 1))); err == nil {
			t.Fatal("invalid FRA accepted", pair)
		}
	}
	raw := strings.Replace(wirelessFRAModel, "2.4GHz_5GHz", "5GHz_6GHz", 1)
	p, err = parseWirelessFRA([]byte(raw))
	if err != nil || p[0].Attributes.Band != "5GHZ_6GHZ" || p[0].Attributes.Sensitivity != "" {
		t.Fatal("band mapping", err)
	}
}

func TestWirelessFeatureSummaryPaging(t *testing.T) {
	calls := 0
	flag := false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		calls++
		if r.URL.Path != wirelessFeatureSummaryPath || r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("type") != wirelessFRAKind {
			t.Error("wrong summary request")
		}
		rows := []wirelessFeatureInstance{}
		switch r.URL.Query().Get("offset") {
		case "1":
			for i := range 25 {
				rows = append(rows, wirelessFeatureInstance{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("name-%d", i), System: &flag})
			}
		case "26":
			rows = append(rows, wirelessFeatureInstance{ID: "last", Name: "last", System: &flag})
		default:
			t.Error("offset")
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": wirelessFRAKind, "count": 26, "instances": rows}}})
	})
	rows, err := c.wirelessFeatureInstances(context.Background(), wirelessFRAKind)
	if err != nil || len(rows) != 26 || calls != 2 {
		t.Fatal("summary pagination", len(rows), calls, err)
	}
}

func TestWirelessFRASystemPreflight(t *testing.T) {
	desired, _ := parseWirelessFRA([]byte(wirelessFRAModel))
	protected := desired[0]
	protected.ID = "system-id"
	protected.Attributes.Interval = 1
	desired = append([]wirelessFRA{{Name: "earlier-missing", Attributes: desired[0].Attributes}}, desired...)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("system mutation")
		}
		if r.URL.Path == wirelessFeatureSummaryPath {
			fmt.Fprint(w, `{"response":[{"type":"RRM_FRA_CONFIGURATION","count":1,"instances":[{"id":"system-id","designName":"CVK_LAB","systemTemplate":true}]}]}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": protected})
	})
	e := &wirelessFRAExecutor{client: c, desired: desired}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("earlier create released despite protected template drift")
	}
}

func TestWirelessFeatureSummaryRejectsIncompleteInventory(t *testing.T) {
	for _, body := range []string{
		`{"response":[{"type":"RRM_FRA_CONFIGURATION","count":2,"instances":[]}]}`,
		`{"response":[{"type":"RRM_FRA_CONFIGURATION","count":0}]}`,
		`{"response":[{"type":"OTHER","count":0,"instances":[]}]}`,
		`{"response":[{"type":"RRM_FRA_CONFIGURATION","count":1,"instances":[{"id":"one","designName":"A"}]}]}`,
		`{"response":[{"type":"RRM_FRA_CONFIGURATION","count":2,"instances":[{"id":"one","designName":"A","systemTemplate":false},{"id":"one","designName":"B","systemTemplate":false}]}]}`,
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			fmt.Fprint(w, body)
		})
		if _, err := c.wirelessFeatureInstances(context.Background(), wirelessFRAKind); err == nil {
			t.Fatal("incomplete inventory accepted", body)
		}
	}
}

func wirelessFRAQualificationStates() []wirelessFRAAttributes {
	states := []wirelessFRAAttributes{
		{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"},
		{Band: "2_4GHZ_5GHZ", Interval: 6, Sensitivity: "Medium"},
	}
	for i, sensitivity := range []string{"Low", "Medium", "High", "Higher", "Even Higher", "Super High"} {
		interval := 6
		if i == 0 {
			interval = 1
		}
		if i == 1 {
			interval = 24
		}
		states = append(states, wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: interval, Sensitivity: sensitivity, Freeze: i%2 == 0, Status: i%3 == 0})
	}
	return append(states, wirelessFRAAttributes{Band: "5GHZ_6GHZ", Interval: 24, Status: true}, wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"})
}
