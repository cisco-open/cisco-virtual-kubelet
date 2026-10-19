// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// The producer has just completed native create and rotation in a generated
// credential fixture. Ownership objects and receipts remain simulated Kubernetes
// actors: this qualifies native UUID/metadata composition, not installed workers.
func qualifyLiveWiredBulkOwnedCredential(t *testing.T, c *client, ctx context.Context, site string, want globalCredentialIntent, receipts []cvk.NetworkControllerConfigurationCredentialApplication) {
	t.Helper()
	if !strings.HasPrefix(site, "Global/CVK-Cred-") || len(receipts) != 1 {
		t.Fatal("owned bulk qualification requires isolated native credential fixture")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(globalCredentialFixtureEntries[want.kind.model]), &entry); err != nil {
		t.Fatal(err)
	}
	entry["name"] = want.metadata.Name
	for _, leaf := range want.credentials {
		parts := strings.Split(leaf.path, "/")
		entry[parts[len(parts)-1]+"_version"] = leaf.version
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{
		"network_settings": map[string]any{"device_credentials": map[string]any{want.kind.model: []any{entry}}},
		"sites":            map[string]any{"areas": []any{map[string]any{"name": strings.TrimPrefix(site, "Global/"), "parent_name": "Global"}}},
		"inventory":        map[string]any{"devices": []any{map[string]any{"name": "qualification-no-device", "site": site, "state": "REPROVISION"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, config := journalFixture(t)
	a.client = c
	source := &configSourceSnapshot{raw: raw}
	installWiredBulkOwnedCredentials(t, a, config, source, newWiredBulkCredentialFixture(), receipts)
	observers, err := a.wiredBulkDependencies(ctx, source)
	if err != nil || len(observers) != 1 {
		t.Fatal("native versioned credential dependency unavailable", err)
	}
	before, err := observers[0](ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := observers[0](ctx)
	if err != nil || before != after {
		t.Fatal("native owned credential dependency changed", err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.CredentialApplications[0].Versions[0].Version++
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if _, err := observers[0](ctx); err == nil {
		t.Fatal("changed persisted version receipt retained dependency proof")
	}
	t.Log("native UUID/metadata and task-backed credential version composed with simulated owner; changed receipt refused without credential projection reads", want.kind.model, receipts[0].TargetID)
}
