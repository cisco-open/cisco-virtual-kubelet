// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSDATransitReservedFamilyRetainsJournal(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Unknown", "Verifying", "Verified", "Cancelling"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			if _, err := a.prepareConfiguration(ctx, config, nil, "sites", "placeholder", "Update"); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation
			op.Section, op.Resource, op.Action, op.Phase, op.PhysicalGroup = "fabric", sdaTransitCreateFamily+"malformed", "Create", phase, nil
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			before := journalState(t, a).Operation.DeepCopy()
			status := &configapi.NetworkControllerConfigStatus{}
			_, pending, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
			if !pending || reason != "GroupReservationRecoveryRequired" {
				t.Fatal("reserved SDA journal escaped group recovery", reason)
			}
			for _, err := range []error{a.startConfiguration(ctx, config, nil, before.ID), a.advanceConfiguration(ctx, before.ID, "Unknown", ""), a.finishConfiguration(ctx, before.ID)} {
				if !errors.Is(err, errConfigurationDeviceAdmission) {
					t.Fatal("legacy lifecycle accepted reserved SDA", err)
				}
			}
			if !reflect.DeepEqual(before, journalState(t, a).Operation) {
				t.Fatal("pruned reserved SDA journal changed")
			}
			if validConfigurationGroupAction("fabric", sdaTransitResource(&sdaTransitSnapshot{digest: "test"}), "Create") {
				t.Fatal("staged SDA enabled public dispatch")
			}
		})
	}
}

func sdaTransitTestModel(row map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"transits": []any{row}}}})
	return raw
}

func TestSDATransitModel(t *testing.T) {
	for _, mode := range []string{"pubsub", "bgp", "false", "omitted", "per-site", "ip-settings", "bgp-multicast", "empty", "duplicate", "null", "unknown", "wrong-type"} {
		t.Run(mode, func(t *testing.T) {
			row := map[string]any{"name": "Transit", "type": "SDA_LISP_PUB_SUB_TRANSIT", "control_plane_devices": []any{"edge1", "edge0"}, "multicast_over_sda_transit": true}
			switch mode {
			case "bgp":
				row["type"] = "SDA_LISP_BGP_TRANSIT"
				delete(row, "multicast_over_sda_transit")
			case "false":
				row["multicast_over_sda_transit"], row["create_per_site"] = false, false
			case "omitted":
				delete(row, "multicast_over_sda_transit")
			case "per-site":
				row["create_per_site"] = true
			case "ip-settings":
				row["autonomous_system_number"] = 65001
			case "bgp-multicast":
				row["type"] = "SDA_LISP_BGP_TRANSIT"
			case "empty":
				row["control_plane_devices"] = []any{}
			case "duplicate":
				row["control_plane_devices"] = []any{"edge0", "edge0"}
			case "null":
				row["multicast_over_sda_transit"] = nil
			case "unknown":
				row["other"] = true
			case "wrong-type":
				row["type"] = "SDA"
			}
			wants, err := parseSDATransitIntents(sdaTransitTestModel(row))
			valid := mode == "pubsub" || mode == "bgp" || mode == "bgp-multicast" || mode == "false" || mode == "omitted"
			if (err == nil) != valid {
				t.Fatal("unexpected model acceptance", err)
			}
			if valid && (len(wants) != 1 || !reflect.DeepEqual(wants[0].controlPlanes, []string{"edge0", "edge1"})) {
				t.Fatal("control-plane set not canonical")
			}
			if mode == "bgp-multicast" && wants[0].multicast != nil {
				t.Fatal("BGP multicast projection diverged from pinned module")
			}
			if _, err := parseFabricTransits(sdaTransitTestModel(row)); err == nil {
				t.Fatal("SDA bypassed reserved execution through the ordinary IP executor")
			}
		})
	}
}

type sdaTransitFixture struct {
	devices                                                  []Device
	transits                                                 []fabricTransit
	posts                                                    int
	unprovisioned, unassigned                                bool
	lost                                                     bool
	taskMode                                                 string
	provisionID                                              string
	afterTask                                                func()
	afterPost                                                func()
	stamp                                                    int64
	unprovisionedIDs, unassignedIDs                          map[string]bool
	expectedCPs                                              []string
	inventoryReads, siteReads, transitReads, assignmentReads int
}

