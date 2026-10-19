// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Ownership is produced by guarded receipt writers, not manufactured status.
// Explicit fake proof/manager actors isolate current-state ownership checks;
// the public cycle tests independently exercise real worker evidence/release.
func ownedLANDeletedFixture(t *testing.T, retire bool, omitPool ...bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, *lanPreflightFixture) {
	t.Helper()
	ctx := context.Background()
	a, config, source, want, f, members := dispatchedLANLinkDeleteFixture(t, false, omitPool...)
	for i := range f.ports {
		f.ports[i].IPv4, f.ports[i].Mask = "", ""
	}
	authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
	if err := a.beginLANLinkDeleteVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Whole-second receipt chronology must be strict, including fast fixtures.
	previous := journalState(t, a).ActionApplications[0].VerifiedAt.Time
	deadline := time.Now().Add(2 * time.Second)
	for !time.Now().UTC().Truncate(time.Second).After(previous) {
		if time.Now().After(deadline) {
			t.Fatal("receipt clock boundary unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	proof := func(context.Context, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time) error {
		return nil
	}
	if err := a.recordLANLinkDeleteApplication(ctx, config, journalState(t, a).Operation.ID, members, authorize, proof, time.Now()); err != nil {
		t.Fatal(err)
	}
	a.configurationAdmission = nil
	testFabricHandoffCompletion(t, a, config, true)
	if retire {
		var rows ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &rows); err != nil {
			t.Fatal(err)
		}
		for i := range rows.Items {
			if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, &rows.Items[i], func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return a, config, source, want, f
}

func updateLANIntentFixture(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, want lanLinkIntent) *configSourceSnapshot {
	t.Helper()
	ctx := context.Background()
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	row := lanLinkFixture()
	row["name"], row["action"] = want.name, want.action
	row["primary_device_management_ip_address"], row["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
	row["primary_device_interface_name"], row["secondary_device_interface_name"] = want.primaryInterface, want.secondaryInterface
	if want.pool == "" {
		delete(row, "ip_pool_name")
	} else {
		row["ip_pool_name"] = want.pool
	}
	config.Spec.Source.Inline.Raw = lanLinkModel(t, row)
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestLANLinkDeletedStateOwnership(t *testing.T) {
	for _, mode := range []string{"owned", "down-link", "absent-neighbor", "retained-settled-members", "unowned", "missing-interface", "partial-address", "replaced-port", "replaced-node", "changed-source", "foreign-owner", "newer-effect", "newer-reversed-effect", "same-operation-effect"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, _, f := ownedLANDeletedFixture(t, mode != "retained-settled-members")
			switch mode {
			case "down-link":
				f.ports[0].Status = "Down"
			case "absent-neighbor":
				f.noLink = true
			case "partial-address":
				f.ports[0].IPv4 = "192.0.2.130"
			case "missing-interface":
				f.ports[0].PortName = "TenGigabitEthernet1/0/20"
			case "replaced-port":
				f.ports[0].ID = "replaced-port"
			case "changed-source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				config.Generation--
			case "replaced-node":
				var node core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "edge1"}, &node); err != nil {
					t.Fatal(err)
				}
				if err := a.statusWriter.Delete(ctx, &node); err != nil {
					t.Fatal(err)
				}
				node.ResourceVersion = ""
				node.UID = "replacement"
				if err := a.statusWriter.Create(ctx, &node); err != nil {
					t.Fatal(err)
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unowned":
				nc.Status.Configuration.ActionApplications = nil
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "newer-effect", "newer-reversed-effect", "same-operation-effect":
				// Explicit malformed/ambiguous newer same-pair history must hold. This is
				// not a synthetic qualified ADD and is never used as ownership evidence.
				receipt := nc.Status.Configuration.ActionApplications[0].DeepCopy()
				receipt.Operation.ID = "99999999-9999-4999-8999-999999999999"
				receipt.VerifiedAt = nc.Status.Configuration.ActionApplications[1].VerifiedAt.DeepCopy()
				if mode == "same-operation-effect" {
					receipt.Operation.ID = nc.Status.Configuration.ActionApplications[1].Operation.ID
				}
				if mode == "newer-reversed-effect" {
					receipt.Operation.TargetID, receipt.Operation.ParentID = receipt.Operation.ParentID, receipt.Operation.TargetID
				}
				receipt.Resource = "lan-link:" + lanLinkDigest("another-pool") + ":" + lanLinkDigest("result")
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, *receipt)
			}
			if mode == "unowned" || mode == "foreign-owner" || mode == "newer-effect" || mode == "newer-reversed-effect" || mode == "same-operation-effect" {
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			observed, _, err := a.observeLANLinkDeleteModel(ctx, config, source)
			success := mode == "owned" || mode == "down-link" || mode == "absent-neighbor" || mode == "retained-settled-members"
			if (err == nil) != success {
				t.Fatal("unexpected deleted ownership", err)
			}
			if success && !observed["seed-peer"].owned {
				t.Fatal("deleted receipt not recognized")
			}
			if journalState(t, a).Operation != nil || f.posts != 2 {
				t.Fatal("deleted observation mutated state")
			}
		})
	}
}

func TestLANLinkReAddRequiresRetiredDelete(t *testing.T) {
	for _, mode := range []string{"retired", "retained-members", "changed-native", "foreign-owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, want, f := ownedLANDeletedFixture(t, mode != "retained-members")
			want.action = "ADD_LINK"
			source := updateLANIntentFixture(t, a, config, want)
			if mode == "changed-native" {
				f.ports[0].Mask = "255.255.255.254"
			}
			if mode == "foreign-owner" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			op, err := a.prepareLANLinkAdd(ctx, config, source, want.name)
			if (err == nil) != (mode == "retired") {
				t.Fatal("unexpected re-add preparation", err)
			}
			if mode == "retired" && (op.Phase != "Prepared" || !validLANLinkAddResource(op.Resource, op.Action)) {
				t.Fatal("fresh ADD identity missing")
			}
			if mode != "retired" && journalState(t, a).Operation != nil {
				t.Fatal("unverified re-add prepared")
			}
			if f.posts != 2 {
				t.Fatal("re-add preparation mutated native state")
			}
		})
	}
}
