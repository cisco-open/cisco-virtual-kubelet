// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

type lanPreflightFixture struct {
	mode                             string
	inventory                        []Device
	activityReads, usageReads, posts int
	afterPost                        func()
	ports                            []lanLinkInterface
	extraPorts                       []lanLinkInterface
	mutationPorts                    []lanLinkInterface
	mutationFeature, deletePool      string
	noLink                           bool
	taskEnd                          time.Time
	taskMode                         string
	topologyID                       string
	discoveryAddresses               bool
	poolChanged                      bool
}

func newLANPreflightFixture(t *testing.T, mode string) (*client, lanLinkIntent, *lanPreflightFixture, ctrlclient.Client) {
	t.Helper()
	_, _, native, api := newManagedFabricDevicesBulkFixture(t, "valid")
	f := &lanPreflightFixture{mode: mode, inventory: native.inventory}
	ports := []lanLinkInterface{lanPortFixture("edge-0", "TenGigabitEthernet1/0/1"), lanPortFixture("edge-1", "TenGigabitEthernet1/0/2")}
	f.ports = ports
	f.topologyID = "physical-link"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}

		if r.Method == "PUT" && r.URL.Path == "/dna/intent/api/v1/lan-automation/updateDevice" {
			feature, pool := "LINK_ADD", "access"
			if f.mutationFeature != "" {
				feature = f.mutationFeature
			}
			if feature == "LINK_DELETE" {
				pool = f.deletePool
			}
			if r.URL.Query().Get("feature") != feature {
				t.Error("wrong link feature")
			}
			var body struct {
				Link map[string]string `json:"linkUpdate"`
			}
			selected := ports
			if len(f.mutationPorts) == 2 {
				selected = f.mutationPorts
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Link["sourceDeviceManagementIPAddress"] != f.inventory[0].ManagementIP || body.Link["destinationDeviceManagementIPAddress"] != f.inventory[1].ManagementIP || body.Link["sourceDeviceInterfaceName"] != selected[0].PortName || body.Link["destinationDeviceInterfaceName"] != selected[1].PortName || body.Link["ipPoolName"] != pool {
				t.Error("incorrect frozen LAN update payload")
			}
			f.posts++
			if f.afterPost != nil {
				f.afterPost()
			}
			if f.mode == "lost" {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"lan-task"}}`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected mutation")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == taskPath+"lan-task/tree" {
			end := f.taskEnd
			if end.IsZero() {
				end = time.Now().UTC().Truncate(time.Second)
			}
			var finished any = end.UnixMilli()
			if f.taskMode == "running" {
				finished = nil
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "lan-task", "isError": false, "startTime": end.UnixMilli(), "endTime": end.UnixMilli()}, {"id": "child", "parentId": "lan-task", "rootId": "lan-task", "isError": f.taskMode == "failed", "startTime": end.UnixMilli(), "endTime": finished}}})
			return
		}
		var response any
		switch r.URL.Path {
		case lanActiveSessionsPath:
			f.activityReads++
			response = map[string]any{"maxSupportedCount": "5", "activeSessions": "0", "activeSessionIds": []string{}}
			if f.mode == "disabled" {
				response.(map[string]any)["maxSupportedCount"] = "0"
			}
			if f.mode == "active" || f.mode == "active-race" && f.activityReads > 1 {
				response = map[string]any{"maxSupportedCount": "5", "activeSessions": "1", "activeSessionIds": []string{"active"}}
			}
		case devicesPath:
			response = f.inventory
		case "/dna/intent/api/v1/interface/network-device/edge-0":
			response = append([]lanLinkInterface{ports[0]}, lanExtraDevicePorts(f.extraPorts, "edge-0")...)
		case "/dna/intent/api/v1/interface/network-device/edge-1":
			response = append([]lanLinkInterface{ports[1]}, lanExtraDevicePorts(f.extraPorts, "edge-1")...)
		case lanPhysicalTopologyPath:
			nodes := []lanTopologyNode{}
			for _, device := range f.inventory {
				nodes = append(nodes, lanTopologyNode{ID: device.ID, IP: device.ManagementIP, Type: "device"})
			}
			response = lanPhysicalTopology{Nodes: nodes, Links: []lanPhysicalLink{{ID: f.topologyID, Source: "edge-0", Target: "edge-1", StartID: ports[0].ID, EndID: ports[1].ID, StartName: ports[0].PortName, EndName: ports[1].PortName, Status: "up"}}}
			if f.mode == "topology-address" {
				topology := response.(lanPhysicalTopology)
				topology.Links[0].StartIP = "192.0.2.130"
				response = topology
			}
			if len(f.extraPorts) == 2 {
				topology := response.(lanPhysicalTopology)
				primary, secondary := f.extraPorts[0], f.extraPorts[1]
				topology.Links = append(topology.Links, lanPhysicalLink{ID: "next-physical-link", Source: primary.DeviceID, Target: secondary.DeviceID, StartID: primary.ID, EndID: secondary.ID, StartName: primary.PortName, EndName: secondary.PortName, Status: "up"})
				response = topology
			}
			if f.noLink {
				topology := response.(lanPhysicalTopology)
				topology.Links = []lanPhysicalLink{}
				response = topology
			}
		case lanSessionStatusPath:
			devices := []lanDiscoveredDevice{}
			for _, device := range f.inventory[:2] {
				devices = append(devices, lanDiscoveredDevice{Name: device.Hostname, Serial: device.Serial, State: "recorded", Addresses: []string{device.ManagementIP}})
			}
			if f.discoveryAddresses {
				devices[0].Addresses = append(devices[0].Addresses, "192.0.2.130")
				devices[1].Addresses = append(devices[1].Addresses, "192.0.2.131")
			}
			response = []lanSession{{ID: "origin", Site: "Global/Lab", PrimaryIP: "198.51.100.200", Ports: []string{}, Pools: []lanSessionPool{}, Action: "STOP", Status: "recorded", Created: "2026-10-15T00:00:00Z", Discovered: devices}}
		case sitesPath:
			response = sampleSites()
		case globalPoolsPath:
			response = fixturePools()
		case sitePoolsPath:
			rows := fixtureReservations()
			if f.poolChanged {
				rows[0].ID = "replacement-pool"
			}
			response = rows
		case sitePoolsPath + "/reservation":
			f.usageReads++
			usage := lanUsageFixture()
			space := usage["ipV4AddressSpace"].(map[string]any)
			switch f.mode {
			case "exhausted":
				space["assignedAddresses"] = "125"
			case "one-left":
				space["assignedAddresses"] = "124"
			case "usage-race":
				if f.usageReads > 1 {
					space["assignedAddresses"] = "3"
				}
			case "missing-usage":
				delete(space, "totalAddresses")
			}
			response = usage
		default:
			t.Error("unexpected read", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": response})
	})
	want := lanLinkIntent{name: "seed-peer", action: "ADD_LINK", primaryIP: f.inventory[0].ManagementIP, secondaryIP: f.inventory[1].ManagementIP, primaryInterface: ports[0].PortName, secondaryInterface: ports[1].PortName, pool: "access"}
	return c, want, f, api
}

func TestLANLinkPreflightManagedSnapshot(t *testing.T) {
	for _, mode := range []string{"idle", "active", "disabled", "active-race", "usage-race", "missing-usage", "exhausted", "one-left", "delete", "managed-replaced"} {
		t.Run(mode, func(t *testing.T) {
			c, want, f, api := newLANPreflightFixture(t, mode)
			if mode == "delete" {
				want.action = "DELETE_LINK"
				want.pool = ""
			}
			if mode == "managed-replaced" {
				var device cvk.CiscoDevice
				if err := api.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "lab", Name: "edge1"}, &device); err != nil {
					t.Fatal(err)
				}
				device.Spec.PhysicalIdentity = "replaced"
				if err := api.Update(context.Background(), &device); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, targets, err := c.lanLinkPreflightTargets(context.Background(), api, "lab", want)
			valid := mode == "idle" || mode == "delete"
			if (err == nil) != valid {
				t.Fatal("unexpected execution preflight", err)
			}
			if valid && (len(targets) != 2 || snapshot.observationDigest() == "") {
				t.Fatal("preflight lost complete endpoint footprint")
			}
			if mode == "idle" && snapshot.usage.remaining() != 123 {
				t.Fatal("usage binding lost")
			}
			if mode == "delete" && (snapshot.usage != nil || f.usageReads != 0) {
				t.Fatal("delete assumed address allocation")
			}
		})
	}
}

func lanExtraDevicePorts(ports []lanLinkInterface, device string) []lanLinkInterface {
	var result []lanLinkInterface
	for _, port := range ports {
		if port.DeviceID == device {
			result = append(result, port)
		}
	}
	return result
}
