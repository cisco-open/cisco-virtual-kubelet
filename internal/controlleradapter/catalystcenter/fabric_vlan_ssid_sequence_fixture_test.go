// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fabricVLANSSIDSequenceNative struct {
	rows      []fabricVLANSSID
	posts     int
	multicast bool
	tasks     map[string]int64
}

func fabricVLANSSIDSequenceFixture(t *testing.T, embeddedMode ...bool) (*adapter, *configapi.NetworkControllerConfig, []configurationDispatchFixtureMember, *fabricVLANSSIDSequenceNative) {
	t.Helper()
	embedded := len(embeddedMode) > 0 && embeddedMode[0]
	native := &fabricVLANSSIDSequenceNative{rows: []fabricVLANSSID{}, tasks: map[string]int64{}}
	a, config, _, members, _ := portChannelJournalFixture(t, "valid")
	inventory, err := a.client.configurationDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inventory[1].Family = "Wireless Controller"
	inventory[1].Reachability = "Reachable"
	inventory[1].Hostname = "controller"
	if embedded {
		inventory[0].Family = "Switches and Hubs"
		inventory[0].Reachability = "Reachable"
		inventory[0].Hostname = "edge"
		inventory[1].Family = "Switches and Hubs"
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		if r.Method == "PUT" {
			if r.URL.Path == "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast" {
				var body map[string]bool
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || len(native.rows) == 0 {
					t.Error("multicast before mappings or malformed body")
					w.WriteHeader(400)
					return
				}
				native.multicast = body["multicastEnabled"]
				native.posts++
				id := fmt.Sprintf("task-%d", native.posts)
				native.tasks[id] = time.Now().UnixMilli()
				reply(map[string]string{"taskId": id})
				return
			}
			if r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids" {
				t.Error("unexpected mutation")
				w.WriteHeader(400)
				return
			}
			var rows []fabricVLANSSID
			if json.NewDecoder(r.Body).Decode(&rows) != nil {
				t.Error("malformed replacement")
				w.WriteHeader(400)
				return
			}
			canonical, err := canonicalFabricVLANSSIDs(rows)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			native.rows = canonical
			native.posts++
			task := fmt.Sprintf("task-%d", native.posts)
			native.tasks[task] = time.Now().UnixMilli()
			reply(map[string]string{"taskId": task})
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected method")
			w.WriteHeader(400)
			return
		}
		if strings.HasPrefix(r.URL.Path, taskPath) && strings.HasSuffix(r.URL.Path, "/tree") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := native.tasks[id]
			if !ok {
				w.WriteHeader(404)
				return
			}
			reply([]map[string]any{{"id": id, "isError": false, "startTime": stamp - 1000, "endTime": stamp}})
			return
		}
		switch r.URL.Path {
		case sitesPath:
			reply(sampleSites())
		case fabricSitesPath:
			b := false
			reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
		case devicesPath:
			reply(inventory)
		case fabricDevicesPath:
			if embedded {
				reply([]fabricDeviceMembership{{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "wlc-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}})
				return
			}
			reply([]fabricDeviceMembership{{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "wlc-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}}})
		case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
			reply(native.rows)
		case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
			reply(map[string]bool{"multicastEnabled": native.multicast})
		case "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting":
			reply([]fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Percentage: 25}}})
		case wiredProvisionPath:
			reply([]wiredProvisionedDevice{{ID: "provisioned", DeviceID: "edge-id", SiteID: "building"}})
		case assignedDevicesPath:
			reply([]map[string]string{{"deviceId": "edge-id", "siteId": "building"}})
		case "/dna/intent/api/v1/wirelessControllers/edge-id/managedApLocations/count":
			reply(map[string]int{"primaryManagedApLocationsCount": 1, "secondaryManagedApLocationsCount": 0, "anchorManagedApLocationsCount": 0})
		case "/dna/intent/api/v1/wirelessControllers/edge-id/primaryManagedApLocations":
			reply([]any{map[string]any{"managedApLocations": []wirelessManagedAPLocation{{SiteID: "building", SitePath: "Global/Lab/Building"}}}})
		case "/dna/intent/api/v1/wirelessControllers/edge-id/secondaryManagedApLocations":
			reply([]any{map[string]any{"managedApLocations": []wirelessManagedAPLocation{}}})
		case fabricL2NetworksPath:
			b := true
			reply([]fabricL2Network{{ID: "l2", FabricID: "fabric", VLANName: "Clients", VLAN: 100, Traffic: "DATA", Wireless: &b}, {ID: "l2-new", FabricID: "fabric", VLANName: "New", VLAN: 200, Traffic: "DATA", Wireless: &b}})
		case anycastGatewaysPath:
			reply([]any{})
		case wirelessProfilesPath:
			reply([]map[string]any{{"id": "profile", "wirelessProfileName": "Wireless", "ssidDetails": []map[string]any{{"ssidName": "Staff", "enableFabric": true}, {"ssidName": "Guest", "enableFabric": true}}}})
		case profileSiteAssignmentsPath("profile"):
			reply([]map[string]string{{"id": "area"}})
		case wirelessSSIDPath("area"), wirelessSSIDPath("root"):
			reply([]map[string]any{{"id": "ssid", "ssid": "Staff", "authType": "OPEN", "wlanType": "Enterprise"}, {"id": "guest", "ssid": "Guest", "authType": "OPEN", "wlanType": "Enterprise"}})
		case "/dna/intent/api/v1/wirelessControllers/peer-id/provisionStatus":
			reply(map[string]any{"networkDeviceId": "peer-id", "deviceName": "controller", "siteId": "area", "siteNameHierarchy": "Global/Lab", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "SUCCESS", "lastProvisionedTimeStamp": "12345", "outOfSync": false})

		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	return a, config, members, native
}
