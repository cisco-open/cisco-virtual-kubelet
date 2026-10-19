// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const policyServerApplyModel = `{"catalyst_center":{"system_settings":{"authentication_and_policy_servers":{"aaa":[{"ip_address":"192.0.2.43","protocols":{"radius":{"authentication_port":1812,"accounting_port":1813}},"retries":1,"timeout":5,"shared_secret_version":1}]}}}}`

func TestPolicyServerApplyParser(t *testing.T) {
	intent, err := parsePolicyServerApply([]byte(policyServerApplyModel))
	if err != nil || len(intent) != 1 || intent[0].version != 1 || intent[0].path != projectedSecretPointer {
		t.Fatal("valid write-only model rejected", err)
	}
	for _, raw := range []string{
		strings.Replace(policyServerApplyModel, `"shared_secret_version":1`, `"shared_secret_version":0`, 1),
		strings.Replace(policyServerApplyModel, `"shared_secret_version":1`, `"shared_secret_version":1.5`, 1),
		strings.Replace(policyServerApplyModel, `"shared_secret_version":1`, `"shared_secret_version":null`, 1),
		strings.Replace(policyServerApplyModel, `"shared_secret_version":1`, `"shared_secret":"do-not-print"`, 1),
		strings.Replace(policyServerApplyModel, `"shared_secret_version":1`, `"shared_secret_version":1,"shared_secret":""`, 1),
		strings.Replace(policyServerApplyModel, `"retries":1`, `"retries":4`, 1),
		strings.Replace(policyServerApplyModel, `"timeout":5`, `"timeout":60`, 1),
		strings.Replace(policyServerApplyModel, `"timeout":5,`, ``, 1),
		strings.Replace(policyServerApplyModel, `"radius":{`, `"radius":{"enable_key_wrap":{},`, 1),
	} {
		if _, err := parsePolicyServerApply([]byte(raw)); err == nil || strings.Contains(err.Error(), "do-not-print") {
			t.Fatal("invalid credential model accepted or exposed")
		}
	}
}

func TestPolicyServerExecutorLifecycle(t *testing.T) {
	for _, scenario := range []string{"create", "noop", "rotate", "metadata", "foreign", "replaced", "rollback", "source-change", "keywrap"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, source, binding := credentialVersionJournalFixture(t)
			desired, err := parsePolicyServerApply([]byte(policyServerApplyModel))
			if err != nil {
				t.Fatal(err)
			}
			got := desired[0].server
			got.ID = "server"
			if scenario == "metadata" {
				n := 10
				got.Timeout = &n
			}
			if scenario == "keywrap" {
				got.EncryptionScheme = "KEYWRAP"
			}
			receipt := ciskov1.NetworkControllerConfigurationCredentialApplication{
				Section: "system_settings", Resource: got.IP, ConfigUID: source.secrets.uid, TargetID: "server", OperationID: strings.Repeat("a", 32), Versions: binding.credentials,
			}
			switch scenario {
			case "rotate":
				desired[0].version = 2
				source.secrets.values[0].value = versionedSecretFixture(t, 2, projectedCredential)
			case "replaced":
				receipt.TargetID = "old-server"
			case "rollback":
				receipt.Versions[0].Version = 2
			case "source-change":
				receipt.Versions[0].SecretKey = "previous-key"
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" && r.URL.Path == authenticationPolicyServersPath {
					list := []authenticationPolicyServer{}
					if scenario != "create" || writes > 0 {
						list = append(list, got)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": list})
					return
				}
				writes++
				wantMethod, wantPath := "PUT", authenticationPolicyServersPath+"/server"
				if scenario == "create" {
					wantMethod, wantPath = "POST", authenticationPolicyServersPath
				}
				if r.Method != wantMethod || r.URL.Path != wantPath {
					t.Error("incorrect AAA write endpoint")
				}
				body, _ := io.ReadAll(r.Body)
				var payload map[string]any
				if json.Unmarshal(body, &payload) != nil || payload["sharedSecret"] != projectedCredential || payload["shared_secret_version"] != nil {
					t.Error("credential payload or rotation counter transport invalid")
				}
				got = desired[0].server
				got.ID = "server"
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			e := &policyServerExecutor{client: c, desired: desired, secrets: source.secrets, owner: source.secrets.uid, receipts: []ciskov1.NetworkControllerConfigurationCredentialApplication{receipt}}
			if scenario == "create" || scenario == "foreign" {
				e.receipts = nil
			}
			write, err := e.next(context.Background())
			switch scenario {
			case "foreign", "replaced", "rollback", "source-change", "keywrap":
				if err == nil || writes != 0 {
					t.Fatal("unsafe AAA plan accepted")
				}
				return
			case "noop":
				if err != nil || write != nil || writes != 0 {
					t.Fatal("matching receipt did not converge", err)
				}
				return
			}
			if err != nil || write == nil || writes != 0 {
				t.Fatal("invalid execution plan", err)
			}
			if task, err := write.submit(context.Background()); err != nil || task.ID != "task" {
				t.Fatal("write did not return receipt", err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Section: "system_settings", ConfigUID: e.owner, Resource: got.IP, Action: write.action, TargetID: write.identity.target, CredentialVersions: write.identity.credentials}
			if id, verified, err := e.verifyCredential(context.Background(), op); err != nil || !verified || id != "server" {
				t.Fatal("write verification failed", err)
			}
			op.CredentialVersions[0].Version++
			if _, _, err := e.verifyCredential(context.Background(), op); err == nil {
				t.Fatal("different credential version verified")
			}
		})
	}
}

