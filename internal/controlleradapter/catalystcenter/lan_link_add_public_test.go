// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLANLinkPublicApplyPreparation(t *testing.T) {
	for _, mode := range []string{"prepare", "no-grants", "missing-runtime", "mixed-delete", "invalid-later", "addressed", "changed-generation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := lanLinkAddJournalFixture(t)
			switch mode {
			case "missing-runtime":
				a.configurationAdmission = nil
			case "addressed":
				setLANLinkResult(f)
			case "mixed-delete", "invalid-later":
				var model map[string]any
				if err := json.Unmarshal(source.raw, &model); err != nil {
					t.Fatal(err)
				}
				section := model["catalyst_center"].(map[string]any)["lan_automation"].(map[string]any)
				later := lanLinkFixture()
				later["name"] = "zz-late"
				later["primary_device_management_ip_address"], later["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
				later["primary_device_interface_name"], later["secondary_device_interface_name"] = "TenGigabitEthernet1/0/3", "TenGigabitEthernet1/0/4"
				if mode == "mixed-delete" {
					later["action"] = "DELETE_LINK"
				}
				section["links"] = append(section["links"].([]any), later)
				raw, err := json.Marshal(model)
				if err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = raw
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			r := &configReconciler{a: a}
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			prepared := mode == "prepare" || mode == "no-grants" || mode == "changed-generation"
			if prepared {
				if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" || !strings.HasPrefix(state.Operation.Resource, lanLinkAddRecoveryPrefix) || len(state.Operation.PhysicalGroup.Targets) != 2 {
					t.Fatal("public Apply did not prepare the original group")
				}
			} else if state != nil && state.Operation != nil {
				t.Fatal("partial or unowned model created a journal")
			}
			if mode == "changed-generation" {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "no-grants" || mode == "changed-generation" {
				if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if journalState(t, a).Operation.Phase != "Prepared" {
					t.Fatal("unadmitted or changed work advanced")
				}
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("new/unverified link reported InSync")
			}
			if f.posts != 0 {
				t.Fatal("preparation bypassed all-member dispatch")
			}
		})
	}
}

// Explicit fake deployment/worker/lease fixtures exercise the production claim
// path. They do not qualify installed actors or real LAN mutation.
func TestLANLinkPublicApplyDispatch(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "lost", "active"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _, f := lanLinkAddJournalFixture(t)
			r := &configReconciler{a: a}
			req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			var members []configurationDispatchFixtureMember
			for _, name := range []string{"edge0", "edge1"} {
				var d cvk.CiscoDevice
				var n core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: name}, &d); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
					t.Fatal(err)
				}
				members = append(members, configurationDispatchFixtureMember{device: &d, node: &n})
			}
			_, revoke := configurationGroupDispatchFixture(t, a, members, false)
			a.configurationAdmission = nil
			installFabricGroupAuthorityFixture(t, a)
			if mode == "accepted" || mode == "lost" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var list ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(ctx, &list); err != nil {
					t.Fatal(err)
				}
				rows := make([]*ops.ControllerConfigurationReservation, len(list.Items))
				for i := range list.Items {
					rows[i] = &list.Items[i]
				}
				if err := contract.ReadGroupAuthority(ctx, a.statusReader, nc, rows, ops.ConfigurationReservationReference{Name: a.workerPodName, UID: a.workerPodUID}, "leases", time.Now()); err != nil {
					t.Fatal("fixture group authority", err)
				}
			}
			if mode == "denied" {
				revoke()
			}
			if mode == "accepted" {
				f.taskMode = "running"
			}
			if mode == "lost" || mode == "active" {
				f.mode = mode
			}
			for range 3 {
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			state := journalState(t, a)
			expected, posts := "Prepared", 0
			if mode == "accepted" {
				expected, posts = "Accepted", 1
			}
			if mode == "lost" {
				expected, posts = "Submitting", 1
			}
			if state.Operation.Phase != expected || f.posts != posts || len(state.ActionApplications) != 0 {
				t.Fatal("unexpected public dispatch/replay outcome", state.Operation.Phase, f.posts)
			}
			if mode == "accepted" || mode == "lost" {
				assertLANClaimsRetained(t, a, false)
			}
		})
	}
}

func TestLANLinkPublicApplyCycle(t *testing.T) {
	ctx := context.Background()
	a, config, _, want, f := lanLinkAddJournalFixture(t)
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
	configurationGroupDispatchFixture(t, a, physical, false)
	a.configurationAdmission = nil
	installFabricGroupAuthorityFixture(t, a)
	reconcile()
	op := journalState(t, a).Operation.DeepCopy()
	if op.Phase != "Accepted" || f.posts != 1 {
		t.Fatal("public path did not submit once", op.Phase, f.posts)
	}
	f.taskEnd = op.SubmittedAt.Time
	setLANLinkResult(f)
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
	native, _, err := a.client.lanLinkCompletionTargets(ctx, a.statusReader, config.Namespace, want, nc.Status.Configuration.Operation, time.Now())
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
		endpoints := native.dependencies.adjacency.endpoints
		endpoint, peer := endpoints.primary, endpoints.secondary
		if member.Spec.InventoryID != endpoint.device.ID {
			endpoint, peer = peer, endpoint
		}
		at := time.Now().UTC().Truncate(time.Second)
		stampLANProofFixture(leaf, member, endpoint, peer, at, at, at)
		if err := a.statusWriter.Update(ctx, leaf); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	if state := journalState(t, a); state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 {
		t.Fatal("public worker proof did not publish the atomic receipt", state.Operation.Phase)
	}
	assertLANClaimsRetained(t, a, false)
	// Reuse the established fake worker/manager settlement actor. It proves
	// denial, partial/lost completion, release and all-member settlement fences.
	a.configurationAdmission = nil
	testFabricHandoffCompletion(t, a, config, true)
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("unchanged owned link requested mutation authority")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		reconcile()
		var published configapi.NetworkControllerConfig
		if err := a.statusReader.Get(ctx, req.NamespacedName, &published); err != nil {
			t.Fatal(err)
		}
		if published.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || published.Status.SectionStatus[0].ResourceCount != 1 {
			t.Fatal("settled owned link did not converge", published.Status.Phase)
		}
		if journalState(t, a).Operation != nil || f.posts != 1 {
			t.Fatal("settled reconciliation repeated LAN work")
		}
	}
}
