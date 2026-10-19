// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestLiveCatalystCenterAuthenticationPolicyServerRegistration(t *testing.T) {
	if os.Getenv("CATC_POLICY_SERVER_EXECUTION") != "isolated-lab-server" {
		t.Skip("isolated lab server registration not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	servers, err := c.authenticationPolicyServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := c.externalAuthentication(ctx)
	if err != nil {
		t.Fatal(err)
	}
	attribute, err := c.externalAAAAttribute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 || enabled || attribute != "" {
		t.Fatal("qualification requires no policy servers and disabled/empty external authentication")
	}
	// The registration points to the authorized lab runner, not a production
	// authentication service. Do not enable authentication against this fixture.
	desired := policyServerFixture()
	entropy := make([]byte, 24)
	if _, err = rand.Read(entropy); err != nil {
		t.Fatal("cannot generate fixture credential")
	}
	secret := hex.EncodeToString(entropy)
	id := ""
	settled := true
	wait := func(ctx context.Context, task Task, submitted time.Time) error {
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil || done {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	t.Cleanup(func() {
		if !settled || id == "" {
			t.Log("retained uncertain policy server registration", id)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		enabled, err := c.externalAuthentication(cleanup)
		if err != nil || enabled {
			t.Error("cleanup refused enabled external authentication", err)
			return
		}
		attribute, err := c.externalAAAAttribute(cleanup)
		if err != nil || attribute != "" {
			t.Error("cleanup refused unrestored custom attribute", err)
			return
		}
		servers, err := c.authenticationPolicyServers(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		server, exists := servers[desired.IP]
		if !exists {
			return
		}
		if server.ID != id || !policyServerMatches(desired, server) {
			t.Error("cleanup refused replaced policy server")
			return
		}
		submitted := time.Now()
		raw, err := c.mutate(cleanup, http.MethodDelete, authenticationPolicyServersPath+"/"+id, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("cleanup task receipt missing")
			return
		}
		if err = wait(cleanup, receipt.Response, submitted); err != nil {
			t.Error(err)
			return
		}
		servers, err = c.authenticationPolicyServers(cleanup)
		if err != nil || len(servers) != 0 {
			t.Error("policy server baseline not restored", err)
			return
		}
		t.Log("policy server cleanup and empty baseline verified", id)
	})
	write, err := c.planAuthenticationPolicyServerRegistration(ctx, desired, secret)
	if err != nil {
		t.Fatal(err)
	}
	settled = false
	submitted := time.Now()
	task, err := write.submit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("policy server registration submitted", task.ID)
	if err = wait(ctx, task, submitted); err != nil {
		t.Fatal(err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action}
	var verified bool
	id, verified, err = c.verifyAuthenticationPolicyServerRegistration(ctx, desired, op)
	if err != nil || !verified {
		t.Fatal("policy server metadata verification failed", err)
	}
	settled = true
	t.Log("registration metadata verified", id)
	plans, err := c.compileConfigReports([]byte(policyServerReportModel), []string{"system_settings"})
	if err != nil || len(plans) != 1 || plans[0].count != 2 {
		t.Fatal("policy server model report did not compile", err)
	}
	if drift, err := plans[0].observe(ctx); err != nil || len(drift) != 0 {
		t.Fatal("policy server model metadata drift", err)
	}
	t.Log("pinned NetAsCode AAA metadata model and external flag report converged")
	// Qualify the version-driven PUT on the same isolated server. The manifest
	// reconciler's Kubernetes journal is tested separately; this live executor
	// uses in-memory fixture bindings and does not claim a worker rollout.
	intent, err := parsePolicyServerApply([]byte(strings.Replace(policyServerApplyModel, `"shared_secret_version":1`, `"shared_secret_version":2`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(entropy); err != nil {
		t.Fatal("cannot generate rotation credential")
	}
	version := ciskov1.NetworkControllerConfigurationCredentialVersion{Path: projectedSecretPointer, Version: 1, SourceAlias: "fixture", SecretName: "fixture", SecretKey: "shared-secret"}
	executor := &policyServerExecutor{
		client: c, desired: intent, owner: "live-qualification",
		secrets: &configurationSecretSnapshot{uid: "live-qualification", values: []configurationSecretValue{{
			ref:    configapi.NetworkControllerSecretRef{Section: "system_settings", Path: projectedSecretPointer, Source: "fixture"},
			source: ciskov1.NetworkControllerIntentSecretSource{Alias: "fixture", Name: "fixture", Key: "shared-secret"}, value: versionedSecretFixture(t, 2, hex.EncodeToString(entropy)),
		}}},
		receipts: []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "system_settings", Resource: desired.IP, ConfigUID: "live-qualification", TargetID: id, OperationID: strings.Repeat("a", 32), Versions: []ciskov1.NetworkControllerConfigurationCredentialVersion{version}}},
	}
	rotation, err := executor.next(ctx)
	if err != nil || rotation == nil || rotation.action != "Update" || rotation.identity.target != id {
		t.Fatal("rotation did not bind existing fixture UUID", err)
	}
	settled = false
	submitted = time.Now()
	task, err = rotation.submit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("version-driven policy server update submitted", task.ID)
	if err := wait(ctx, task, submitted); err != nil {
		t.Fatal(err)
	}
	rotationOp := &ciskov1.NetworkControllerConfigurationOperation{Section: "system_settings", ConfigUID: executor.owner, Resource: desired.IP, Action: "Update", TargetID: id, CredentialVersions: rotation.identity.credentials}
	if observed, ok, err := executor.verifyCredential(ctx, rotationOp); err != nil || !ok || observed != id {
		t.Fatal("rotation task/readback failed", err)
	}
	settled = true
	executor.receipts[0].Versions = rotation.identity.credentials
	if next, err := executor.next(ctx); err != nil || next != nil {
		t.Fatal("rotated version did not converge", err)
	}
	t.Log("credential version update, UUID readback and no-op planning verified; external RADIUS authentication not tested")
	if os.Getenv("CATC_KEYWRAP_EXECUTION") == "isolated-lab-server" {
		wrapped, err := parsePolicyServerApply([]byte(strings.Replace(policyKeyWrapModel(), `"shared_secret_version":1`, `"shared_secret_version":2`, 1)))
		if err != nil {
			t.Fatal(err)
		}
		executor.desired = wrapped
		for _, leaf := range wrapped[0].extra {
			size := 8
			if leaf.field == "messageKey" {
				size = 10
			}
			key := make([]byte, size)
			if _, err := rand.Read(key); err != nil {
				t.Fatal("cannot generate fixture key")
			}
			executor.secrets.values = append(executor.secrets.values, configurationSecretValue{
				ref:    configapi.NetworkControllerSecretRef{Section: "system_settings", Path: leaf.path, Source: leaf.field},
				source: ciskov1.NetworkControllerIntentSecretSource{Alias: leaf.field, Name: "fixture", Key: leaf.field},
				value:  versionedSecretFixture(t, 1, hex.EncodeToString(key)),
			})
		}
		for step := 0; step < 2; step++ {
			if step == 1 {
				executor.desired[0].extra[0].version = 2
				if write, err := executor.next(ctx); err == nil || write != nil {
					t.Fatal("stale encryption-key projection accepted")
				}
				key := make([]byte, 8)
				if _, err := rand.Read(key); err != nil {
					t.Fatal("cannot generate rotation key")
				}
				executor.secrets.values[1].value = versionedSecretFixture(t, 2, hex.EncodeToString(key))
			}
			write, err := executor.next(ctx)
			if err != nil || write == nil || len(write.identity.credentials) != 3 {
				t.Fatal("keywrap planning failed", err)
			}
			settled = false
			submitted := time.Now()
			task, err := write.submit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Log("keywrap step submitted", step, task.ID)
			if err := wait(ctx, task, submitted); err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Section: "system_settings", ConfigUID: executor.owner, Resource: desired.IP, Action: "Update", TargetID: id, CredentialVersions: write.identity.credentials}
			if observed, ok, err := executor.verifyCredential(ctx, op); err != nil || !ok || observed != id {
				t.Fatal("keywrap readback failed", err)
			}
			desired = executor.desired[0].server
			settled = true
			executor.receipts[0].Versions = write.identity.credentials
			if write, err := executor.next(ctx); err != nil || write != nil {
				t.Fatal("keywrap convergence failed", err)
			}
		}
		t.Log("keywrap addition, independent encryption-key rotation, stale projection refusal and no-op verified")
	}

	if err = c.submitExternalAuth(ctx, externalAuthPath, map[string]any{"enable": false}); err != nil {
		t.Fatal(err)
	}
	if enabled, err = c.externalAuthentication(ctx); err != nil || enabled {
		t.Fatal("disabled flag readback failed", err)
	}
	t.Log("synchronous disabled-flag write/readback verified; enabling and actual RADIUS authentication unqualified")
	t.Run("CustomAttribute", TestLiveCatalystCenterExternalAuthenticationAttribute)
}
