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

func TestWirelessOpenSSIDManifestWithoutCredentials(t *testing.T) {
	exerciseWirelessOpenSSIDManifestWithoutCredentials(t, nil)
}

func exerciseWirelessOpenSSIDManifestWithoutCredentials(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	ctx := context.Background()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{"wireless"}
	row := ssidTestRow("OPEN-LAB")
	row["enabled"] = false
	c.Spec.Source.Inline.Raw = ssidTestModel(row)
	if install != nil {
		install(a, c)
	} else {
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	rows := []map[string]any{}
	writes := 0
	tasks := map[string]int64{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.Method == http.MethodGet && r.URL.Path == wirelessSSIDPath("global"):
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == http.MethodPost && r.URL.Path == wirelessSSIDPath("global") || r.Method == http.MethodPut && r.URL.Path == wirelessSSIDPath("global")+"/ssid-id":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if body["isEnabled"] != false {
				t.Error("qualification SSID enabled")
			}
			body["id"] = "ssid-id"
			rows = []map[string]any{body}
			writes++
			id := fmt.Sprintf("task-%d", writes)
			tasks[id] = time.Now().UnixMilli()
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimPrefix(r.URL.Path, taskPath)
			stamp, ok := tasks[id]
			if !ok {
				t.Error("unknown task", id)
				w.WriteHeader(500)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "isError": false, "startTime": stamp, "endTime": stamp}})
		default:
			t.Errorf("unexpected native request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	settle := func() {
		t.Helper()
		for range 25 {
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
		t.Fatal("OPEN SSID metadata did not converge", c.Status.Conditions, journalState(t, a))
	}
	settle()
	if writes != 1 {
		t.Fatal("unexpected OPEN create count", writes)
	}
	row["name"] = "OPEN-LAB"
	row["session_timeout"] = 600
	c.Generation++
	c.Spec.Source.Inline.Raw = ssidTestModel(row)
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	settle()
	settle()
	if writes != 2 || journalState(t, a).Operation != nil || len(journalState(t, a).CredentialApplications) != 0 {
		t.Fatal("metadata OPEN update replayed or invented credential ownership", writes)
	}
}
