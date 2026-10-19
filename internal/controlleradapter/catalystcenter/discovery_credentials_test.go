// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const discoveryCredentialMarker = "CVK_SENSITIVE_TEST_VALUE"

func discoveryCredentialSource(t *testing.T, extra map[string]any, global bool) []byte {
	t.Helper()
	var model map[string]any
	if err := json.Unmarshal(discoveryModel(), &model); err != nil {
		t.Fatal(err)
	}
	entry := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)[0].(map[string]any)
	if !global {
		delete(entry, "global_credential_list")
	}
	for k, v := range extra {
		entry[k] = v
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func discoveryProjectionFixture(t *testing.T, raw []byte, values map[string]string) (*adapter, *cvk.NetworkController, *configapi.NetworkControllerConfig, *configSourceSnapshot, map[string]string) {
	t.Helper()
	ctx := context.Background()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{"inventory"}
	c.Spec.Source.Inline.Raw = raw
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = t.TempDir()
	wants, err := parseDiscoveryCredentialIntents(raw)
	if err != nil {
		t.Fatal("invalid test model", err)
	}
	paths := map[string]string{}
	for _, want := range wants {
		for _, leaf := range want.credentials {
			alias := fmt.Sprint("discovery-", len(c.Spec.SecretRefs))
			ref := configapi.NetworkControllerSecretRef{Section: "inventory", Path: leaf.path, Source: alias}
			c.Spec.SecretRefs = append(c.Spec.SecretRefs, ref)
			nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: "discovery-inputs", Key: alias})
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: c.Name, Section: "inventory", JSONPointer: ref.Path, SourceAlias: alias, SecretName: "discovery-inputs", SecretKey: alias})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.intentSecretPath, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			value, ok := values[leaf.field]
			if !ok {
				t.Fatal("test projection absent", leaf.field)
			}
			if err := os.WriteFile(path, versionedSecretFixture(t, leaf.version, value), 0600); err != nil {
				t.Fatal(err)
			}
			paths[leaf.field] = path
		}
	}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{raw: raw}
	return a, nc, c, source, paths
}

func TestDiscoveryCredentialProjectionKinds(t *testing.T) {
	cases := []struct {
		name   string
		model  map[string]any
		values map[string]string
	}{
		{"cli", map[string]any{"user_name_list": []any{"operator", "admin"}, "password_list_version": int64(1), "enable_password_list_version": int64(2)}, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE_B","CVK_SENSITIVE_TEST_VALUE_A"]`, "enablePasswordList": `["CVK_SENSITIVE_TEST_VALUE_ENABLE"]`}},
		{"http-read", map[string]any{"http_read_credential_version": int64(1)}, map[string]string{"httpReadCredential": discoveryCredentialMarker}},
		{"http-write", map[string]any{"http_write_credential_version": int64(1)}, map[string]string{"httpWriteCredential": discoveryCredentialMarker}},
		{"snmp-v2", map[string]any{"snmp_version": "v2", "snmp_ro_community_version": int64(1), "snmp_rw_community_version": int64(2), "snmp_ro_community_desc": "read", "snmp_rw_community_desc": "write"}, map[string]string{"snmpRoCommunity": discoveryCredentialMarker + "_RO", "snmpRwCommunity": discoveryCredentialMarker + "_RW"}},
		{"snmp-auth-priv", map[string]any{"snmp_version": "v3", "snmp_user_name": "operator", "snmp_mode": "AuthPriv", "snmp_auth_protocol": "SHA", "snmp_priv_protocol": "AES128", "snmp_auth_passphrase_version": int64(1), "snmp_priv_passphrase_version": int64(2)}, map[string]string{"snmpAuthPassphrase": discoveryCredentialMarker + "_AUTH", "snmpPrivPassphrase": discoveryCredentialMarker + "_PRIV"}},
		{"snmp-auth-no-priv", map[string]any{"snmp_version": "v3", "snmp_user_name": "operator", "snmp_mode": "AuthNoPriv", "snmp_auth_protocol": "MD5", "snmp_auth_passphrase_version": int64(1)}, map[string]string{"snmpAuthPassphrase": discoveryCredentialMarker}},
		{"snmp-no-auth", map[string]any{"snmp_version": "v3", "snmp_user_name": "operator", "snmp_mode": "NoAuthNoPriv"}, nil},
	}
	for _, test := range cases {
		for _, global := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/global=%t", test.name, global), func(t *testing.T) {
				raw := discoveryCredentialSource(t, test.model, global)
				a, nc, c, source, _ := discoveryProjectionFixture(t, raw, test.values)
				before := append([]byte(nil), source.raw...)
				projection, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
				if err != nil {
					t.Fatal(err)
				}
				request, err := projection.requestFields(nc, c, source, 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(request.versions) != len(c.Spec.SecretRefs) || (len(request.globalNames) > 0) != global {
					t.Fatal("credential references/versions were discarded")
				}
				for field, value := range test.values {
					if field == "passwordList" || field == "enablePasswordList" {
						var list []string
						if json.Unmarshal([]byte(value), &list) != nil {
							t.Fatal("bad test set")
						}
						sort.Strings(list)
						if !reflect.DeepEqual(request.fields[field], list) {
							t.Fatal("secret set mapping failed")
						}
					} else if request.fields[field] != value {
						t.Fatal("scalar secret mapping failed")
					}
				}
				payload, _ := json.Marshal(request.fields)
				if strings.Contains(string(payload), "CVK_PRIVATE_VALIDATION_ONLY") || !reflect.DeepEqual(source.raw, before) {
					t.Fatal("input mutated or validation reference escaped")
				}
				for _, value := range []any{projection, request} {
					encoded, err := json.Marshal(value)
					if err != nil || !json.Valid(encoded) || strings.Contains(string(encoded), discoveryCredentialMarker) || strings.Contains(fmt.Sprintf("%v %+v %#v", value, value, value), discoveryCredentialMarker) {
						t.Fatal("formatting exposed credential material")
					}
				}
				request.fields["name"] = "caller-mutated"
				request.globalNames = append(request.globalNames, "caller")
				if users, ok := request.fields["userNameList"].([]string); ok {
					users[0] = "caller"
				}
				if passwords, ok := request.fields["passwordList"].([]string); ok {
					passwords[0] = "caller"
				}
				next, err := projection.requestFields(nc, c, source, 0)
				if err != nil || next.fields["name"] != "fixture" || slices.Contains(next.globalNames, "caller") {
					t.Fatal("caller rewrote a frozen projection")
				}
				if users, ok := next.fields["userNameList"].([]string); ok && slices.Contains(users, "caller") {
					t.Fatal("caller rewrote username set")
				}
			})
		}
	}
}

