// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func lanLinkAddJournalFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, *lanPreflightFixture) {
	t.Helper()
	ctx := context.Background()
	c, want, f, api := newLANPreflightFixture(t, "idle")
	a, config := journalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"lan_automation"}
	row := lanLinkFixture()
	row["primary_device_management_ip_address"] = want.primaryIP
	row["secondary_device_management_ip_address"] = want.secondaryIP
	row["ip_pool_name"] = want.pool
	raw := lanLinkModel(t, row)
	config.Spec.Source.Inline.Raw = raw
	nc.ResourceVersion = ""
	config.ResourceVersion = ""
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := api.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.statusReader, a.statusWriter, a.client = api, api, c
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return a, config, &configSourceSnapshot{raw: raw}, want, f
}

func TestLANLinkAddPreparationUsesReservedGroupJournal(t *testing.T) {
	for _, mode := range []string{"addressed-port", "late-native-prerequisite", "mixed-delete", "prepare", "unsupported-delete", "invalid-source", "unknown-link", "no-runtime", "prune-delete", "unresolved-model"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := lanLinkAddJournalFixture(t)
			switch mode {
			case "addressed-port":
				f.ports[0].IPv4 = "192.0.2.130"
				f.ports[0].Mask = "255.255.255.254"
			case "late-native-prerequisite", "mixed-delete":
				var document map[string]any
				if err := json.Unmarshal(source.raw, &document); err != nil {
					t.Fatal(err)
				}
				section := document["catalyst_center"].(map[string]any)["lan_automation"].(map[string]any)
				later := lanLinkFixture()
				later["name"] = "zz-late"
				later["primary_device_management_ip_address"] = want.primaryIP
				later["secondary_device_management_ip_address"] = want.secondaryIP
				later["primary_device_interface_name"] = "TenGigabitEthernet1/0/3"
				later["secondary_device_interface_name"] = "TenGigabitEthernet1/0/4"
				if mode == "mixed-delete" {
					later["action"] = "DELETE_LINK"
				}
				section["links"] = append(section["links"].([]any), later)
				raw, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				source.raw = raw
				config.Spec.Source.Inline.Raw = raw
			case "unsupported-delete":
				source.raw = []byte(strings.Replace(string(source.raw), `"ADD_LINK"`, `"DELETE_LINK"`, 1))
				config.Spec.Source.Inline.Raw = source.raw
			case "invalid-source":
				source.raw = []byte(`{"catalyst_center":{"lan_automation":{"links":[],"devices":[]}}}`)
				config.Spec.Source.Inline.Raw = source.raw
			case "unknown-link":
				want.name = "unknown"
			case "no-runtime":
				a.configurationAdmission = nil
			case "prune-delete":
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "unresolved-model":
				config.Spec.ModelSource.Resolved = false
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareLANLinkAdd(ctx, config, source, want.name)
			if (err == nil) != (mode == "prepare") {
				t.Fatal("unexpected preparation outcome", err)
			}
			state := journalState(t, a)
			if mode == "prepare" {
				if op.Phase != "Prepared" || op.Section != "lan_automation" || op.PhysicalGroup == nil || len(op.PhysicalGroup.Targets) != 2 || !validLANLinkAddResource(op.Resource, "Update") {
					t.Fatal("group preparation identity incomplete")
				}
				if _, err := a.prepareConfiguration(ctx, config, source, "lan_automation", op.Resource, "Update", configurationResourceIdentity{target: op.TargetID, parent: op.ParentID}); err == nil {
					t.Fatal("group family fell through logical preparation")
				}
			} else if state != nil && state.Operation != nil {
				t.Fatal("invalid model prepared a partial action")
			}
			if f.posts != 0 {
				t.Fatal("preparation mutated native state")
			}
		})
	}
}

func TestLANLinkAddDispatchUsesSharedOneUsePermit(t *testing.T) {
	for _, mode := range []string{"accepted", "missing-permit", "denied", "lost", "revoked-after-acceptance", "changed-source", "exhausted", "active", "replaced-peer", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := lanLinkAddJournalFixture(t)
			prepared, err := a.prepareLANLinkAdd(ctx, config, source, want.name)
			if err != nil {
				t.Fatal(err)
			}
			members := []configurationDispatchFixtureMember{}
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
			permit, revoke := configurationGroupDispatchPermitFixture(t, a, members)
			switch mode {
			case "missing-permit":
				permit = nil
			case "denied":
				revoke()
			case "lost":
				f.mode = "lost"
			case "revoked-after-acceptance":
				f.afterPost = revoke
			case "changed-source":
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			case "exhausted", "active":
				f.mode = mode
			case "replaced-peer":
				f.inventory[1].Serial = "FOC99999999"
			case "pruned-group":
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				current.Status.Configuration.Operation.PhysicalGroup = nil
				if err := a.statusWriter.Status().Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			task, err := a.dispatchLANLinkAdd(ctx, config, source, want.name, permit)
			if (err == nil) != (mode == "accepted") {
				t.Fatal("unexpected dispatch outcome", err)
			}
			op := journalState(t, a).Operation
			expectedPosts := 0
			if mode == "accepted" || mode == "lost" || mode == "revoked-after-acceptance" {
				expectedPosts = 1
			}
			if f.posts != expectedPosts {
				t.Fatal("unexpected native submission count", f.posts)
			}
			if mode == "accepted" {
				if op.Phase != "Accepted" || op.TaskID != "lan-task" || task.ID != "lan-task" {
					t.Fatal("native acknowledgement not retained")
				}
			} else if expectedPosts == 1 {
				if op.Phase != "Submitting" || op.TaskID != "" || op.SubmittedAt == nil {
					t.Fatal("ambiguous native acceptance lost its fence")
				}
			} else if op.Phase != "Prepared" || op.SubmittedAt != nil {
				t.Fatal("refused dispatch advanced")
			}
			if op.ID != prepared.ID {
				t.Fatal("retained operation replaced")
			}
			restarted := *a
			if _, err := restarted.dispatchLANLinkAdd(ctx, config, source, want.name, permit); err == nil || f.posts != expectedPosts {
				t.Fatal("restart replayed native update")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if len(reservations.Items) != 2 {
				t.Fatal("endpoint reservation missing")
			}
			for _, member := range reservations.Items {
				if member.Status.Claim == nil || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Grant == nil || member.Status.Grant.Phase == "Released" {
					t.Fatal("dispatch completed/released an endpoint")
				}
			}
		})
	}
}

func TestLANLinkAddGroupFamilyMalformed(t *testing.T) {
	for _, resource := range []string{"lan-link-add:v2:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), "lan-link-add:v1:a:b", "lan-link-add:v1:" + strings.Repeat("A", 64) + ":" + strings.Repeat("b", 64)} {
		if !isReservedConfigurationGroupResource("lan_automation", resource) || validConfigurationGroupAction("lan_automation", resource, "Update") {
			t.Fatal("malformed group identity admitted or lost reservation")
		}
	}
}
