// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestDiscoveryCredentialRetainedScopeIdentity(t *testing.T) {
	base := discoveryLineageBase(discoveryLineageVersions(1, 0))
	effect := discoveryCredentialPublicEffect{mask: discoveryCredentialRequiredMask, digest: strings.Repeat("a", 64)}
	resource := discoveryCredentialScopedInput(base, effect)
	if got, context, ok := discoveryCredentialInputParts(resource); !ok || got != base || context != "" || len(resource) > 512 {
		t.Fatal("retained scope identity round trip")
	}
	if got, ok := discoveryCredentialRetainedEffect(resource); !ok || got != effect {
		t.Fatal("scope not retained")
	}
	context := strings.Repeat("b", 64)
	child := discoveryCredentialContinuation(resource, context)
	if got, link, ok := discoveryCredentialInputParts(child); !ok || got != base || link != context || discoveryCredentialInputPrototype(child) != resource {
		t.Fatal("context lost retained scope")
	}
	for _, bad := range []string{strings.Replace(resource, ":f:", ":0f:", 1), strings.Replace(resource, ":f:", ":1:", 1), strings.Replace(resource, ":v3:", ":v9:", 1), strings.TrimSuffix(resource, "-"), resource + ":extra", strings.Replace(resource, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)} {
		if _, _, ok := discoveryCredentialInputParts(bad); ok {
			t.Fatal("malformed retained scope accepted")
		}
	}
}

func TestDiscoveryCredentialRetainedCreateObservation(t *testing.T) {
	for _, mode := range []string{"source-lost", "legacy", "drift", "foreign-uuid", "failed-task", "pending-task", "active-job", "journal-changed", "owner-revoked", "scope-version"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialRetainedObservationScenario(t, mode, nil) })
	}
}
func discoveryCredentialRetainedObservationScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, source, p, op := discoveryCredentialCompletionFixture(t, install)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := discoveryCredentialRetainedEffect(op.Resource); !ok {
		t.Fatal("new dispatch did not retain scope")
	}
	if mode == "legacy" {
		base, _, _ := discoveryCredentialInputParts(op.Resource)
		op.Resource = base
		nc.Status.Configuration.Operation.Resource = base
	}
	if mode == "scope-version" {
		op.Resource = strings.Replace(op.Resource, ":v3:", ":v9:", 1)
		nc.Status.Configuration.Operation.Resource = op.Resource
	}
	if mode == "owner-revoked" {
		nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	// Losing the config and projected files cannot erase the acknowledged scope.
	// The observer can read it, but the normal public writer must remain fenced.
	if err := a.statusWriter.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(a.intentSecretPath); err != nil {
		t.Fatal(err)
	}
	before := journalState(t, a).DeepCopy()
	reads, writes := 0, 0
	changed := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			w.WriteHeader(500)
			return
		}
		reads++
		switch r.URL.Path {
		case taskPath + "original-task":
			end := op.SubmittedAt.UnixMilli()
			if mode == "pending-task" {
				end = 0
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "original-task", "startTime": op.SubmittedAt.UnixMilli(), "endTime": end, "isError": mode == "failed-task", "data": `{"discoveryId":"job-id"}`}})
		case discoveryPath + "/1/100":
			id, user, status, condition := "job-id", "operator", "Inactive", "Complete"
			if mode == "foreign-uuid" {
				id = "foreign"
			}
			if mode == "drift" {
				user = "other"
			}
			if mode == "active-job" {
				status = "Active"
				condition = "In Progress"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{{ID: id, Name: "fixture", Type: "RANGE", Addresses: "192.0.2.1-192.0.2.1", Protocol: "ssh", Usernames: []string{user}, Status: status, Condition: condition}}})
			if mode == "journal-changed" && !changed {
				changed = true
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				nc.Status.Configuration.Operation.TaskID = "different-task"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Error(err)
				}
			}
		default:
			t.Error("unexpected read", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	observation, err := a.observeRetainedDiscoveryCredentialCreate(ctx, op)
	if mode == "source-lost" {
		if err != nil || observation == nil || observation.id != "job-id" || observation.input != op.Resource {
			t.Fatal("retained task/UUID/effect did not correlate", err)
		}
	} else if err == nil || observation != nil {
		t.Fatal("uncertain retained scope accepted")
	}
	if mode == "journal-changed" && !changed {
		t.Fatal("journal race not exercised")
	}
	if mode == "legacy" || mode == "owner-revoked" || mode == "scope-version" {
		if reads != 0 {
			t.Fatal("invalid scope read native state")
		}
	}
	if mode != "journal-changed" && !reflect.DeepEqual(before, journalState(t, a)) {
		t.Fatal("read-only candidate mutated journal")
	}
	if err := a.recordDiscoveryCredentialCreate(ctx, c, source, p, 0, op); err == nil {
		t.Fatal("lost source granted public application authority")
	}
	if writes != 0 {
		t.Fatal("retained observation issued mutation")
	}
	if err == nil && reads < 3 {
		t.Fatal("missing repeated native proof")
	}
}

func TestDiscoveryCredentialRetainedScopeLegacyNoopAndPair(t *testing.T) {
	f := newDiscoveryLineageFixture()
	versions := discoveryLineageVersions(1, 0)
	f.create(t, versions, "")
	history, err := discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	creator := history.latest[discoveryPublicDigest("fixture")]
	prototype := discoveryCredentialScopedInput(creator.base, creator.identity.effect)
	got, err := history.input(creator.identity.name, prototype)
	if err != nil || got != creator.receipt.Operation.Resource {
		t.Fatal("new protocol rotated unchanged legacy input", err)
	}
	// A new-format operation must agree with the independent native-effect
	// receipt, even when both input-bearing copies are changed consistently.
	state := f.state.DeepCopy()
	state.ActionApplications[0].Operation.Resource = prototype
	state.CredentialApplications[0].Resource = prototype
	if !discoveryCredentialReceiptPairPresent(state, &state.ActionApplications[0].Operation) {
		t.Fatal("matching retained scope rejected")
	}
	wrong := creator.identity.effect
	wrong.digest = strings.Repeat("f", 64)
	bad := discoveryCredentialScopedInput(creator.base, wrong)
	state.ActionApplications[0].Operation.Resource = bad
	state.CredentialApplications[0].Resource = bad
	if discoveryCredentialReceiptPairPresent(state, &state.ActionApplications[0].Operation) {
		t.Fatal("inconsistent pre/post native scope accepted")
	}
}
