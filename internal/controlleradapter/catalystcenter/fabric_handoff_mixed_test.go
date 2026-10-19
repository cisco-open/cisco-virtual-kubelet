// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sigs.k8s.io/yaml"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const mixedFabricHandoffModel = `{"catalyst_center":{"inventory":{"devices":[{"name":"border","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"border","border_types":["LAYER_2","LAYER_3"],"local_autonomous_system_number":65000,"default_exit":true,"import_external_routes":true,"sda_transit":"SDA","l2_handoffs":{"l2_with_anycast_gateway":[{"l3_virtual_network":"Campus","ip_pool_name":"Users","external_vlan":400,"interfaces":["GigabitEthernet1/0/1"]}]},"l3_handoffs":[{"name":"Transit","interfaces":[{"name":"GigabitEthernet1/0/1","virtual_networks":[{"name":"Users","vlan":300,"local_ip_address":"192.0.2.1/30","peer_ip_address":"192.0.2.2/30"}]}]}]}]}}}`

// Native effects are simulated by the HTTP fixture. Kubernetes authority uses
// the same persisted-evidence fixture as single-family public dispatch tests.
func mixedHandoffFixture(t *testing.T, present [3]bool, broken string) (*adapter, *configapi.NetworkControllerConfig, []configurationDispatchFixtureMember, *[]string) {
	t.Helper()
	a, config := journalFixture(t)
	ctx := context.Background()
	config.Spec.ManagedSections = []string{"fabric"}
	config.Spec.Source.Inline.Raw = []byte(mixedFabricHandoffModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	_, l2, ip, sda, err := parseAllFabricHandoffDeployment(config.Spec.Source.Inline.Raw)
	if err != nil || len(l2) != 1 || len(ip) != 1 || len(sda) != 1 {
		t.Fatal("mixed fixture model", err)
	}
	members := []configurationDispatchFixtureMember{}
	for i, name := range []string{"border", "cp"} {
		serial := "serial-" + name
		uid := types.UID(name + "-uid")
		nodeUID := types.UID(name + "-node-uid")
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: name, UID: uid}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: fmt.Sprintf("198.51.100.%d", 101+i), PhysicalIdentity: serial}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(uid), NodeName: name, NodeUID: string(nodeUID), PhysicalIdentity: serial}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: nodeUID}}
		node.Status.NodeInfo.MachineID = serial
		for _, obj := range []ctrlclient.Object{device, node} {
			if err := a.statusWriter.Create(ctx, obj); err != nil {
				t.Fatal(err)
			}
		}
		members = append(members, configurationDispatchFixtureMember{device, node})
	}
	flag := true
	border := &fabricBorderSettings{Types: []string{"LAYER_2", "LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}
	desired2 := fabricL2Handoff{DeviceID: "device", FabricID: "fabric", Port: "GigabitEthernet1/0/1", InternalVLAN: 100, ExternalVLAN: 400}
	desiredIP := fabricL3IPHandoff{DeviceID: "device", FabricID: "fabric", TransitID: "ip-transit", Port: "GigabitEthernet1/0/1", Network: "Users", VLAN: 300, Local4: "192.0.2.1/30", Peer4: "192.0.2.2/30"}
	desiredSDA := fabricSDAHandoff{DeviceID: "device", FabricID: "fabric", TransitID: "sda-transit"}
	posts := []string{}
	l2Reads := 0
	now := time.Now().UTC().Truncate(time.Second)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "POST" {
			posts = append(posts, r.URL.Path)
			switch r.URL.Path {
			case fabricL2HandoffsPath:
				var payload []fabricL2Handoff
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !reflect.DeepEqual(payload, []fabricL2Handoff{desired2}) {
					t.Error("L2 payload")
				}
				present[0] = true
			case fabricL3IPHandoffsPath:
				var payload []fabricL3IPHandoff
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !reflect.DeepEqual(payload, []fabricL3IPHandoff{desiredIP}) {
					t.Error("IP payload")
				}
				present[1] = true
			case fabricSDAHandoffsPath:
				var payload []fabricSDAHandoff
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !reflect.DeepEqual(payload, []fabricSDAHandoff{desiredSDA}) {
					t.Error("SDA payload")
				}
				present[2] = true
			default:
				t.Error("unexpected mutation", r.URL.Path)
				w.WriteHeader(400)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected method", r.Method)
			w.WriteHeader(400)
			return
		}
		var rows any
		switch r.URL.Path {
		case sitesPath:
			rows = sampleSites()
		case fabricSitesPath:
			rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}}
		case devicesPath:
			rows = []Device{{ID: "device", Hostname: "border", Serial: "SERIAL-BORDER", ManagementIP: "198.51.100.101"}, {ID: "cp", Hostname: "cp", Serial: "SERIAL-CP", ManagementIP: "198.51.100.102"}}
		case assignedDevicesPath:
			rows = []map[string]string{{"deviceId": "device", "siteId": "building"}}
		case wiredProvisionPath:
			rows = []wiredProvisionedDevice{{ID: "provision", DeviceID: "device", SiteID: "building"}}
		case fabricDevicesPath:
			rows = []fabricDeviceConfiguration{{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: border}}
		case "/dna/intent/api/v1/interface/network-device/device":
			rows = []lanLinkInterface{{ID: "port", DeviceID: "device", PortName: "GigabitEthernet1/0/1", Type: "Physical", Index: "1"}}
		case fabricTransitsPath:
			rows = []fabricTransit{{ID: "ip-transit", Name: "Transit", Type: "IP_BASED_TRANSIT", IP: &ipTransitSettings{Protocol: "BGP", ASN: "65001"}}, {ID: "sda-transit", Name: "SDA", Type: "SDA_LISP_PUB_SUB_TRANSIT", SDA: &sdaTransitSettings{Multicast: &flag, ControlPlanes: []string{"cp"}}}}
			if broken == "last-dependency" || broken == "later-drift" && len(posts) > 0 {
				rows = rows.([]fabricTransit)[:1]
			}
		case fabricL3NetworksPath:
			rows = []fabricL3Network{{ID: "network", Name: "Users", Fabrics: []string{"fabric"}}}
		case anycastGatewaysPath:
			rows = []map[string]any{{"id": "gateway", "fabricId": "fabric", "virtualNetworkName": "Campus", "ipPoolName": "Users", "vlanName": "Users", "vlanId": 100, "trafficType": "DATA"}}
		case fabricL2NetworksPath:
			rows = []fabricL2Network{}
		case fabricL2HandoffsPath:
			l2Reads++
			rows = []fabricL2Handoff{}
			if present[0] {
				row := desired2
				row.ID = "l2-handoff"
				if broken == "conflict-l2" {
					row.ExternalVLAN = 401
				}
				if broken == "changing-readback" && l2Reads > 1 {
					row.ID = "replacement"
				}
				rows = []fabricL2Handoff{row}
			}
		case fabricL3IPHandoffsPath:
			rows = []fabricL3IPHandoff{}
			if present[1] {
				row := desiredIP
				row.ID = "ip-handoff"
				rows = []fabricL3IPHandoff{row}
			}
		case fabricSDAHandoffsPath:
			rows = []fabricSDAHandoff{}
			if present[2] {
				rows = []fabricSDAHandoff{desiredSDA}
			}
		case taskPath + "task/tree":
			rows = []map[string]any{{"id": "task", "isError": false, "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}}
		default:
			t.Error("unexpected read", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
			t.Error(err)
		}
	})
	return a, config, members, &posts
}

func TestMixedFabricHandoffPreparation(t *testing.T) {
	for _, mode := range []string{"l2-first", "ip-second", "sda-third", "converged", "last-dependency"} {
		t.Run(mode, func(t *testing.T) {
			present := [3]bool{}
			expected := "fabric-l2-handoff-create:"
			switch mode {
			case "ip-second":
				present[0] = true
				expected = "fabric-l3-ip-handoff-create:"
			case "sda-third":
				present[0], present[1] = true, true
				expected = "fabric-sda-handoff-create:"
			case "converged":
				present = [3]bool{true, true, true}
			}
			a, config, _, posts := mixedHandoffFixture(t, present, mode)
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
				t.Fatal("preparation authorized a mutation")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for repeat := 0; repeat < 2; repeat++ {
				result, err := (&configReconciler{a: a}).Reconcile(context.Background(), ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
				if err != nil {
					t.Fatal(err)
				}
				state := journalState(t, a)
				if mode == "last-dependency" || mode == "converged" {
					if state != nil && state.Operation != nil {
						t.Fatal("unexpected partial preparation")
					}
					if (result.RequeueAfter == 5*time.Minute) != (mode == "converged") {
						t.Fatal("incorrect convergence result", result)
					}
				} else if state == nil || state.Operation == nil || !strings.HasPrefix(state.Operation.Resource, expected) || state.Operation.Phase != "Prepared" {
					t.Fatal("incorrect sequence selection", state)
				}
			}
			if len(*posts) != 0 {
				t.Fatal("preparation submitted native work")
			}
		})
	}
}

func TestMixedFabricHandoffPublicLifecycle(t *testing.T) {
	for index, name := range []string{"l2", "ip", "sda"} {
		t.Run(name, func(t *testing.T) {
			present := [3]bool{true, true, true}
			present[index] = false
			a, config, all, posts := mixedHandoffFixture(t, present, "")
			testPublicHandoffPreparation(t, a, config)
			affected := all[:1]
			if index == 2 {
				affected = all
			}
			configurationGroupDispatchFixture(t, a, affected, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			a.configurationAdmission = nil
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(context.Background(), &list); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				members[i] = &list.Items[i]
			}
			testPublicHandoffRecovery(t, a, config, members, "dispatch-recovery-public")
			expected := []string{fabricL2HandoffsPath, fabricL3IPHandoffsPath, fabricSDAHandoffsPath}[index]
			if !reflect.DeepEqual(*posts, []string{expected}) {
				t.Fatal("native work replayed or wrong family submitted", *posts)
			}
		})
	}
}

// Recovery must finish the already accepted L2 operation even if an unrelated
// future SDA dependency disappears. Subsequent preparation still fails closed.
func TestMixedFabricHandoffRecoveryIgnoresUnrelatedFamily(t *testing.T) {
	a, config, all, posts := mixedHandoffFixture(t, [3]bool{false, true, true}, "later-drift")
	testPublicHandoffPreparation(t, a, config)
	configurationGroupDispatchFixture(t, a, all[:1], false)
	installFabricGroupAuthorityFixture(t, a)
	testPublicHandoffDispatch(t, a, config)
	for _, phase := range []string{"Verifying", "Verified"} {
		result, err := (&configReconciler{a: a}).Reconcile(context.Background(), ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
		if err != nil || result.RequeueAfter != 5*time.Second {
			t.Fatal("recovery", result, err)
		}
		if got := journalState(t, a).Operation.Phase; got != phase {
			t.Fatalf("phase=%s, want %s", got, phase)
		}
	}
	a.configurationAdmission = nil
	testFabricHandoffCompletion(t, a, config, true)
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("new dispatch authorized with missing dependency")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := (&configReconciler{a: a}).Reconcile(context.Background(), ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
	if err != nil || result.RequeueAfter != 5*time.Second {
		t.Fatal("missing dependency accepted", result, err)
	}
	if state := journalState(t, a); state.Operation != nil || len(state.ActionApplications) != 1 {
		t.Fatal("retained receipt lost or new work prepared")
	}
	if !reflect.DeepEqual(*posts, []string{fabricL2HandoffsPath}) {
		t.Fatal("unexpected native submissions", *posts)
	}
}

func TestMixedFabricHandoffExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/fabric-handoff-execution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("unsupported example")
	}
	devices, l2, ip, sda, err := parseAllFabricHandoffDeployment(config.Spec.Source.Inline.Raw)
	if err != nil || len(devices) != 1 || len(l2) != 1 || len(ip) != 1 || len(sda) != 1 {
		t.Fatal("example dropped declarations", err)
	}
}
