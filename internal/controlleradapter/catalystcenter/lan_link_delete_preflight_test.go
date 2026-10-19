// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Derive ownership through the existing guarded ADD receipt writer and settle
// its explicit fake actors. Deletion cannot use a fabricated native-only owner.
func ownedLANDeleteFixture(t *testing.T, retire bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, *lanPreflightFixture) {
	t.Helper()
	ctx := context.Background()
	a, config, source, want, f, members, _ := dispatchedLANLinkAddFixture(t, false)
	setLANLinkResult(f)
	authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
	if err := a.beginLANLinkAddVerification(ctx, config, source, members, authorize, time.Now()); err != nil {
		t.Fatal(err)
	}
	proof := func(context.Context, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time) error {
		return nil
	}
	if err := a.recordLANLinkAddApplication(ctx, config, journalState(t, a).Operation.ID, members, authorize, proof, time.Now()); err != nil {
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
				t.Fatal("retire original ADD member", err)
			}
		}
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	want.action = "DELETE_LINK"
	row := lanLinkFixture()
	row["action"], row["ip_pool_name"], row["primary_device_management_ip_address"], row["secondary_device_management_ip_address"] = "DELETE_LINK", want.pool, want.primaryIP, want.secondaryIP
	config.Spec.Source.Inline.Raw = lanLinkModel(t, row)
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.inventory[0].Reachability, f.inventory[1].Reachability = "Reachable", "Reachable"
	f.mutationFeature = "LINK_DELETE"
	f.deletePool = want.pool
	return a, config, source, want, f
}

func TestLANLinkDeleteOwnedPreflight(t *testing.T) {
	for _, mode := range []string{"owned", "optional-pool", "exhausted", "down-link", "no-neighbor", "unreachable", "retained-members", "unowned", "foreign-owner", "changed-config", "changed-native", "active", "wrong-pool", "undeclared-target", "changed-node"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := ownedLANDeleteFixture(t, mode != "retained-members")
			switch mode {
			case "optional-pool":
				want.pool = ""
				f.deletePool = ""
				row := lanLinkFixture()
				row["action"] = "DELETE_LINK"
				delete(row, "ip_pool_name")
				row["primary_device_management_ip_address"], row["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
				config.Spec.Source.Inline.Raw = lanLinkModel(t, row)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				var err error
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
			case "exhausted":
				f.mode = "exhausted"
			case "down-link":
				f.ports[0].Status = "Down"
				f.ports[1].AdminStatus = "DOWN"
			case "no-neighbor":
				f.noLink = true
			case "unreachable":
				f.inventory[0].Reachability = "Unreachable"
			case "unowned", "foreign-owner":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "unowned" {
					nc.Status.Configuration.ActionApplications = nil
				} else {
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "changed-config":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				// A stale caller snapshot must not borrow the newer source.
				config.Generation--
			case "changed-native":
				f.ports[0].IPv4 = "192.0.2.132"
			case "active":
				f.mode = "active"
			case "wrong-pool":
				want.pool = "other-pool"
				row := lanLinkFixture()
				row["action"], row["ip_pool_name"] = "DELETE_LINK", want.pool
				row["primary_device_management_ip_address"], row["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
				config.Spec.Source.Inline.Raw = lanLinkModel(t, row)
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				var err error
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
			case "undeclared-target":
				want.name = "unrequested-link"
			case "changed-node":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.ActionApplications[0].Operation.PhysicalGroup.Targets[0].Node.UID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, targets, err := a.observeLANLinkDelete(ctx, config, source, want, time.Now())
			success := mode == "owned" || mode == "optional-pool" || mode == "exhausted" || mode == "down-link" || mode == "no-neighbor"
			if (err == nil) != success {
				t.Fatal("unexpected owned deletion eligibility", err)
			}
			if success && (snapshot.previous == nil || snapshot.pool != "access" || len(targets) != 2) {
				t.Fatal("missing predecessor/pool/complete footprint")
			}
			if f.posts != 1 || journalState(t, a).Operation != nil {
				t.Fatal("observation mutated or prepared deletion")
			}
		})
	}
}

func TestLANLinkDeleteFrozenMutation(t *testing.T) {
	for _, mode := range []string{"submit", "optional-pool", "denied", "changed-port", "missing-runtime", "lost"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := ownedLANDeleteFixture(t, true)
			if mode == "optional-pool" {
				want.pool = ""
				f.deletePool = ""
				row := lanLinkFixture()
				row["action"] = "DELETE_LINK"
				delete(row, "ip_pool_name")
				row["primary_device_management_ip_address"], row["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
				config.Spec.Source.Inline.Raw = lanLinkModel(t, row)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				var err error
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
			}
			guardCalls := 0
			guard := func(context.Context) error {
				guardCalls++
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			if mode == "missing-runtime" {
				a.configurationAdmission = nil
			}
			write, err := a.planManagedLANLinkDelete(ctx, config, source, want, guard)
			if mode == "missing-runtime" {
				if err == nil {
					t.Fatal("missing runtime planned mutation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseLANLinkDeleteResource(write.resource, write.action); err != nil {
				t.Fatal("missing typed delete scope", err)
			}
			if mode == "changed-port" {
				f.ports[0].ID = "replacement-port"
			}
			if mode == "lost" {
				f.mode = "lost"
			}
			_, err = write.submit(ctx)
			success := mode == "submit" || mode == "optional-pool"
			if (err == nil) != success {
				t.Fatal("unexpected frozen mutation", err)
			}
			expected := 1
			if success || mode == "lost" {
				expected = 2
			}
			if f.posts != expected {
				t.Fatal("native deletion count", f.posts)
			}
			if _, err := write.submit(ctx); err == nil || f.posts != expected {
				t.Fatal("one-use sender replayed")
			}
			if mode == "changed-port" && guardCalls != 0 {
				t.Fatal("authority requested before native validation")
			}
		})
	}
}

func TestLANLinkDeleteRejectsPrunedJournal(t *testing.T) {
	ctx := context.Background()
	a, config, source, want, f := ownedLANDeleteFixture(t, true)
	write, err := a.planManagedLANLinkDelete(ctx, config, source, want, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{write.resource, lanLinkDeletePrefix + "pruned"} {
		if !isReservedConfigurationGroupResource("lan_automation", resource) {
			t.Fatal("lost reserved family")
		}
		if _, err := a.prepareConfiguration(ctx, config, source, "lan_automation", resource, "Update", configurationResourceIdentity{target: write.identity.target, parent: write.identity.parent}); err == nil {
			t.Fatal("missing group admitted")
		}
	}
	if validConfigurationGroupAction("lan_automation", lanLinkDeletePrefix+"pruned", "Update") {
		t.Fatal("malformed identity admitted")
	}
	if journalState(t, a).Operation != nil || f.posts != 1 {
		t.Fatal("pruned deletion prepared or dispatched")
	}
}