func newSDATransitFixture(t *testing.T) (*client, sdaTransitIntent, *sdaTransitFixture, ctrlclient.Client) {
	t.Helper()
	_, _, native, api := newManagedFabricDevicesBulkFixture(t, "create")
	f := &sdaTransitFixture{devices: append([]Device(nil), native.inventory[:2]...), transits: []fabricTransit{}, stamp: time.Now().UnixMilli(), provisionID: "provisioned"}
	for i := range f.devices {
		f.devices[i].Reachability, f.devices[i].Family = "Reachable", "Switches and Hubs"
	}
	flag := true
	want := sdaTransitIntent{name: "Transit", kind: "SDA_LISP_PUB_SUB_TRANSIT", controlPlanes: []string{"edge1", "edge0"}, multicast: &flag}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case devicesPath:
			f.inventoryReads++
			reply(f.devices)
		case sitesPath:
			f.siteReads++
			reply(sampleSites())
		case wiredProvisionPath:
			id := r.URL.Query().Get("networkDeviceId")
			rows := []wiredProvisionedDevice{}
			if !f.unprovisioned && !f.unprovisionedIDs[id] {
				rows = append(rows, wiredProvisionedDevice{ID: f.provisionID + "-" + id, DeviceID: id, SiteID: "building"})
			}
			reply(rows)
		case assignedDevicesPath:
			f.assignmentReads++
			rows := []map[string]string{}
			if !f.unassigned {
				for _, d := range f.devices {
					if !f.unassignedIDs[d.ID] {
						rows = append(rows, map[string]string{"deviceId": d.ID, "siteId": "building"})
					}
				}
			}
			reply(rows)
		case fabricTransitsPath:
			if r.Method == "GET" {
				f.transitReads++
				reply(f.transits)
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected native verb", r.Method)
				w.WriteHeader(405)
				return
			}
			f.posts++
			var rows []fabricTransit
			expected := f.expectedCPs
			if expected == nil {
				expected = []string{"edge-0", "edge-1"}
			}
			if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 || rows[0].ID != "" || rows[0].IP != nil || rows[0].SDA == nil || !reflect.DeepEqual(rows[0].SDA.ControlPlanes, expected) {
				t.Error("invalid native SDA body")
				w.WriteHeader(400)
				return
			}
			rows[0].ID = "transit-id"
			f.transits = append(f.transits, rows[0])
			if f.afterPost != nil {
				f.afterPost()
			}
			if f.lost {
				reply(map[string]any{})
			} else {
				reply(map[string]any{"taskId": "task"})
			}
		case taskPath + "task":
			reply(map[string]any{"id": "task", "isError": false, "startTime": f.stamp, "endTime": f.stamp})
		case taskPath + "task/tree":
			var end any = f.stamp
			if f.taskMode == "running" {
				end = nil
			}
			reply([]map[string]any{{"id": "task", "isError": false, "startTime": f.stamp, "endTime": f.stamp}, {"id": "child", "parentId": "task", "rootId": "task", "isError": f.taskMode == "failed", "errorCode": "FAILED", "startTime": f.stamp, "endTime": end}})
			if f.afterTask != nil {
				f.afterTask()
			}
		default:
			t.Error("unexpected endpoint", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	return c, want, f, api
}

func TestSDATransitCreateGuardsAndRecovery(t *testing.T) {
	for _, mode := range []string{"create", "bgp", "unprovisioned", "unassigned", "unreachable", "physical-alias", "peer-overlap", "peer-incomplete", "missing-guard", "denied", "late-provision", "late-name", "lost", "frozen-source", "running", "failed-child", "late-task-state", "replaced-CP", "same-name-conflict", "same-state"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, want, f, _ := newSDATransitFixture(t)
			switch mode {
			case "bgp":
				want.kind, want.multicast = "SDA_LISP_BGP_TRANSIT", nil
			case "unprovisioned":
				f.unprovisioned = true
			case "unassigned":
				f.unassigned = true
			case "unreachable":
				f.devices[1].Reachability = "Unreachable"
			case "physical-alias":
				f.devices[1].Serial = f.devices[0].Serial
			case "lost":
				f.lost = true
			case "peer-overlap":
				f.transits = []fabricTransit{{ID: "peer", Name: "Other", Type: "SDA_LISP_BGP_TRANSIT", SDA: &sdaTransitSettings{ControlPlanes: []string{"edge-0"}}}}
			case "peer-incomplete":
				f.transits = []fabricTransit{{ID: "peer", Name: "Other", Type: "SDA_LISP_BGP_TRANSIT"}}
			case "same-state", "same-name-conflict":
				s, err := c.sdaTransitSnapshot(ctx, want)
				if err != nil {
					t.Fatal(err)
				}
				s.native.ID = "existing"
				if mode == "same-name-conflict" {
					s.native.SDA.ControlPlanes = []string{"other"}
				}
				f.transits = []fabricTransit{s.native}
			}
			guarded := 0
			guard := func(_ context.Context, devices []Device) error {
				guarded++
				if len(devices) != 2 || devices[0].ID != "edge-0" || devices[1].ID != "edge-1" {
					t.Fatal("partial physical guard")
				}
				switch mode {
				case "denied":
					return errors.New("denied")
				case "late-provision":
					f.provisionID = "replacement"
				case "late-name":
					f.transits = []fabricTransit{{ID: "external", Name: "Transit", Type: "SDA_LISP_BGP_TRANSIT"}}
				}
				return nil
			}
			if mode == "missing-guard" {
				guard = nil
			}
			write, err := c.planSDATransitCreate(ctx, want, guard)
			if mode == "same-state" {
				if err != nil || write != nil || guarded != 0 {
					t.Fatal("same-state lookup gained mutation authority", err)
				}
				return
			}
			if mode == "unprovisioned" || mode == "unassigned" || mode == "unreachable" || mode == "physical-alias" || mode == "peer-overlap" || mode == "peer-incomplete" || mode == "missing-guard" || mode == "same-name-conflict" {
				if err == nil || write != nil || f.posts != 0 {
					t.Fatal("invalid prerequisites admitted", err)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if mode == "frozen-source" {
				want.controlPlanes[0] = "changed"
				*want.multicast = false
			}
			task, err := write.submit(ctx)
			if mode == "denied" || mode == "late-provision" || mode == "late-name" || mode == "lost" {
				wantPosts := 0
				if mode == "lost" {
					wantPosts = 1
				}
				if err == nil || f.posts != wantPosts {
					t.Fatal("guard/uncertainty lost", err, f.posts)
				}
				if _, err := write.submit(ctx); err == nil || f.posts != wantPosts {
					t.Fatal("one-use write replayed")
				}
				return
			}
			if err != nil || task.ID != "task" || f.posts != 1 || guarded != 1 {
				t.Fatal("create failed", err)
			}
			if _, err := write.submit(ctx); err == nil || f.posts != 1 {
				t.Fatal("accepted write replayed")
			}
			if mode == "frozen-source" {
				if !*f.transits[0].SDA.Multicast {
					t.Fatal("captured desired flag aliased caller")
				}
				return
			}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: "Create", TargetID: write.identity.target, Phase: "Verifying", TaskID: task.ID, SubmittedAt: &metav1.Time{Time: time.UnixMilli(f.stamp).Add(-time.Millisecond)}}
			if mode == "running" {
				f.taskMode = "running"
			}
			if mode == "failed-child" {
				f.taskMode = "failed"
			}
			if mode == "late-task-state" {
				f.afterTask = func() { f.provisionID = "replacement" }
			}
			if mode == "replaced-CP" {
				f.devices[1].Serial = "FOC99999999"
			}
			verified, err := c.observeSDATransitCreate(ctx, want, op, time.Now().Add(time.Second))
			positive := mode == "create" || mode == "bgp"
			if positive && (!verified || err != nil) || !positive && verified {
				t.Fatal("incorrect task/readback observation", verified, err)
			}
			if positive {
				if next, err := c.planSDATransitCreate(ctx, want, nil); err != nil || next != nil {
					t.Fatal("settled no-op failed", err)
				}
			}
		})
	}
}

