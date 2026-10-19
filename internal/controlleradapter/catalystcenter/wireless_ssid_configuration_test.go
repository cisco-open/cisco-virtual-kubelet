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

func TestWirelessSSIDManifestRotationRestart(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	model := `{"catalyst_center":{"wireless":{"ssids":[{"name":"SSID-LAB","auth_type":"WPA2_PERSONAL","wlan_type":"Enterprise","auth_key_psk":true,"rsn_cipher_suite_ccmp128":true,"passphrase_version":1}]}}}`
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(model)
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "wireless", Path: "/ssids/0/passphrase", Source: "ssid"}}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []ciskov1.NetworkControllerIntentSecretSource{{Alias: "ssid", Name: "ssid-secret", Key: "password"}}
	if err = a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = t.TempDir()
	relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "wireless", JSONPointer: "/ssids/0/passphrase", SourceAlias: "ssid", SecretName: "ssid-secret", SecretKey: "password"})
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
			if p["passphrase"] != "fixture-password" {
				t.Error("missing projected credential")
			}
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
		if op != nil && op.Phase == "Accepted" && isWirelessSSIDCredentialResource(op.Section, op.Resource) {
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
	state := journalState(t, a)
	if !accepted || writes != 1 || len(state.CredentialApplications) != 1 {
		t.Fatal("restart receipt missing")
	}
	receipt := state.CredentialApplications[0]
	if receipt.TargetID != "ssid-id" || len(receipt.Versions) != 1 || receipt.Versions[0].Version != 1 || receipt.Versions[0].Key != "passphrase" {
		t.Fatal("durable identity lost")
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), "fixture-password") {
		t.Fatal("credential leaked")
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `"passphrase_version":1`, `"passphrase_version":2`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	tick()
	if writes != 1 {
		t.Fatal("stale projection submitted")
	}
	project(2)
	settle()
	tick()
	if writes != 2 || journalState(t, a).CredentialApplications[0].Versions[0].Version != 2 {
		t.Fatal("rotation failed")
	}
	config.Generation++
	config.Spec.SecretRefs = nil
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `,"passphrase_version":1`, "", 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	tick()
	if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || writes != 2 {
		t.Fatal("credential ownership removed")
	}
}

func TestWirelessSSIDPrunedCredentialJournalCannotRelease(t *testing.T) {
	a, config := journalFixture(t)
	ctx := context.Background()
	config.Spec.ManagedSections = []string{"wireless"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(ctx, config, nil, "wireless", "ssid-credential:global:SSID-LAB", "Create")
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
		t.Fatal("pruned credential accepted as metadata")
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
	// Simulate an old CRD pruning only Key from otherwise matching evidence.
	nc, err = a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	versions := []ciskov1.NetworkControllerConfigurationCredentialVersion{{Path: "/ssids/0/passphrase", Version: 1, SourceAlias: "ssid", SecretName: "ssid-secret", SecretKey: "password"}}
	nc.Status.Configuration.Operation.CredentialVersions = versions
	nc.Status.Configuration.CredentialApplications = []ciskov1.NetworkControllerConfigurationCredentialApplication{{ConfigUID: op.ConfigUID, Section: op.Section, Resource: op.Resource, TargetID: "ssid-id", OperationID: op.ID, Versions: versions}}
	if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err = a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("pruned semantic keys released fence")
	}
}

func TestWirelessSSIDCombinedACLPreflight(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing-", missing), func(t *testing.T) {
			model := strings.Replace(wirelessACLModel, `"wireless":{`, `"wireless":{"ssids":[{"name":"CVK","auth_type":"OPEN","wlan_type":"Enterprise","posturing":true,"acl_name":"CVK_LAB"}],`, 1)
			if missing {
				model = strings.Replace(model, `"acl_name":"CVK_LAB"`, `"acl_name":"absent"`, 1)
			}
			acls := []wirelessACL{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
				case r.URL.Path == wirelessACLsPath && r.Method == "GET":
					json.NewEncoder(w).Encode(map[string]any{"response": acls})
				case r.URL.Path == wirelessSSIDPath("global") && r.Method == "GET":
					fmt.Fprint(w, `{"response":[]}`)
				case r.URL.Path == wirelessACLsPath && r.Method == "POST":
					var acl wirelessACL
					if json.NewDecoder(r.Body).Decode(&acl) != nil {
						t.Error("invalid ACL")
					}
					acl.ID = "acl-id"
					acls = append(acls, acl)
					fmt.Fprint(w, `{"response":{"taskId":"acl-task"}}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e, err := c.compileWirelessConfiguration([]byte(model))
			if err != nil {
				t.Fatal(err)
			}
			write, err := e.next(context.Background())
			if missing {
				if err == nil || write != nil {
					t.Fatal("missing dependency released prerequisite")
				}
				return
			}
			if err != nil || write == nil || strings.HasPrefix(write.resource, "ssid:") {
				t.Fatal("ACL prerequisite not first", err)
			}
			if _, err = write.submit(context.Background()); err != nil {
				t.Fatal(err)
			}
			write, err = e.next(context.Background())
			if err != nil || write == nil || !strings.HasPrefix(write.resource, "ssid:global:") {
				t.Fatal("SSID did not follow ACL", err)
			}
		})
	}
}
