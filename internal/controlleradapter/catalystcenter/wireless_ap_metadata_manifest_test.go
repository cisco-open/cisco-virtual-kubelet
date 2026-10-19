// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessAPCalendarManifestWithoutCredentials(t *testing.T) {
	exerciseWirelessAPCalendarManifestWithoutCredentials(t, nil)
}

func exerciseWirelessAPCalendarManifestWithoutCredentials(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	ctx := context.Background()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{"wireless"}
	c.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"wireless":{"power_profiles":[{"name":"POWER1","rules":[{"interface_type":"ETHERNET","interface_id":"GIGABITETHERNET0","parameter_type":"SPEED","parameter_value":"1000MBPS"}]},{"name":"POWER2","rules":[{"interface_type":"ETHERNET","interface_id":"GIGABITETHERNET0","parameter_type":"SPEED","parameter_value":"1000MBPS"}]}],"ap_profiles":[{"name":"AP","time_zone":"Controller","country_code":"US","calendar_power_profiles":[{"power_profile":"POWER1","scheduler_type":"DAILY","scheduler_start_time":"22:00","scheduler_end_time":"23:00"}]}]}}}`)
	if install != nil {
		install(a, c)
	} else {
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	powers := []wirelessPowerProfile{}
	aps := []map[string]any{}
	writes := []string{}
	tasks := map[string]int64{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessPowerPath && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"response": powers})
		case r.URL.Path == wirelessAPPath && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"response": aps})
		case r.URL.Path == wirelessPowerPath && r.Method == http.MethodPost:
			var p wirelessPowerProfile
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			p.ID = "id-" + p.Name
			powers = append(powers, p)
			writes = append(writes, p.Name)
			id := "task-" + p.Name
			tasks[id] = time.Now().UnixMilli()
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		case (r.URL.Path == wirelessAPPath && r.Method == http.MethodPost) || (r.URL.Path == wirelessAPPath+"/ap-id" && r.Method == http.MethodPut):
			if len(powers) != 2 {
				t.Error("AP preceded declared power prerequisites")
				w.WriteHeader(500)
				return
			}
			var p map[string]any
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			p["id"] = "ap-id"
			aps = []map[string]any{p}
			label := "AP"
			if r.Method == http.MethodPut {
				label = "AP-update"
			}
			writes = append(writes, label)
			id := "task-" + label
			tasks[id] = time.Now().UnixMilli()
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := tasks[id]
			if !ok {
				t.Error("unknown native task", id)
				w.WriteHeader(500)
				return
			}
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "isError": false, "startTime": stamp, "endTime": stamp})
		default:
			t.Errorf("unexpected native request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	settle := func() {
		t.Helper()
		for range 40 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && c.Status.ObservedGeneration == c.Generation {
				return
			}
		}
		t.Fatal("metadata-only AP manifest did not converge", c.Status.Conditions, journalState(t, a))
	}
	settle()
	if !reflect.DeepEqual(writes, []string{"POWER1", "POWER2", "AP"}) {
		t.Fatal("unexpected mutation order", writes)
	}
	c.Generation++
	c.Spec.Source.Inline.Raw = []byte(strings.Replace(string(c.Spec.Source.Inline.Raw), "23:00", "23:30", 1))
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	settle()
	settle()
	if !reflect.DeepEqual(writes, []string{"POWER1", "POWER2", "AP", "AP-update"}) {
		t.Fatal("metadata update replayed", writes)
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.CredentialApplications) != 0 {
		t.Fatal("metadata-only AP retained operation or invented credential receipt")
	}
}
