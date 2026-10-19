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
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

const wirelessRFModel = `{"catalyst_center":{"wireless":{"rf_profiles":[{"name":"CVK_LAB","enable_radio_type_a":true,"enable_radio_type_b":false,"enable_radio_type_c":false,"radio_type_a_properties":{"max_radio_clients":100}}]}}}`

func TestWirelessRFBuiltinObservation(t *testing.T) {
	raw, err := os.ReadFile("testdata/wireless-rf-builtins.json")
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != http.MethodGet {
			t.Error("unexpected mutation")
		}
		w.Write(raw)
	})
	profiles, err := c.wirelessRFProfiles(context.Background())
	if err != nil || len(profiles) != 3 {
		t.Fatal("built-in readback", err)
	}
	for name, profile := range profiles {
		parent, _ := getWirelessAPField(profile.fields, "radioType6GHzProperties.parentProfile")
		if parent != name || profile.unknownFields {
			t.Fatal("unexpected built-in projection", name)
		}
	}
	row := rfTestRow()
	row["radio_type_c_properties"] = map[string]any{"parent_profile": "LOW"}
	if _, err = parseWirelessRFProfiles(rfTestModel(row)); err == nil {
		t.Fatal("observation-only parent accepted as intent")
	}
}

func TestWirelessRFManifestRestartUpdate(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(wirelessRFModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessRFPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case (r.URL.Path == wirelessRFPath && r.Method == "POST") || (r.URL.Path == wirelessRFPath+"/profile-id" && r.Method == "PUT"):
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["id"] != nil || body["rfProfileName"] != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body["id"] = "profile-id"
			rows = []map[string]any{body}
			fmt.Fprint(w, `{"response":{"taskId":"profile-task"}}`)
		case r.URL.Path == taskPath+"profile-task" || r.URL.Path == taskPath+"profile-task/tree":
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "profile-task", "isError": false, "startTime": stamp, "endTime": stamp})
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
	for _, vlan := range []string{"100", "200"} {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(wirelessRFModel, "100", vlan, 1))
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
	if writes != 2 || len(rows) != 1 || rows[0]["radioTypeAProperties"].(map[string]any)["maxRadioClients"] != float64(200) {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessRFProfiles(config.Spec.Source.Inline.Raw)
	e := &wirelessRFExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "rf-profile:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != nil || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0]["radioTypeAProperties"].(map[string]any)["maxRadioClients"] = float64(100)
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"wireless"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessRFDefaultAndBuiltinPreflight(t *testing.T) {
	ctx := context.Background()
	row := rfTestRow()
	row["default_rf_profile"] = true
	desired, err := parseWirelessRFProfiles(rfTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"id": "old-id", "rfProfileName": "existing", "defaultRfProfile": true, "enableRadioTypeA": true, "enableRadioTypeB": false, "enableRadioType6GHz": false, "radioTypeAProperties": map[string]any{"maxRadioClients": 250}}
	var payload map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"response": []any{old}})
		case r.Method == http.MethodPut && r.URL.Path == wirelessRFPath+"/old-id":
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `{"response":{"taskId":"rf-task"}}`)
		default:
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	})
	e := &wirelessRFExecutor{client: c, desired: desired}
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("unmanaged default replacement allowed")
	}
	deselect := rfTestRow()
	deselect["name"] = "existing"
	e.desired, err = parseWirelessRFProfiles(rfTestModel(row, deselect))
	if err != nil {
		t.Fatal(err)
	}
	write, err := e.next(ctx)
	if err != nil || write == nil || write.resource != "rf-profile:existing" || write.action != "Update" {
		t.Fatal("default not deselected first", write, err)
	}
	if _, err = write.submit(ctx); err != nil {
		t.Fatal(err)
	}
	if payload["defaultRfProfile"] != false || payload["id"] != nil || payload["radioTypeAProperties"].(map[string]any)["maxRadioClients"] != float64(250) {
		t.Fatal("lost unowned RF field", payload)
	}
	old["radioTypeAProperties"].(map[string]any)["futureSetting"] = true
	if write, err = e.next(ctx); err == nil || write != nil {
		t.Fatal("unmapped field would be lost")
	}
	delete(old["radioTypeAProperties"].(map[string]any), "futureSetting")
	// Preflight must reject a later protected write before returning an earlier
	// otherwise valid creation from the same manifest.
	old["rfProfileName"] = "HIGH"
	old["defaultRfProfile"] = false
	deselect["name"] = "HIGH"
	deselect["enable_radio_type_b"] = true
	row["default_rf_profile"] = false
	e.desired, err = parseWirelessRFProfiles(rfTestModel(row, deselect))
	if err != nil {
		t.Fatal(err)
	}
	if write, err = e.next(ctx); err == nil || write != nil {
		t.Fatal("built-in mutation escaped preflight")
	}
}

func TestWirelessRFPartialUpdateValidatesRetainedBounds(t *testing.T) {
	ctx := context.Background()
	row := rfTestRow()
	row["name"] = "existing"
	row["radio_type_a_properties"] = map[string]any{"min_power_level": 20}
	// Earlier creation must not be returned before the invalid later update
	// has been checked against the controller's retained upper bound.
	first := rfTestRow()
	first["name"] = "AAA-new"
	desired, err := parseWirelessRFProfiles(rfTestModel(first, row))
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != http.MethodGet {
			t.Error("invalid plan caused mutation")
		}
		fmt.Fprint(w, `{"response":[{"id":"old-id","rfProfileName":"existing","defaultRfProfile":false,"enableRadioTypeA":true,"enableRadioTypeB":false,"enableRadioType6GHz":false,"radioTypeAProperties":{"minPowerLevel":0,"maxPowerLevel":10}}]}`)
	})
	e := &wirelessRFExecutor{client: c, desired: desired}
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("partial update inverted effective power bounds")
	}
}
