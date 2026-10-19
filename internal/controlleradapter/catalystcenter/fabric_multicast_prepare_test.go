// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"net/http"
	"testing"
)

func multicastVNJournalFixture(t *testing.T, mode string, intercept ...func(http.ResponseWriter, *http.Request) bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, []configurationDispatchFixtureMember) {
	t.Helper()
	ctx := context.Background()
	a, config, _, managed, _ := portChannelJournalFixture(t, "valid")
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inventory[0].Hostname = "border-a.lab"
	inventory[1].Hostname = "edge-peer.lab"
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if len(intercept) > 0 && intercept[0](w, r) {
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected mutation")
			w.WriteHeader(400)
			return
		}
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		switch r.URL.Path {
		case sitesPath:
			reply(sampleSites())
		case fabricSitesPath:
			b := false
			reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
		case fabricL3NetworksPath:
			reply([]fabricL3Network{{ID: "vn", Name: "Corp", Fabrics: []string{"fabric"}}})
		case fabricMulticastVNsPath:
			if mode == "existing" || mode == "update" {
				row := multicastSiteRow(t, "Corp", "access", "edge-id").fields
				if mode == "existing" {
					row["ipv4SsmRanges"] = []any{"232.0.0.0/8"}
				}
				reply([]map[string]any{row})
			} else {
				reply([]any{})
			}
		case globalPoolsPath:
			reply(fixturePools())
		case sitePoolsPath:
			reply(fixtureReservations())
		case devicesPath:
			reply(inventory)
		case fabricDevicesPath:
			reply([]fabricDeviceMembership{{ID: "border", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"BORDER_NODE"}}, {ID: "edge", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	config.Spec.ManagedSections = []string{"fabric"}
	config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"multicast":{"virtual_networks":[{"name":"Corp","ip_pool_name":"access","ipv4_ssm_ranges":["232.0.0.0/8"],"multicast_rps":[{"rp_location":"FABRIC","fabric_rps":["border-a"]}]}]}}]}}}`)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, managed
}

func TestMulticastVNPrepare(t *testing.T) {
	for _, mode := range []string{"create", "update", "existing", "undeclared", "lost-ack", "pruned", "changed-source", "changed-node", "changed-native", "wrong-phase", "changed-target-settings"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			revised := false
			nativeMode := mode
			if mode == "changed-target-settings" {
				nativeMode = "update"
			}
			a, config, source, members := multicastVNJournalFixture(t, nativeMode, func(w http.ResponseWriter, r *http.Request) bool {
				if revised && mode == "changed-target-settings" && r.URL.Path == fabricMulticastVNsPath {
					row := multicastSiteRow(t, "Corp", "access", "edge-id").fields
					row["ipv4SsmRanges"] = []any{"233.0.0.0/8"}
					json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{row}})
					return true
				}
				if !revised || mode == "changed-target-settings" || r.URL.Path != sitePoolsPath {
					return false
				}
				rows := fixtureReservations()
				rows[0].ID = "replacement"
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
				return true
			})
			if mode == "lost-ack" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			name := "Corp"
			if mode == "undeclared" {
				name = "Other"
			}
			op, err := a.prepareFabricMulticastVN(ctx, config, source, "Global/Lab", name)
			if mode == "undeclared" || mode == "existing" {
				if (err == nil) != (mode == "existing") || op != nil {
					t.Fatal("preparation", op, err)
				}
				if state := journalState(t, a); state != nil && state.Operation != nil {
					t.Fatal("unexpected journal")
				}
				return
			}
			if mode != "lost-ack" && mode != "pruned" && err != nil {
				t.Fatal("prepare", err)
			}
			state := journalState(t, a)
			if state == nil {
				t.Fatal("missing journal", err)
			}
			current := state.Operation
			if current == nil || current.Phase != "Prepared" || current.TaskID != "" || current.SubmittedAt != nil {
				t.Fatal("missing durable preparation", current)
			}
			if mode == "pruned" {
				if err := a.revalidateFabricMulticastVNOperation(ctx, config, source, "Global/Lab", "Corp", current, "Prepared"); err == nil {
					t.Fatal("pruned journal accepted")
				}
				return
			}
			if contract.ValidateGroupJournalIdentity(current) != nil || len(current.PhysicalGroup.Targets) != 2 {
				t.Fatal("incomplete physical group")
			}
			if nativeMode == "update" && (current.Action != "Update" || current.TargetID != "vn") || nativeMode != "update" && (current.Action != "Create" || current.TargetID != "vn") {
				t.Fatal("wrong action identity", current)
			}
			switch mode {
			case "changed-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "changed-node":
				node := members[1].node
				if err := a.statusWriter.Delete(ctx, node); err != nil {
					t.Fatal(err)
				}
				node.ResourceVersion = ""
				node.UID = "replacement"
				if err := a.statusWriter.Create(ctx, node); err != nil {
					t.Fatal(err)
				}
			case "changed-native", "changed-target-settings":
				revised = true
			case "wrong-phase":
				current = current.DeepCopy()
				current.Phase = "Accepted"
			}
			valid := mode == "create" || mode == "update" || mode == "lost-ack"
			if err := a.revalidateFabricMulticastVNOperation(ctx, config, source, "Global/Lab", "Corp", current, "Prepared"); (err == nil) != valid {
				t.Fatal("revalidation", err)
			}
			// Restart cannot replace a retained journal, including uncertain preparation acknowledgment.
			if _, err := a.prepareFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp"); err == nil {
				t.Fatal("retained operation replaced")
			}
		})
	}
}
