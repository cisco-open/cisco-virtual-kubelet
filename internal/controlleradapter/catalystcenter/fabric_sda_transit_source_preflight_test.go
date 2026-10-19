// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func sdaTransitMultiSourceFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, []sdaTransitIntent, *sdaTransitFixture, []configurationDispatchFixtureMember) {
	t.Helper()
	a, config, _, f, physical := sdaTransitJournalFixture(t)
	raw := sdaTransitMultiSourceDocument(f)
	config.Spec.Source.Inline.Raw = raw
	config.Generation++
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	wants, err := parseSDATransitIntents(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, wants, f, physical
}

func sdaTransitMultiSourceDocument(f *sdaTransitFixture) []byte {
	rows := []any{}
	for i, name := range []string{"Alpha", "Zulu"} {
		rows = append(rows, map[string]any{"name": name, "type": "SDA_LISP_PUB_SUB_TRANSIT", "multicast_over_sda_transit": true, "control_plane_devices": []string{f.devices[i].Hostname}})
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"transits": rows}}})
	return raw
}

func TestSDATransitWholeSourcePreparation(t *testing.T) {
	for _, mode := range []string{"valid", "later-unprovisioned", "later-unassigned", "later-unmanaged", "later-conflict", "shared-physical", "existing-peer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, wants, f, physical := sdaTransitMultiSourceFixture(t)
			switch mode {
			case "later-unprovisioned":
				f.unprovisionedIDs = map[string]bool{"edge-1": true}
			case "later-unassigned":
				f.unassignedIDs = map[string]bool{"edge-1": true}
			case "later-unmanaged":
				for _, p := range physical {
					if p.device.Name == "edge1" {
						if err := a.statusWriter.Delete(ctx, p.device); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "shared-physical":
				var root map[string]any
				json.Unmarshal(config.Spec.Source.Inline.Raw, &root)
				root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)["transits"].([]any)[1].(map[string]any)["control_plane_devices"] = []string{"edge0"}
				config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "later-conflict", "existing-peer":
				flag := mode == "existing-peer"
				f.transits = []fabricTransit{{ID: "existing-zulu", Name: "Zulu", Type: "SDA_LISP_PUB_SUB_TRANSIT", SDA: &sdaTransitSettings{ControlPlanes: []string{"edge-1"}, Multicast: &flag}}}
			}
			op, err := a.prepareSDATransit(ctx, config, nil, wants[0])
			positive := mode == "valid" || mode == "existing-peer"
			if positive {
				if err != nil || op == nil || len(op.PhysicalGroup.Targets) != 1 || op.PhysicalGroup.Targets[0].InventoryID != "edge-0" {
					t.Fatal("selected subset not prepared", err)
				}
			} else if err == nil || op != nil || journalState(t, a) != nil && journalState(t, a).Operation != nil {
				t.Fatal("later invalid source retained first preparation", err)
			}
			if f.posts != 0 {
				t.Fatal("preflight submitted a native request")
			}
		})
	}
}

type sdaSourceSubmittingClient struct {
	ctrlclient.Client
	after func()
}
type sdaSourceSubmittingWriter struct {
	ctrlclient.SubResourceWriter
	owner *sdaSourceSubmittingClient
}

func (c *sdaSourceSubmittingClient) Status() ctrlclient.SubResourceWriter {
	return &sdaSourceSubmittingWriter{c.Client.Status(), c}
}
func (w *sdaSourceSubmittingWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	err := w.SubResourceWriter.Update(ctx, obj, opts...)
	if nc, ok := obj.(*cvk.NetworkController); err == nil && ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" && w.owner.after != nil {
		after := w.owner.after
		w.owner.after = nil
		after()
	}
	return err
}

