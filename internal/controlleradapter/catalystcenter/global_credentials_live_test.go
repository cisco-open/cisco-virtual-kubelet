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
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestLiveGlobalCredentialLifecycle(t *testing.T) {
	if os.Getenv("CATC_GLOBAL_CREDENTIAL_EXECUTION") != "isolated-unassigned-credentials" {
		t.Skip("isolated credential qualification not enabled")
	}
	c := liveClient(t)
	assignmentSite := ""
	if os.Getenv("CATC_SITE_CREDENTIAL_EXECUTION") == "isolated-empty-area" {
		assignmentSite = liveCredentialAssignmentSite(t, c)
	}
	for _, kind := range globalCredentialKinds {
		t.Run(kind.model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			before, err := c.globalCredentials(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want, snapshot := globalCredentialFixture(t, kind.model)
			random := make([]byte, 12)
			if _, err := rand.Read(random); err != nil {
				t.Fatal("cannot generate fixture name")
			}
			want.metadata.Name = "CVK-Qualification-" + hex.EncodeToString(random)
			for i := range snapshot.values {
				if _, err := rand.Read(random); err != nil {
					t.Fatal("cannot generate credential")
				}
				snapshot.values[i].value = versionedSecretFixture(t, 1, hex.EncodeToString(random))
			}
			e := &globalCredentialExecutor{client: c, desired: []globalCredentialIntent{want}, secrets: snapshot, owner: "owner"}
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
				if t.Failed() && assignmentSite != "" {
					t.Log("assigned fixture retained after qualification failure", kind.model, id)
					return
				}
				if !settled || id == "" {
					t.Log("uncertain global credential retained for inspection", kind.model, id)
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				actual, err := c.globalCredentials(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				got, exists := actual[kind.model][want.metadata.Name]
				if !exists || got.ID != id || !globalCredentialMatches(want.metadata, got) {
					t.Error("cleanup refused missing or replaced credential")
					return
				}
				submitted := time.Now()
				raw, err := c.mutate(ctx, http.MethodDelete, globalCredentialsPath+"/"+url.PathEscape(id), nil)
				if err != nil {
					t.Error(err)
					return
				}
				var receipt struct {
					Response Task `json:"response"`
				}
				if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
					t.Error("cleanup receipt unavailable")
					return
				}
				if err := wait(ctx, receipt.Response, submitted); err != nil {
					t.Error(err)
					return
				}
				after, err := c.globalCredentials(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				for group, entries := range before {
					if len(entries) != len(after[group]) {
						t.Error("credential baseline count changed")
						return
					}
					for name, entry := range entries {
						if after[group][name].ID != entry.ID {
							t.Error("credential baseline identity changed")
							return
						}
					}
				}
				t.Log("unassigned fixture removed; original credential identities retained", kind.model, receipt.Response.ID)
			})
			for step := 0; step < 2; step++ {
				if step == 1 {
					e.desired[0].credentials[0].version = 2
					if write, err := e.next(ctx); err == nil || write != nil {
						t.Fatal("stale projection planned a rotation")
					}
					if _, err := rand.Read(random); err != nil {
						t.Fatal("cannot generate rotated credential")
					}
					snapshot.values[0].value = versionedSecretFixture(t, 2, hex.EncodeToString(random))
				}
				write, err := e.next(ctx)
				if err != nil || write == nil {
					t.Fatal("credential plan failed", err)
				}
				settled = false
				submitted := time.Now()
				task, err := write.submit(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Log("credential operation submitted", kind.model, write.action, task.ID)
				if err := wait(ctx, task, submitted); err != nil {
					t.Fatal(err)
				}
				op := &ciskov1.NetworkControllerConfigurationOperation{Section: "network_settings", ConfigUID: e.owner, Resource: write.resource, Action: write.action, TargetID: write.identity.target, CredentialVersions: write.identity.credentials}
				var verified bool
				id, verified, err = e.verifyCredential(ctx, op)
				if err != nil || !verified {
					t.Fatal("credential readback failed", err)
				}
				settled = true
				e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: write.resource, ConfigUID: e.owner, TargetID: id, OperationID: strings.Repeat("a", 32), Versions: write.identity.credentials}}
				if next, err := e.next(ctx); err != nil || next != nil {
					t.Fatal("credential did not converge", err)
				}
			}
			if assignmentSite != "" {
				qualifyLiveCredentialAssignment(t, c, assignmentSite, want)
			}
			if assignmentSite != "" && os.Getenv("CATC_WIRED_BULK_OWNED_CREDENTIAL_EXECUTION") == "simulated-owner-native-credential" {
				qualifyLiveWiredBulkOwnedCredential(t, c, ctx, assignmentSite, want, e.receipts)
			}
			t.Log("create, independent rotation, readback and no-op qualified; device authentication not tested", kind.model, id)
		})
		if t.Failed() {
			return
		}
	}
}
