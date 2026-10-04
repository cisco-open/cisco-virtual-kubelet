// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwarelifecycle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

func TestPreparationRetirementRequiresCompleteQuiescentInventory(t *testing.T) {
	const base = `{"install-location-information":[{"oper-state":{"sys-activity":"install-no-activity"},"install-version-info":[{"version":"17.18.03","current":"install-version-state-provisioned-committed"},{"version":"17.18.02","current":"install-version-state-installed"}]}]}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
		state     lifecycle.InventoryState
	}{
		{"inactive", base, true, lifecycle.InventoryStateInstalled},
		{"absent", strings.Replace(base, `,{"version":"17.18.02","current":"install-version-state-installed"}`, "", 1), true, lifecycle.InventoryStateAbsent},
		{"active-installer", strings.Replace(base, "install-no-activity", "install-add", 1), false, ""},
		{"unknown-installer", strings.Replace(base, "install-no-activity", "", 1), false, ""},
		{"target-in-progress", strings.Replace(base, "install-version-state-installed", "install-version-state-in-progress", 1), false, ""},
		{"running-uncommitted", strings.Replace(base, "provisioned-committed", "provisioned-uncommitted", 1), false, ""},
		{"target-active", strings.Replace(base, "install-version-state-installed", "install-version-state-provisioned-committed", 1), false, ""},
		{"missing-locations", `{}`, false, ""},
		{"empty-locations", `{"install-location-information":[]}`, false, ""},
		{"malformed", `{"install-location-information":{}}`, false, ""},
		{"unidentified-target", strings.Replace(base, `"version":"17.18.02"`, `"version":""`, 1), false, ""},
		{"duplicate-identity", strings.Replace(base, "17.18.02", "17.18.03", 1), false, ""},
		{"unknown-unrelated-image", strings.Replace(base, `"version":"17.18.02","current":"install-version-state-installed"`, `"version":"17.18.01","current":"unknown"`, 1), false, ""},
		{"unknown-operation", strings.TrimSuffix(base, "}") + `,"install-oper":[{"op-status":"unknown"}]}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: []byte(tc.raw)}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.ObservePreparationRetirement(context.Background(), "17.18.02", "17.18.03")
			if (err == nil) != tc.valid {
				t.Fatalf("result=%+v error=%v", got, err)
			}
			if tc.valid && (got.TargetState != tc.state || !strings.HasPrefix(got.EvidenceHash, "sha256:") || len(got.EvidenceHash) != 71) {
				t.Fatalf("incomplete proof: %+v", got)
			}
		})
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(base), &root); err != nil {
		t.Fatal(err)
	}
	locations := root["install-location-information"].([]any)
	// One apparently healthy member must not hide a member without a committed OS.
	root["install-location-information"] = append(locations, map[string]any{"oper-state": map[string]any{"sys-activity": "install-no-activity"}, "install-version-info": []any{}})
	raw, _ := json.Marshal(root)
	if _, err := preparationRetirementFromJSON(raw, "17.18.02", "17.18.03"); err == nil {
		t.Fatal("incomplete member accepted")
	}
}
