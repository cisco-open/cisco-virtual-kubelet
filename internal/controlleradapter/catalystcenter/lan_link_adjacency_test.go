// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestLANLinkAdjacencyManagedRevalidation(t *testing.T) {
	for _, mode := range []string{"unchanged", "link-replaced", "new-neighbor", "port-replaced", "interface-config", "managed-peer-replaced", "managed-peer-missing", "native-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			_, _, f, api := newManagedFabricDevicesBulkFixture(t, "valid")
			changed := false
			topologyReads := 0
			ports := []lanLinkInterface{lanPortFixture("edge-0", "TenGigabitEthernet1/0/1"), lanPortFixture("edge-1", "TenGigabitEthernet1/0/2")}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				var response any
				switch r.URL.Path {
				case devicesPath:
					response = f.inventory
				case "/dna/intent/api/v1/interface/network-device/edge-0":
					response = []lanLinkInterface{ports[0]}
				case "/dna/intent/api/v1/interface/network-device/edge-1":
					port := ports[1]
					if changed && mode == "port-replaced" {
						port.ID = "new-port"
					}
					if changed && mode == "interface-config" {
						port.Mode = "access"
					}
					response = []lanLinkInterface{port}
				case lanPhysicalTopologyPath:
					topologyReads++
					nodes := []lanTopologyNode{}
					for _, device := range f.inventory {
						nodes = append(nodes, lanTopologyNode{ID: device.ID, IP: device.ManagementIP, Type: "device"})
					}
					link := lanPhysicalLink{ID: "physical-link", Source: "edge-0", Target: "edge-1", StartID: ports[0].ID, EndID: ports[1].ID, StartName: ports[0].PortName, EndName: ports[1].PortName, Status: "up"}
					if changed && mode == "link-replaced" || mode == "native-race" && topologyReads >= 2 {
						link.ID = "new-link"
					}
					if changed && mode == "new-neighbor" {
						link.Target = "peer"
						link.EndID = "peer-port"
					}
					response = lanPhysicalTopology{Nodes: nodes, Links: []lanPhysicalLink{link}}
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			wants, err := parseLANLinks(lanLinkModel(t, lanLinkFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			want.primaryIP = f.inventory[0].ManagementIP
			want.secondaryIP = f.inventory[1].ManagementIP
			if mode == "managed-peer-missing" {
				var d cvk.CiscoDevice
				if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "edge1"}, &d); err != nil {
					t.Fatal(err)
				}
				if err := api.Delete(ctx, &d); err != nil {
					t.Fatal(err)
				}
			}
			before, targets, err := c.lanLinkAdjacencyTargets(ctx, api, "lab", want)
			if mode == "native-race" || mode == "managed-peer-missing" {
				if err == nil {
					t.Fatal("incomplete/racing footprint accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != 2 || before.link == nil {
				t.Fatal("endpoint footprint/adjacency incomplete")
			}
			changed = true
			if mode == "managed-peer-replaced" {
				var node core.Node
				if err := api.Get(ctx, ctrlclient.ObjectKey{Name: "edge1"}, &node); err != nil {
					t.Fatal(err)
				}
				if err := api.Delete(ctx, &node); err != nil {
					t.Fatal(err)
				}
				node.ResourceVersion = ""
				node.UID = "replacement-node"
				if err := api.Create(ctx, &node); err != nil {
					t.Fatal(err)
				}
			}
			after, current, err := c.lanLinkAdjacencyTargets(ctx, api, "lab", want)
			if mode == "unchanged" {
				if err != nil || before.observationDigest() != after.observationDigest() || len(current) != 2 {
					t.Fatal("unchanged adjacency rejected", err)
				}
				if err := c.revalidateLANLinkAdjacency(ctx, want, before); err != nil {
					t.Fatal(err)
				}
			} else if mode == "managed-peer-replaced" {
				if err == nil {
					t.Fatal("replaced managed peer accepted")
				}
			} else {
				if err == nil && before.observationDigest() == after.observationDigest() {
					t.Fatal("changed adjacency was hidden")
				}
				if err := c.revalidateLANLinkAdjacency(ctx, want, before); err == nil {
					t.Fatal("changed native adjacency accepted")
				}
			}
		})
	}
}
