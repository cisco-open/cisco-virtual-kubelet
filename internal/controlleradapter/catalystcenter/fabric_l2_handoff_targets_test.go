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

func TestFabricL2HandoffManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "unmanaged", "missing-node", "late-alias", "late-node-change", "late-native", "late-device-change", "existing"} {
		t.Run(mode, func(t *testing.T) {
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
				members = append(members, fabricDeviceConfiguration{ID: "member-" + name, NetworkDeviceID: name, FabricID: "fabric-id", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_2"}}})
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
				case fabricL2HandoffsPath:
					if mode == "existing" {
						fmt.Fprint(w, `{"response":[{"id":"handoff","fabricId":"fabric-id","networkDeviceId":"a","interfaceName":"GigabitEthernet1/0/1","internalVlanId":100,"externalVlanId":400}]}`)
					} else {
						fmt.Fprint(w, `{"response":[]}`)
					}
				case anycastGatewaysPath:
					fmt.Fprint(w, `{"response":[]}`)
				case fabricL2NetworksPath:
					fmt.Fprint(w, `{"response":[{"id":"network","fabricId":"fabric-id","vlanName":"IoT","vlanId":100,"trafficType":"DATA","isFabricEnabledWireless":false}]}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want := fabricL2HandoffDeploymentIntent{handoff: fabricL2HandoffIntent{device: "a", port: "GigabitEthernet1/0/1", vlanName: "IoT", externalVLAN: 400}, device: fabricDeviceIntent{inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "a"}, site: "Global/Lab/Building"}, state: "PROVISION", fabricSite: "Global/Lab", roles: []string{"BORDER_NODE"}, border: &fabricBorderSettings{Types: []string{"LAYER_2"}}}}
			snapshot, targets, err := c.planManagedFabricL2Handoff(context.Background(), reader, "lab", want)
			if (err == nil) != (mode == "valid" || mode == "existing") {
				t.Fatal("managed target result", err)
			}
			if mode != "valid" && mode != "existing" {
				if snapshot != nil || targets != nil {
					t.Fatal("partial target evidence escaped")
				}
				return
			}
			if (snapshot == nil) != (mode == "existing") || len(targets) != 2 {
				t.Fatal("incomplete group")
			}
			testFabricL2HandoffPrepareFlow(t, c, objects, want.handoff, mode == "existing")
			for i, name := range []string{"a", "b"} {
				if targets[i].InventoryID != name || targets[i].PhysicalIdentity != "serial-"+name || targets[i].Device.UID != "device-"+name || targets[i].Node.UID != "node-"+name {
					t.Fatal("lost group identity")
				}
			}
		})
	}
}
