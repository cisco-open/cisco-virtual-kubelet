// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessNoDispatchReplan(t *testing.T) {
	for _, entry := range definitionRecoveryCases {
		for _, mode := range []string{"field-edit", "read-error", "lost-ack"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) { exerciseWirelessNoDispatchReplan(t, entry, mode, nil) })
		}
	}
}
func exerciseWirelessNoDispatchReplan(t *testing.T, entry definitionRecoveryCase, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(entry.model, entry.from, entry.to, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	rows := []map[string]any{}
	writes, sequence := 0, 0
	active, injected := false, false
	stamps := map[string]int64{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == entry.path && r.Method == http.MethodGet:
			if active && !injected {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
					injected = true
					if mode == "read-error" {
						w.WriteHeader(503)
						return
					}
					if mode == "field-edit" {
						switch entry.name {
						case "anchor_groups":
							rows[0]["mobilityAnchors"].([]any)[0].(map[string]any)["anchorPriority"] = "TERTIARY"
						case "interfaces":
							rows[0]["vlanId"] = 3996
						case "ap_profiles":
							rows[0]["description"] = "external preserved description"
						case "power_profiles":
							rows[0]["description"] = "external preserved description"
						case "dot11be_profiles":
							rows[0]["mloGroup"] = map[string]any{"retained": true}
						case "security_settings":
							rows[0]["walledGardenUrls"] = []string{"external.example"}
						case "rf_profiles":
							rows[0]["radioTypeAProperties"].(map[string]any)["maxRadioClients"] = 150
						}
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.URL.Path == entry.path && r.Method == http.MethodPost || r.URL.Path == entry.path+"/native-id" && r.Method == http.MethodPut:
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid body")
				w.WriteHeader(400)
				return
			}
			writes++
			sequence++
			id := fmt.Sprintf("nd-task-%d", sequence)
			stamps[id] = time.Now().UnixMilli()
			body["id"] = "native-id"
			rows = []map[string]any{body}
			if active && mode == "lost-ack" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				fmt.Fprintf(w, `{"response":{"taskId":%q}}`, id)
			}
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := stamps[id]
			if !ok {
				t.Error("unbound task")
				w.WriteHeader(404)
				return
			}
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	seed, err := a.client.compileWirelessConfiguration([]byte(entry.model))
	if err != nil {
		t.Fatal(err)
	}
	write, err := seed.next(ctx)
	if err != nil || write == nil {
		t.Fatal("seed plan", err)
	}
	if _, err = write.submit(ctx); err != nil {
		t.Fatal("seed", err)
	}
	writes = 0
	active = true
	key := ctrlclient.ObjectKeyFromObject(config)
	sawReplan, converged := false, false
	var lastResult ctrl.Result
	tick := func() {
		t.Helper()
		restarted := *a
		result, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatal(err)
		}
		lastResult = result
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	for range 30 {
		tick()
		for _, condition := range config.Status.Conditions {
			if condition.Reason == "NativePreflightRefused" {
				if writes != 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting || lastResult.RequeueAfter <= 0 {
					t.Fatal("no-dispatch status after a write", writes, config.Status)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.Owners) != 1 {
					t.Fatal("no-dispatch retirement lost exact ownership")
				}
				sawReplan = true
			}
		}
		if mode == "lost-ack" && injected {
			for range 3 {
				tick()
			}
			break
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			converged = true
			break
		}
	}
	if !injected || writes != 1 {
		t.Fatal("incorrect injection/native count", injected, writes)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "lost-ack" {
		if sawReplan || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Unknown" || nc.Status.Configuration.Operation.TaskID != "" {
			t.Fatal("lost acknowledgement was unfenced")
		}
	} else {
		if !sawReplan || !converged || nc.Status.Configuration.Operation != nil {
			t.Fatal("automatic fresh replan failed", sawReplan, config.Status)
		}
		tick()
		if writes != 1 {
			t.Fatal("converged mutation replayed")
		}
	}
}