func TestCredentialReceiptCapacityBeforeSubmission(t *testing.T) {
	a, config, source, identity := credentialVersionJournalFixture(t)
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &ciskov1.NetworkControllerConfigurationStatus{}
	for i := 0; i < 256; i++ {
		nc.Status.Configuration.CredentialApplications = append(nc.Status.Configuration.CredentialApplications, ciskov1.NetworkControllerConfigurationCredentialApplication{Section: "system_settings", Resource: fmt.Sprintf("other-%d", i), ConfigUID: string(config.UID), TargetID: "server", Versions: identity.credentials})
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := a.submitConfigurationOperation(ctx, config, source, "system_settings", "192.0.2.43", "Create", func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }, identity); err == nil || calls != 0 {
		t.Fatal("full receipt store allowed mutation")
	}
	if journalState(t, a).Operation != nil {
		t.Fatal("full receipt store reserved operation")
	}
	// Count alone is insufficient: legal long references can exceed the
	// component's serialized status budget with only a few receipts.
	large := []ciskov1.NetworkControllerConfigurationCredentialVersion{}
	for i := 0; i < 128; i++ {
		large = append(large, ciskov1.NetworkControllerConfigurationCredentialVersion{Path: fmt.Sprintf("/%03d/", i) + strings.Repeat("x", 1000), Version: 1, SourceAlias: "source", SecretName: strings.Repeat("n", 253), SecretKey: strings.Repeat("k", 253)})
	}
	state := &ciskov1.NetworkControllerConfigurationStatus{CredentialApplications: []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "system_settings", Resource: "other", ConfigUID: string(config.UID), Versions: large}}}
	if err := configurationCredentialCapacity(state, string(config.UID), "system_settings", "new", large); err == nil {
		t.Fatal("oversized serialized receipt store accepted")
	}
}

