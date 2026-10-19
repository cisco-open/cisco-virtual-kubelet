// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

func wiredBulkOwnedCredentialDocument(t *testing.T) map[string]any {
	t.Helper()
	doc := wiredBulkCredentialDocument(t, true)
	definitions := map[string]any{}
	for _, kind := range globalCredentialKinds {
		var entry map[string]any
		if err := json.Unmarshal([]byte(globalCredentialFixtureEntries[kind.model]), &entry); err != nil {
			t.Fatal(err)
		}
		entry["name"] = "existing"
		definitions[kind.model] = []any{entry}
	}
	doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"] = definitions
	return doc
}

func wiredBulkOwnedMetadataDocument(t *testing.T) map[string]any {
	t.Helper()
	doc := wiredBulkOwnedCredentialDocument(t)
	doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"] = map[string]any{"snmpv3_credentials": []any{map[string]any{"name": "existing", "username": "tester", "snmp_mode": "NOAUTHNOPRIV"}}}
	return doc
}

// Simulate only persisted ownership/provenance receipts. No secret values are
// present in this fixture and the observer must never resolve projections.
func installWiredBulkOwnedCredentials(t *testing.T, a *adapter, bulk *configapi.NetworkControllerConfig, source *configSourceSnapshot, f *wiredBulkCredentialFixture, actual ...[]cvk.NetworkControllerConfigurationCredentialApplication) *configapi.NetworkControllerConfig {
	t.Helper()
	ctx := context.Background()
	model, err := compileWiredBulkSource(source)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(source.raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["catalyst_center"].(map[string]any), "inventory")
	raw, _ := json.Marshal(doc)
	owner := bulk.DeepCopy()
	owner.Name = "credential-owner"
	owner.UID = types.UID("credential-owner-uid")
	if len(actual) > 0 && len(actual[0]) > 0 {
		owner.UID = types.UID(actual[0][0].ConfigUID)
	}
	owner.ResourceVersion = ""
	owner.Generation = 1
	owner.Spec.ManagedSections = []string{"network_settings"}
	owner.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	owner.Spec.Source.ConfigMapRef = nil
	owner.Spec.SecretRefs = nil
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration == nil {
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
	}
	nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "network_settings", ConfigUID: string(owner.UID), ConfigName: owner.Name})
	for _, want := range model.network.credentials {
		metadata := want.metadata
		metadata.ID = "credential-" + want.kind.api
		f.metadata[want.kind.api] = []globalCredentialMetadata{metadata}
		if len(actual) > 0 {
			found := false
			for _, receipt := range actual[0] {
				if receipt.Resource == want.resource() {
					if found {
						t.Fatal("duplicate actual credential receipt")
					}
					found = true
					for _, version := range receipt.Versions {
						nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: version.SourceAlias, Name: version.SecretName, Key: version.SecretKey})
						owner.Spec.SecretRefs = append(owner.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: version.Path, Source: version.SourceAlias})
					}
					nc.Status.Configuration.CredentialApplications = append(nc.Status.Configuration.CredentialApplications, receipt)
				}
			}
			if !found {
				t.Fatal("actual receipt missing for declared credential")
			}
			continue
		}
		if want.metadataOnly() {
			receipt := noAuthCredentialReceipt(want, string(owner.UID), owner.Name)
			receipt.Resource = want.metadataKey() + ":" + metadata.ID
			nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, receipt)
			continue
		}
		receipt := cvk.NetworkControllerConfigurationCredentialApplication{Section: "network_settings", Resource: want.resource(), ConfigUID: string(owner.UID), TargetID: metadata.ID, OperationID: strings.Repeat("c", 32)}
		for _, leaf := range want.credentials {
			alias := fmt.Sprintf("owned-source-%d", len(nc.Spec.IntentSecretSources))
			nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: alias, Key: "value"})
			owner.Spec.SecretRefs = append(owner.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: leaf.path, Source: alias})
			receipt.Versions = append(receipt.Versions, cvk.NetworkControllerConfigurationCredentialVersion{Path: leaf.path, Version: leaf.version, SourceAlias: alias, SecretName: alias, SecretKey: "value"})
		}
		sort.Slice(receipt.Versions, func(i, j int) bool { return receipt.Versions[i].Path < receipt.Versions[j].Path })
		nc.Status.Configuration.CredentialApplications = append(nc.Status.Configuration.CredentialApplications, receipt)
	}
	owner.Status.Phase = configapi.NetworkControllerConfigPhaseInSync
	owner.Status.ObservedGeneration = owner.Generation
	if err := a.statusWriter.Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	owner.Status.Phase = configapi.NetworkControllerConfigPhaseInSync
	owner.Status.ObservedGeneration = owner.Generation
	if err := a.statusWriter.Status().Update(ctx, owner); err != nil {
		t.Fatal(err)
	}
	status := nc.Status.DeepCopy()
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	nc.Status = *status
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = "/definitely-absent-cvk-owned-credential-projections"
	return owner
}

