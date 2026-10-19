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

const wirelessCleanAirModel = `{"catalyst_center":{"feature_templates":{"wireless":{"cleanair":[{"name":"CVK_LAB","radio_band":"2.4GHz","enable":false}]}}}}`

func TestWirelessCleanAirManifestRestartUpdate(t *testing.T) {
	exerciseWirelessCleanAirManifestRestartUpdate(t, nil)
}

func exerciseWirelessCleanAirManifestRestartUpdate(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig), recovery ...string) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	config.Spec.Source.Inline.Raw = []byte(wirelessCleanAirModel)
	if install != nil {
		install(a, config)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []wirelessCleanAir{}
	writes := 0
	stamp := int64(0)
	verb := "created"
	mode := ""
	if len(recovery) > 0 {
		mode = recovery[0]
	}
	injected := map[string]bool{}
	poisoned := false
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
			json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": wirelessCleanAirKind, "count": len(instances), "instances": instances}}})
		case r.URL.Path == wirelessCleanAirPath+"/"+featureTestCAUUID && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows[0]})
		case (r.URL.Path == wirelessCleanAirPath && r.Method == "POST") || (r.URL.Path == wirelessCleanAirPath+"/"+featureTestCAUUID && r.Method == "PUT"):
			var body wirelessCleanAir
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
			body.ID = featureTestCAUUID
			rows = []wirelessCleanAir{body}
			fmt.Fprint(w, `{"response":{"taskId":"cleanair-task"}}`)
		case r.URL.Path == taskPath+"cleanair-task":
			id, start, end := "cleanair-task", stamp, stamp
			if poisoned {
				switch mode {
				case "foreign-task":
					id = "foreign-task"
				case "stale-task":
					start -= int64(time.Hour / time.Millisecond)
				case "pending":
					end = 0
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "isError": poisoned && mode == "failed", "startTime": start, "endTime": end, "errorCode": "qualification", "serviceType": "NCNC", "data": featureTestCAUUID, "progress": "Successfully " + verb + " model config design with name CVK_LAB"}})
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
		if mode != "" {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if nc.Status.Configuration != nil {
				op := nc.Status.Configuration.Operation
				selected := op != nil && (op.Action == "Update" || mode == "create")
				if selected && op.Phase == "Accepted" && !injected[op.ID] {
					op.Phase = "Unknown"
					if mode == "create" {
						op.Resource = "cleanair:CVK_LAB"
					}
					if mode == "missing-task" {
						op.TaskID = ""
					}
					if mode == "foreign-owner" {
						op.ConfigUID = "foreign-owner"
					}
					if mode == "replacement" {
						rows[0].ID = "replacement-id"
					}
					raw, err := json.Marshal(nc)
					if err != nil {
						t.Fatal(err)
					}
					restored := &ciskov1.NetworkController{}
					if err = json.Unmarshal(raw, restored); err != nil {
						t.Fatal(err)
					}
					if err = a.statusWriter.Status().Update(ctx, restored); err != nil {
						t.Fatal(err)
					}
					injected[op.ID] = true
					poisoned = mode != "known"
					if mode == "source" {
						config.Generation++
						config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"enable":true`, `"enable":true,"description":"new-source"`, 1))
						if err = a.statusWriter.Update(ctx, config); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
	}
	for _, state := range []struct{ enable, band string }{
		{"false", "2.4GHz"}, {"true", "2.4GHz"}, {"true", "5GHz"},
		{"true", "6GHz"}, {"true", "2.4GHz"}, {"true", "6GHz"},
		{"true", "5GHz"}, {"true", "2.4GHz"},
	} {
		config.Generation++
		model := strings.Replace(wirelessCleanAirModel, "false", state.enable, 1)
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, "2.4GHz", state.band, 1))
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			tick()
			if mode != "" && mode != "known" && len(injected) > 0 {
				expectedWrites := writes
				for range 5 {
					tick()
				}
				retained := journalState(t, a).Operation
				if retained == nil || retained.Phase != "Unknown" && retained.Phase != "Verifying" || writes != expectedWrites || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("uncertain outcome escaped fence", mode, writes, config.Status, retained)
				}
				if mode == "create" && writes != 1 || mode != "create" && writes != 2 {
					t.Fatal("unexpected mutation count", mode, writes)
				}
				return
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("not converged", config.Status)
		}
		tick()
	}
	if mode == "known" && len(injected) != 7 {
		t.Fatal("missing known-task recovery", len(injected))
	}
	if writes != 8 || len(rows) != 1 || !rows[0].Attributes.Enable || rows[0].Attributes.Band != "2_4GHZ" {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessCleanAir(config.Spec.Source.Inline.Raw)
	e := &wirelessCleanAirExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "cleanair:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != errConfigurationTransition || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].Attributes.Enable = false
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"feature_templates"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessCleanAirFields(t *testing.T) {
	if len(cleanAirInterferers) != 19 {
		t.Fatal("pinned interferer count")
	}
	for key, dest := range cleanAirInterferers {
		raw := strings.Replace(wirelessCleanAirModel, `"enable":false`, `"enable":false,"interferers":{"`+key+`":false}`, 1)
		p, err := parseWirelessCleanAir([]byte(raw))
		if err != nil || len(p) != 1 {
			t.Fatal(key, err)
		}
		value, ok := p[0].Attributes.Interferers[dest]
		if !ok || value {
			t.Fatal("false lost", key)
		}
	}
	for _, pair := range [][2]string{{`"enable":false`, `"enable":null`}, {`"enable":false`, `"enable":"false"`}, {`"enable":false`, `"enable":false,"interferers":{"unknown":true}`}, {`"enable":false`, `"enable":false,"interferers":{"jammer":null}`}, {`"name":"CVK_LAB"`, `"name":"bad/name"`}} {
		if _, err := parseWirelessCleanAir([]byte(strings.Replace(wirelessCleanAirModel, pair[0], pair[1], 1))); err == nil {
			t.Fatal("invalid CleanAir accepted", pair)
		}
	}
	desired, _ := parseWirelessCleanAir([]byte(wirelessCleanAirModel))
	got := desired[0]
	description := "retained"
	got.Attributes.Description = &description
	got.Attributes.Interferers = map[string]bool{"jammer": true}
	if !wirelessCleanAirMatches(desired[0], got) {
		t.Fatal("omitted optional attributes acquired ownership")
	}
	desired[0].Attributes.Interferers = map[string]bool{"jammer": false}
	if wirelessCleanAirMatches(desired[0], got) {
		t.Fatal("explicit false ignored")
	}
}

func TestWirelessCleanAirPreservesOptionalFields(t *testing.T) {
	desired, _ := parseWirelessCleanAir([]byte(wirelessCleanAirModel))
	desired[0].Attributes.Interferers = map[string]bool{"jammer": false}
	actual := desired[0]
	actual.ID = "cleanair-id"
	description := "keep"
	actual.Attributes.Description = &description
	actual.Attributes.Interferers = map[string]bool{"jammer": true, "microwaveOven": true}
	actual.Unlocked = []string{"cleanAir"}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == wirelessFeatureSummaryPath {
			fmt.Fprint(w, `{"response":[{"type":"CLEANAIR_CONFIGURATION","count":1,"instances":[{"id":"cleanair-id","designName":"CVK_LAB","systemTemplate":false}]}]}`)
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"response": actual})
			return
		}
		if r.Method != "PUT" {
			t.Error("unexpected mutation")
		}
		var body wirelessCleanAir
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("payload")
		}
		if body.Attributes.Description == nil || *body.Attributes.Description != "keep" || !body.Attributes.Interferers["microwaveOven"] || body.Attributes.Interferers["jammer"] || len(body.Unlocked) != 1 {
			t.Error("optional fields lost")
		}
		fmt.Fprint(w, `{"response":{"taskId":"cleanair-task"}}`)
	})
	e := &wirelessCleanAirExecutor{client: c, desired: desired}
	write, err := e.next(context.Background())
	if err != nil || write == nil {
		t.Fatal(err)
	}
	if _, err = write.submit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWirelessFeaturesCombinedPreflight(t *testing.T) {
	raw := strings.Replace(wirelessCleanAirModel, `"wireless":{`, `"wireless":{"rrm_fra":[{"name":"new","radio_band":"2.4GHz_5GHz"}],`, 1)
	desired, _ := parseWirelessCleanAir([]byte(wirelessCleanAirModel))
	actual := desired[0]
	actual.ID = "system-id"
	actual.Attributes.Enable = true
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("write before complete preflight")
		}
		if r.URL.Path == wirelessFeatureSummaryPath {
			if r.URL.Query().Get("type") == wirelessFRAKind {
				fmt.Fprint(w, `{"response":[]}`)
				return
			}
			fmt.Fprint(w, `{"response":[{"type":"CLEANAIR_CONFIGURATION","count":1,"instances":[{"id":"system-id","designName":"CVK_LAB","systemTemplate":true}]}]}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": actual})
	})
	e, err := c.compileWirelessFeatureConfiguration([]byte(raw))
	if err != nil || e.count() != 2 {
		t.Fatal(err)
	}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("earlier FRA create released despite protected CleanAir drift")
	}
}

// A prepared PUT must not overwrite a concurrent native change, including
// fields omitted by the manifest or an already-converged sibling template.
func TestWirelessCleanAirDispatchFence(t *testing.T) {
	for _, scenario := range []string{"stable", "parallel", "description", "interferer", "unlocked", "identity", "system", "sibling", "appearance", "desired", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			desired, err := parseWirelessCleanAir([]byte(wirelessCleanAirModel))
			if err != nil {
				t.Fatal(err)
			}
			sibling := desired[0]
			sibling.Name = "sibling"
			desired = append(desired, sibling)
			actual := desired[0]
			actual.ID = "cleanair-id"
			actual.Attributes.Enable = true
			sibling.ID = "sibling-id"
			rows := map[string]wirelessCleanAir{actual.Name: actual, sibling.Name: sibling}
			if scenario == "appearance" {
				delete(rows, actual.Name)
			}
			system := false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessFeatureSummaryPath:
					instances := []wirelessFeatureInstance{}
					for _, row := range rows {
						instances = append(instances, wirelessFeatureInstance{ID: row.ID, Name: row.Name, System: &system})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": wirelessCleanAirKind, "count": len(instances), "instances": instances}}})
				case r.Method == http.MethodGet:
					for _, row := range rows {
						if r.URL.Path == wirelessCleanAirPath+"/"+row.ID {
							json.NewEncoder(w).Encode(map[string]any{"response": row})
							return
						}
					}
					w.WriteHeader(404)
				default:
					writes++
					if scenario == "ambiguous" {
						fmt.Fprint(w, `{}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"cleanair-task"}}`)
				}
			})
			e := &wirelessCleanAirExecutor{client: c, desired: desired}
			write, err := e.next(ctx)
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			switch scenario {
			case "description":
				value := "concurrent"
				actual.Attributes.Description = &value
			case "interferer":
				actual.Attributes.Interferers = map[string]bool{"jammer": true}
			case "unlocked":
				actual.Unlocked = []string{"cleanAir"}
			case "identity":
				actual.ID = "replacement-id"
			case "system":
				system = true
			case "sibling":
				sibling.Attributes.Enable = true
				rows[sibling.Name] = sibling
			case "desired":
				e.desired[0].Attributes.Enable = true
			}
			rows[actual.Name] = actual
			if scenario == "parallel" {
				results := make(chan error, 16)
				for range 16 {
					go func() { _, submitErr := write.submit(ctx); results <- submitErr }()
				}
				success := 0
				for range 16 {
					submitErr := <-results
					if submitErr == nil {
						success++
					} else if submitErr != errConfigurationTransition {
						t.Fatal("unexpected parallel error", submitErr)
					}
				}
				if success != 1 {
					t.Fatal("parallel permit count", success)
				}
			} else {
				_, err = write.submit(ctx)
			}
			allowed := scenario == "stable" || scenario == "parallel" || scenario == "ambiguous"
			if allowed && writes != 1 {
				t.Fatal("dispatch missing", writes, err)
			}
			if scenario == "stable" && err != nil {
				t.Fatal(err)
			}
			if !allowed && (err == nil || writes != 0) {
				t.Fatal("stale dispatch allowed", writes, err)
			}
			if _, err = write.submit(ctx); err != errConfigurationTransition {
				t.Fatal("prepared write reused", err)
			}
			if allowed && writes != 1 || !allowed && writes != 0 {
				t.Fatal("replayed", writes)
			}
		})
	}
}

func TestWirelessCleanAirRetainedTaskRecovery(t *testing.T) {
	for _, mode := range []string{"known", "missing-task", "create", "source", "foreign-owner", "replacement", "failed", "foreign-task", "stale-task", "pending"} {
		t.Run(mode, func(t *testing.T) { exerciseWirelessCleanAirManifestRestartUpdate(t, nil, mode) })
	}
}
