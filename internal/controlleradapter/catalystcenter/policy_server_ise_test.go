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

const policyISEModel = `{"catalyst_center":{"system_settings":{"authentication_and_policy_servers":{"ise":{"ip_address":"192.0.2.44","fqdn":"ise.example.test","username":"admin","pxgrid_enabled":true,"use_catc_cert_for_pxgrid":true,"shared_secret_version":1,"password_version":1,"protocols":{"radius":{"authentication_port":1812,"accounting_port":1813}},"retries":1,"timeout":5}}}}}`

func policyISEExecutorFixture(t *testing.T, c *client) *policyServerExecutor {
	t.Helper()
	desired, err := parsePolicyServerApply([]byte(policyISEModel))
	if err != nil {
		t.Fatal(err)
	}
	e := &policyServerExecutor{client: c, desired: desired, owner: "ise-owner", secrets: &configurationSecretSnapshot{uid: "ise-owner"}}
	for _, leaf := range desired[0].credentialIntents() {
		e.secrets.values = append(e.secrets.values, configurationSecretValue{
			ref:    configapi.NetworkControllerSecretRef{Section: "system_settings", Path: leaf.path, Source: leaf.field},
			source: ciskov1.NetworkControllerIntentSecretSource{Alias: leaf.field, Name: "ise", Key: leaf.field}, value: versionedSecretFixture(t, 1, "private-"+leaf.field),
		})
	}
	return e
}

func TestISERegistrationIntegrationAndCredentialRotation(t *testing.T) {
	var actual *authenticationPolicyServer
	status, step := "INPROGRESS", "WAITING_USER_INPUT"
	target := "ise-server"
	writes, accepts := 0, 0
	var c *client
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == authenticationPolicyServersPath && r.Method == "GET":
			list := []authenticationPolicyServer{}
			if actual != nil {
				list = append(list, *actual)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": list})
		case r.URL.Path == policyISEIntegrationPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"aaaServerSettingId": target, "overallStatus": status, "steps": []any{map[string]any{"stepId": "certificate", "stepStatus": step, "certAcceptedByUser": accepts > 0}}})
		case r.URL.Path == "/dna/intent/api/v1/integrate-ise/ise-server":
			if r.Method != "PUT" {
				t.Error("wrong integration method")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["isCertAcceptedByUser"] != true {
				t.Error("invalid acceptance payload")
			}
			accepts++
			step = "IN_PROGRESS"
			fmt.Fprint(w, `{}`)
		case r.Method == "POST" || r.Method == "PUT":
			writes++
			if writes == 1 && (r.Method != "POST" || r.URL.Path != authenticationPolicyServersPath) {
				t.Error("wrong registration endpoint")
			}
			if writes == 2 && (r.Method != "PUT" || r.URL.Path != authenticationPolicyServersPath+"/ise-server") {
				t.Error("wrong rotation endpoint")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			dto := body["ciscoIseDtos"].([]any)[0].(map[string]any)
			password := "private-isePassword"
			if writes == 2 {
				password = "rotated-password"
			}
			if dto["password"] != password || dto["subscriberName"] != "192.0.2.44" || body["sharedSecret"] != "private-sharedSecret" || body["isIseEnabled"] != true || body["pxgridEnabled"] != true {
				t.Error("incomplete ISE credential payload")
			}
			raw, _ := json.Marshal(body)
			actual = &authenticationPolicyServer{}
			_ = json.Unmarshal(raw, actual)
			actual.ID = "ise-server"
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	})
	e := policyISEExecutorFixture(t, c)
	ctx := context.Background()
	if e.count() != 2 {
		t.Fatal("registration and integration not counted")
	}
	register, err := e.next(ctx)
	if err != nil || register == nil || len(register.identity.credentials) != 2 {
		t.Fatal("registration plan", err)
	}
	if _, err = register.submit(ctx); err != nil {
		t.Fatal(err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Section: "system_settings", ConfigUID: e.owner, Resource: e.desired[0].server.IP, Action: "Create", CredentialVersions: register.identity.credentials}
	id, ok, err := e.verifyCredential(ctx, op)
	if err != nil || !ok || id != "ise-server" {
		t.Fatal("registration verification", err)
	}
	// Do not adopt a registration without the journal's durable receipt.
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("adopted unreceipted ISE")
	}
	e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "system_settings", Resource: op.Resource, ConfigUID: e.owner, TargetID: id, OperationID: strings.Repeat("a", 32), Versions: op.CredentialVersions}}
	integrate, err := e.next(ctx)
	if err != nil || integrate == nil || integrate.submitCompleted == nil || integrate.identity.target != id {
		t.Fatal("integration plan", err)
	}
	if err := integrate.submitCompleted(ctx); err != nil {
		t.Fatal(err)
	}
	integrationOp := &ciskov1.NetworkControllerConfigurationOperation{Section: "system_settings", ConfigUID: e.owner, Resource: integrate.resource, Action: "Update", TargetID: id}
	if e.credentialOperation(integrationOp) {
		t.Fatal("certificate operation mistaken for credential write")
	}
	if verified, err := e.verify(ctx, integrationOp); err != nil || verified {
		t.Fatal("incomplete integration verified", err)
	}
	// Reconstruct the executor as after worker restart: same UUID must complete.
	resumed := *e
	target = "replacement"
	if verified, err := resumed.verify(ctx, integrationOp); err == nil || verified {
		t.Fatal("global status for another server accepted")
	}
	target = id
	status = "COMPLETE"
	step = "COMPLETE"
	if verified, err := resumed.verify(ctx, integrationOp); err != nil || !verified {
		t.Fatal("correlated integration did not verify", err)
	}
	if next, err := resumed.next(ctx); err != nil || next != nil {
		t.Fatal("integrated ISE did not converge", err)
	}
	e.desired[0].extra[0].version = 2
	if next, err := e.next(ctx); err == nil || next != nil {
		t.Fatal("stale password projection used")
	}
	e.secrets.values[1].value = versionedSecretFixture(t, 2, "rotated-password")
	rotate, err := e.next(ctx)
	if err != nil || rotate == nil {
		t.Fatal("password rotation plan", err)
	}
	if _, err := rotate.submit(ctx); err != nil {
		t.Fatal(err)
	}
	if writes != 2 || accepts != 1 {
		t.Fatal("operation replay")
	}
	raw, _ := json.Marshal(actual)
	if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "rotated-password") {
		t.Fatal("read projection retained password")
	}
}

