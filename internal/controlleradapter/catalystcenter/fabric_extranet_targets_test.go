// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func extranetManagedFixture(t *testing.T, mode string, intercept ...func(http.ResponseWriter, *http.Request) bool) (*adapter, *configapi.NetworkControllerConfig, fabricExtranetIntent, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	ctx := context.Background()
	a, config, _, members, _ := portChannelJournalFixture(t, "valid")
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote := mode == "remote-vn" || mode == "expansion-remote"
	if remote {
		d := Device{ID: "remote-id", Serial: "FOC77777777", Hostname: "remote", ManagementIP: "198.51.100.103", Family: "Switches and Hubs", Reachability: "Reachable"}
		inventory = append(inventory, d)
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "remote", Namespace: config.Namespace, UID: "remote-uid"}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: d.ManagementIP, PhysicalIdentity: "foc77777777"}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "remote-uid", NodeName: "remote", NodeUID: "remote-node-uid", PhysicalIdentity: "foc77777777"}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "remote", UID: "remote-node-uid"}}
		node.Status.NodeInfo.MachineID = "foc77777777"
		for _, object := range []ctrlclient.Object{device, node} {
			if err := a.statusWriter.Create(ctx, object); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "missing-managed-peer" {
		if err := a.statusWriter.Delete(ctx, members[1].device); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "replaced-node" {
		node := members[1].node
		if err := a.statusWriter.Delete(ctx, node); err != nil {
			t.Fatal(err)
		}
		node.UID = "replacement"
		node.ResourceVersion = ""
		if err := a.statusWriter.Create(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	writes := &atomic.Int32{}
	late := &atomic.Bool{}
	reads, vnReads, policyReads := 0, 0, 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if len(intercept) > 0 && intercept[0](w, r) {
			return
		}
		if r.Method != "GET" {
			writes.Add(1)
			if r.Method != "POST" || r.URL.Path != fabricExtranetPath {
				t.Error("unexpected mutation", r.Method, r.URL.Path)
				w.WriteHeader(500)
				return
			}
			if mode == "lost-receipt" {
				w.WriteHeader(503)
				return
			}
			var body []fabricExtranetPolicy
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].Name != "Shared" {
				t.Error("wrong body")
			}
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			return
		}
		var response any
		switch r.URL.Path {
		case sitesPath:
			response = sampleSites()
		case fabricSitesPath:
			yes := true
			rows := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &yes}}
			if remote {
				rows = append(rows, fabricSite{ID: "remote", SiteID: "building", Authentication: "Closed Authentication", PubSub: &yes})
			}
			response = rows
		case fabricZonesPath:
			rows := []fabricZone{}
			if mode == "zone" {
				rows = append(rows, fabricZone{ID: "zone", SiteID: "building", Authentication: "Closed Authentication"})
			}
			response = rows
		case fabricL3NetworksPath:
			vnReads++
			rows := []fabricL3Network{{ID: "services", Name: "Services", Fabrics: []string{"fabric"}}, {ID: "corp", Name: "Corp", Fabrics: []string{"fabric"}}, {ID: "guest", Name: "Guest", Fabrics: []string{"fabric"}}}
			if remote {
				rows[2].Fabrics = []string{"remote"}
			}
			if mode == "late-vn" && vnReads > 1 {
				rows[0].ID = "replacement"
			}
			if mode == "missing-membership" {
				rows[0].Fabrics = nil
			}
			response = rows
		case devicesPath:
			reads++
			rows := append([]Device(nil), inventory...)
			if mode == "late-serial" && reads > 1 || late.Load() && mode != "expansion-policy-drift" {
				rows[0].Serial = "FOC99999999"
			}
			response = rows
		case fabricDevicesPath:
			id := r.URL.Query().Get("fabricId")
			rows := []fabricDeviceMembership{{ID: id + "-edge", FabricID: id, NetworkDeviceID: "edge-id", Roles: []string{"EDGE_NODE"}}}
			if id == "fabric" {
				rows = append(rows, fabricDeviceMembership{ID: "border", FabricID: id, NetworkDeviceID: "peer-id", Roles: []string{"BORDER_NODE"}})
			}
			if id == "remote" {
				rows[0].NetworkDeviceID = "remote-id"
			}
			response = rows
		case fabricExtranetPath:
			policyReads++
			rows := []fabricExtranetPolicy{}
			if strings.HasPrefix(mode, "expansion") || mode == "existing" || mode == "late-policy" && policyReads > 1 || mode == "transient-policy" && policyReads == 2 {
				rows = append(rows, fabricExtranetPolicy{ID: "policy", Name: "Shared", Provider: "Services", Subscribers: []string{"Corp", "Guest"}, Fabrics: []string{"fabric"}})
				if strings.HasPrefix(mode, "expansion") {
					rows[len(rows)-1].Subscribers = []string{"Corp"}
					if mode == "expansion-policy-drift" && late.Load() {
						rows[len(rows)-1].Subscribers = []string{"Guest"}
					}
				}
			}
			if mode == "late-peer" && policyReads > 1 {
				rows = append(rows, fabricExtranetPolicy{ID: "peer-policy", Name: "Other", Provider: "Services", Subscribers: []string{"Corp"}, Fabrics: []string{"fabric"}})
			}
			response = rows
		default:
			t.Error("unexpected read", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": response})
	})
	want := fabricExtranetIntent{policy: fabricExtranetPolicy{Name: "Shared", Provider: "Services", Subscribers: []string{"Corp", "Guest"}}, paths: []string{"Global/Lab"}}
	if mode == "zone" {
		want.paths = []string{"Global/Lab/Building"}
	}
	if mode == "unscoped" {
		want.paths = nil
	}
	return a, config, want, writes, late
}

