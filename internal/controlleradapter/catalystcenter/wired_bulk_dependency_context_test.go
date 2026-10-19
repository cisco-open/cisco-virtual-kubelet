// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func wiredBulkDependencyDocument(t *testing.T) map[string]any {
	t.Helper()
	source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"REPROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)}
	var doc, allocation map[string]any
	if err := json.Unmarshal(wiredBulkNetworkSource(t, source), &doc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(poolModel), &allocation); err != nil {
		t.Fatal(err)
	}
	root := doc["catalyst_center"].(map[string]any)
	root["network_settings"].(map[string]any)["ip_pools"] = allocation["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["ip_pools"]
	root["sites"].(map[string]any)["areas"].([]any)[0].(map[string]any)["ip_pools_reservations"] = []any{"access"}
	return doc
}

func TestWiredBulkDependencyContextReusesNetworkParser(t *testing.T) {
	for _, mode := range []string{"pool", "all-credentials", "metadata-credentials", "invalid-version", "floor-geometry", "missing-pool-reference", "duplicate-pool-reference", "bad-pool-reference-type", "bad-credential-reference", "unknown-site-field", "bare-secret"} {
		t.Run(mode, func(t *testing.T) {
			doc := wiredBulkDependencyDocument(t)
			root := doc["catalyst_center"].(map[string]any)
			sites := root["sites"].(map[string]any)
			area := sites["areas"].([]any)[0].(map[string]any)
			network := root["network_settings"].(map[string]any)
			switch mode {
			case "all-credentials", "metadata-credentials", "invalid-version":
				definitions := map[string]any{}
				for _, kind := range globalCredentialKinds {
					var entry map[string]any
					if err := json.Unmarshal([]byte(globalCredentialFixtureEntries[kind.model]), &entry); err != nil {
						t.Fatal(err)
					}
					if mode == "metadata-credentials" {
						for key := range entry {
							if strings.HasSuffix(key, "_version") {
								delete(entry, key)
							}
						}
					}
					if mode == "invalid-version" && kind.model == "cli_credentials" {
						entry["password_version"] = "invalid"
					}
					definitions[kind.model] = []any{entry}
					area[kind.model] = "fixture"
				}
				network["device_credentials"] = definitions
			case "floor-geometry":
				var floor map[string]any
				if err := json.Unmarshal([]byte(plannedAPFixture), &floor); err != nil {
					t.Fatal(err)
				}
				sites["floors"] = floor["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"]
			case "missing-pool-reference":
				area["ip_pools_reservations"] = []any{"missing"}
			case "duplicate-pool-reference":
				area["ip_pools_reservations"] = []any{"access", "access"}
			case "bad-pool-reference-type":
				area["ip_pools_reservations"] = "access"
			case "bad-credential-reference":
				area["cli_credentials"] = 3
			case "unknown-site-field":
				area["unknown_credentials"] = "fixture"
			case "bare-secret":
				network["device_credentials"] = map[string]any{"cli_credentials": []any{map[string]any{"name": "fixture", "username": "tester", "password": "fixture-plaintext"}}}
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			source := &configSourceSnapshot{raw: raw}
			before := bytes.Clone(raw)
			model, err := compileWiredBulkSource(source)
			valid := mode == "pool" || mode == "all-credentials" || mode == "metadata-credentials" || mode == "floor-geometry"
			if (err == nil) != valid || !bytes.Equal(before, source.raw) {
				t.Fatal("context projection dropped validation or changed source", err)
			}
			if !valid {
				return
			}
			if model.network == nil || len(model.network.pools) != 1 || len(model.network.reservations) != 1 || model.network.reservations[0].site != "Global/Lab" {
				t.Fatal("allocation context was lost")
			}
			if (mode == "all-credentials" || mode == "metadata-credentials") && (len(model.network.credentials) != 6 || len(model.network.assignments) != 6) {
				t.Fatal("credential slot context was lost")
			}
			if mode == "all-credentials" || mode == "metadata-credentials" {
				versions := 0
				for _, credential := range model.network.credentials {
					versions += len(credential.credentials)
				}
				if mode == "all-credentials" && versions != 8 || mode == "metadata-credentials" && versions != 0 {
					t.Fatal("credential parser changed declared version semantics")
				}
			}
			if mode == "floor-geometry" && len(model.sites) != 3 {
				t.Fatal("geometry/AP validation was not composed")
			}
			batches, err := groupWiredBulkMembers(model.inventory, model.states, model.sites, wiredBulkGrouping{root: "Global/Lab"})
			if err != nil || len(batches) != 1 {
				t.Fatal("dependency context changed grouping", err)
			}
			_, err = compileWiredBulkNetworkContext(source, batches[0])
			if mode == "metadata-credentials" {
				if !errors.Is(err, errWiredBulkNetworkContextUnavailable) {
					t.Fatal("unqualified credential context became device proof", err)
				}
			} else if err != nil {
				t.Fatal("allocation context lost its scoped network declaration", err)
			}
		})
	}
}

func TestWiredBulkCredentialDependenciesCannotPrepareWithoutIdentityProof(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global/Lab"}
	doc := wiredBulkDependencyDocument(t)
	doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"] = map[string]any{"cli_credentials": []any{map[string]any{"name": "fixture", "username": "tester", "password_version": 1}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline.Raw = raw
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{raw: raw}
	batches, err := configuredWiredBulkBatches(config, source, nil)
	if err != nil {
		t.Fatal("context did not validate before execution gate", err)
	}
	requests := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Error("unqualified context contacted native API")
		w.WriteHeader(http.StatusForbidden)
	})
	// Private admission installation permits planning, never creates grants.
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
	if !errors.Is(err, errConfigurationOwned) || op != nil || requests != 0 {
		t.Fatal("missing dependency proof prepared or contacted native API", err)
	}
	if state := journalState(t, a); state != nil && (state.Operation != nil || len(state.Owners) != 0 || len(state.ActionApplications) != 0) {
		t.Fatal("refusal changed journal ownership or receipts")
	}
}
