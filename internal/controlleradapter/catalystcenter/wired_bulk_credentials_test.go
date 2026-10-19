// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type wiredBulkCredentialFixture struct {
	metadata   map[string][]globalCredentialMetadata
	slots      map[string]map[string]any
	reads      int
	beforeRead func()
}

func newWiredBulkCredentialFixture() *wiredBulkCredentialFixture {
	f := &wiredBulkCredentialFixture{metadata: map[string][]globalCredentialMetadata{}, slots: map[string]map[string]any{}}
	for _, site := range sampleSites() {
		f.slots[site.ID] = map[string]any{}
		for _, kind := range globalCredentialKinds {
			f.slots[site.ID][credentialSlot(kind.model)] = nil
		}
	}
	for _, kind := range globalCredentialKinds {
		id := "credential-" + kind.api
		f.metadata[kind.api] = []globalCredentialMetadata{{ID: id, Name: "existing"}}
		f.slots["area"][credentialSlot(kind.model)] = map[string]any{"credentialsId": id}
	}
	return f
}

func (f *wiredBulkCredentialFixture) reply(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.URL.Path != globalCredentialsPath && !strings.HasSuffix(r.URL.Path, "/deviceCredentials") {
		return false
	}
	if r.Method != "GET" {
		t.Error("credential dependency observation attempted mutation")
		w.WriteHeader(403)
		return true
	}
	f.reads++
	if f.beforeRead != nil {
		f.beforeRead()
	}
	var response any = f.metadata
	if r.URL.Path != globalCredentialsPath {
		if r.URL.Query().Get("_inherited") != "false" {
			t.Error("reader failed to request local assignment")
		}
		parts := strings.Split(r.URL.Path, "/")
		response = f.slots[parts[len(parts)-2]]
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"response": response}); err != nil {
		t.Error(err)
	}
	return true
}

func wiredBulkCredentialDocument(t *testing.T, withIPAM bool) map[string]any {
	t.Helper()
	doc := wiredBulkDependencyDocument(t)
	root := doc["catalyst_center"].(map[string]any)
	area := root["sites"].(map[string]any)["areas"].([]any)[0].(map[string]any)
	for _, kind := range globalCredentialKinds {
		area[kind.model] = "existing"
	}
	if !withIPAM {
		delete(root["network_settings"].(map[string]any), "ip_pools")
		delete(area, "ip_pools_reservations")
	}
	return doc
}

func wiredBulkDependencyOnlyDocument(t *testing.T, kind string) map[string]any {
	t.Helper()
	doc := wiredBulkDependencyDocument(t)
	if kind != "ipam" {
		doc = wiredBulkCredentialDocument(t, kind == "combined")
	}
	root := doc["catalyst_center"].(map[string]any)
	delete(root["network_settings"].(map[string]any), "network")
	delete(root["sites"].(map[string]any), "global")
	return doc
}

func TestWiredBulkDependencyOnlyContextIsNotDeviceProof(t *testing.T) {
	for _, kind := range []string{"ipam", "credentials", "combined", "empty"} {
		t.Run(kind, func(t *testing.T) {
			doc := wiredBulkDependencyOnlyDocument(t, kind)
			if kind == "empty" {
				root := doc["catalyst_center"].(map[string]any)
				root["network_settings"] = map[string]any{}
				area := root["sites"].(map[string]any)["areas"].([]any)[0].(map[string]any)
				delete(area, "ip_pools_reservations")
				for _, slot := range globalCredentialKinds {
					delete(area, slot.model)
				}
			}
			raw, _ := json.Marshal(doc)
			source := &configSourceSnapshot{raw: raw}
			model, err := compileWiredBulkSource(source)
			if err != nil {
				t.Fatal(err)
			}
			batches, err := groupWiredBulkMembers(model.inventory, model.states, model.sites, wiredBulkGrouping{root: "Global/Lab"})
			if err != nil || len(batches) != 1 {
				t.Fatal(err)
			}
			expected, err := compileWiredBulkNetworkContext(source, batches[0])
			if kind == "empty" {
				if err == nil {
					t.Fatal("empty context bypassed verification")
				}
				return
			}
			if err != nil || len(expected) != 2 {
				t.Fatal("dependency-only manifest required unrelated scalar policy", err)
			}
			for _, settings := range expected {
				if len(settings) != 0 {
					t.Fatal("undeclared policy was invented")
				}
				if _, err := wiredBulkNetworkCommands(settings); err == nil {
					t.Fatal("empty scalar context became device proof")
				}
			}
		})
	}
}

