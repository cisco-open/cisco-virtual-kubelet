// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFabricL3IPHandoffManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "existing", "unmanaged", "missing-node", "late-alias", "late-node-change", "late-native", "late-device-change"} {
		t.Run(mode, func(t *testing.T) {
			flag := true
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			objects := []ctrlclient.Object{}
			inventory := []Device{}
			members := []fabricDeviceConfiguration{}
			for i, name := range []string{"a", "b"} {
				serial, ip := "serial-"+name, fmt.Sprintf("198.51.100.%d", 101+i)
				inventory = append(inventory, Device{ID: name, Hostname: name, Serial: serial, ManagementIP: ip})
				members = append(members, fabricDeviceConfiguration{ID: "member-" + name, NetworkDeviceID: name, FabricID: "fabric-id", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}})
				device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab", UID: types.UID("device-" + name)}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, PhysicalIdentity: serial, Address: ip}}
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-" + name, NodeName: name, NodeUID: "node-" + name, PhysicalIdentity: serial}
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("node-" + name)}}
				node.Status.NodeInfo.MachineID = serial
				if mode != "unmanaged" || name != "b" {
					objects = append(objects, device)
				}
				if mode != "missing-node" || name != "b" {
					objects = append(objects, node)
				}
			}
			reader := &fabricBindingReader{Reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(), mode: mode}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("identity observation mutated native state")
					return
				}
				switch r.URL.Path {
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case fabricSitesPath:
					pubsub := true
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricSite{{ID: "fabric-id", SiteID: "area", Authentication: "Closed Authentication", PubSub: &pubsub}}})
				case fabricDevicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": members})
				case devicesPath:
					reads++
					rows := append([]Device(nil), inventory...)
					if reads > 1 && mode == "late-native" {
						rows[0].Serial = "replacement-serial"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case assignedDevicesPath:
					fmt.Fprint(w, `{"response":[{"deviceId":"a","siteId":"building"}]}`)
				case wiredProvisionPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []wiredProvisionedDevice{{ID: "provision", DeviceID: "a", SiteID: "building"}}})
				case "/dna/intent/api/v1/interface/network-device/a":
					json.NewEncoder(w).Encode(map[string]any{"response": []lanLinkInterface{{ID: "port", DeviceID: "a", PortName: "GigabitEthernet1/0/1", Type: "Physical", Index: "1"}}})
				case fabricL3IPHandoffsPath:
					rows := []fabricL3IPHandoff{}
					if mode == "existing" {
						rows = append(rows, fabricL3IPHandoff{ID: "handoff", DeviceID: "a", FabricID: "fabric-id", TransitID: "transit", Port: "GigabitEthernet1/0/1", Network: "Users", VLAN: 300, Local4: "192.0.2.1/30", Peer4: "192.0.2.2/30"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case fabricTransitsPath:
					rows := []fabricTransit{{ID: "transit", Name: "Transit", Type: "IP_BASED_TRANSIT", IP: &ipTransitSettings{Protocol: "BGP", ASN: "65001"}}}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case fabricL3NetworksPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricL3Network{{ID: "vn", Name: "Users", Fabrics: []string{"fabric-id"}}}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want := fabricL3IPHandoffDeploymentIntent{handoff: fabricL3IPHandoffIntent{device: "a", port: "GigabitEthernet1/0/1", network: "Users", transit: "Transit", vlan: 300, local4: "192.0.2.1/30", peer4: "192.0.2.2/30"}, device: fabricDeviceIntent{inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "a"}, site: "Global/Lab/Building"}, state: "PROVISION", fabricSite: "Global/Lab", roles: []string{"BORDER_NODE"}, border: &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}}}
			snapshot, targets, err := c.fabricL3IPHandoffTargets(context.Background(), reader, "lab", want)
			if (err == nil) != (mode == "valid" || mode == "existing") {
				t.Fatal("managed target result", err)
			}
			if mode != "valid" && mode != "existing" {
				if snapshot != nil || targets != nil {
					t.Fatal("partial target evidence escaped")
				}
				return
			}
			if snapshot == nil || len(targets) != 2 {
				t.Fatal("incomplete group")
			}
			testFabricL3IPHandoffPrepareFlow(t, c, objects, want.handoff, mode == "existing")
			write, plannedTargets, err := c.planManagedFabricL3IPHandoff(context.Background(), reader, "lab", want)
			if err != nil || len(plannedTargets) != 2 {
				t.Fatal("managed plan", err)
			}
			if mode == "existing" {
				if write != nil {
					t.Fatal("existing mapping generated a write")
				}
			} else {
				if write == nil {
					t.Fatal("missing plan")
				}
				if _, err := write.submit(context.Background()); err == nil {
					t.Fatal("planning obtained native submission authority")
				}
				if _, err := write.submit(context.Background()); err == nil {
					t.Fatal("denied plan was reusable")
				}
			}
			for i, name := range []string{"a", "b"} {
				if targets[i].InventoryID != name || targets[i].PhysicalIdentity != "serial-"+name || targets[i].Device.UID != "device-"+name || targets[i].Node.UID != "node-"+name {
					t.Fatal("lost group identity")
				}
			}
		})
	}
}
