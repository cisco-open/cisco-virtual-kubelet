// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func fabricAuthManagedFixture(t *testing.T, names ...string) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, *fabricAuthFixture, []configurationDispatchFixtureMember) {
	t.Helper()
	ctx := context.Background()
	a, config, _, members, _ := portChannelJournalFixture(t, "valid")
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "Closed Authentication"
	var bpdu any = false
	if len(names) != 0 {
		name = names[0]
		if name != "Closed Authentication" {
			bpdu = nil
		}
	}
	c, f, _ := newFabricAuthFixture(t, name)
	f.inventory = inventory
	f.zones = []fabricZone{{ID: "zone-id", SiteID: "building", Authentication: "No Authentication"}}
	f.memberships = map[string][]fabricDeviceMembership{
		"fabric-id": {{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric-id", Roles: []string{"EDGE_NODE"}}},
		"zone-id":   {{ID: "peer-member", NetworkDeviceID: "peer-id", FabricID: "zone-id", Roles: []string{"EXTENDED_NODE"}}},
	}
	a.client = c
	config.Spec.Source.Inline.Raw = fabricAuthModel(t, name, bpdu)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, f, members
}

func TestFabricAuthenticationManagedTargetsBracketWholeFootprint(t *testing.T) {
	for _, mode := range []string{"valid", "missing-managed-zone-member", "replaced-node", "missing-native", "duplicate-serial", "late-serial", "late-membership", "late-profile", "late-zone", "unknown-zone-hierarchy", "empty-members"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, f, members := fabricAuthManagedFixture(t)
			want, err := a.declaredFabricAuthentication(ctx, source, "Global/Lab")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-managed-zone-member":
				if err := a.statusWriter.Delete(ctx, members[1].device); err != nil {
					t.Fatal(err)
				}
			case "replaced-node":
				node := members[1].node
				if err := a.statusWriter.Delete(ctx, node); err != nil {
					t.Fatal(err)
				}
				node.ResourceVersion, node.UID = "", "replacement-node"
				if err := a.statusWriter.Create(ctx, node); err != nil {
					t.Fatal(err)
				}
			case "missing-native":
				f.inventory = f.inventory[:1]
			case "duplicate-serial":
				f.inventory[1].Serial = f.inventory[0].Serial
			case "unknown-zone-hierarchy":
				f.zones[0].SiteID = "absent-hierarchy-id"
			case "empty-members":
				f.memberships = nil
			}
			f.onInventoryRead = func() {
				if f.inventoryReads != 1 {
					return
				}
				switch mode {
				case "late-serial":
					f.inventory[1].Serial = "FOC99999999"
				case "late-membership":
					f.memberships["zone-id"] = []fabricDeviceMembership{}
				case "late-profile":
					v := false
					f.profile.BPDU = &v
				case "late-zone":
					f.zones[0].ID = "replacement-zone-id"
				}
			}
			state, targets, err := a.client.fabricAuthenticationTargets(ctx, a.statusReader, config.Namespace, want)
			if mode != "valid" {
				if err == nil || state != nil || targets != nil || f.writes != 0 {
					t.Fatal("partial/stale root and zone footprint accepted", err)
				}
				return
			}
			if err != nil || state == nil || len(state.physical) != 2 || len(targets) != 2 || f.inventoryReads != 2 {
				t.Fatal("root and descendant zone identities not bound", err, targets)
			}
			write, plannedTargets, err := a.client.planManagedFabricAuthentication(ctx, a.statusReader, config.Namespace, want)
			if err != nil || write == nil || !reflect.DeepEqual(targets, plannedTargets) {
				t.Fatal("managed preparation failed", err)
			}
			parts, err := parseFabricAuthManagedResource(write.resource)
			if err != nil || len(parts) != 5 || parts[2] == parts[3] || parts[4] != state.digest {
				t.Fatal("before/result/physical binding missing", err)
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) || f.writes != 0 {
				t.Fatal("preparation granted native submission", err)
			}
		})
	}
}