func TestExtranetManagedCreationFootprint(t *testing.T) {
	for _, mode := range []string{"valid", "existing", "zone", "remote-vn", "missing-managed-peer", "replaced-node", "late-serial", "late-vn", "late-policy", "transient-policy", "late-peer", "missing-membership", "unscoped"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, want, writes, _ := extranetManagedFixture(t, mode)
			write, targets, err := a.client.planManagedExtranetCreate(ctx, a.statusReader, config.Namespace, want)
			allowed := mode == "valid" || mode == "existing" || mode == "zone" || mode == "remote-vn"
			if !allowed {
				if err == nil || write != nil || targets != nil {
					t.Fatal("partial or changed scope accepted", err, targets)
				}
				if writes.Load() != 0 {
					t.Fatal("failed observation mutated native state")
				}
				return
			}
			expected := 2
			if mode == "remote-vn" {
				expected = 3
			}
			if err != nil || len(targets) != expected {
				t.Fatal("lost parent, non-border or remote VN member", err, targets)
			}
			if mode == "existing" {
				if write != nil {
					t.Fatal("converged policy planned mutation")
				}
			} else {
				if write == nil {
					t.Fatal("missing create candidate")
				}
				if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) {
					t.Fatal("preparation acquired authority", err)
				}
				if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
					t.Fatal("denied closure replayed", err)
				}
			}
			if writes.Load() != 0 {
				t.Fatal("managed planning submitted native write")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("planning created reservations", err)
			}
			var nodes core.NodeList
			if err := a.statusReader.List(ctx, &nodes); err != nil {
				t.Fatal(err)
			}
			for _, node := range nodes.Items {
				if node.Spec.Unschedulable {
					t.Fatal("planning cordoned node")
				}
			}
		})
	}
}

func TestExtranetGuardedCreateOneUse(t *testing.T) {
	for _, mode := range []string{"allowed", "denied", "late-native", "lost-receipt", "concurrent", "missing-guard"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, _, want, writes, late := extranetManagedFixture(t, mode)
			calls := atomic.Int32{}
			guard := func(context.Context) error {
				calls.Add(1)
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				if mode == "late-native" {
					late.Store(true)
				}
				return nil
			}
			if mode == "missing-guard" {
				guard = nil
			}
			write, err := a.client.planGuardedExtranetCreate(ctx, want, guard)
			if mode == "missing-guard" {
				if err == nil || write != nil {
					t.Fatal("missing guard accepted")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if mode == "concurrent" {
				var wg sync.WaitGroup
				for range 2 {
					wg.Add(1)
					go func() { defer wg.Done(); write.submit(ctx) }()
				}
				wg.Wait()
			} else {
				_, err = write.submit(ctx)
				if mode == "allowed" && err != nil || mode != "allowed" && err == nil {
					t.Fatal("wrong guarded result", err)
				}
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
				t.Fatal("guarded write replayed", err)
			}
			expected := int32(1)
			if mode == "denied" || mode == "late-native" {
				expected = 0
			}
			if writes.Load() != expected || calls.Load() != 1 {
				t.Fatal("wrong single-use mutation/guard count", writes.Load(), calls.Load())
			}
		})
	}
}

