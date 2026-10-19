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
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessAPManifestDependenciesRotationRestart(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	model := strings.Replace(wirelessAPModel, `"name":"AP-LAB"`, `"name":"AP-LAB","power_profile":"POWER","management_password_version":1`, 1)
	model = strings.Replace(model, `"wireless":{`, `"wireless":{"power_profiles":[{"name":"POWER","rules":[{"interface_type":"ETHERNET","interface_id":"GIGABITETHERNET0","parameter_type":"SPEED","parameter_value":"1000MBPS"}]}],`, 1)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(model)
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "wireless", Path: "/ap_profiles/0/management_password", Source: "ap"}}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []ciskov1.NetworkControllerIntentSecretSource{{Alias: "ap", Name: "ap-secret", Key: "password"}}
	if err = a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = t.TempDir()
	relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: "/ap_profiles/0/management_password", SourceAlias: "ap", SecretName: "ap-secret", SecretKey: "password"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.intentSecretPath, relative)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	project := func(version int64) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"version": version, "value": "fixture-password"})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	project(1)
	powers := []wirelessPowerProfile{}
	profiles := []map[string]any{}
	writes := []string{}
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessPowerPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": powers})
		case r.URL.Path == wirelessAPPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": profiles})
		case r.URL.Path == wirelessPowerPath && r.Method == "POST":
			var p wirelessPowerProfile
			json.NewDecoder(r.Body).Decode(&p)
			p.ID = "power-id"
			powers = []wirelessPowerProfile{p}
			writes = append(writes, "power")
			stamp = time.Now().UnixMilli()
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
		case r.URL.Path == wirelessAPPath && r.Method == "POST" || r.URL.Path == wirelessAPPath+"/ap-id" && r.Method == "PUT":
			if len(powers) != 1 {
				t.Error("AP submitted before power exists")
			}
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if v, _ := getWirelessAPField(p, "managementSetting.managementPassword"); v != "fixture-password" {
				t.Error("missing projected credential")
			}
			p["id"] = "ap-id"
			profiles = []map[string]any{p}
			writes = append(writes, "ap")
			stamp = time.Now().UnixMilli()
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
		case r.URL.Path == taskPath+"task" || r.URL.Path == taskPath+"task/tree":
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "task", "isError": false, "startTime": stamp, "endTime": stamp})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	accepted := false
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		op := journalState(t, a).Operation
		if op != nil && op.Phase == "Accepted" && isWirelessAPCredentialResource(op.Section, op.Resource) {
			accepted = true
		}
	}
	settle := func() {
		t.Helper()
		for range 40 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("not converged", config.Status)
	}
	settle()
	tick()
	if !accepted || strings.Join(writes, ",") != "power,ap" {
		t.Fatal("ordering or restart receipt missing", writes)
	}
	state := journalState(t, a)
	if len(state.CredentialApplications) != 1 || state.CredentialApplications[0].TargetID != "ap-id" || state.CredentialApplications[0].Versions[0].Version != 1 {
		t.Fatal("durable receipt missing")
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), "fixture-password") {
		t.Fatal("credential leaked into status")
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `"management_password_version":1`, `"management_password_version":2`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	tick()
	if len(writes) != 2 {
		t.Fatal("stale projection submitted")
	}
	project(2)
	settle()
	tick()
	if strings.Join(writes, ",") != "power,ap,ap" || journalState(t, a).CredentialApplications[0].Versions[0].Version != 2 {
		t.Fatal("rotation failed")
	}
	config.Generation++
	config.Spec.SecretRefs = nil
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `,"management_password_version":1`, "", 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	tick()
	if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(writes) != 3 {
		t.Fatal("credential ownership silently removed")
	}
}
func TestWirelessAPPrunedCredentialJournalCannotRelease(t *testing.T) {
	a, config := journalFixture(t)
	ctx := context.Background()
	config.Spec.ManagedSections = []string{"wireless"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(ctx, config, nil, "wireless", "ap-credential-profile:AP-LAB", "Create")
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.Phase = "Verifying"
	if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
		t.Fatal("pruned credential version treated as metadata")
	}
	nc, err = a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.Phase = "Verified"
	if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err = a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("pruned credential fence released")
	}
}

func TestWirelessAPCombinedPreflightBlocksPrerequisiteWrite(t *testing.T) {
	model := strings.Replace(wirelessAPModel, "AP-LAB", "default-ap-profile", 1)
	model = strings.Replace(model, `"wireless":{`, `"wireless":{"power_profiles":[{"name":"POWER","rules":[{"interface_type":"ETHERNET","interface_id":"GIGABITETHERNET0","parameter_type":"SPEED","parameter_value":"1000MBPS"}]}],`, 1)
	desired, _ := parseWirelessAPProfiles([]byte(strings.Replace(wirelessAPModel, "AP-LAB", "default-ap-profile", 1)))
	actual := cloneWirelessAPFields(desired[0].metadata)
	actual["id"] = "builtin"
	actual["clientLimit"] = int64(20)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("preflight submitted a mutation")
		}
		switch r.URL.Path {
		case wirelessPowerPath:
			fmt.Fprint(w, `{"response":[]}`)
		case wirelessAPPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
		default:
			t.Error("unexpected read")
			w.WriteHeader(404)
		}
	})
	executor, err := c.compileWirelessConfiguration([]byte(model))
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(context.Background()); err == nil || write != nil {
		t.Fatal("power write released before protected AP preflight")
	}
}