func TestSDATransitWholeSourceDispatchAndRecovery(t *testing.T) {
	for _, mode := range []string{"dispatch", "late-other", "submitting-other", "ack-other-lost", "ack-earlier-lost"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, wants, f, physical := sdaTransitMultiSourceFixture(t)
			index := 0
			if mode == "ack-earlier-lost" {
				index = 1
			}
			selected, other := "edge-0", "edge-1"
			if index == 1 {
				selected, other = other, selected
			}
			f.expectedCPs = []string{selected}
			op, err := a.prepareSDATransit(ctx, config, nil, wants[index])
			if err != nil {
				t.Fatal(err)
			}
			members := []configurationDispatchFixtureMember{}
			for _, p := range physical {
				if p.device.Spec.PhysicalIdentity == op.PhysicalGroup.Targets[0].PhysicalIdentity {
					members = append(members, p)
				}
			}
			if len(members) != 1 {
				t.Fatal("selected admission footprint")
			}
			if mode == "submitting-other" {
				a.statusWriter = &sdaSourceSubmittingClient{Client: a.statusWriter, after: func() { f.unprovisionedIDs = map[string]bool{other: true} }}
			}
			permit, _ := configurationGroupDispatchPermitFixture(t, a, members)
			if mode == "late-other" {
				f.unprovisionedIDs = map[string]bool{other: true}
			}
			_, err = a.dispatchSDATransit(ctx, config, nil, wants[index], permit)
			if mode == "late-other" || mode == "submitting-other" {
				expected := "Prepared"
				if mode == "submitting-other" {
					expected = "Submitting"
				}
				if err == nil || f.posts != 0 || journalState(t, a).Operation.Phase != expected {
					t.Fatal("late source dependency escaped dispatch guard", err)
				}
				return
			}
			if err != nil || f.posts != 1 {
				t.Fatal("selected creation failed", err)
			}
			if mode == "ack-other-lost" || mode == "ack-earlier-lost" {
				f.unprovisionedIDs = map[string]bool{other: true}
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			a.configurationAdmission = &configurationAdmission{protocol: contract.GroupReservationProtocol, leaseNamespace: "leases", authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
			nc, _ := a.currentController(ctx)
			status := config.Status.DeepCopy()
			_, pending, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
			if !pending || reason != "ApplyProgressing" || journalState(t, a).Operation.Phase != "Verifying" || f.posts != 1 {
				t.Fatal("unrelated dependency prevented acknowledged recovery", reason)
			}
			if len(journalState(t, a).Operation.PhysicalGroup.Targets) != 1 || len(journalState(t, a).ActionApplications) != 0 {
				t.Fatal("read dependencies became action members or completion")
			}
		})
	}
}

func TestSDATransitSourceObservationBatching(t *testing.T) {
	ctx := context.Background()
	a, config, wants, f, _ := sdaTransitMultiSourceFixture(t)
	flag := true
	f.transits = []fabricTransit{{ID: "alpha", Name: "Alpha", Type: wants[0].kind, SDA: &sdaTransitSettings{ControlPlanes: []string{"edge-0"}, Multicast: &flag}}, {ID: "zulu", Name: "Zulu", Type: wants[1].kind, SDA: &sdaTransitSettings{ControlPlanes: []string{"edge-1"}, Multicast: &flag}}}
	before, _ := json.Marshal(f.transits)
	s, targets, err := a.client.sdaTransitSourceTargets(ctx, a.statusReader, config.Namespace, wants)
	if err != nil || len(targets) != 2 || len(s.entries) != 2 {
		t.Fatal("complete source observation failed", err)
	}
	if f.inventoryReads != 2 || f.siteReads != 2 || f.transitReads != 2 || f.assignmentReads != 2 {
		t.Fatal("global/site inventories were fetched per declaration", f.inventoryReads, f.siteReads, f.transitReads, f.assignmentReads)
	}
	if s.entries["Alpha"].peers["Zulu"].ID != "zulu" || s.entries["Zulu"].peers["Alpha"].ID != "alpha" {
		t.Fatal("selected result removal mutated shared peer inventory")
	}
	after, _ := json.Marshal(f.transits)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read mutated inventory")
	}
	reversed := []sdaTransitIntent{wants[1], wants[0]}
	other, _, err := a.client.sdaTransitSourceTargets(ctx, a.statusReader, config.Namespace, reversed)
	if err != nil || other.digest != s.digest {
		t.Fatal("source order changed observation identity", err)
	}
}

func TestSDATransitSourceBoundsBeforeObservation(t *testing.T) {
	for _, mode := range []string{"empty", "empty-cp", "duplicate-name", "excess-footprint"} {
		t.Run(mode, func(t *testing.T) {
			c, want, f, _ := newSDATransitFixture(t)
			wants := []sdaTransitIntent{want}
			switch mode {
			case "empty":
				wants = nil
			case "empty-cp":
				wants[0].controlPlanes = nil
			case "duplicate-name":
				wants = append(wants, want)
			case "excess-footprint":
				wants = []sdaTransitIntent{{name: "A", kind: want.kind}, {name: "B", kind: want.kind}}
				for i := 0; i < 1001; i++ {
					at := 0
					if i >= 500 {
						at = 1
					}
					wants[at].controlPlanes = append(wants[at].controlPlanes, fmt.Sprintf("cp-%d", i))
				}
			}
			if data, err := c.sdaTransitSourceSnapshot(context.Background(), wants); err == nil || data != nil {
				t.Fatal("invalid source scope accepted")
			}
			if f.inventoryReads != 0 || f.siteReads != 0 || f.transitReads != 0 || f.posts != 0 {
				t.Fatal("scope bounds were checked after observation")
			}
		})
	}
}