func TestExtranetManagedOwnedExpansion(t *testing.T) {
	for _, mode := range []string{"valid", "remote-vn", "unowned", "foreign-owner", "wrong-native-id", "undeclared-want", "changed-want", "changed-source", "source-changes-during-observation", "controller-changes-during-observation", "removed-member", "noop"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nativeMode := "expansion"
			if mode == "remote-vn" {
				nativeMode = "expansion-remote"
			}
			if mode == "noop" {
				nativeMode = "existing"
			}
			a, config, want, writes, _ := extranetManagedFixture(t, nativeMode)
			config.Spec.Source.Inline.Raw = []byte(extranetModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, targets, err := a.client.extranetManagedTargets(ctx, a.statusReader, config.Namespace, want)
			if err != nil {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "fabric", Resource: "extranet-create:" + strings.Repeat("b", 64), Action: "Create", Phase: "Prepared"}
			op, err = contract.WithGroupIdentity(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			op.Phase = "Verified"
			op.TaskID = "created-task"
			op.SubmittedAt = &metav1.Time{Time: now.Add(-time.Hour)}
			prefix, _ := extranetReceiptPrefix(want.policy.Name)
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: prefix + "policy", Operation: *op, VerifiedAt: &metav1.Time{Time: now.Add(-time.Minute)}}}}
			switch mode {
			case "unowned":
				nc.Status.Configuration.ActionApplications = nil
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "wrong-native-id":
				nc.Status.Configuration.ActionApplications[0].Resource = prefix + "replaced"
			case "undeclared-want":
				want.policy.Name = "Other"
			case "changed-want":
				want.policy.Subscribers = []string{"Corp"}
			case "changed-source":
				source.version = "stale"
			case "removed-member":
				want.policy.Subscribers = []string{"Guest"}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "source-changes-during-observation" || mode == "controller-changes-during-observation" {
				original := a.statusReader
				var once sync.Once
				a.statusReader = extranetSourceRaceReader{Reader: original, change: func() {
					once.Do(func() {
						if mode == "controller-changes-during-observation" {
							revised := nc.DeepCopy()
							revised.Generation++
							if err := a.statusWriter.Update(ctx, revised); err != nil {
								t.Fatal(err)
							}
							return
						}
						revised := config.DeepCopy()
						revised.Spec.Source.Inline.Raw = []byte(strings.Replace(extranetModel, "Shared", "Changed", 1))
						if err := a.statusWriter.Update(ctx, revised); err != nil {
							t.Fatal(err)
						}
					})
				}}
			}
			write, bound, err := a.planManagedOwnedExtranetExpansion(ctx, config, source, want)
			if mode != "valid" && mode != "remote-vn" && mode != "noop" {
				if err == nil || write != nil || bound != nil {
					t.Fatal("unowned or undeclared expansion prepared", err)
				}
				return
			}
			expected := 2
			if mode == "remote-vn" {
				expected = 3
			}
			if err != nil || len(bound) != expected {
				t.Fatal("incomplete expansion footprint", err, bound)
			}
			if mode == "noop" {
				if write != nil {
					t.Fatal("noop produced update")
				}
				return
			}
			if write == nil || write.action != "Update" || write.identity.target != "policy" {
				t.Fatal("missing UUID-bound expansion", write)
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("managed expansion acquired authority", err)
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
				t.Fatal("managed expansion closure replayed", err)
			}
			if writes.Load() != 0 {
				t.Fatal("preparation submitted native update")
			}
		})
	}
}

type extranetSourceRaceReader struct {
	ctrlclient.Reader
	change func()
}

func (r extranetSourceRaceReader) List(ctx context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	if _, ok := list.(*cvk.CiscoDeviceList); ok {
		r.change()
	}
	return r.Reader.List(ctx, list, opts...)
}
