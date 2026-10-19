// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func policyKeyWrapModel() string {
	return strings.Replace(policyServerApplyModel, `"radius":{`, `"radius":{"enable_key_wrap":{"encryption_key_version":1,"message_key_version":1},`, 1)
}

func TestPolicyServerKeyWrapCreateRotateAndVerify(t *testing.T) {
	_, _, source, _ := credentialVersionJournalFixture(t)
	desired, err := parsePolicyServerApply([]byte(policyKeyWrapModel()))
	if err != nil || len(desired) != 1 || len(desired[0].extra) != 2 || desired[0].server.EncryptionScheme != "KEYWRAP" {
		t.Fatal("keywrap parse failed", err)
	}
	for _, leaf := range desired[0].extra {
		source.secrets.values = append(source.secrets.values, configurationSecretValue{
			ref:    configapi.NetworkControllerSecretRef{Section: "system_settings", Path: leaf.path, Source: "keywrap"},
			source: ciskov1.NetworkControllerIntentSecretSource{Alias: "keywrap", Name: "keys", Key: leaf.field},
			value:  versionedSecretFixture(t, 1, "fixture-"+leaf.field),
		})
	}
	var actual *authenticationPolicyServer
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" {
			list := []authenticationPolicyServer{}
			if actual != nil {
				list = append(list, *actual)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": list})
			return
		}
		writes++
		if (writes == 1 && r.Method != "POST") || (writes == 2 && (r.Method != "PUT" || r.URL.Path != authenticationPolicyServersPath+"/server")) {
			t.Error("wrong keywrap write endpoint")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid payload")
		}
		expected := "fixture-encryptionKey"
		if writes == 2 {
			expected = "rotated-encryptionKey"
		}
		if body["encryptionScheme"] != "KEYWRAP" || body["encryptionKey"] != expected || body["messageKey"] != "fixture-messageKey" || body["sharedSecret"] != projectedCredential {
			t.Error("full replacement lost or mixed credentials")
		}
		actual = &authenticationPolicyServer{}
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, actual)
		actual.ID = "server"
		fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
	})
	e := &policyServerExecutor{client: c, desired: desired, secrets: source.secrets, owner: source.secrets.uid}
	ctx := context.Background()
	for step := 0; step < 2; step++ {
		if step == 1 {
			for i := range e.desired[0].extra {
				if e.desired[0].extra[i].field == "encryptionKey" {
					e.desired[0].extra[i].version = 2
				}
			}
			// A newly requested counter cannot use an older projection.
			if write, err := e.next(ctx); err == nil || write != nil || writes != 1 {
				t.Fatal("stale keywrap projection submitted")
			}
			for i := range e.secrets.values {
				if e.secrets.values[i].source.Key == "encryptionKey" {
					e.secrets.values[i].value = versionedSecretFixture(t, 2, "rotated-encryptionKey")
				}
			}
		}
		write, err := e.next(ctx)
		if err != nil || write == nil || len(write.identity.credentials) != 3 {
			t.Fatal("keywrap write plan failed", err)
		}
		if err := validateConfigurationCredentialVersions(source, "system_settings", write.identity.credentials); err != nil {
			t.Fatal("credential journal binding failed", err)
		}
		if _, err := write.submit(ctx); err != nil {
			t.Fatal(err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Section: "system_settings", ConfigUID: e.owner, Resource: desired[0].server.IP, Action: write.action, TargetID: write.identity.target, CredentialVersions: write.identity.credentials}
		// Some appliance APIs accept the task but silently ignore the scheme.
		// A successful task must not turn that response into convergence.
		actual.EncryptionScheme = ""
		if _, verified, err := e.verifyCredential(ctx, op); err != nil || verified {
			t.Fatal("ignored keywrap fields falsely verified", err)
		}
		actual.EncryptionScheme = "KEYWRAP"
		if id, verified, err := e.verifyCredential(ctx, op); err != nil || !verified || id != "server" {
			t.Fatal("keywrap verification failed", err)
		}
		e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "system_settings", Resource: op.Resource, ConfigUID: e.owner, TargetID: "server", OperationID: strings.Repeat("a", 32), Versions: write.identity.credentials}}
		if next, err := e.next(ctx); err != nil || next != nil {
			t.Fatal("keywrap receipt did not converge", err)
		}
	}
	// Removing the nested block cannot silently discard two managed keys.
	plain, err := parsePolicyServerApply([]byte(policyServerApplyModel))
	if err != nil {
		t.Fatal(err)
	}
	e.desired = plain
	if next, err := e.next(ctx); err == nil || next != nil || writes != 2 {
		t.Fatal("keywrap was silently removed")
	}
}

func TestPolicyServerKeyWrapRejectsIncompleteOrInlineKeys(t *testing.T) {
	for _, wrap := range []string{`null`, `{}`, `{"encryption_key_version":1}`, `{"encryption_key_version":1,"message_key_version":0}`, `{"encryption_key_version":1,"message_key_version":1,"encryption_key":"private-value"}`, `{"encryption_key_version":1,"message_key_version":1,"unknown":true}`} {
		raw := strings.Replace(policyServerApplyModel, `"radius":{`, `"radius":{"enable_key_wrap":`+wrap+`,`, 1)
		if _, err := parsePolicyServerApply([]byte(raw)); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid keywrap intent accepted or exposed")
		}
	}
}

func TestPolicyCredentialRotationRejectsMalformedBindings(t *testing.T) {
	base := ciskov1.NetworkControllerConfigurationCredentialVersion{Path: "/authentication_and_policy_servers/aaa/0/shared_secret", Version: 2, SourceAlias: "source", SecretName: "secret", SecretKey: "key"}
	for _, mode := range []string{"duplicate", "rollback", "source-without-version", "unknown", "empty-source", "zero-version", "removal"} {
		t.Run(mode, func(t *testing.T) {
			desired := []ciskov1.NetworkControllerConfigurationCredentialVersion{base}
			switch mode {
			case "duplicate":
				desired = append(desired, base)
			case "rollback":
				desired[0].Version = 1
			case "source-without-version":
				desired[0].SecretKey = "replacement"
			case "unknown":
				desired[0].Path += "/unknown"
			case "empty-source":
				desired[0].SourceAlias = ""
			case "zero-version":
				desired[0].Version = 0
			case "removal":
				desired = nil
			}
			if _, err := policyCredentialRotation([]ciskov1.NetworkControllerConfigurationCredentialVersion{base}, desired); err == nil {
				t.Fatal("unsafe credential transition accepted")
			}
		})
	}
	reordered := base
	reordered.Path = "/authentication_and_policy_servers/aaa/3/shared_secret"
	if rotate, err := policyCredentialRotation([]ciskov1.NetworkControllerConfigurationCredentialVersion{base}, []ciskov1.NetworkControllerConfigurationCredentialVersion{reordered}); err != nil || rotate {
		t.Fatal("array reordering changed credential identity", err)
	}
}

func TestPlainRegistrationRejectsKeyWrapBeforeHTTP(t *testing.T) {
	desired := policyServerFixture()
	desired.EncryptionScheme = "KEYWRAP"
	if write, err := (&client{}).planAuthenticationPolicyServerRegistration(context.Background(), desired, "fixture"); err == nil || write != nil {
		t.Fatal("scalar credential helper accepted incomplete keywrap")
	}
}
