// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func mixedLANLinkFixture(t *testing.T, omitPool bool, sharedName ...bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, lanLinkIntent, *lanPreflightFixture) {
	t.Helper()
	ctx := context.Background()
	a, config, _, remove, f := ownedLANDeleteFixture(t, true)
	add := remove
	add.name = "aaa-new-link"
	if len(sharedName) > 0 && sharedName[0] {
		add.name = remove.name
	}
	add.action = "ADD_LINK"
	add.primaryInterface, add.secondaryInterface = "TenGigabitEthernet1/0/3", "TenGigabitEthernet1/0/4"
	if omitPool {
		remove.pool = ""
		f.deletePool = ""
	}
	f.extraPorts = append([]lanLinkInterface(nil), f.ports...)
	for i := range f.extraPorts {
		port := &f.extraPorts[i]
		port.ID += "-extra"
		port.IPv4, port.Mask = "", ""
		if i == 0 {
			port.PortName = add.primaryInterface
			port.Index = "3"
		} else {
			port.PortName = add.secondaryInterface
			port.Index = "4"
		}
	}
	row := func(w lanLinkIntent) map[string]any {
		out := lanLinkFixture()
		out["name"], out["action"] = w.name, w.action
		out["primary_device_management_ip_address"], out["secondary_device_management_ip_address"] = w.primaryIP, w.secondaryIP
		out["primary_device_interface_name"], out["secondary_device_interface_name"] = w.primaryInterface, w.secondaryInterface
		if w.pool == "" {
			delete(out, "ip_pool_name")
		} else {
			out["ip_pool_name"] = w.pool
		}
		return out
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline.Raw = lanLinkModel(t, row(add), row(remove))
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, remove, add, f
}

