// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGlobalCredentialMetadataPromotion(t *testing.T) {
	exerciseGlobalCredentialMetadataPromotion(t)
}

// Exercise the public reconciler across both receipt families, projection
// delivery, original Unknown task recovery, restart and subsequent rotation.
func exerciseGlobalCredentialMetadataPromotion(t *testing.T, installers ...func(*adapter, *configapi.NetworkControllerConfig)) {
	ctx := context.Background()
	a, config := noAuthCredentialConfigurationFixture(t)
	var actual *globalCredentialMetadata
	writes := 0
	tasks := map[string]int64{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == "GET" && r.URL.Path == globalCredentialsPath:
			list := []globalCredentialMetadata{}
			if actual != nil {
				list = append(list, *actual)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": list}})
		case (r.Method == "POST" || r.Method == "PUT") && r.URL.Path == globalCredentialsPath:
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
				t.Error("bad envelope")
				w.WriteHeader(400)
				return
			}
			var payload map[string]any
			if writes == 0 {
				var list []map[string]any
				if r.Method != "POST" || json.Unmarshal(body["snmpV3"], &list) != nil || len(list) != 1 {
					t.Error("invalid create")
					w.WriteHeader(400)
					return
				}
				payload = list[0]
			} else {
				if r.Method != "PUT" || json.Unmarshal(body["snmpV3"], &payload) != nil || payload["id"] != "owned-snmp-id" {
					t.Error("invalid promotion UUID/method")
					w.WriteHeader(400)
					return
				}
			}
			expectedMode := []string{"NOAUTHNOPRIV", "AUTHNOPRIV", "AUTHPRIV", "AUTHPRIV"}
			if writes >= len(expectedMode) || payload["snmpMode"] != expectedMode[writes] {
				t.Error("wrong mode or replay")
				w.WriteHeader(400)
				return
			}
			if writes > 0 {
				expected := "private-authPassword"
				if writes == 3 {
					expected = "rotated-auth"
				}
				if payload["authPassword"] != expected {
					t.Error("wrong projected authentication value")
				}
			}
			if writes > 1 && payload["privacyPassword"] != "private-privacyPassword" {
				t.Error("wrong projected privacy value")
			}
			raw, _ := json.Marshal(payload)
			actual = &globalCredentialMetadata{}
			json.Unmarshal(raw, actual)
			actual.ID = "owned-snmp-id"
			writes++
			id := fmt.Sprintf("promotion-task-%d", writes)
			tasks[id] = time.Now().UnixMilli()
			fmt.Fprintf(w, `{"response":{"taskId":%q}}`, id)
		case strings.HasPrefix(r.URL.Path, taskPath) && r.Method == "GET":
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			ended, ok := tasks[id]
			if !ok {
				t.Error("uncorrelated task")
			}
			row := map[string]any{"id": id, "rootId": id, "startTime": ended, "endTime": ended, "isError": false}
			var response any = row
			if strings.HasSuffix(r.URL.Path, "/tree") {
				response = []any{row}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": response})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot := globalCredentialFixture(t, "snmpv3_credentials")
	a.intentSecretPath = t.TempDir()
	refs := []configapi.NetworkControllerSecretRef{}
	paths := map[string]string{}
	for _, value := range snapshot.values {
		alias := strings.ToLower(value.ref.Source)
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: "generated-unused-snmp", Key: alias})
		ref := value.ref
		ref.Source = alias
		refs = append(refs, ref)
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: ref.Path, SourceAlias: alias, SecretName: "generated-unused-snmp", SecretKey: alias})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(a.intentSecretPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value.value, 0600); err != nil {
			t.Fatal(err)
		}
		paths[ref.Path] = path
	}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for _, install := range installers {
		install(a, config)
	}
	recovered := map[string]bool{}
	converge := func() {
		t.Helper()
		for range 40 {
			noAuthCredentialTick(t, a, config)
			state := journalState(t, a)
			if state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered[state.Operation.ID] {
				if writes == 2 {
					assertGlobalCredentialPromotionCommitBoundaries(t, a, config)
				}
				recovered[state.Operation.ID] = true
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation && state.Operation == nil {
				return
			}
		}
		t.Fatal("promotion failed to converge", config.Status.Conditions)
	}
	converge()
	origin := journalState(t, a).ActionApplications[0].DeepCopy()
	for step := 1; step <= 3; step++ {
		item := map[string]any{"name": "fixture", "username": "tester", "snmp_mode": "AUTHNOPRIV", "auth_type": "SHA", "auth_password_version": 1}
		if step > 1 {
			item["snmp_mode"] = "AUTHPRIV"
			item["privacy_type"] = "AES128"
			item["privacy_password_version"] = 1
		}
		if step == 3 {
			item["auth_password_version"] = 2
			for path, file := range paths {
				if strings.HasSuffix(path, "/auth_password") {
					if err := os.WriteFile(file, versionedSecretFixture(t, 2, "rotated-auth"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		rawItem, _ := json.Marshal(item)
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: globalCredentialModel("snmpv3_credentials", string(rawItem))}
		config.Spec.SecretRefs = nil
		for _, ref := range refs {
			if step > 1 || strings.HasSuffix(ref.Path, "/auth_password") {
				config.Spec.SecretRefs = append(config.Spec.SecretRefs, ref)
			}
		}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		converge()
		state := journalState(t, a)
		if writes != step+1 || len(state.ActionApplications) != 1 || !reflect.DeepEqual(*origin, state.ActionApplications[0]) || len(state.CredentialApplications) != 1 || state.CredentialApplications[0].TargetID != "owned-snmp-id" {
			t.Fatal("receipt lineage or same-UUID update failed")
		}
		for range 3 {
			noAuthCredentialTick(t, a, config)
		}
		if writes != step+1 {
			t.Fatal("restart replayed write")
		}
	}
	if len(recovered) != 4 {
		t.Fatal("missing original-task recovery")
	}
	raw, _ := json.Marshal(journalState(t, a))
	for _, secret := range []string{"private-authPassword", "private-privacyPassword", "rotated-auth"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret leaked into journal")
		}
	}
	// No permission to drop counters or switch the owned definition back to NOAUTH.
	config.Spec.SecretRefs = nil
	config.Spec.Source.Inline.Raw = noAuthCredentialModel("fixture", "tester")
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		noAuthCredentialTick(t, a, config)
	}
	if writes != 4 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("authentication downgrade was accepted")
	}
}

func assertGlobalCredentialPromotionCommitBoundaries(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.compileGlobalCredentialApply(nc, config, source); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "missing-origin", "foreign-origin", "duplicate-origin", "replaced-origin-target", "reused-origin-task", "future-origin"} {
		current := nc.DeepCopy()
		state := current.Status.Configuration
		switch mode {
		case "missing-origin":
			state.ActionApplications = nil
		case "foreign-origin":
			state.ActionApplications[0].Operation.ConfigUID = "foreign"
		case "duplicate-origin":
			state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
		case "replaced-origin-target":
			key, _ := metadataCredentialKey(state.ActionApplications[0].Operation.Resource)
			state.ActionApplications[0].Resource = key + ":replacement"
		case "reused-origin-task":
			state.Operation.TaskID = state.ActionApplications[0].Operation.TaskID
		case "future-origin":
			state.ActionApplications[0].Operation.ConfigGeneration = config.Generation + 1
		}
		err := a.checkGlobalCredentialPromotionCommit(ctx, config, source, current, state.Operation.TargetID)
		if (err == nil) != (mode == "valid") {
			t.Fatal("promotion commit lineage check failed", mode, err)
		}
	}
}

func TestGlobalCredentialPromotionTaskBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-target", "create", "old-task", "old-operation", "before-origin", "future-config", "future-controller", "foreign-root", "duplicate-root", "missing-verified", "different-prior", "same-prior-operation"} {
		t.Run(mode, func(t *testing.T) {
			e, op := globalRecoveryFixture(t, true)
			e.receipts = nil
			wants, _, _ := splitNetworkCredentialConfiguration(noAuthCredentialModel("fixture", "tester"))
			root := noAuthCredentialReceipt(wants[0], e.owner, e.configName)
			root.Operation.TaskID = "origin-task"
			e.metadataReceipts = []cvk.NetworkControllerConfigurationActionApplication{root}
			op.TargetID = "owned-snmp-id"
			op.ID = strings.Repeat("b", 32)
			switch mode {
			case "wrong-target":
				op.TargetID = "replacement"
			case "create":
				op.Action = "Create"
				op.TargetID = ""
			case "old-task":
				op.TaskID = root.Operation.TaskID
			case "old-operation":
				op.ID = root.Operation.ID
			case "before-origin":
				op.SubmittedAt = root.Operation.SubmittedAt.DeepCopy()
			case "future-config":
				e.metadataReceipts[0].Operation.ConfigGeneration++
			case "future-controller":
				e.metadataReceipts[0].Operation.ControllerGeneration++
			case "foreign-root":
				e.metadataReceipts[0].Operation.ConfigUID = "foreign"
			case "duplicate-root":
				e.metadataReceipts = append(e.metadataReceipts, root)
			case "missing-verified":
				e.metadataReceipts[0].VerifiedAt = nil
			case "different-prior", "same-prior-operation":
				id := "replacement"
				if mode == "same-prior-operation" {
					id = op.TargetID
				}
				e.receipts = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: e.desired[0].resource(), ConfigUID: e.owner, TargetID: id, OperationID: root.Operation.ID, Versions: op.CredentialVersions}}
			}
			if e.knownCredentialTask(op) != (mode == "valid") {
				t.Fatal("promotion observation envelope accepted wrong lineage", mode)
			}
		})
	}
}

