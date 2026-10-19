// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessSSIDMultiPSKManifestReorderAndRotate(t *testing.T) {
	qualifyWirelessSSIDMultiPSKFixture(t, false, false, false)
}
func TestWirelessSSIDMultiPSKManifestRetirement(t *testing.T) {
	qualifyWirelessSSIDMultiPSKFixture(t, true, false, false)
}
func TestWirelessSSIDUnknownWithoutTaskRemainsFenced(t *testing.T) {
	qualifyWirelessSSIDMultiPSKFixture(t, true, true, false)
}
func TestWirelessSSIDTimedOutKnownTaskRecovers(t *testing.T) {
	qualifyWirelessSSIDMultiPSKFixture(t, true, false, true)
}
func qualifyWirelessSSIDMultiPSKFixture(t *testing.T, retire, missingTask, timedOut bool) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	a.intentSecretPath = t.TempDir()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: "main-psk", Name: "ssid-keys", Key: "main"})
	for _, priority := range []int{0, 4} {
		alias := fmt.Sprintf("psk-%d", priority)
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: alias, Name: "ssid-keys", Key: alias})
	}
	if err = a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	expected := map[int]string{}
	setModel := func(order []int, versions map[int]int) {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Spec.ManagedSections = []string{"wireless"}
		config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "wireless", Path: "/ssids/0/passphrase", Source: "main-psk"}}
		expected = map[int]string{}
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: "/ssids/0/passphrase", SourceAlias: "main-psk", SecretName: "ssid-keys", SecretKey: "main"})
		if err != nil {
			t.Fatal(err)
		}
		mainPath := filepath.Join(a.intentSecretPath, relative)
		if err = os.MkdirAll(filepath.Dir(mainPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(mainPath, []byte(`{"version":1,"value":"main-fixture-secret"}`), 0600); err != nil {
			t.Fatal(err)
		}
		entries := []any{}
		for index, priority := range order {
			version := versions[priority]
			entries = append(entries, map[string]any{"priority": priority, "passphrase_type": "ASCII", "passphrase_wo_version": version})
			pointer := fmt.Sprintf("/ssids/0/multi_psk_settings/%d/passphrase_wo", index)
			alias := fmt.Sprintf("psk-%d", priority)
			config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "wireless", Path: pointer, Source: alias})
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: pointer, SourceAlias: alias, SecretName: "ssid-keys", SecretKey: alias})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.intentSecretPath, relative)
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			value := fmt.Sprintf("fixture-psk-%d-version-%d", priority, version)
			expected[priority] = value
			raw, _ := json.Marshal(map[string]any{"version": version, "value": value})
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		row := ssidTestRow("MultiPSK")
		row["auth_type"] = "WPA2_PERSONAL"
		row["auth_key_psk"] = true
		row["rsn_cipher_suite_ccmp128"] = true
		row["multi_psk_settings"] = entries
		row["passphrase_version"] = 1
		config.Spec.Source.Inline.Raw = ssidTestModel(row)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	profiles := []map[string]any{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.Method == "GET" && r.URL.Path == wirelessSSIDPath("global"):
			json.NewEncoder(w).Encode(map[string]any{"response": profiles})
		case r.Method == "POST" && r.URL.Path == wirelessSSIDPath("global") || r.Method == "PUT" && r.URL.Path == wirelessSSIDPath("global")+"/ssid-id":
			var p map[string]any
			if json.NewDecoder(r.Body).Decode(&p) != nil {
				t.Error("invalid payload")
			}
			entries, ok := p["multiPSKSettings"].([]any)
			if !ok || len(entries) != len(expected) {
				t.Error("missing keys")
			}
			for _, entry := range entries {
				key := entry.(map[string]any)
				priority := int(key["priority"].(float64))
				if key["passphrase"] != expected[priority] {
					t.Error("credential assigned to wrong priority")
				}
				// Readback cannot prove a secret's value; only task evidence and the
				// durable version binding may establish application.
				delete(key, "passphrase")
			}
			if p["passphrase"] != "main-fixture-secret" {
				t.Error("main credential missing")
			}
			delete(p, "passphrase")
			p["id"] = "ssid-id"
			profiles = []map[string]any{p}
			writes++
			stamp = time.Now().UnixMilli()
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
		case r.URL.Path == taskPath+"task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task", "isError": false, "startTime": stamp, "endTime": stamp}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	injected := map[int]bool{}
	settle := func() {
		t.Helper()
		for range 40 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if retire && state.Operation != nil && state.Operation.Phase == "Accepted" && state.Operation.Action == "Update" && !injected[writes] {
				controller, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if timedOut {
					expired := metav1.NewTime(time.Now().Add(-time.Hour))
					controller.Status.Configuration.Operation.SubmittedAt = &expired
				} else {
					controller.Status.Configuration.Operation.Phase = "Unknown"
				}
				if missingTask {
					controller.Status.Configuration.Operation.TaskID = ""
				}
				if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
					t.Fatal(err)
				}
				injected[writes] = true
			}
			if missingTask && state.Operation != nil && state.Operation.Phase == "Unknown" && state.Operation.TaskID == "" && config.Status.Phase == configapi.NetworkControllerConfigPhaseWaiting {
				return
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("did not converge", config.Status)
	}
	setModel([]int{4, 0}, map[int]int{0: 1, 4: 1})
	settle()
	if writes != 1 {
		t.Fatal("create count", writes)
	}
	setModel([]int{0, 4}, map[int]int{0: 1, 4: 1})
	settle()
	if writes != 1 {
		t.Fatal("reorder replayed write")
	}
	setModel([]int{0, 4}, map[int]int{0: 1, 4: 2})
	settle()
	if writes != 2 {
		t.Fatal("rotation count", writes)
	}
	if missingTask {
		for range 3 {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
		}
		state := journalState(t, a)
		if writes != 2 || state.Operation == nil || state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" || state.CredentialApplications[0].SSIDCredentialHistory[1].Version != 1 {
			t.Fatal("unknown task outcome replayed or released")
		}
		return
	}

	receipts := journalState(t, a).CredentialApplications
	if len(receipts) != 1 || len(receipts[0].Versions) != 3 {
		t.Fatal("missing receipt")
	}
	found := false
	for _, version := range receipts[0].Versions {
		if version.Key == "multi-psk:4" && version.Version == 2 && version.Path == "/ssids/0/multi_psk_settings/1/passphrase_wo" {
			found = true
		}
	}
	if !found {
		t.Fatal("rotated priority identity lost")
	}
	if !retire {
		return
	}
	// Delete old projection files: retirement must not require an obsolete secret.
	if err := os.RemoveAll(a.intentSecretPath); err != nil {
		t.Fatal(err)
	}
	setModel([]int{0}, map[int]int{0: 1})
	settle()
	if writes != 3 {
		t.Fatal("single-key retirement", writes)
	}
	state := journalState(t, a)
	raw, _ := json.Marshal(state)
	restored := nc.DeepCopy()
	if err := a.statusReader.Get(ctx, a.key, restored); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &restored.Status.Configuration); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Status().Update(ctx, restored); err != nil {
		t.Fatal(err)
	}
	setModel([]int{0, 4}, map[int]int{0: 1, 4: 2})
	for range 4 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 3 {
		t.Fatal("retired counter replayed", writes)
	}
	setModel([]int{4, 0}, map[int]int{0: 1, 4: 3})
	settle()
	if writes != 4 {
		t.Fatal("advanced reintroduction", writes)
	}
	if err := os.RemoveAll(a.intentSecretPath); err != nil {
		t.Fatal(err)
	}
	setModel([]int{}, map[int]int{})
	settle()
	if writes != 5 {
		t.Fatal("explicit empty retirement", writes)
	}
	if len(injected) != 4 {
		t.Fatal("retirement stages did not all recover", len(injected))
	}
	state = journalState(t, a)
	receipt := state.CredentialApplications[0]
	if len(state.CredentialApplications) != 1 || len(receipt.Versions) != 1 || len(receipt.SSIDCredentialHistory) != 2 || receipt.SSIDCredentialHistory[1].Priority != 4 || receipt.SSIDCredentialHistory[1].Version != 3 {
		t.Fatal("retirement history lost", receipt)
	}
	if err := validateSSIDCredentialHistory(receipt.Section, receipt.Resource, receipt.Versions, nil); err == nil {
		t.Fatal("pruned history accepted")
	}

}