func TestDiscoveryCredentialModelRefusal(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"plaintext-password": {"password_list": []any{discoveryCredentialMarker}}, "plaintext-community": {"snmp_ro_community": discoveryCredentialMarker}, "plaintext-http": {"http_read_credential": discoveryCredentialMarker},
		"unknown": {"unknown_field": nil}, "zero-counter": {"password_list_version": 0}, "null-counter": {"password_list_version": nil}, "string-counter": {"password_list_version": "1"},
		"v2-mode": {"snmp_version": "v2", "snmp_mode": "AuthPriv", "snmp_ro_community_version": 1}, "v3-community": {"snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": "operator", "snmp_ro_community_version": 1},
		"missing-auth": {"snmp_version": "v3", "snmp_mode": "AuthNoPriv", "snmp_user_name": "operator", "snmp_auth_protocol": "SHA"}, "missing-privacy": {"snmp_version": "v3", "snmp_mode": "AuthPriv", "snmp_user_name": "operator", "snmp_auth_protocol": "SHA", "snmp_auth_passphrase_version": 1},
		"noauth-with-secret": {"snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": "operator", "snmp_auth_passphrase_version": 1}, "description-no-community": {"snmp_ro_community_desc": "read"},
		"duplicate-users": {"user_name_list": []any{"operator", "operator"}}, "bad-enum": {"snmp_version": "v3", "snmp_user_name": "operator", "snmp_mode": "AUTHPRIV"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseDiscoveryCredentialIntents(discoveryCredentialSource(t, extra, true))
			if err == nil || strings.Contains(err.Error(), discoveryCredentialMarker) {
				t.Fatal("invalid credential model accepted or exposed")
			}
		})
	}
}

