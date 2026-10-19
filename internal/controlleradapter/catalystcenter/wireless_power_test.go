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

const wirelessPowerModel = `{"catalyst_center":{"wireless":{"power_profiles":[{"name":"CVK_LAB","rules":[{"interface_type":"ETHERNET","interface_id":"GIGABITETHERNET0","parameter_type":"SPEED","parameter_value":"100MBPS"}]}]}}}`

func TestWirelessPowerManifestRestartUpdate(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(wirelessPowerModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []wirelessPowerProfile{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessPowerPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case (r.URL.Path == wirelessPowerPath && r.Method == "POST") || (r.URL.Path == wirelessPowerPath+"/power-id" && r.Method == "PUT"):
			var body wirelessPowerProfile
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "" || body.Name != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body.ID = "power-id"
			rows = []wirelessPowerProfile{body}
			fmt.Fprint(w, `{"response":{"taskId":"power-task"}}`)
		case r.URL.Path == taskPath+"power-task" || r.URL.Path == taskPath+"power-task/tree":
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "power-task", "isError": false, "startTime": stamp, "endTime": stamp})
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
	for _, vlan := range []string{"100MBPS", "1000MBPS"} {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(wirelessPowerModel, "100MBPS", vlan, 1))
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
	if writes != 2 || len(rows) != 1 || rows[0].Rules[0].ParameterValue != "1000MBPS" {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessPower(config.Spec.Source.Inline.Raw)
	e := &wirelessPowerExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "power-profile:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != nil || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].Rules[0].ParameterValue = "100MBPS"
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"wireless"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessPowerModelAndOrdering(t *testing.T) {
	for _, pair := range [][2]string{{`"ETHERNET"`, `"invalid"`}, {`"GIGABITETHERNET0"`, `"bad"`}, {`"SPEED"`, `null`}, {`"100MBPS"`, `100`}, {`"name":"CVK_LAB"`, `"name":"CVK_LAB","extra":true`}} {
		if _, err := parseWirelessPower([]byte(strings.Replace(wirelessPowerModel, pair[0], pair[1], 1))); err == nil {
			t.Fatal("invalid field accepted", pair)
		}
	}
	p, err := parseWirelessPower([]byte(wirelessPowerModel))
	if err != nil {
		t.Fatal(err)
	}
	first := p[0].Rules[0]
	second := first
	second.ParameterValue = "1000MBPS"
	want := p[0]
	want.Rules = []wirelessPowerRule{first, second}
	got := want
	got.Rules = []wirelessPowerRule{second, first}
	if wirelessPowerMatches(want, got) {
		t.Fatal("ordered rules treated as a set")
	}
	desc := "existing"
	got = want
	got.Description = &desc
	if !wirelessPowerMatches(want, got) {
		t.Fatal("omitted description acquired ownership")
	}
	empty := ""
	want.Description = &empty
	if wirelessPowerMatches(want, got) {
		t.Fatal("explicit description clearing ignored")
	}
}

func TestWirelessPowerDescriptionPayload(t *testing.T) {
	for _, clear := range []bool{false, true} {
		desired, _ := parseWirelessPower([]byte(wirelessPowerModel))
		actual := desired[0]
		actual.ID = "power-id"
		old := "retained"
		actual.Description = &old
		actual.Rules = append([]wirelessPowerRule(nil), actual.Rules...)
		actual.Rules[0].ParameterValue = "1000MBPS"
		if clear {
			empty := ""
			desired[0].Description = &empty
		}
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]any{"response": []wirelessPowerProfile{actual}})
				return
			}
			if r.Method != "PUT" || r.URL.Path != wirelessPowerPath+"/power-id" {
				t.Error("unexpected mutation")
			}
			var payload wirelessPowerProfile
			if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Description == nil {
				t.Error("description missing")
			} else if clear && *payload.Description != "" || !clear && *payload.Description != "retained" {
				t.Error("description ownership changed")
			}
			fmt.Fprint(w, `{"response":{"taskId":"power-task"}}`)
		})
		e := &wirelessPowerExecutor{client: c, desired: desired}
		write, err := e.next(context.Background())
		if clear {
			if err == nil || write != nil {
				t.Fatal("native-invalid blank description planned")
			}
			continue
		}
		if err != nil || write == nil {
			t.Fatal(err)
		}
		if _, err = write.submit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWirelessPowerCombinedPreflight(t *testing.T) {
	raw := strings.Replace(wirelessPowerModel, `"wireless":{`, `"wireless":{"interfaces":[{"name":"new","vlan_id":123}],`, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutation before full preflight")
		}
		if r.URL.Path == wirelessInterfacesPath {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		if r.URL.Path != wirelessPowerPath {
			t.Error("unexpected endpoint")
		}
		fmt.Fprint(w, `{"response":[{"id":"broken","profileName":"CVK_LAB"}]}`)
	})
	e, err := c.compileWirelessConfiguration([]byte(raw))
	if err != nil || e.count() != 2 {
		t.Fatal(err)
	}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("interface write released despite missing power rules")
	}
}
