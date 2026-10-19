// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func portChannelDependencyFixture(t *testing.T, mode string, post ...func(http.ResponseWriter, *http.Request)) (*client, fabricPortChannelDeploymentIntent, *bool) {
	t.Helper()
	assignmentReads := strings.HasPrefix(mode, "assignment/")
	mode = strings.TrimPrefix(mode, "assignment/")
	_, wants, err := parseFabricPortChannelDeployment(portChannelTestModel("[" + portChannelTestRow + "]"))
	if err != nil {
		t.Fatal(err)
	}
	revised := false
	deviceReads := 0
	channelReads := 0
	taskTime := time.Now().UTC().Truncate(time.Second)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "POST" && r.URL.Path == fabricPortChannelsPath && len(post) == 1 {
			post[0](w, r)
			return
		}
		if ((assignmentReads || mode == "edge-mixed") && r.URL.Path == fabricPortAssignmentsPath || mode == "assignment-sequence" && strings.HasPrefix(r.URL.Path, taskPath) || mode == "edge-mixed" && r.URL.Path == fabricPortChannelsPath) && len(post) == 1 {
			post[0](w, r)
			return
		}
		if r.Method != "GET" {
			t.Error("dependency mutation", r.Method)
			w.WriteHeader(400)
			return
		}
		var rows any
		switch r.URL.Path {
		case taskPath + "task/tree":
			end := taskTime.UnixMilli()
			if mode == "recovery-running" {
				end = 0
			}
			rows = []map[string]any{{"id": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": taskTime.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": mode == "recovery-failed", "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": end}}
		case devicesPath:
			deviceReads++
			inventory := []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", ManagementIP: "198.51.100.101"}, {ID: "peer-id", Hostname: "peer", Serial: "FOC87654321", ManagementIP: "198.51.100.102"}}
			if revised && mode == "changed-peer" || deviceReads > 1 && mode == "late-peer" {
				inventory[1].Serial = "FOC99999999"
			}
			rows = inventory
		case sitesPath:
			rows = sampleSites()
		case fabricSitesPath:
			flag := false
			rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}}
		case assignedDevicesPath:
			rows = []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
		case wiredProvisionPath:
			id := "provision"
			if revised && mode == "changed-provision" {
				id = "replacement"
			}
			rows = []wiredProvisionedDevice{{ID: id, DeviceID: "edge-id", SiteID: "building"}}
		case fabricDevicesPath:
			members := []fabricDeviceConfiguration{{ID: "member-edge", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "member-peer", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}}
			if mode == "not-member" {
				members = members[1:]
			}
			if mode == "wrong-role" {
				members[0].Roles = []string{"CONTROL_PLANE_NODE"}
			}
			rows = members
		case "/dna/intent/api/v1/interface/network-device/edge-id":
			ports := []lanLinkInterface{{ID: "port-1", DeviceID: "edge-id", PortName: "GigabitEthernet1/0/1", Type: "Physical", Index: "1"}, {ID: "port-2", DeviceID: "edge-id", PortName: "GigabitEthernet1/0/2", Type: "Physical", Index: "2"}}
			if revised && mode == "changed-port" {
				ports[1].ID = "replaced-port"
			}
			if revised && mode == "changed-port-config" {
				ports[1].AdminStatus = "DOWN"
			}
			if mode == "virtual-port" {
				ports[0].MappedID = "mapped"
			}
			if mode == "missing-port" {
				ports = ports[:1]
			}
			rows = ports
		case fabricPortAssignmentsPath:
			assignments := []fabricPortAssignment{}
			if mode == "assigned-port" || mode == "changed-assignment" && revised {
				port := "GigabitEthernet1/0/3"
				if mode == "assigned-port" {
					port = "GigabitEthernet1/0/1"
				}
				assignments = append(assignments, fabricPortAssignment{ID: "assignment", FabricID: "fabric", DeviceID: "edge-id", Interface: port, Type: "USER_DEVICE"})
			}
			rows = assignments
		case fabricPortChannelsPath:
			channelReads++
			channels := []fabricPortChannel{}
			if mode == "existing" || mode == "overlap" || mode == "description" || revised && (mode == "created" || strings.HasPrefix(mode, "recovery-")) && mode != "recovery-missing" {
				channel := fabricPortChannel{ID: "native-channel", FabricID: "fabric", DeviceID: "edge-id", Name: "Port-channel99", Interfaces: []string{"GigabitEthernet1/0/2", "GigabitEthernet1/0/1"}, Type: "TRUNK", Protocol: "LACP"}
				if mode == "recovery-replaced" && channelReads%2 == 0 {
					channel.ID = "replacement"
				}
				if mode == "overlap" {
					channel.Interfaces = channel.Interfaces[:1]
				}
				channels = append(channels, channel)
			}
			if revised && mode == "foreign-channel" {
				channels = append(channels, fabricPortChannel{ID: "unrelated", FabricID: "fabric", DeviceID: "edge-id", Name: "Port-channel2", Interfaces: []string{"GigabitEthernet1/0/3"}, Type: "TRUNK", Protocol: "LACP"})
			}
			rows = channels
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
			t.Error(err)
		}
	})
	want := wants[0]
	if mode == "description" {
		value := "required"
		want.channel.Description = &value
	}
	if mode == "unverified-vlan" {
		vlan := 100
		want.channel.NativeVLAN = &vlan
	}
	return c, want, &revised
}

func TestFabricPortChannelDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "existing", "created", "assigned-port", "changed-assignment", "changed-port", "changed-port-config", "changed-provision", "changed-peer", "foreign-channel", "not-member", "wrong-role", "virtual-port", "missing-port", "overlap", "description", "unverified-vlan"} {
		t.Run(mode, func(t *testing.T) {
			c, want, revised := portChannelDependencyFixture(t, mode)
			before, err := c.fabricPortChannelDependencies(context.Background(), want)
			invalid := mode == "assigned-port" || mode == "not-member" || mode == "wrong-role" || mode == "virtual-port" || mode == "missing-port" || mode == "overlap" || mode == "description" || mode == "unverified-vlan"
			if invalid {
				if err == nil || before != nil {
					t.Fatal("invalid dependencies accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if before.deviceID != "edge-id" || before.fabricID != "fabric" || len(before.physical) != 2 || len(before.ports) != 2 || len(before.digest) != 64 {
				t.Fatal("incomplete dependency snapshot")
			}
			if (before.existing != nil) != (mode == "existing") {
				t.Fatal("incorrect existing mapping")
			}
			if before.existing != nil && before.existing.Name != "Port-channel99" {
				t.Fatal("local ID used as native name")
			}
			*revised = true
			after, err := c.fabricPortChannelDependencies(context.Background(), want)
			if err != nil {
				t.Fatal(err)
			}
			unchanged := mode == "valid" || mode == "existing"
			if (before.digest == after.digest) != unchanged {
				t.Fatal("incorrect full evidence stability")
			}
			if (before.recoveryDigest == after.recoveryDigest) != (unchanged || mode == "created") {
				t.Fatal("unrelated change hidden by recovery digest")
			}
			if mode == "created" && (after.existing == nil || after.existing.ID != "native-channel") {
				t.Fatal("created readback not bound")
			}
		})
	}
}

func TestFabricPortChannelManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "missing-peer", "replaced-node", "late-peer"} {
		t.Run(mode, func(t *testing.T) {
			c, want, _ := portChannelDependencyFixture(t, mode)
			a, config := journalFixture(t)
			ctx := context.Background()
			for i, name := range []string{"edge", "peer"} {
				if mode == "missing-peer" && i == 1 {
					continue
				}
				serial := []string{"foc12345678", "foc87654321"}[i]
				uid, nodeUID := types.UID(name+"-uid"), types.UID(name+"-node-uid")
				device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: name, UID: uid}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: fmt.Sprintf("198.51.100.%d", 101+i), PhysicalIdentity: serial}}
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(uid), NodeName: name, NodeUID: string(nodeUID), PhysicalIdentity: serial}
				node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: nodeUID}}
				node.Status.NodeInfo.MachineID = serial
				if mode == "replaced-node" && i == 1 {
					node.UID = "replacement"
				}
				for _, obj := range []ctrlclient.Object{device, node} {
					if err := a.statusWriter.Create(ctx, obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			deps, targets, err := c.fabricPortChannelTargets(ctx, a.statusReader, config.Namespace, want)
			if mode != "valid" {
				if err == nil || deps != nil || targets != nil {
					t.Fatal("incomplete or changed physical scope accepted")
				}
				return
			}
			if err != nil || deps == nil || len(targets) != 2 {
				t.Fatal("missing managed footprint", err)
			}
		})
	}
}
