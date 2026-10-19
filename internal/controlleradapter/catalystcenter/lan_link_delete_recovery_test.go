// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func dispatchedLANLinkDeleteFixture(t *testing.T, lost bool, omitPool ...bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, *lanPreflightFixture, []*ops.ControllerConfigurationReservation) {
	t.Helper()
	ctx := context.Background()
	a, config, source, want, f := ownedLANDeleteFixture(t, true)
	if len(omitPool) > 0 && omitPool[0] {
		want.pool = ""
		f.deletePool = ""
		source = updateLANIntentFixture(t, a, config, want)
	}
	if _, err := a.prepareLANLinkDelete(ctx, config, source, want.name); err != nil {
		t.Fatal(err)
	}
	var leases coordination.LeaseList
	if err := a.statusReader.List(ctx, &leases); err != nil {
		t.Fatal(err)
	}
	// Predecessor helper simulates actor settlement; remove its fixture leases.
	for i := range leases.Items {
		if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
			t.Fatal(err)
		}
	}
	physical := []configurationDispatchFixtureMember{}
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
	permit, _ := configurationGroupDispatchPermitFixture(t, a, physical)
	if lost {
		f.mode = "lost"
	}
	_, err := a.dispatchLANLinkDelete(ctx, config, source, want.name, permit)
	if (err == nil) == lost {
		t.Fatal("unexpected fixture dispatch", err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.taskEnd = nc.Status.Configuration.Operation.SubmittedAt.Time
	var rows ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &rows); err != nil {
		t.Fatal(err)
	}
	members := []*ops.ControllerConfigurationReservation{}
	for i := range rows.Items {
		members = append(members, rows.Items[i].DeepCopy())
	}
	return a, config, source, want, f, members
}

func TestLANLinkDeleteAcknowledgedVerification(t *testing.T) {
	for _, mode := range []string{"configured", "deconfigured", "no-neighbor", "lost", "partial", "stale-topology", "replaced-port", "replaced-peer", "unreachable", "active", "failed-task", "denied", "foreign-owner", "lost-predecessor", "changed-source", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, _, f, members := dispatchedLANLinkDeleteFixture(t, mode == "lost")
			for i := range f.ports {
				f.ports[i].IPv4, f.ports[i].Mask = "", ""
			}
			switch mode {
			case "configured":
				setLANLinkResult(f)
			case "no-neighbor":
				f.noLink = true
			case "partial":
				f.ports[1].IPv4 = "192.0.2.131"
			case "stale-topology":
				f.mode = "topology-address"
			case "replaced-port":
				f.ports[0].ID = "replaced-port"
			case "replaced-peer":
				f.inventory[1].Serial = "FOC99999999"
			case "unreachable":
				f.inventory[1].Reachability = "Unreachable"
			case "active":
				f.mode = "active"
			case "failed-task":
				f.taskMode = "failed"
			case "changed-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				config.Generation--
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "lost-predecessor":
				nc.Status.Configuration.ActionApplications = nil
			case "pruned-group":
				nc.Status.Configuration.Operation.PhysicalGroup = nil
			}
			if mode == "foreign-owner" || mode == "lost-predecessor" || mode == "pruned-group" {
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			}
			err = a.beginLANLinkDeleteVerification(ctx, config, source, members, authorize, time.Now())
			success := mode == "deconfigured" || mode == "no-neighbor"
			if (err == nil) != success {
				t.Fatal("unexpected verification eligibility", err)
			}
			state := journalState(t, a)
			phase := "Accepted"
			if mode == "lost" {
				phase = "Submitting"
			}
			if success {
				phase = "Verifying"
			}
			if state.Operation.Phase != phase || f.posts != 2 {
				t.Fatal("wrong transition or mutation replay", state.Operation.Phase, f.posts)
			}
			// Native success is never enough for a DELETE application or member release.
			if mode != "lost-predecessor" && len(state.ActionApplications) != 1 {
				t.Fatal("native-only deconfiguration published ownership")
			}
			var rows ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &rows); err != nil {
				t.Fatal(err)
			}
			for _, r := range rows.Items {
				if r.Status.Completion != nil || r.Status.ManagerSettledAt != nil || r.Status.Grant.Phase != "Held" {
					t.Fatal("verification released a member")
				}
			}
			restart := *a
			_ = restart.beginLANLinkDeleteVerification(ctx, config, source, members, authorize, time.Now())
			if f.posts != 2 {
				t.Fatal("recovery replayed DELETE")
			}
		})
	}
}

// The ADD-only compiler must not silently execute a DELETE source.
func TestLANLinkDeleteCannotUseAddOnlyCompiler(t *testing.T) {
	a, config, source, _, f := ownedLANDeleteFixture(t, true)
	if _, _, err := a.observeLANLinkAddModel(context.Background(), config, source); err == nil {
		t.Fatal("ADD compiler accepted DELETE")
	}
	if journalState(t, a).Operation != nil || f.posts != 1 {
		t.Fatal("public compiler prepared deletion")
	}
}