func TestDiscoveryCredentialSetAndProjectionFences(t *testing.T) {
	for _, mode := range []string{"empty-set", "duplicate-set", "non-string", "trailing", "null-set", "missing-file", "projection-lag", "changed-bytes", "controller-uid", "config-uid", "source-uid", "source-version", "source-edit", "destination-edit", "revoked", "wrong-use"} {
		t.Run(mode, func(t *testing.T) {
			a, nc, c, source, paths := discoveryProjectionFixture(t, discoveryCredentialSource(t, map[string]any{"password_list_version": 1, "user_name_list": []any{"operator"}}, false), map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
			path := paths["passwordList"]
			switch mode {
			case "empty-set":
				_ = os.WriteFile(path, versionedSecretFixture(t, 1, `[]`), 0600)
			case "duplicate-set":
				_ = os.WriteFile(path, versionedSecretFixture(t, 1, `["same","same"]`), 0600)
			case "non-string":
				_ = os.WriteFile(path, versionedSecretFixture(t, 1, `[1]`), 0600)
			case "trailing":
				_ = os.WriteFile(path, versionedSecretFixture(t, 1, `["x"] {}`), 0600)
			case "null-set":
				_ = os.WriteFile(path, versionedSecretFixture(t, 1, `null`), 0600)
			case "missing-file":
				_ = os.Remove(path)
			case "projection-lag":
				_ = os.WriteFile(path, versionedSecretFixture(t, 2, `["changed"]`), 0600)
			case "wrong-use":
				c.Spec.SecretRefs[0].Use = "PrivateComparisonKey"
			}
			p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
			if slices.Contains([]string{"empty-set", "duplicate-set", "non-string", "trailing", "null-set", "missing-file", "projection-lag", "wrong-use"}, mode) {
				if err == nil {
					t.Fatal("invalid projection accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "changed-bytes":
				_ = os.WriteFile(path, versionedSecretFixture(t, 1, `["changed"]`), 0600)
			case "controller-uid":
				nc.UID = "changed"
			case "config-uid":
				c.UID = "changed"
			case "source-uid":
				source.uid = "changed"
			case "source-version":
				source.version = "changed"
			case "source-edit":
				source.raw = append(source.raw, ' ')
			case "destination-edit":
				c.Spec.SecretRefs[0].Path = "/discovery/1/password_list"
			case "revoked":
				nc.Spec.IntentSecretSources = nil
			}
			if _, err := p.requestFields(nc, c, source, 0); err == nil {
				t.Fatal("changed projection or identity accepted")
			}
		})
	}
}

func TestDiscoveryCredentialProjectionDoesNotEnableUnsupportedLifecycle(t *testing.T) {
	ctx := context.Background()
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareDiscoveryCredentialProjection(nc, c, source); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	if supportedConfigurationApply(c) {
		t.Fatal("input contract advertised execution")
	}
	before := nc.Status.Configuration.DeepCopy()
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	after, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ready := meta.FindStatusCondition(c.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
	if ready == nil || ready.Reason != "CleanupEnrollmentPending" {
		t.Fatal("unenrolled Delete policy did not retain the exact enrollment fence")
	}

	if calls != 0 || c.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting || hasDiscoveryConfigFinalizer(c) || !reflect.DeepEqual(before, after.Status.Configuration) {
		t.Fatal("unqualified inline credentials acquired public mutation")
	}
}

func TestDiscoveryCredentialNullGlobalAndMetadataIdentity(t *testing.T) {
	raw := discoveryCredentialSource(t, map[string]any{"password_list_version": 1, "user_name_list": []any{"operator"}, "global_credential_list": nil}, false)
	a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
	if err != nil {
		t.Fatal(err)
	}
	request, err := p.requestFields(nc, c, source, 0)
	if err != nil || len(request.globalNames) != 0 {
		t.Fatal("null global references replaced valid inline intent", err)
	}
	for _, mode := range []string{"uid", "generation", "paused", "source-version"} {
		t.Run(mode, func(t *testing.T) {
			raw := discoveryCredentialSource(t, map[string]any{"snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": "operator"}, false)
			a, nc, c, source, _ := discoveryProjectionFixture(t, raw, nil)
			p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "uid":
				nc.UID = "replacement"
			case "generation":
				c.Generation++
			case "paused":
				nc.Spec.Paused = true
			case "source-version":
				source.version = "replacement"
			}
			if _, err := p.requestFields(nc, c, source, 0); err == nil {
				t.Fatal("metadata-only credentials bypassed subject fence")
			}
		})
	}
}

func TestDiscoveryCredentialSortedJobsKeepProjectionPaths(t *testing.T) {
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	var model map[string]any
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	inventory := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
	row := inventory["discovery"].([]any)[0].(map[string]any)
	row["name"] = "z-last"
	first := map[string]any{}
	for k, v := range row {
		first[k] = v
	}
	first["name"] = "a-first"
	inventory["discovery"] = []any{row, first}
	raw, _ = json.Marshal(model)
	a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range []string{"/discovery/1/password_list", "/discovery/0/password_list"} {
		request, err := p.requestFields(nc, c, source, i)
		if err != nil || len(request.versions) != 1 || request.versions[0].Path != expected {
			t.Fatal("sorting cross-wired projected job credentials", err)
		}
	}
}

func TestDiscoveryCredentialInputIdentityAndGlobalResolution(t *testing.T) {
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator", "admin"}, "password_list_version": 1}, true)
	a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE_B","CVK_SENSITIVE_TEST_VALUE_A"]`})
	p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
	if err != nil {
		t.Fatal(err)
	}
	globals := map[string]map[string]globalCredentialMetadata{"cli_credentials": {"lab-cli": {ID: "credential-id", Name: "lab-cli"}}}
	request, key, err := p.bindRequest(nc, c, source, 0, globals)
	if err != nil {
		t.Fatal(err)
	}
	ids, ok := request.fields["globalCredentialIdList"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "credential-id" || !strings.HasPrefix(key, discoveryCredentialScopePrefix) || len(key) > 512 || strings.Contains(key, discoveryCredentialMarker) {
		t.Fatal("incomplete or unsafe native input identity")
	}
	for _, group := range []map[string]map[string]globalCredentialMetadata{nil, {"cli_credentials": {"lab-cli": {ID: "bad/id", Name: "lab-cli"}}}, {"cli_credentials": {"lab-cli": {ID: "one", Name: "lab-cli"}}, "https_read_credentials": {"lab-cli": {ID: "two", Name: "lab-cli"}}}} {
		if _, _, err := p.bindRequest(nc, c, source, 0, group); err == nil {
			t.Fatal("mixed global references were discarded or guessed")
		}
	}
	globals["cli_credentials"]["lab-cli"] = globalCredentialMetadata{ID: "replacement-id", Name: "lab-cli"}
	if _, newKey, err := p.bindRequest(nc, c, source, 0, globals); err != nil || key == newKey {
		t.Fatal("global UUID replacement lost input identity", err)
	}
	// No generic owned-discovery fingerprint may interpret this credential input
	// as a native effect, creator receipt or dispatch capability.
	if ownedDiscoverySelected(key) {
		t.Fatal("private input identity acquired public authority")
	}
}

func TestDiscoveryCredentialInputIdentityUsesVersionsAndSets(t *testing.T) {
	keys := []string{}
	for _, test := range []struct {
		users     []any
		passwords string
		version   int64
	}{
		{[]any{"operator", "admin"}, `["CVK_SENSITIVE_TEST_VALUE_B","CVK_SENSITIVE_TEST_VALUE_A"]`, 1},
		{[]any{"admin", "operator"}, `["CVK_SENSITIVE_TEST_VALUE_A","CVK_SENSITIVE_TEST_VALUE_B"]`, 1},
		{[]any{"admin", "operator"}, `["CVK_SENSITIVE_TEST_VALUE_A","CVK_SENSITIVE_TEST_VALUE_B"]`, 2},
	} {
		raw := discoveryCredentialSource(t, map[string]any{"user_name_list": test.users, "password_list_version": test.version}, false)
		a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": test.passwords})
		p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
		if err != nil {
			t.Fatal(err)
		}
		request, key, err := p.bindRequest(nc, c, source, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, present := request.fields["globalCredentialIdList"]; present {
			t.Fatal("inline-only request invented global selection")
		}
		keys = append(keys, key)
	}
	if keys[0] != keys[1] || keys[1] == keys[2] {
		t.Fatal("unordered sets or explicit version changes lost identity")
	}
}

func TestDiscoveryCredentialProjectionRequiresExactActorAndSource(t *testing.T) {
	for _, mode := range []string{"actor-uid", "actor-generation", "actor-namespace", "report", "inline-mismatch", "inline-source-uid"} {
		t.Run(mode, func(t *testing.T) {
			raw := discoveryCredentialSource(t, map[string]any{"password_list_version": 1, "user_name_list": []any{"operator"}}, false)
			a, nc, c, source, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
			switch mode {
			case "actor-uid":
				nc.UID = "foreign"
			case "actor-generation":
				nc.Generation++
			case "actor-namespace":
				nc.Namespace = "foreign"
				c.Namespace = "foreign"
			case "report":
				c.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "inline-mismatch":
				source.raw = append(source.raw, ' ')
			case "inline-source-uid":
				source.uid = "foreign"
			}
			if _, err := a.prepareDiscoveryCredentialProjection(nc, c, source); err == nil {
				t.Fatal("unbound actor/source read a credential projection")
			}
		})
	}
}