func TestSDATransitManagedTargets(t *testing.T) {
	for _, mode := range []string{"bound", "missing-node", "node-replaced", "cross-namespace-alias"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, want, _, api := newSDATransitFixture(t)
			var devices cvk.CiscoDeviceList
			if err := api.List(ctx, &devices); err != nil {
				t.Fatal(err)
			}
			device := devices.Items[0].DeepCopy()
			if mode == "missing-node" {
				var node corev1.Node
				if err := api.Get(ctx, ctrlclient.ObjectKey{Name: device.Status.NodeIdentity.NodeName}, &node); err != nil {
					t.Fatal(err)
				}
				if err := api.Delete(ctx, &node); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "node-replaced" {
				device.Status.NodeIdentity.NodeUID = "replacement"
				if err := api.Update(ctx, device); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cross-namespace-alias" {
				device.Namespace, device.Name, device.UID, device.ResourceVersion = "other", "alias", "alias-id", ""
				if err := api.Create(ctx, device); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, targets, err := c.sdaTransitTargets(ctx, api, "lab", want)
			if mode == "bound" {
				if err != nil || snapshot == nil || len(targets) != 2 {
					t.Fatal("complete managed footprint not bound", err)
				}
			} else if err == nil || snapshot != nil || targets != nil {
				t.Fatal("invalid managed target admitted")
			}
		})
	}
}

func TestSDATransitManagedSubmissionBracket(t *testing.T) {
	for _, mode := range []string{"create", "denied", "late-address", "late-native", "missing-guard"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, want, f, api := newSDATransitFixture(t)
			guard := func(ctx context.Context, physical []Device) error {
				if len(physical) != 2 {
					t.Fatal("partial managed guard")
				}
				if mode == "denied" {
					return errors.New("denied")
				}
				if mode == "late-native" {
					f.provisionID = "replacement"
				}
				if mode == "late-address" {
					var d cvk.CiscoDevice
					if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "edge0"}, &d); err != nil {
						return err
					}
					d.Spec.Address = "198.51.100.200"
					return api.Update(ctx, &d)
				}
				return nil
			}
			if mode == "missing-guard" {
				guard = nil
			}
			write, targets, err := c.planManagedSDATransitCreate(ctx, api, "lab", want, guard)
			if mode == "missing-guard" {
				if err == nil || write != nil || targets != nil {
					t.Fatal("missing admission accepted")
				}
				return
			}
			if err != nil || write == nil || len(targets) != 2 || !reflect.DeepEqual(write.identity.groupTargets, targets) {
				t.Fatal("managed stage not bound", err)
			}
			_, err = write.submit(ctx)
			if mode == "create" {
				if err != nil || f.posts != 1 {
					t.Fatal("managed create failed", err)
				}
			} else if err == nil || f.posts != 0 {
				t.Fatal("late managed guard did not fence", err)
			}
		})
	}
}