func TestISEIntentRejectsUnsafeOrIncompleteInput(t *testing.T) {
	for _, pair := range [][2]string{
		{`"password_version":1`, `"password_version":0`},
		{`"password_version":1`, `"password":"private-value","password_version":1`},
		{`"pxgrid_enabled":true`, `"pxgrid_enabled":null`},
		{`"fqdn":"ise.example.test"`, `"fqdn":"bad host"`},
		{`"username":"admin"`, `"username":""`},
		{`"password_version":1`, `"password_version":1,"unknown":true`},
	} {
		if _, err := parsePolicyServerApply([]byte(strings.Replace(policyISEModel, pair[0], pair[1], 1))); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("unsafe ISE model accepted or exposed")
		}
	}
}

func TestISEIntegrationRejectsUncorrelatedOrIncompleteStatus(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"aaaServerSettingId":"other","overallStatus":"COMPLETE","steps":[]}`,
		`{"aaaServerSettingId":"ise-server","overallStatus":"COMPLETE","steps":[]}`,
		`{"aaaServerSettingId":"ise-server","overallStatus":"FAILED","overallErrorMessage":"private-value","steps":[{"stepId":"1","stepStatus":"FAILED","certAcceptedByUser":false}]}`,
		`{"aaaServerSettingId":"ise-server","overallStatus":"COMPLETE","steps":[{"stepId":"1","stepStatus":"IN_PROGRESS","certAcceptedByUser":true}]}`,
		`{"aaaServerSettingId":"ise-server","overallStatus":"COMPLETE","steps":[{"stepId":"1","stepStatus":"COMPLETE"}]}`,
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			fmt.Fprint(w, body)
		})
		if _, err := c.policyISEIntegration(context.Background(), "ise-server"); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("unsafe integration status accepted or exposed")
		}
	}
}

func TestISECertificateAcknowledgementLossUsesSharedFence(t *testing.T) {
	for _, response := range []string{`{}`, `{"response":{"taskId":"unexpected"}}`, `{"error":"private-value"}`, `null`} {
		t.Run(response, func(t *testing.T) {
			a, config := journalFixture(t)
			ctx := context.Background()
			config.Spec.ManagedSections = []string{"system_settings"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			accepts := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == policyISEIntegrationPath {
					fmt.Fprint(w, `{"aaaServerSettingId":"ise-server","overallStatus":"INPROGRESS","steps":[{"stepId":"cert","stepStatus":"WAITING_USER_INPUT","certAcceptedByUser":false}]}`)
					return
				}
				accepts++
				fmt.Fprint(w, response)
			})
			e := policyISEExecutorFixture(t, c)
			server := e.desired[0].server
			server.ID = "ise-server"
			write, err := e.nextISEIntegration(ctx, map[string]authenticationPolicyServer{server.IP: server})
			if err != nil || write == nil {
				t.Fatal("integration planning failed", err)
			}
			err = a.submitConfigurationWrite(ctx, config, nil, "system_settings", write)
			expected := "Unknown"
			if response == `{}` {
				expected = "Verifying"
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid acknowledgement accepted")
			}
			op := journalState(t, a).Operation
			if op.Phase != expected || op.TargetID != server.ID || op.TaskID != "" {
				t.Fatal("wrong persisted integration boundary")
			}
			resumed := *a
			if err := resumed.submitConfigurationWrite(ctx, config, nil, "system_settings", write); err == nil || accepts != 1 {
				t.Fatal("certificate acceptance replayed")
			}
			if err := resumed.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("unverified integration released")
			}
		})
	}
}

func TestISESingletonAndIntegrationDependency(t *testing.T) {
	for _, mode := range []string{"other-ise", "integration-pending"} {
		t.Run(mode, func(t *testing.T) {
			e := policyISEExecutorFixture(t, nil)
			server := e.desired[0].server
			server.ID = "ise-server"
			if mode == "other-ise" {
				server.IP = "192.0.2.45"
			}
			_, versions, err := e.credentials(e.desired[0])
			if err != nil {
				t.Fatal(err)
			}
			e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "system_settings", Resource: e.desired[0].server.IP, ConfigUID: e.owner, TargetID: server.ID, OperationID: strings.Repeat("b", 32), Versions: versions}}
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("dependent write submitted before integration")
				}
				switch r.URL.Path {
				case authenticationPolicyServersPath:
					_ = json.NewEncoder(w).Encode(map[string]any{"response": []authenticationPolicyServer{server}})
				case policyISEIntegrationPath:
					fmt.Fprint(w, `{"aaaServerSettingId":"ise-server","overallStatus":"INPROGRESS","steps":[{"stepId":"connection","stepStatus":"IN_PROGRESS","certAcceptedByUser":true}]}`)
				case externalAuthPath:
					fmt.Fprint(w, `{"response":{"external-authentication-flag":[{"enabled":false}]}}`)
				default:
					t.Error("unexpected read")
					w.WriteHeader(404)
				}
			})
			combined := &systemSettingsExecutor{servers: e, external: &externalAuthExecutor{client: e.client, desired: externalAuthIntent{enabled: true}}}
			if write, err := combined.next(context.Background()); err == nil || write != nil {
				t.Fatal("unowned or pending ISE allowed a dependent action")
			}
		})
	}
}

func TestISECredentialReceiptCannotAdoptAAAIdentity(t *testing.T) {
	prior := ciskov1.NetworkControllerConfigurationCredentialVersion{Path: "/authentication_and_policy_servers/aaa/0/shared_secret", Version: 1, SourceAlias: "source", SecretName: "secret", SecretKey: "key"}
	desired := prior
	desired.Path = "/authentication_and_policy_servers/ise/shared_secret"
	for _, version := range []int64{1, 2} {
		desired.Version = version
		if _, err := policyCredentialRotation([]ciskov1.NetworkControllerConfigurationCredentialVersion{prior}, []ciskov1.NetworkControllerConfigurationCredentialVersion{desired}); err == nil {
			t.Fatal("AAA receipt transferred to ISE identity")
		}
	}
}