func TestFabricAuthenticationDurablePreparationAndRevalidation(t *testing.T) {
	for _, mode := range []string{"valid", "source", "owner", "node", "baseline", "member", "zone", "profile-id", "already-converged", "undeclared", "lost-journal-ack", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, f, members := fabricAuthManagedFixture(t)
			path := "Global/Lab"
			if mode == "undeclared" {
				path = "Global/Other"
			}
			if mode == "already-converged" {
				want, err := a.declaredFabricAuthentication(ctx, source, path)
				if err != nil {
					t.Fatal(err)
				}
				f.profile.Hosts, f.profile.BPDU = want.profile.Hosts, want.profile.BPDU
			}
			if mode == "lost-journal-ack" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned-group" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			op, err := a.prepareFabricAuthentication(ctx, config, source, path)
			state := journalState(t, a)
			if mode == "undeclared" || mode == "already-converged" {
				if op != nil || state != nil && state.Operation != nil || mode == "undeclared" && err == nil || mode == "already-converged" && err != nil {
					t.Fatal("undeclared/no-op created a journal", err)
				}
				return
			}
			if mode == "lost-journal-ack" || mode == "pruned-group" {
				if err == nil || state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" || f.writes != 0 {
					t.Fatal("uncertain preparation lost evidence", err)
				}
				return
			}
			if err != nil || op == nil || op.Phase != "Prepared" || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != 2 || op.TargetID != "profile-id" || op.ParentID != "fabric-id" || f.writes != 0 {
				t.Fatal("durable physical preparation failed", err)
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("preparation claimed devices", err)
			}
			before := op.DeepCopy()
			switch mode {
			case "source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "owner":
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Owners = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "node":
				if err := a.statusWriter.Delete(ctx, members[1].node); err != nil {
					t.Fatal(err)
				}
			case "baseline":
				v := 20
				f.profile.Timeout = &v
			case "member":
				f.memberships["zone-id"] = []fabricDeviceMembership{}
			case "zone":
				f.zones[0].Authentication = "Open Authentication"
			case "profile-id":
				f.profile.ID = "replacement-profile"
			}
			err = a.revalidateFabricAuthenticationOperation(ctx, config, source, "Global/Lab", op, "Prepared")
			if (err == nil) != (mode == "valid") || !reflect.DeepEqual(before, journalState(t, a).Operation) || f.writes != 0 {
				t.Fatal("changed scope/source/journal accepted during revalidation", mode, err)
			}
			if mode == "valid" {
				if _, err := a.prepareFabricAuthentication(ctx, config, source, path); err == nil {
					t.Fatal("active prepared journal replaced")
				}
				if err := a.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationDeviceAdmission) {
					t.Fatal("prepared physical group completed as metadata", err)
				}
			}
		})
	}
}

func TestFabricAuthenticationManagedResourceValidation(t *testing.T) {
	valid := fabricAuthManagedPrefix + strings.TrimSuffix(strings.Repeat(strings.Repeat("a", 64)+":", 5), ":")
	for _, resource := range []string{valid, strings.ToUpper(valid), strings.Replace(valid, "aaaa", "xxxa", 1), valid + ":extra", strings.Replace(valid, ":v2:", ":v1:", 1)} {
		_, err := parseFabricAuthManagedResource(resource)
		if (err == nil) != (resource == valid) || validConfigurationGroupAction("fabric", resource, "Update") != (resource == valid) {
			t.Fatal("managed operation resource validation mismatch")
		}
	}
	if validConfigurationGroupAction("fabric", valid, "Create") || validConfigurationGroupAction("authentication_templates", valid, "Update") {
		t.Fatal("wrong action or section accepted")
	}
}

func TestFabricAuthenticationPreparedDescriptionIsPreservedNotBound(t *testing.T) {
	ctx := context.Background()
	a, config, source, f, _ := fabricAuthManagedFixture(t, "Low Impact")
	op, err := a.prepareFabricAuthentication(ctx, config, source, "Global/Lab")
	if err != nil || op == nil {
		t.Fatal("Low Impact preparation failed", err)
	}
	description := "independent native description change"
	f.profile.ACL.Description = &description
	if err := a.revalidateFabricAuthenticationOperation(ctx, config, source, "Global/Lab", op, "Prepared"); err != nil {
		t.Fatal("upstream-unmanaged description incorrectly bound into operation", err)
	}
	if f.writes != 0 {
		t.Fatal("description observation mutated native state")
	}
}

func TestFabricAuthenticationFootprintUsesNearestFabric(t *testing.T) {
	ctx := context.Background()
	a, config, source, f, _ := fabricAuthManagedFixture(t)
	want, err := a.declaredFabricAuthentication(ctx, source, "Global/Lab")
	if err != nil {
		t.Fatal(err)
	}
	floor := f.sites[2]
	floor.ID, floor.ParentID, floor.Name, floor.NameHierarchy = "floor-id", "building", "Floor", "Global/Lab/Building/Floor"
	floor.Type = "floor"
	f.sites = append(f.sites, floor)
	pubsub := false
	f.fabrics = append(f.fabrics, fabricSite{ID: "independent-fabric", SiteID: "building", Authentication: "Closed Authentication", PubSub: &pubsub})
	f.zones[0].SiteID = "floor-id"
	state, targets, err := a.client.fabricAuthenticationTargets(ctx, a.statusReader, config.Namespace, want)
	if err != nil || state == nil || len(targets) != 1 || targets[0].InventoryID != "edge-id" {
		t.Fatal("independent nested fabric's zone incorrectly included", err, targets)
	}
}