func TestWiredBulkOwnedCredentialPrerequisites(t *testing.T) {
	for _, mode := range []string{"matched", "owner-replaced", "owner-not-reconciled", "missing-owner", "duplicate-owner", "missing-receipt", "receipt-version", "receipt-source", "source-revoked", "native-replaced", "native-drift", "receipt-replaced", "owner-status-heartbeat", "definition-version", "metadata-matched", "metadata-missing-receipt", "metadata-old-generation", "native-unmanaged-metadata", "receipt-during-observation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			doc := wiredBulkOwnedCredentialDocument(t)
			if strings.HasPrefix(mode, "metadata-") {
				doc = wiredBulkOwnedMetadataDocument(t)
			}
			raw, _ := json.Marshal(doc)
			source := &configSourceSnapshot{raw: raw}
			credentials := newWiredBulkCredentialFixture()
			ipam := &wiredBulkIPAMFixture{pools: fixturePools(), reservations: fixtureReservations()}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if credentials.reply(t, w, r) || ipam.reply(t, w, r) {
					return
				}
				if r.Method == "GET" && r.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				t.Error("owned credential observer attempted unexpected native operation")
				w.WriteHeader(403)
			})
			owner := installWiredBulkOwnedCredentials(t, a, config, source, credentials)
			observers, err := a.wiredBulkDependencies(ctx, source)
			if err != nil || len(observers) != 1 {
				t.Fatal("valid owned credential prerequisites refused", err)
			}
			before, err := observers[0](ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "native-unmanaged-metadata":
				port := 22
				credentials.metadata["cliCredential"][0].Port = &port
			case "receipt-during-observation":
				start := credentials.reads
				credentials.beforeRead = func() {
					if credentials.reads == start+1 {
						fresh, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							return
						}
						fresh.Status.Configuration.CredentialApplications[0].OperationID = strings.Repeat("d", 32)
						if err := a.statusWriter.Status().Update(ctx, fresh); err != nil {
							t.Error(err)
						}
					}
				}
			case "metadata-missing-receipt":
				nc.Status.Configuration.ActionApplications = nil
			case "metadata-old-generation":
				owner.Generation++
				owner.Status.ObservedGeneration = owner.Generation
			case "owner-replaced":
				owner.UID = "replacement"
			case "owner-not-reconciled":
				owner.Generation++
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "missing-receipt":
				nc.Status.Configuration.CredentialApplications = nc.Status.Configuration.CredentialApplications[1:]
			case "receipt-version":
				nc.Status.Configuration.CredentialApplications[0].Versions[0].Version++
			case "receipt-source":
				nc.Status.Configuration.CredentialApplications[0].Versions[0].SecretName = "changed"
			case "source-revoked":
				nc.Spec.IntentSecretSources = nil
			case "native-replaced":
				credentials.metadata["cliCredential"][0].ID = "replacement"
			case "native-drift":
				credentials.metadata["cliCredential"][0].Username = "changed"
			case "receipt-replaced":
				nc.Status.Configuration.CredentialApplications[0].OperationID = strings.Repeat("d", 32)
			case "owner-status-heartbeat":
				owner.Status.LastAppliedHash = "sha256:" + strings.Repeat("a", 64)
			case "definition-version":
				var doc map[string]any
				json.Unmarshal(owner.Spec.Source.Inline.Raw, &doc)
				doc["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"].(map[string]any)["cli_credentials"].([]any)[0].(map[string]any)["password_version"] = 2
				owner.Spec.Source.Inline.Raw, _ = json.Marshal(doc)
			}
			ownerStatus := owner.Status.DeepCopy()
			if mode == "owner-replaced" {
				if err := a.statusWriter.Delete(ctx, owner); err != nil {
					t.Fatal(err)
				}
				owner.ResourceVersion = ""
				if err := a.statusWriter.Create(ctx, owner); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusWriter.Update(ctx, owner); err != nil {
				t.Fatal(err)
			}
			owner.Status = *ownerStatus
			if err := a.statusWriter.Status().Update(ctx, owner); err != nil {
				t.Fatal(err)
			}
			status := nc.Status.DeepCopy()
			if err := a.statusWriter.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc.Status = *status
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			after, err := observers[0](ctx)
			valid := mode == "matched" || mode == "receipt-replaced" || mode == "owner-status-heartbeat" || mode == "metadata-matched" || mode == "metadata-old-generation" || mode == "native-unmanaged-metadata"
			if (err == nil) != valid {
				t.Fatal("owned credential boundary accepted invalid provenance", err)
			}
			if valid && (before == after) != (mode != "receipt-replaced" && mode != "metadata-old-generation" && mode != "native-unmanaged-metadata") {
				t.Fatal("receipt replacement/status churn identity mismatch")
			}
		})
	}
}