func TestWiredBulkExternalCredentialIdentity(t *testing.T) {
	for _, mode := range []string{"matched", "declared-child", "same-name-replacement", "metadata-change", "missing-name", "duplicate-name", "local-inherited", "local-disabled", "child-disabled", "child-override", "broken-ancestry", "missing-slot", "during-observation", "definitions", "inheritance-removal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newWiredBulkCredentialFixture()
			sites := sampleSites()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if f.reply(t, w, r) {
					return
				}
				if r.Method == "GET" && r.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
					return
				}
				t.Error("unexpected credential dependency request")
				w.WriteHeader(403)
			})
			doc := wiredBulkCredentialDocument(t, false)
			if mode == "declared-child" {
				doc["catalyst_center"].(map[string]any)["sites"].(map[string]any)["buildings"].([]any)[0].(map[string]any)["cli_credentials"] = "child"
				f.metadata["cliCredential"] = append(f.metadata["cliCredential"], globalCredentialMetadata{ID: "child-id", Name: "child"})
				f.slots["building"]["cliCredentialsId"] = map[string]any{"credentialsId": "child-id"}
			}
			raw, _ := json.Marshal(doc)
			source := &configSourceSnapshot{raw: raw}
			observers, err := c.wiredBulkDependencies(source)
			if err != nil || len(observers) != 1 {
				t.Fatal("missing external credential observer", err)
			}
			before, err := observers[0](ctx)
			if err != nil {
				t.Fatal("matching native assignments refused", err)
			}
			model, err := compileWiredBulkSource(source)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := c.observeWiredBulkCredentials(ctx, model)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, snapshot), "credential-cli") {
					t.Fatal("formatter exposed metadata")
				}
			}
			switch mode {
			case "same-name-replacement":
				f.metadata["cliCredential"][0].ID = "replacement"
				f.slots["area"]["cliCredentialsId"] = map[string]any{"credentialsId": "replacement"}
			case "metadata-change":
				f.metadata["cliCredential"][0].Username = "changed-user"
			case "missing-name":
				f.metadata["cliCredential"] = nil
			case "duplicate-name":
				f.metadata["cliCredential"] = append(f.metadata["cliCredential"], globalCredentialMetadata{ID: "other", Name: "existing"})
			case "local-inherited":
				f.slots["area"]["cliCredentialsId"] = nil
			case "local-disabled":
				f.slots["area"]["cliCredentialsId"] = map[string]any{}
			case "child-disabled":
				f.slots["building"]["cliCredentialsId"] = map[string]any{}
			case "child-override":
				f.slots["building"]["cliCredentialsId"] = map[string]any{"credentialsId": "other"}
			case "broken-ancestry":
				sites[2].ParentID = "root"
			case "missing-slot":
				delete(f.slots["building"], "snmpv3CredentialsId")
			case "during-observation":
				start := f.reads
				f.beforeRead = func() {
					if f.reads == start+6 {
						f.metadata["cliCredential"][0].Username = "changed"
					}
				}
			case "definitions":
				doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"] = map[string]any{"cli_credentials": []any{map[string]any{"name": "existing", "username": "tester", "password_version": 1}}}
			case "inheritance-removal":
				doc["catalyst_center"].(map[string]any)["sites"].(map[string]any)["areas"].([]any)[0].(map[string]any)["cli_credentials"] = nil
			}
			if mode == "definitions" || mode == "inheritance-removal" {
				raw, _ = json.Marshal(doc)
				start := f.reads
				if _, err = c.wiredBulkDependencies(&configSourceSnapshot{raw: raw}); err == nil || f.reads != start {
					t.Fatal("unsupported credential mutation accepted", err)
				}
				return
			}
			after, err := observers[0](ctx)
			valid := mode == "matched" || mode == "declared-child" || mode == "same-name-replacement" || mode == "metadata-change"
			if (err == nil) != valid {
				t.Fatal("credential dependency validation mismatch", err)
			}
			if valid && (before == after) != (mode == "matched" || mode == "declared-child") {
				t.Fatal("changed credential incarnation retained original digest")
			}
		})
	}
}
