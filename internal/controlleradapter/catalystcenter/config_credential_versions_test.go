// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func versionedSecretFixture(t *testing.T, version int64, value string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"version": version, "value": value})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVersionedSecretProjectionContract(t *testing.T) {
	if value, err := decodeConfigurationVersionedSecret(versionedSecretFixture(t, 2, projectedCredential), 2); err != nil || value != projectedCredential {
		t.Fatal("valid versioned projection rejected")
	}
	for _, raw := range []string{
		`bare-secret`, `{"version":1,"value":"sensitive"}`, `{"version":2,"value":""}`, `{"version":2,"value":null}`,
		`{"version":2,"version":2,"value":"sensitive"}`, `{"version":2,"value":"first","value":"second"}`,
		`{"version":2,"value":"sensitive","extra":true}`, `{"version":2.0,"value":"sensitive"}`,
		`{"version":2,"value":"sensitive"} {}`, `{"version":2,"value":"a\u0000b"}`,
	} {
		if _, err := decodeConfigurationVersionedSecret([]byte(raw), 2); err != errConfigurationSecret {
			t.Fatal("invalid or stale projection accepted")
		}
	}
}

func credentialVersionJournalFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, configurationResourceIdentity) {
	t.Helper()
	nc, config, root, path, destinations := configurationSecretFixture(t)
	if err := os.WriteFile(path, versionedSecretFixture(t, 1, projectedCredential), 0600); err != nil {
		t.Fatal(err)
	}
	nc.Spec.Type = TypeName
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	scheme := runtime.NewScheme()
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := configapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, config).WithObjects(nc, config).Build()
	if err := k8s.Get(context.Background(), ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolveConfigurationSecrets(nc, config, root, destinations)
	if err != nil {
		t.Fatal(err)
	}
	a := &adapter{key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation, statusReader: k8s, statusWriter: k8s}
	identity := configurationResourceIdentity{credentials: []ciskov1.NetworkControllerConfigurationCredentialVersion{{
		Path: projectedSecretPointer, Version: 1, SourceAlias: "aaa-secret", SecretName: "aaa-credentials", SecretKey: "radius",
	}}}
	return a, config, &configSourceSnapshot{secrets: snapshot}, identity
}

func TestCredentialVersionJournalPersistsAndRequiresBinding(t *testing.T) {
	a, config, source, identity := credentialVersionJournalFixture(t)
	ctx := context.Background()
	op, err := a.prepareConfiguration(ctx, config, source, "system_settings", "192.0.2.43", "Create", identity)
	if err != nil {
		t.Fatal(err)
	}
	// Neither a parser mutating its own slice nor DeepCopy callers may change
	// the journal's version record or another operation copy.
	copy := op.DeepCopy()
	copy.CredentialVersions[0].Version = 99
	if op.CredentialVersions[0].Version != 1 || journalState(t, a).Operation.CredentialVersions[0].Version != 1 {
		t.Fatal("credential version slice aliases journal")
	}
	if err := a.startConfiguration(ctx, config, source, op.ID); err != errConfigurationStale {
		t.Fatal("versioned operation accepted absent expected binding", err)
	}
	identity.credentials[0].Version = 2
	if err := a.startConfiguration(ctx, config, source, op.ID, identity); err != errConfigurationStale {
		t.Fatal("changed version obtained permit", err)
	}
	identity.credentials[0].Version = 1
	if err := a.startConfiguration(ctx, config, source, op.ID, identity); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "task"); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != errConfigurationTransition {
		t.Fatal("metadata verification fabricated applied credential receipt", err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != errConfigurationBusy {
		t.Fatal("credential operation released without applied-version receipt", err)
	}
}

func TestCredentialVersionJournalRejectsInvalidBindings(t *testing.T) {
	for _, field := range []string{"version", "path", "alias", "name", "key", "duplicate", "snapshot"} {
		t.Run(field, func(t *testing.T) {
			a, config, source, identity := credentialVersionJournalFixture(t)
			v := &identity.credentials[0]
			switch field {
			case "version":
				v.Version = 0
			case "path":
				v.Path = "/other"
			case "alias":
				v.SourceAlias = "other"
			case "name":
				v.SecretName = "other"
			case "key":
				v.SecretKey = "other"
			case "duplicate":
				identity.credentials = append(identity.credentials, *v)
			case "snapshot":
				source.secrets = nil
			}
			if _, err := a.prepareConfiguration(context.Background(), config, source, "system_settings", "192.0.2.43", "Create", identity); err != errConfigurationSecret {
				t.Fatal("invalid credential binding prepared", err)
			}
			if state := journalState(t, a); state != nil && state.Operation != nil {
				t.Fatal("invalid binding persisted")
			}
		})
	}
}

func TestCredentialVersionJournalRejectsPrunedSchema(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting"} {
		t.Run(phase, func(t *testing.T) {
			a, config, source, identity := credentialVersionJournalFixture(t)
			original := a.statusWriter
			a.statusWriter = rotatingSecretJournalClient{Client: original, phase: phase, rotate: func() error {
				nc, err := a.currentController(context.Background())
				if err != nil {
					return err
				}
				nc.Status.Configuration.Operation.CredentialVersions = nil
				return original.Status().Update(context.Background(), nc)
			}}
			calls := 0
			err := a.submitConfigurationOperation(context.Background(), config, source, "system_settings", "192.0.2.43", "Create", func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }, identity)
			if err != errConfigurationStale || calls != 0 {
				t.Fatal("pruned version binding allowed API call", err, calls)
			}
			if state := journalState(t, a); state.Operation.Phase != phase {
				t.Fatal("lost journal fence")
			}
		})
	}
}

