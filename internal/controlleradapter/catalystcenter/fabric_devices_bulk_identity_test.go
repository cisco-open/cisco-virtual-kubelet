// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func TestFabricDevicesBulkIncomingIdentity(t *testing.T) {
	for _, mode := range []string{"v2", "v2-declared-peer", "v2-parent-projection", "zone-only-parent-refused", "legacy", "legacy-declared-peer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, wants, _ := fabricDevicesBulkPreparationFixture(t, "prepare")
			if mode == "v2-parent-projection" || mode == "zone-only-parent-refused" {
				var root map[string]any
				if err := json.Unmarshal(source.raw, &root); err != nil {
					t.Fatal(err)
				}
				devices := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["devices"].([]any)
				devices[1].(map[string]any)["fabric_zone"] = "Global/Lab/Building"
				if mode == "zone-only-parent-refused" {
					devices[0].(map[string]any)["fabric_zone"] = "Global/Lab/Building"
				}
				source.raw, _ = json.Marshal(root)
				config.Spec.Source.Inline.Raw = source.raw
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			op, err := a.prepareFabricDevicesBulk(ctx, config, source, wants)
			if mode == "zone-only-parent-refused" {
				if err == nil || op != nil {
					t.Fatal("zone-only source authorized new parent cohort")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "legacy") {
				snapshot, err := a.client.fabricDevicesBulkSnapshot(ctx, wants)
				if err != nil {
					t.Fatal(err)
				}
				op.Resource = snapshot.legacyResource(snapshot.mask)
				// Construct a historical fixture locally; never rewrite a retained journal.
				op.PhysicalGroup.Digest, err = contract.GroupOperationDigest(op, op.PhysicalGroup.Targets)
				if err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(mode, "declared-peer") {
				var root map[string]any
				if err := json.Unmarshal(source.raw, &root); err != nil {
					t.Fatal(err)
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				inventory["devices"] = append(inventory["devices"].([]any), map[string]any{"name": "peer", "serial_number": "FOC12345680", "site": "Global/Lab/Building", "state": "PROVISION", "fabric_site": "Global/Lab", "fabric_roles": []string{"EDGE_NODE"}})
				source.raw, err = json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.retainedFabricDevicesBulk(ctx, source, op)
			if mode == "legacy-declared-peer" {
				if err == nil {
					t.Fatal("ambiguous legacy incoming set was guessed")
				}
			} else if err != nil || len(got) != 2 || got[0].inventory.reference.Name != "edge0" || got[1].inventory.reference.Name != "edge1" {
				t.Fatal("original incoming set not reconstructed", err)
			}
			if mode == "v2-parent-projection" && got[1].fabricZone != "" {
				t.Fatal("root operation recovered as zone work")
			}
		})
	}
}

func TestFabricDevicesBulkIdentityRejectsMalformedIncomingSet(t *testing.T) {
	base := fabricDevicesBulkIDsPrefix + "2:003:" + strings.Repeat("a", 64) + ":"
	for _, ids := range []string{"edge-0", "edge-0,edge-0", "edge-1,edge-0", "edge-0,edge/1", "edge-0,"} {
		resource := base + ids
		if !isReservedConfigurationGroupResource("fabric", resource) || validConfigurationGroupAction("fabric", resource, "Create") {
			t.Fatal("malformed incoming identity escaped group fence")
		}
	}
	oversized := fabricDevicesBulkIDsPrefix + "4:00f:" + strings.Repeat("a", 64) + ":" + strings.Repeat("a", 128) + "," + strings.Repeat("b", 128) + "," + strings.Repeat("c", 128) + "," + strings.Repeat("d", 128)
	if len(oversized) <= 512 || validConfigurationGroupAction("fabric", oversized, "Create") {
		t.Fatal("otherwise canonical identity exceeded journal resource limit")
	}
	if !validConfigurationGroupAction("fabric", base+"edge-0,edge-1", "Create") {
		t.Fatal("canonical explicit incoming identity rejected")
	}
}
