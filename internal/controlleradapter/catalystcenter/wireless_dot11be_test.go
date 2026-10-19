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

const wirelessDot11beModel = `{"catalyst_center":{"wireless":{"dot11be_profiles":[{"name":"CVK_LAB","ofdma_multi_ru":false}]}}}`

func TestWirelessDot11beManifestRestartUpdate(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(wirelessDot11beModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []wirelessDot11beProfile{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessDot11bePath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case (r.URL.Path == wirelessDot11bePath && r.Method == "POST") || (r.URL.Path == wirelessDot11bePath+"/profile-id" && r.Method == "PUT"):
			var body wirelessDot11beProfile
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "" || body.Name != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body.ID = "profile-id"
			rows = []wirelessDot11beProfile{body}
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
	for _, vlan := range []string{"false", "true"} {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(wirelessDot11beModel, "false", vlan, 1))
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
	if writes != 2 || len(rows) != 1 || !rows[0].MultiRU {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessDot11be(config.Spec.Source.Inline.Raw)
	e := &wirelessDot11beExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "dot11be-profile:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != nil || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].MultiRU = false
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"wireless"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessDot11beValidation(t *testing.T) {
	desired, err := parseWirelessDot11be([]byte(wirelessDot11beModel))
	if err != nil || len(desired) != 1 {
		t.Fatal(err)
	}
	p := desired[0]
	if !p.Down || !p.Up || p.MIMODown || p.MIMOUp || p.MultiRU {
		t.Fatal("pinned defaults", p)
	}
	for _, value := range []string{`"false"`, `null`, `0`} {
		if _, err := parseWirelessDot11be([]byte(strings.Replace(wirelessDot11beModel, "false", value, 1))); err == nil {
			t.Fatal("nonboolean accepted")
		}
	}
	for _, key := range []string{"default", "mloGroup", "unknown"} {
		raw := strings.Replace(wirelessDot11beModel, `"name":"CVK_LAB"`, `"name":"CVK_LAB","`+key+`":false`, 1)
		if _, err := parseWirelessDot11be([]byte(raw)); err == nil {
			t.Fatal("unpinned field accepted")
		}
	}
}

func TestWirelessDot11bePreservesNewerFields(t *testing.T) {
	desired, _ := parseWirelessDot11be([]byte(wirelessDot11beModel))
	actual := desired[0]
	actual.ID = "profile-id"
	actual.MultiRU = true
	flag := false
	actual.Default = &flag
	actual.MLO = json.RawMessage(`{"primary24GhzEnable":true,"primary5GhzEnable":false}`)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"response": []wirelessDot11beProfile{actual}})
			return
		}
		if r.Method != "PUT" || r.URL.Path != wirelessDot11bePath+"/profile-id" {
			t.Error("unexpected mutation")
		}
		var body wirelessDot11beProfile
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Default == nil || *body.Default || string(body.MLO) != string(actual.MLO) {
			t.Error("newer fields changed")
		}
		fmt.Fprint(w, `{"response":{"taskId":"profile-task"}}`)
	})
	e := &wirelessDot11beExecutor{client: c, desired: desired}
	write, err := e.next(context.Background())
	if err != nil || write == nil {
		t.Fatal(err)
	}
	if _, err = write.submit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWirelessDot11beMissingBooleanRefused(t *testing.T) {
	for _, field := range []string{"ofdmaDownLink", "ofdmaUpLink", "muMimoDownLink", "muMimoUpLink", "ofdmaMultiRu"} {
		for _, missing := range []bool{true, false} {
			row := map[string]any{"id": "profile-id", "profileName": "CVK_LAB", "ofdmaDownLink": true, "ofdmaUpLink": true, "muMimoDownLink": false, "muMimoUpLink": false, "ofdmaMultiRu": false}
			if missing {
				delete(row, field)
			} else {
				row[field] = nil
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{row}})
			})
			if _, err := c.wirelessDot11beProfiles(context.Background()); err == nil {
				t.Fatal("missing boolean accepted", field, missing)
			}
		}
	}
}