func TestCredentialApplicationRequiresTaskAndAtomicReceipt(t *testing.T) {
	for _, outcome := range []string{"success", "failed-task", "running-task", "wrong-resource", "unknown", "no-task", "lost-ack", "pruned-receipt"} {
		t.Run(outcome, func(t *testing.T) {
			a, config, source, identity := credentialVersionJournalFixture(t)
			identity.target = "server"
			ctx := context.Background()
			op, err := a.prepareConfiguration(ctx, config, source, "system_settings", "192.0.2.43", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, source, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "task"); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
				t.Fatal(err)
			}
			observed := "server"
			if outcome == "wrong-resource" {
				observed = "replacement"
			}
			if outcome == "unknown" || outcome == "no-task" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "unknown" {
					nc.Status.Configuration.Operation.Phase = "Unknown"
				} else {
					nc.Status.Configuration.Operation.TaskID = ""
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != taskPath+"task" {
					t.Error("unexpected API call")
					w.WriteHeader(404)
					return
				}
				start := time.Now().UnixMilli()
				end := start
				if outcome == "running-task" {
					end = 0
				}
				fmt.Fprintf(w, `{"response":{"id":"task","startTime":%d,"endTime":%d,"isError":%t}}`, start, end, outcome == "failed-task")
			})
			original := a.statusWriter
			if outcome == "lost-ack" {
				a.statusWriter = lostJournalAckClient{original}
			}
			if outcome == "pruned-receipt" {
				a.statusWriter = rotatingSecretJournalClient{Client: original, phase: "Verified", rotate: func() error {
					nc, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					nc.Status.Configuration.CredentialApplications = nil
					return original.Status().Update(ctx, nc)
				}}
			}
			err = a.recordConfigurationCredentialApplication(ctx, config, source, op.ID, observed)
			if (err == nil) != (outcome == "success") {
				t.Fatal("unexpected receipt outcome", outcome, err)
			}
			a.statusWriter = original
			state := journalState(t, a)
			if outcome == "success" || outcome == "lost-ack" {
				if state.Operation.Phase != "Verified" || len(state.CredentialApplications) != 1 {
					t.Fatal("receipt and verification not atomic")
				}
				if err := a.finishConfiguration(ctx, op.ID); err != nil {
					t.Fatal(err)
				}
				state = journalState(t, a)
				if state.Operation != nil || len(state.CredentialApplications) != 1 || state.CredentialApplications[0].TargetID != "server" || state.CredentialApplications[0].Versions[0].Version != 1 {
					t.Fatal("applied-version receipt did not survive operation release")
				}
			} else {
				if len(state.CredentialApplications) != 0 {
					t.Fatal("unproven credential application recorded")
				}
				if err := a.finishConfiguration(ctx, op.ID); err != errConfigurationBusy {
					t.Fatal("unproven credential operation released", err)
				}
			}
		})
	}
}
