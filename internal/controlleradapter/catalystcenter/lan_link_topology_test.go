// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func lanTopologyFixture() (*lanLinkEndpointSnapshot, *lanPhysicalTopology) {
	p := lanLinkEndpoint{device: Device{ID: "seed", ManagementIP: "192.0.2.10", Serial: "seed-serial"}, port: lanPortFixture("seed", "TenGigabitEthernet1/0/1")}
	s := lanLinkEndpoint{device: Device{ID: "peer", ManagementIP: "192.0.2.11", Serial: "peer-serial"}, port: lanPortFixture("peer", "TenGigabitEthernet1/0/2")}
	topology := &lanPhysicalTopology{Nodes: []lanTopologyNode{{ID: "seed", IP: p.device.ManagementIP, Type: "device"}, {ID: "peer", IP: s.device.ManagementIP, Type: "device"}, {ID: "other", IP: "192.0.2.12", Type: "device"}}, Links: []lanPhysicalLink{{ID: "seed:port--peer:port", Source: "seed", Target: "peer", StartID: p.port.ID, EndID: s.port.ID, StartName: p.port.PortName, EndName: s.port.PortName, Status: "up"}}}
	return &lanLinkEndpointSnapshot{primary: p, secondary: s}, topology
}

func TestLANPhysicalTopologyInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "unknown-state", "host", "duplicate-node", "duplicate-link", "unknown-node", "invalid-port", "missing-link-id", "missing-state", "missing-nodes", "missing-links", "null", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			_, topology := lanTopologyFixture()
			switch mode {
			case "empty":
				topology.Nodes = []lanTopologyNode{}
				topology.Links = []lanPhysicalLink{}
			case "unknown-state":
				topology.Links[0].Status = "NEW_STATE"
			case "host":
				topology.Nodes[2].Type = "HOST"
			case "duplicate-node":
				topology.Nodes = append(topology.Nodes, topology.Nodes[0])
			case "duplicate-link":
				topology.Links = append(topology.Links, topology.Links[0])
			case "unknown-node":
				topology.Links[0].Target = "absent"
			case "invalid-port":
				topology.Links[0].EndName = "port\n"
			case "missing-link-id":
				topology.Links[0].ID = ""
			case "missing-state":
				topology.Links[0].Status = ""
			case "missing-nodes":
				topology.Nodes = nil
			case "missing-links":
				topology.Links = nil
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != lanPhysicalTopologyPath || r.URL.RawQuery != "" {
					t.Error("unexpected topology request")
					w.WriteHeader(400)
					return
				}
				switch mode {
				case "null":
					fmt.Fprint(w, `{"response":null}`)
					return
				case "partial":
					w.WriteHeader(206)
				case "no-content":
					w.WriteHeader(204)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": topology})
			})
			got, err := c.lanPhysicalTopology(context.Background())
			valid := mode == "valid" || mode == "empty" || mode == "unknown-state" || mode == "host"
			if (err == nil) != valid {
				t.Fatal("unexpected topology validity", err)
			}
			if err == nil && (got.Nodes == nil || got.Links == nil) {
				t.Fatal("absence inferred from incomplete topology")
			}
		})
	}
}

func TestLANLinkAdjacencyBinding(t *testing.T) {
	for _, mode := range []string{"forward", "reverse", "absent", "unknown-state", "wrong-neighbor", "wrong-port-id", "wrong-port-name", "duplicate-adjacency", "second-neighbor", "foreign-port-id", "wrong-node-ip", "host-node", "grey-node", "grey-link", "missing-node", "unrelated-link"} {
		t.Run(mode, func(t *testing.T) {
			endpoints, topology := lanTopologyFixture()
			expected := topology.Links[0]
			switch mode {
			case "reverse":
				link := &topology.Links[0]
				link.Source, link.Target = link.Target, link.Source
				link.StartID, link.EndID = link.EndID, link.StartID
				link.StartName, link.EndName = link.EndName, link.StartName
			case "absent":
				topology.Links = []lanPhysicalLink{}
			case "unknown-state":
				topology.Links[0].Status = "UNKNOWN"
				expected = topology.Links[0]
			case "wrong-neighbor":
				topology.Links[0].Target = "other"
			case "wrong-port-id":
				topology.Links[0].EndID = "replacement"
			case "wrong-port-name":
				topology.Links[0].EndName = "Te1/0/2"
			case "duplicate-adjacency":
				other := topology.Links[0]
				other.ID = "duplicate"
				topology.Links = append(topology.Links, other)
			case "second-neighbor":
				other := topology.Links[0]
				other.ID = "second"
				other.Target = "other"
				other.EndID = "other-port"
				topology.Links = append(topology.Links, other)
			case "foreign-port-id":
				topology.Links[0].Source = "other"
			case "wrong-node-ip":
				topology.Nodes[0].IP = "192.0.2.99"
			case "host-node":
				topology.Nodes[0].Type = "HOST"
			case "grey-node":
				topology.Nodes[0].Greyed = true
			case "grey-link":
				topology.Links[0].Greyed = true
			case "missing-node":
				topology.Nodes = topology.Nodes[1:]
			case "unrelated-link":
				topology.Links = append(topology.Links, lanPhysicalLink{ID: "unrelated", Source: "seed", Target: "other", StartID: "other-seed-port", EndID: "other-port", StartName: "TenGigabitEthernet1/0/3", EndName: "TenGigabitEthernet1/0/1", Status: "up"})
			}
			before, _ := json.Marshal(topology)
			link, err := bindLANLinkAdjacency(endpoints, topology)
			valid := mode == "forward" || mode == "reverse" || mode == "absent" || mode == "unknown-state" || mode == "unrelated-link"
			if (err == nil) != valid {
				t.Fatal("unexpected adjacency validity", err)
			}
			if valid && mode != "absent" && !reflect.DeepEqual(link, &expected) {
				t.Fatal("exact oriented adjacency lost")
			}
			if valid && mode == "absent" && link != nil {
				t.Fatal("fabricated adjacency")
			}
			after, _ := json.Marshal(topology)
			if string(before) != string(after) {
				t.Fatal("topology mutated by binding")
			}
		})
	}
}
