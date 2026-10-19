// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actual native catalog with simulated source ownership/managed Kubernetes.
// No submitted journal, claims or device proof are fabricated for dispatch.
func TestLiveCatalystCenterOwnedWiredBulkRefusal(t *testing.T) {
	exerciseLiveOwnedWiredBulkRefusal(t, false)
}

func TestLiveCatalystCenterResolvedWiredBulkRefusal(t *testing.T) {
	exerciseLiveOwnedWiredBulkRefusal(t, true)
}

// This live qualification must never send a configuration mutation, even if a
// planner accidentally lets a guarded closure escape. Authentication is the
// only permitted POST. Rejected attempts fail qualification at cleanup.
type wiredBulkReadOnlyTransport struct {
	base     http.RoundTripper
	attempts atomic.Int32
}

func (r *wiredBulkReadOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && !(req.Method == http.MethodPost && req.URL.Path == authPath) {
		r.attempts.Add(1)
		return nil, errors.New("bulk source qualification refuses all native mutations")
	}
	return r.base.RoundTrip(req)
}

func exerciseLiveOwnedWiredBulkRefusal(t *testing.T, resolved bool) {
	t.Helper()
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trace := &wiredBulkReadOnlyTransport{base: rest.HTTPClient.Transport}
	rest.HTTPClient.Transport = trace
	t.Cleanup(func() {
		if n := trace.attempts.Load(); n != 0 {
			t.Errorf("read-only bulk qualification attempted %d configuration mutations", n)
		}
	})
	members, before, allExisting := liveWiredBulkMembers(t, c, ctx)
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	declared := []string{}
	for _, site := range sites {
		declared = append(declared, site.NameHierarchy)
	}
	rows := []any{}
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ManagedSections = []string{"inventory"}
	for i, member := range members {
		if allExisting {
			member.state = "REPROVISION"
		}
		rows = append(rows, map[string]any{"name": member.desired.reference.Name, "device_ip": member.desired.reference.IP, "serial_number": member.desired.reference.Serial, "site": member.desired.site, "state": member.state})
		native, err := resolveConfigurationDevice(member.desired.reference, inventory)
		if err != nil {
			t.Fatal(err)
		}
		physical, err := topologyidentity.CanonicalPhysicalIdentity(native.Serial)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("owned-bulk-%d", i)
		deviceUID := name + "-device"
		nodeUID := name + "-node"
		device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: name, UID: types.UID(deviceUID), Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: native.ManagementIP, PhysicalIdentity: physical}}
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: deviceUID, NodeName: name, NodeUID: nodeUID, PhysicalIdentity: physical}
		node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(nodeUID)}}
		node.Status.NodeInfo.MachineID = physical
		for _, object := range []ctrlclient.Object{device, node} {
			if err := a.statusWriter.Create(ctx, object); err != nil {
				t.Fatal(err)
			}
		}
	}
	root := map[string]any{"inventory": map[string]any{"devices": rows}}
	if resolved {
		// Explicitly construct a resolved source from this read-only catalog
		// snapshot. Production never substitutes native sites for declarations.
		groups := liveWiredBulkResolvedSites(t, sites)
		root["sites"] = groups
		declared = nil
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": root})
	config.Spec.Source.Inline.Raw = raw
	config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global"}
	if resolved {
		delete(config.Annotations, wiredBulkRootAnnotation)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	batches, err := configuredWiredBulkBatches(config, source, declared)
	if err != nil {
		t.Fatal(err)
	}
	guardCalls := 0
	if _, err := a.dispatchWiredBulkProvision(ctx, config, source, nil); err == nil {
		t.Fatal("bulk dispatch accepted missing shared all-member permit")
	}
	planned, err := a.planOwnedWiredBulkSequence(ctx, config, source, batches, func(context.Context, wiredBulkBatch, []Device) error {
		guardCalls++
		t.Error("authority reached without journal")
		return nil
	}, time.Now())
	if err != nil || len(planned) == 0 || guardCalls != 0 {
		t.Fatal("owned native plan failed or dispatched", err)
	}
	var selected *wiredBulkPlannedBatch
	for i := range planned {
		if planned[i].write != nil {
			selected = &planned[i]
			break
		}
	}
	if selected == nil {
		t.Fatal("no pending candidate for refusal")
	}
	if _, err := selected.write.submit(ctx); err == nil || guardCalls != 0 {
		t.Fatal("owned write bypassed missing Submitting journal")
	}
	if _, err := selected.write.submit(ctx); err == nil || guardCalls != 0 {
		t.Fatal("refused owned closure replayed")
	}
	authorizeCalls := 0
	if err := a.recordWiredBulkApplication(ctx, config, strings.Repeat("a", 32), selected.batch, nil, func(context.Context, []*ops.ControllerConfigurationReservation) error { authorizeCalls++; return nil }, nil, time.Now()); err == nil || authorizeCalls != 0 {
		t.Fatal("missing convergence proof permitted publication")
	}
	if state := journalState(t, a); state.Operation != nil || len(state.ActionApplications) != 0 {
		t.Fatal("qualification fabricated a journal or receipt")
	}
	// Prepare only in the simulated Kubernetes client. This observes real native
	// state but neither claims the switches nor supplies a native mutation permit.
	prepared, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
	if err != nil || prepared == nil || prepared.Phase != "Prepared" || prepared.TaskID != "" || prepared.SubmittedAt != nil || contract.ValidateGroupJournalIdentity(prepared) != nil || len(prepared.PhysicalGroup.Targets) != len(selected.targets) {
		t.Fatal("simulated complete live batch preparation unavailable", err)
	}
	if _, err := a.dispatchWiredBulkProvision(ctx, config, source, nil); err == nil {
		t.Fatal("prepared live batch bypassed shared permit")
	}
	if _, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now()); err == nil {
		t.Fatal("restart replaced the simulated live prepared batch")
	}
	var reservations ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
		t.Fatal("simulated preparation fabricated reservations", err)
	}
	t.Log("three live native identities prepared in simulated Kubernetes; missing permit/restarted preparation refused; no native task or reservation created")
	// Deliberately untrusted/pruned status: no real native task receipt or
	// group claim is fabricated. Corrupt the simulated journal to separately
	// verify that public recovery fences it before task reads.
	current, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current.Status.Configuration.Operation = &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: current.Generation, SourceUID: string(source.uid), SourceVersion: source.version, Section: "inventory", Resource: selected.write.resource, Action: selected.write.action, TargetID: selected.write.identity.target, ParentID: selected.write.identity.parent, Phase: "Unknown", TaskID: "untrusted-placeholder", SubmittedAt: &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}}
	if err := a.statusWriter.Status().Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
		t.Fatal(err)
	}
	retained := journalState(t, a)
	if retained.Operation == nil || retained.Operation.Phase != "Unknown" || retained.Operation.TaskID != "untrusted-placeholder" || len(retained.ActionApplications) != 0 {
		t.Fatal("public pruned-status recovery lost its fence")
	}
	t.Log("public pruned/untrusted bulk status refused before acknowledged-task observation; no valid task/claim invented")
	fresh, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		native, err := resolveConfigurationDevice(member.desired.reference, fresh)
		if err != nil {
			t.Fatal(err)
		}
		baseline, bound := before[native.ID]
		record, _, err := c.wiredProvisionedDevice(ctx, native.ID)
		if err != nil || !bound || record != baseline {
			t.Fatal("native provisioning state changed", err)
		}
	}
	t.Log("owned source planning passed for three live identities; missing journal/proof and public pruned-status recovery refused; no claims, receipts or provisioning writes; Kubernetes state simulated")
	if resolved {
		t.Log("complete source inventory/sites context compiled without an external declaration set; native mutation attempts=0")
	}
}

// Construct test-only resolved geometry from an explicitly read catalog.
func liveWiredBulkResolvedSites(t *testing.T, sites []Site) map[string][]map[string]any {
	t.Helper()
	groups := map[string][]map[string]any{}
	for _, site := range sites {
		if site.Type == "global" {
			continue
		}
		parent := strings.TrimSuffix(site.NameHierarchy, "/"+site.Name)
		if parent == site.NameHierarchy || parent == "" {
			t.Fatal("native site has no canonical parent")
		}
		entry := map[string]any{"name": site.Name, "parent_name": parent}
		group := ""
		switch site.Type {
		case "area":
			group = "areas"
		case "building":
			group = "buildings"
		case "floor":
			group = "floors"
			entry["floor_number"], entry["rf_model"] = site.FloorNumber, site.RFModel
			entry["width"], entry["length"], entry["height"] = site.Width, site.Length, site.Height
			entry["units_of_measure"] = site.UnitsOfMeasure
		default:
			t.Fatal("unsupported native site kind in source fixture")
		}
		groups[group] = append(groups[group], entry)
	}
	return groups
}