func TestGlobalCredentialPromotionPlanningBoundaries(t *testing.T) {
	for _, mode := range []string{"authpriv", "authnopriv", "missing-native", "replaced-native", "changed-payload", "changed-native-mode", "missing-root", "foreign-root", "duplicate-root"} {
		t.Run(mode, func(t *testing.T) {
			want, secrets := globalCredentialFixture(t, "snmpv3_credentials")
			if mode == "authnopriv" {
				want.metadata.Mode = "AUTHNOPRIV"
				want.metadata.PrivacyType = ""
				want.credentials = want.credentials[:1]
			}
			original, _, err := splitNetworkCredentialConfiguration(noAuthCredentialModel("fixture", "tester"))
			if err != nil {
				t.Fatal(err)
			}
			got := original[0].metadata
			got.ID = "owned-snmp-id"
			root := noAuthCredentialReceipt(original[0], "owner", "config")
			e := &globalCredentialExecutor{desired: []globalCredentialIntent{want}, secrets: secrets, owner: "owner", configName: "config", metadataReceipts: []cvk.NetworkControllerConfigurationActionApplication{root}}
			switch mode {
			case "replaced-native":
				got.ID = "replacement"
			case "changed-payload":
				got.Username = "external-change"
			case "changed-native-mode":
				got.Mode = "AUTHNOPRIV"
				got.AuthType = "SHA"
			case "missing-root":
				e.metadataReceipts = nil
			case "foreign-root":
				e.metadataReceipts[0].Operation.ConfigUID = "foreign"
			case "duplicate-root":
				e.metadataReceipts = append(e.metadataReceipts, root)
			}
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != globalCredentialsPath {
					t.Error("preflight mutated native state")
					w.WriteHeader(400)
					return
				}
				list := []globalCredentialMetadata{got}
				if mode == "missing-native" {
					list = nil
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": list}})
			})
			write, err := e.next(context.Background())
			if mode == "authpriv" || mode == "authnopriv" {
				if err != nil || write == nil || write.action != "Update" || write.identity.target != got.ID {
					t.Fatal("same-UUID promotion not planned", err)
				}
			} else if err == nil || write != nil {
				t.Fatal("promotion planning bypassed original ownership", mode)
			}
		})
	}
}
