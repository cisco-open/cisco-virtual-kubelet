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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestISEManifestRegistrationIntegrationAndRestart(t *testing.T) {
	ctx := context.Background()
	a, config, source, _ := credentialVersionJournalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = "https://catc.example"
	nc.Spec.CredentialSecretRef = ciskov1.NetworkControllerSecretReference{Name: "credentials"}
	nc.Spec.IntentSecretSources = nil
	config.Spec.SecretRefs = nil
	paths := map[string]string{}
	desired, err := parsePolicyServerApply([]byte(policyISEModel))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaf := range desired[0].credentialIntents() {
		alias := strings.ToLower(leaf.field)
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: alias, Name: "ise-credentials", Key: alias})
		config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "system_settings", Path: leaf.path, Source: alias})
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "system_settings", JSONPointer: leaf.path, SourceAlias: alias, SecretName: "ise-credentials", SecretKey: alias})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(source.secrets.root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, versionedSecretFixture(t, 1, "fixture-"+leaf.field), 0600); err != nil {
			t.Fatal(err)
		}
		paths[leaf.field] = path
	}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	model := strings.Replace(policyISEModel, `"system_settings":{`, `"system_settings":{"external_authentication":{"enabled":true},`, 1)
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(model)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = source.secrets.root
	var actual *authenticationPolicyServer
	writes, accepts, loginWrites := 0, 0, 0
	integrated, enabled := false, false
	expectedPassword := "fixture-isePassword"
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == authenticationPolicyServersPath && r.Method == "GET":
			list := []authenticationPolicyServer{}
			if actual != nil {
				list = append(list, *actual)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": list})
		case strings.HasPrefix(r.URL.Path, taskPath) && r.Method == "GET":
			now := time.Now().UnixMilli()
			fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, strings.TrimPrefix(r.URL.Path, taskPath), now, now)
		case r.URL.Path == policyISEIntegrationPath:
			status, step := "INPROGRESS", "WAITING_USER_INPUT"
			if accepts > 0 {
				step = "IN_PROGRESS"
			}
			if integrated {
				status, step = "COMPLETE", "COMPLETE"
			}
			fmt.Fprintf(w, `{"aaaServerSettingId":"ise-server","overallStatus":%q,"steps":[{"stepId":"cert","stepStatus":%q,"certAcceptedByUser":%t}]}`, status, step, accepts > 0)
		case r.URL.Path == "/dna/intent/api/v1/integrate-ise/ise-server":
			state := journalState(t, a)
			if writes != 1 || len(state.CredentialApplications) != 1 || len(state.CredentialApplications[0].Versions) != 2 || state.Operation == nil || state.Operation.Phase != "Submitting" || state.Operation.TargetID != "ise-server" {
				t.Error("certificate accepted before registration receipt or permit")
			}
			accepts++
			fmt.Fprint(w, `{}`)
		case r.URL.Path == externalAuthPath:
			if r.Method == "GET" {
				fmt.Fprintf(w, `{"response":{"external-authentication-flag":[{"enabled":%t}]}}`, enabled)
				return
			}
			if !integrated || accepts != 1 {
				t.Error("external login enabled before verified ISE integration")
			}
			loginWrites++
			enabled = true
			fmt.Fprint(w, `{"response":{"message":"updated"}}`)
		case (r.Method == "POST" && r.URL.Path == authenticationPolicyServersPath) || (r.Method == "PUT" && r.URL.Path == authenticationPolicyServersPath+"/ise-server"):
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid registration payload")
			}
			dto := body["ciscoIseDtos"].([]any)[0].(map[string]any)
			if dto["password"] != expectedPassword || body["sharedSecret"] != "fixture-sharedSecret" {
				t.Error("wrong projected credential")
			}
			raw, _ := json.Marshal(body)
			actual = &authenticationPolicyServer{}
			_ = json.Unmarshal(raw, actual)
			actual.ID = "ise-server"
			writes++
			fmt.Fprintf(w, `{"response":{"taskId":"ise-task-%d"}}`, writes)
		default:
			t.Error("unexpected ISE API request")
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
	for i := 0; i < 24 && accepts == 0; i++ {
		tick()
	}
	if accepts != 1 {
		t.Fatal("certificate stage not reached", config.Status.Conditions)
	}
	op := journalState(t, a).Operation
	if op == nil || op.Phase != "Verifying" || op.TaskID != "" {
		t.Fatal("synchronous acknowledgement not persisted")
	}
	for i := 0; i < 3; i++ {
		tick()
	}
	if accepts != 1 || loginWrites != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("pending integration replayed or converged")
	}
	integrated = true
	converge := func() {
		t.Helper()
		for i := 0; i < 24; i++ {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("ISE manifest did not converge", config.Status.Conditions)
	}
	converge()
	if writes != 1 || accepts != 1 || loginWrites != 1 {
		t.Fatal("unexpected operation counts")
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `"password_version":1`, `"password_version":2`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tick()
	}
	if writes != 1 || journalState(t, a).Operation != nil {
		t.Fatal("stale password projection submitted")
	}
	expectedPassword = "rotated-ise-password"
	if err := os.WriteFile(paths["isePassword"], versionedSecretFixture(t, 2, expectedPassword), 0600); err != nil {
		t.Fatal(err)
	}
	converge()
	for i := 0; i < 3; i++ {
		tick()
	}
	state := journalState(t, a)
	if writes != 2 || accepts != 1 || loginWrites != 1 || state.Operation != nil || len(state.CredentialApplications) != 1 {
		t.Fatal("rotation not idempotent")
	}
	versions := state.CredentialApplications[0].Versions
	if len(versions) != 2 || versions[0].Path != "/authentication_and_policy_servers/ise/password" || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatal("independent credential receipts not retained")
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), expectedPassword) || strings.Contains(string(raw), "fixture-") {
		t.Fatal("journal leaked credential")
	}
}