func installMixedLANGroup(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, reuse bool) {
	t.Helper()
	ctx := context.Background()
	if err := coordination.AddToScheme(a.statusWriter.Scheme()); err != nil {
		t.Fatal(err)
	}
	var physical []configurationDispatchFixtureMember
	for _, name := range []string{"edge0", "edge1"} {
		var d cvk.CiscoDevice
		var n core.Node
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: name}, &d); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
			t.Fatal(err)
		}
		physical = append(physical, configurationDispatchFixtureMember{device: &d, node: &n})
	}
	if !reuse {
		// The initial ownership helper simulates predecessor settlement. Remove its
		// fake leases once; subsequent operations must reuse actually released UIDs.
		var leases coordination.LeaseList
		if err := a.statusReader.List(ctx, &leases); err != nil {
			t.Fatal(err)
		}
		for i := range leases.Items {
			if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	configurationGroupDispatchFixture(t, a, physical, false, reuse)
	a.configurationAdmission = nil
	installFabricGroupAuthorityFixture(t, a, reuse)
}

func finishMixedLANOperation(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, want lanLinkIntent, f *lanPreflightFixture, applications int, allocation ...string) {
	t.Helper()
	ctx := context.Background()
	r := &configReconciler{a: a}
	reconcile := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	op := journalState(t, a).Operation.DeepCopy()
	if op.Phase != "Accepted" {
		t.Fatal("native request not acknowledged", op.Phase)
	}
	f.taskEnd = op.SubmittedAt.Time
	if want.action == "DELETE_LINK" {
		for i := range f.ports {
			f.ports[i].IPv4, f.ports[i].Mask = "", ""
		}
	} else {
		ports := f.extraPorts
		if want.primaryInterface == f.ports[0].PortName {
			ports = f.ports
		}
		first, second := "192.0.2.132", "192.0.2.133"
		if len(allocation) > 0 {
			if len(allocation) != 2 {
				t.Fatal("invalid fixture allocation")
			}
			first, second = allocation[0], allocation[1]
		}
		ports[0].IPv4, ports[1].IPv4 = first, second
		for i := range ports {
			ports[i].Mask = "255.255.255.254"
		}
	}
	reconcile()
	if journalState(t, a).Operation.Phase != "Verifying" {
		t.Fatal("mixed retained recovery did not verify")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !time.Now().UTC().Truncate(time.Second).After(f.taskEnd) {
		if time.Now().After(deadline) {
			t.Fatal("post-task clock unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	reconcile()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op = nc.Status.Configuration.Operation.DeepCopy()
	members, err := contract.ReadAcknowledgedGroupReservations(ctx, a.statusReader, nc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var endpoints *lanLinkEndpointSnapshot
	var result string
	commands := lanLinkProofCommands
	if want.action == "DELETE_LINK" {
		native, _, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, op, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		endpoints = native.endpoints
		result, err = lanLinkDeleteResultDigest(native)
		if err != nil {
			t.Fatal(err)
		}
		commands = lanLinkDeleteProofCommands
	} else {
		native, _, err := a.client.lanLinkCompletionTargets(ctx, a.statusReader, config.Namespace, want, op, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		endpoints = native.dependencies.adjacency.endpoints
		result, err = lanLinkAddResultDigest(native)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range members {
		endpoint, peer := endpoints.primary, endpoints.secondary
		if member.Spec.InventoryID != endpoint.device.ID {
			endpoint, peer = peer, endpoint
		}
		expected := lanLinkProofOperationWithCommands(nc, op, member, result, f.taskEnd, commands(endpoint.port.PortName))
		var leaf ops.DeviceOperation
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(expected), &leaf); err != nil {
			t.Fatal("current operation read not created", err)
		}
		at := time.Now().UTC().Truncate(time.Second)
		if want.action == "DELETE_LINK" {
			stampLANDeleteProofFixture(&leaf, member, endpoint, peer, at, at, at)
		} else {
			stampLANProofFixture(&leaf, member, endpoint, peer, at, at, at)
		}
		if err := a.statusWriter.Update(ctx, &leaf); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	if state := journalState(t, a); state.Operation.Phase != "Verified" || len(state.ActionApplications) != applications {
		t.Fatal("mixed native/device proof did not publish")
	}
	settleLANGroupWithWorkers(t, a, applications)
}

// Full public composition with native HTTPS and fake Kubernetes. Device output
// and maintenance cleanup are explicit fixtures; actual shared worker code
// performs release, and canonical lease/actor UIDs survive between operations.
func TestLANLinkMixedPublicCycle(t *testing.T) {
	for _, scenario := range []struct {
		name                 string
		omitPool, sharedName bool
	}{{"explicit-pool", false, false}, {"omitted-pool", true, false}, {"shared-name", false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, remove, add, f := mixedLANLinkFixture(t, scenario.omitPool, scenario.sharedName)
			r := &configReconciler{a: a}
			req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
			reconcile := func() {
				t.Helper()
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			reconcile()
			if op := journalState(t, a).Operation; op == nil || !validLANLinkMutationResource(op.Resource, op.Action) {
				t.Fatal("mixed source not prepared")
			}
			if _, err := parseLANLinkDeleteResource(journalState(t, a).Operation.Resource, "Update"); err != nil {
				t.Fatal("cleanup must precede a lexically earlier ADD", err)
			}
			installMixedLANGroup(t, a, config, false)
			reconcile()
			if f.posts != 2 {
				t.Fatal("DELETE did not submit once", f.posts)
			}
			var original coordination.LeaseList
			if err := a.statusReader.List(ctx, &original); err != nil {
				t.Fatal(err)
			}
			finishMixedLANOperation(t, a, config, remove, f, 2)
			reconcile()
			if op := journalState(t, a).Operation; op == nil || op.Phase != "Prepared" || !validLANLinkAddResource(op.Resource, op.Action) {
				t.Fatal("settled DELETE did not allow remaining ADD")
			}
			installMixedLANGroup(t, a, config, true)
			f.mutationFeature = "LINK_ADD"
			f.mutationPorts = f.extraPorts
			reconcile()
			if f.posts != 3 {
				t.Fatal("remaining ADD not submitted once", f.posts)
			}
			finishMixedLANOperation(t, a, config, add, f, 3)
			for range 2 {
				reconcile()
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, req.NamespacedName, &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || published.Status.SectionStatus[0].ResourceCount != 2 {
				t.Fatal("mixed source failed owned convergence", published.Status)
			}
			if journalState(t, a).Operation != nil || f.posts != 3 {
				t.Fatal("mixed reconciliation replayed")
			}
			for _, prior := range original.Items {
				var current coordination.Lease
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&prior), &current); err != nil {
					t.Fatal(err)
				}
				if current.UID != prior.UID || current.Spec.LeaseTransitions == nil || *current.Spec.LeaseTransitions != *prior.Spec.LeaseTransitions+1 || current.Spec.HolderIdentity != nil && *current.Spec.HolderIdentity != "" {
					t.Fatal("next operation replaced a retained lease or failed to release")
				}
			}
			// Dropping the declaration needed to interpret an opaque newer ADD must
			// hold the historical DELETE rather than infer its unrelated port scope.
			updateLANIntentFixture(t, a, config, remove)
			reconcile()
			if journalState(t, a).Operation != nil || f.posts != 3 {
				t.Fatal("ambiguous history adopted or replayed")
			}
			if err := a.statusReader.Get(ctx, req.NamespacedName, &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("missing port-scope evidence reported ready")
			}
		})
	}
}

func TestLANLinkMixedWholeSourceAdmission(t *testing.T) {
	for _, mode := range []string{"valid", "missing-add-port", "unowned-delete", "unknown-field", "shared-port", "late-source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, _, _, f := mixedLANLinkFixture(t, false)
			switch mode {
			case "missing-add-port":
				f.extraPorts = nil
			case "unowned-delete":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.ActionApplications = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "unknown-field":
				source.raw = append([]byte(nil), source.raw...)
				source.raw = []byte(`{"catalyst_center":{"lan_automation":{"links":[],"devices":[]}}}`)
			case "shared-port":
				f.extraPorts[0].ID = f.ports[0].ID
			case "late-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				config.Generation--
			}
			lifecycle, err := a.publicLANLinkLifecycle(source)
			if err == nil {
				_, err = a.prepareLANLinkMutation(ctx, config, source, "seed-peer", lifecycle)
			}
			if (err == nil) != (mode == "valid") {
				t.Fatal("unexpected whole-model admission", err)
			}
			if mode != "valid" && journalState(t, a).Operation != nil {
				t.Fatal("partial source prepared cleanup")
			}
			if f.posts != 1 {
				t.Fatal("preparation bypassed dispatch")
			}
		})
	}
}

func TestLANLinkRepeatedPublicCycle(t *testing.T) {
	for _, scenario := range []struct {
		name                     string
		omitPool, sameAllocation bool
	}{{"new-allocation", false, false}, {"same-allocation", false, true}, {"omitted-delete-pool", true, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, add, f := lanLinkAddJournalFixture(t)
			f.inventory[0].Reachability, f.inventory[1].Reachability = "Reachable", "Reachable"
			r := &configReconciler{a: a}
			req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
			reconcile := func() {
				t.Helper()
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			// Begin with no operation/owner/lease. All three effects below enter through
			// public reconciliation and complete through actual reservation-worker code.
			reconcile()
			installMixedLANGroup(t, a, config, false)
			reconcile()
			finishMixedLANOperation(t, a, config, add, f, 1, "192.0.2.130", "192.0.2.131")
			original := journalState(t, a).ActionApplications[0].DeepCopy()
			var initial coordination.LeaseList
			if err := a.statusReader.List(ctx, &initial); err != nil {
				t.Fatal(err)
			}
			remove := add
			remove.action = "DELETE_LINK"
			if scenario.omitPool {
				remove.pool = ""
			}
			updateLANIntentFixture(t, a, config, remove)
			reconcile()
			installMixedLANGroup(t, a, config, true)
			f.mutationFeature = "LINK_DELETE"
			f.deletePool = remove.pool
			reconcile()
			finishMixedLANOperation(t, a, config, remove, f, 2)
			updateLANIntentFixture(t, a, config, add)
			reconcile()
			if op := journalState(t, a).Operation; op == nil || op.Phase != "Prepared" || op.ID == original.Operation.ID || !validLANLinkAddResource(op.Resource, op.Action) {
				t.Fatal("retired DELETE did not prepare fresh same-link ADD")
			}
			installMixedLANGroup(t, a, config, true)
			f.mutationFeature = "LINK_ADD"
			reconcile()
			allocation := []string{"192.0.2.132", "192.0.2.133"}
			count := 3
			if scenario.sameAllocation {
				allocation = []string{"192.0.2.130", "192.0.2.131"}
				count = 2
			}
			finishMixedLANOperation(t, a, config, add, f, count, allocation...)
			for range 2 {
				reconcile()
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, req.NamespacedName, &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || published.Status.SectionStatus[0].ResourceCount != 1 || f.posts != 3 || journalState(t, a).Operation != nil {
				t.Fatal("repeated public cycle failed idempotent convergence", published.Status)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parseLANLinkAddResource(original.Operation.Resource, original.Operation.Action)
			if err != nil {
				t.Fatal(err)
			}
			latest, err := latestLANLinkEffect(nc, config, parsed.logical, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if latest == nil || latest.deleted || latest.receipt.Operation.ID == original.Operation.ID {
				t.Fatal("old ADD/DELETE suppressed the latest applied ADD")
			}
			if scenario.sameAllocation && latest.receipt.Resource != original.Resource {
				t.Fatal("identical result did not use guarded receipt replacement")
			}
			for _, old := range initial.Items {
				var current coordination.Lease
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&old), &current); err != nil {
					t.Fatal(err)
				}
				if current.UID != old.UID || *current.Spec.LeaseTransitions != *old.Spec.LeaseTransitions+2 || current.Spec.HolderIdentity != nil && *current.Spec.HolderIdentity != "" {
					t.Fatal("complete cycle replaced or failed to release retained canonical lease")
				}
			}
		})
	}
}