func TestPolicyServerManifestCreateRotateAndRestart(t *testing.T) {
	testPolicyServerManifest(t, false)
}
func TestCombinedSystemSettingsManifestCreateRotateAndRestart(t *testing.T) {
	testPolicyServerManifest(t, true)
}
func testPolicyServerManifest(t *testing.T, combined bool) {
	t.Helper()
	ctx := context.Background()
	a, config, source, _ := credentialVersionJournalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = "https://catc.example"
	nc.Spec.CredentialSecretRef = ciskov1.NetworkControllerSecretReference{Name: "credentials"}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
	model := policyServerApplyModel
	if combined {
		model = strings.Replace(model, `"system_settings":{`, `"system_settings":{"external_authentication":{"enabled":true,"aaa_attribute":"CVK-Role"},`, 1)
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(model)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = source.secrets.root
	var actual *authenticationPolicyServer
	writes := 0
	externalWrites := 0
	enabled, attribute := false, ""
	expectedSecret := projectedCredential
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if combined && (r.URL.Path == externalAuthPath || r.URL.Path == externalAAAAttributePath) {
			if r.Method == "GET" {
				if r.URL.Path == externalAuthPath {
					fmt.Fprintf(w, `{"response":{"external-authentication-flag":[{"enabled":%t}]}}`, enabled)
				} else {
					fmt.Fprintf(w, `{"response":{"aaa-attributes":[{"attributeName":%q}]}}`, attribute)
				}
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected external-auth method")
				w.WriteHeader(400)
				return
			}
			state := journalState(t, a)
			if actual == nil || len(state.CredentialApplications) != 1 || state.CredentialApplications[0].TargetID != actual.ID || state.Operation == nil || state.Operation.Phase != "Submitting" || len(state.Operation.CredentialVersions) != 0 {
				t.Error("external authentication changed before verified server receipt")
			}
			var payload map[string]any
			if json.NewDecoder(r.Body).Decode(&payload) != nil {
				t.Error("invalid external payload")
			}
			if r.URL.Path == externalAuthPath {
				enabled, _ = payload["enable"].(bool)
			} else {
				attribute, _ = payload["attributeName"].(string)
			}
			externalWrites++
			fmt.Fprint(w, `{"response":{"message":"updated"}}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == authenticationPolicyServersPath {
			list := []authenticationPolicyServer{}
			if actual != nil {
				list = append(list, *actual)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": list})
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath) {
			now := time.Now().UnixMilli()
			fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, strings.TrimPrefix(r.URL.Path, taskPath), now, now)
			return
		}
		if (r.Method == "POST" && r.URL.Path == authenticationPolicyServersPath) || (r.Method == "PUT" && r.URL.Path == authenticationPolicyServersPath+"/server") {
			writes++
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if json.Unmarshal(body, &payload) != nil || payload["sharedSecret"] != expectedSecret {
				t.Error("wrong projected credential submitted")
			}
			actual = &authenticationPolicyServer{}
			if json.Unmarshal(body, actual) != nil {
				t.Error("invalid server payload")
			}
			actual.ID = "server"
			fmt.Fprintf(w, `{"response":{"taskId":"task-%d"}}`, writes)
			return
		}
		t.Error("unexpected API request")
		w.WriteHeader(404)
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	run := func() {
		t.Helper()
		for step := 0; step < 24; step++ {
			// Rebuild the reconciler every time: only persisted journal state,
			// fresh projected files and controller reads carry across steps.
			restarted := *a
			r := &configReconciler{a: &restarted}
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatalf("manifest did not converge: phase=%s conditions=%v", config.Status.Phase, config.Status.Conditions)
	}
	run()
	if combined && (externalWrites != 2 || !enabled || attribute != "CVK-Role") {
		t.Fatal("combined external settings did not converge")
	}
	if writes != 1 {
		t.Fatal("create repeated across worker reconstruction", writes)
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.CredentialApplications) != 1 || state.CredentialApplications[0].Versions[0].Version != 1 {
		t.Fatal("create lacked persistent credential receipt")
	}
	for i := 0; i < 3; i++ {
		run()
	}
	if writes != 1 {
		t.Fatal("converged credential replayed")
	}
	expectedSecret = "rotated-secret"
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(model, `"shared_secret_version":1`, `"shared_secret_version":2`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	// Model version arrives before kubelet rotates the projected Secret.
	// It must not label an old credential as successfully applied version 2.
	for i := 0; i < 3; i++ {
		r := &configReconciler{a: a}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 || journalState(t, a).Operation != nil {
		t.Fatal("stale Secret projection submitted under new version")
	}
	if err := os.WriteFile(filepath.Join(source.secrets.root, source.secrets.values[0].relative), versionedSecretFixture(t, 2, expectedSecret), 0600); err != nil {
		t.Fatal(err)
	}
	run()
	if writes != 2 {
		t.Fatal("explicit version rotation did not issue exactly one update", writes)
	}
	state = journalState(t, a)
	if state.Operation != nil || state.CredentialApplications[0].Versions[0].Version != 2 {
		t.Fatal("rotation receipt missing")
	}
	for i := 0; i < 3; i++ {
		run()
	}
	if writes != 2 {
		t.Fatal("rotation repeated after convergence")
	}
	if combined && externalWrites != 2 {
		t.Fatal("server rotation repeated external authentication writes")
	}
	status, _ := json.Marshal(state)
	if strings.Contains(string(status), expectedSecret) || strings.Contains(string(status), "test-credential") {
		t.Fatal("credential persisted in journal")
	}
}
