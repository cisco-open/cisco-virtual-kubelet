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

// Native HTTPS, fake Kubernetes and explicit device/manager evidence. This
// composes the public reconciliation path, not installed/hardware qualification.
func TestLANLinkDeletePublicApplyCycle(t *testing.T) {
	for _, omitPool := range []bool{false, true} {
		name := "explicit-pool"
		if omitPool {
			name = "omitted-pool"
		}
		t.Run(name, func(t *testing.T) { publicLANLinkDeleteCycle(t, omitPool) })
	}
}

func publicLANLinkDeleteCycle(t *testing.T, omitPool bool) {
	ctx := context.Background()
	a, config, _, want, f := ownedLANDeleteFixture(t, true)
	if omitPool {
		want.pool = ""
		f.deletePool = ""
		updateLANIntentFixture(t, a, config, want)
	}
	r := &configReconciler{a: a}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	reconcile := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
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
	// Predecessor ownership fixture simulates lease settlement. Reset only
	// its fake leases before installing new fake grants.
	var leases coordination.LeaseList
	if err := a.statusReader.List(ctx, &leases); err != nil {
		t.Fatal(err)
	}
	for i := range leases.Items {
		if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
			t.Fatal(err)
		}
	}
	configurationGroupDispatchFixture(t, a, physical, false)
	a.configurationAdmission = nil
	installFabricGroupAuthorityFixture(t, a)
	reconcile()
	op := journalState(t, a).Operation.DeepCopy()
	if op.Phase != "Accepted" || f.posts != 2 {
		t.Fatal("public path did not submit once", op.Phase, f.posts)
	}
	f.taskEnd = op.SubmittedAt.Time
	for i := range f.ports {
		f.ports[i].IPv4, f.ports[i].Mask = "", ""
	}
	reconcile()
	if journalState(t, a).Operation.Phase != "Verifying" {
		t.Fatal("native completion did not enter verification")
	}
	// DeviceOperation timestamps serialize to seconds. Ensure their test evidence
	// is strictly after the native task rather than relying on clock precision.
	deadline := time.Now().Add(2 * time.Second)
	for !time.Now().UTC().Truncate(time.Second).After(f.taskEnd) {
		if time.Now().After(deadline) {
			t.Fatal("post-task clock boundary did not advance")
		}
		time.Sleep(10 * time.Millisecond)
	}
	reconcile()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members, err := contract.ReadAcknowledgedGroupReservations(ctx, a.statusReader, nc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	native, _, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, nc.Status.Configuration.Operation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var leaves ops.DeviceOperationList
	if err := a.statusReader.List(ctx, &leaves); err != nil {
		t.Fatal(err)
	}
	if len(leaves.Items) != 2 {
		t.Fatal("public verification did not create both device reads", len(leaves.Items))
	}
	for i := range leaves.Items {
		leaf := &leaves.Items[i]
		var member *ops.ControllerConfigurationReservation
		for _, candidate := range members {
			if candidate.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
				member = candidate
			}
		}
		if member == nil {
			t.Fatal("device read escaped admitted group")
		}
		endpoints := native.endpoints
		endpoint, peer := endpoints.primary, endpoints.secondary
		if member.Spec.InventoryID != endpoint.device.ID {
			endpoint, peer = peer, endpoint
		}
		at := time.Now().UTC().Truncate(time.Second)
		stampLANDeleteProofFixture(leaf, member, endpoint, peer, at, at, at)
		if err := a.statusWriter.Update(ctx, leaf); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	if state := journalState(t, a); state.Operation.Phase != "Verified" || len(state.ActionApplications) != 2 {
		t.Fatal("public worker proof did not publish the atomic receipt", state.Operation.Phase)
	}
	assertLANClaimsRetained(t, a, false)
	// Actual reservation workers release retained fake leases; explicit fake
	// maintenance cleanup is checked by the shared settlement publisher.
	settleLANDeleteWithWorkers(t, a)
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("unchanged owned link requested mutation authority")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Owned deletion does not depend on current pool configuration or capacity.
	f.poolChanged = true
	reads := f.usageReads
	for range 2 {
		reconcile()
		var published configapi.NetworkControllerConfig
		if err := a.statusReader.Get(ctx, req.NamespacedName, &published); err != nil {
			t.Fatal(err)
		}
		if published.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || published.Status.SectionStatus[0].ResourceCount != 1 {
			t.Fatal("settled owned link did not converge", published.Status.Phase)
		}
		if journalState(t, a).Operation != nil || f.posts != 2 || f.usageReads != reads {
			t.Fatal("settled reconciliation repeated LAN work")
		}
	}
}
