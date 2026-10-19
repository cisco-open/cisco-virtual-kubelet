// Copyright © 2026 Cisco Systems Inc.
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
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricDevicesBulkPinnedGroupsAndStableChunks(t *testing.T) {
	wants := []fabricDeviceIntent{}
	for i := 22; i >= 0; i-- {
		want := fabricDeviceIntent{fabricSite: "Global/Lab", state: "PROVISION", roles: []string{"EDGE_NODE"}}
		want.inventory.reference.Name = fmt.Sprintf("edge-%02d", i)
		if i%2 == 1 {
			want.fabricZone = "Global/Lab/Zone"
		}
		wants = append(wants, want)
	}
	before := append([]fabricDeviceIntent(nil), wants...)
	for i := range before {
		before[i].roles = append([]string(nil), wants[i].roles...)
	}
	batches, err := groupFabricDevicesBulk(wants)
	if err != nil || len(batches) != 5 {
		t.Fatal("root and zone cohorts", err)
	}
	for i, count := range []int{10, 10, 3, 10, 1} {
		if len(batches[i].wants) != count || (batches[i].zone == "") != (i < 3) {
			t.Fatal("incorrect stable chunk", i)
		}
		for _, want := range batches[i].wants {
			if want.fabricZone != batches[i].zone {
				t.Fatal("zone projection lost")
			}
		}
	}
	if batches[0].wants[1].inventory.reference.Name != "edge-01" || batches[3].wants[0].inventory.reference.Name != "edge-01" {
		t.Fatal("zoned member missing from parent or zone cohort")
	}
	if !reflect.DeepEqual(before, wants) {
		t.Fatal("source intents mutated")
	}
	for i, j := 0, len(wants)-1; i < j; i, j = i+1, j-1 {
		wants[i], wants[j] = wants[j], wants[i]
	}
	reordered, err := groupFabricDevicesBulk(wants)
	if err != nil || !reflect.DeepEqual(batches, reordered) {
		t.Fatal("source order changed static chunk identities")
	}
	zoneOnly := wants[1:2]
	zoneOnly[0].fabricZone = "Global/Lab/Zone"
	groups, err := groupFabricDevicesBulk(zoneOnly)
	if err != nil || len(groups) != 1 || groups[0].zone == "" {
		t.Fatal("zone-only source manufactured a parent cohort")
	}
}

func TestFabricDevicesBulkPublicManifestPreparation(t *testing.T) {
	for _, mode := range []string{"prepare", "bootstrap-parent", "mixed-zone", "noop", "missing-peer", "missing-runtime", "bad-bool", "wrong-section", "delete", "unsupported", "late-zone"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _, f := fabricDevicesBulkPreparationFixture(t, mode)
			if mode == "mixed-zone" {
				c, _, native := newFabricDevicesBulkFixture(t, "zone")
				for i := range native.inventory {
					native.inventory[i].ManagementIP = f.inventory[i].ManagementIP
				}
				a.client, f = c, native
			}
			if mode == "late-zone" {
				c, _, native := newFabricDevicesBulkFixture(t, "missing-zone")
				for i := range native.inventory {
					native.inventory[i].ManagementIP = f.inventory[i].ManagementIP
				}
				a.client, f = c, native
			}
			config.Annotations = map[string]string{fabricDevicesBulkAnnotation: "true"}
			if mode == "bad-bool" {
				config.Annotations[fabricDevicesBulkAnnotation] = "yes"
			}
			if mode == "wrong-section" {
				config.Spec.ManagedSections = []string{"inventory"}
			}
			if mode == "delete" {
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			if mode == "unsupported" || mode == "late-zone" || mode == "mixed-zone" || mode == "bootstrap-parent" {
				var root map[string]any
				if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &root); err != nil {
					t.Fatal(err)
				}
				cc := root["catalyst_center"].(map[string]any)
				if mode == "unsupported" {
					cc["fabric"] = map[string]any{"fabric_sites": []any{}}
				} else {
					devices := cc["inventory"].(map[string]any)["devices"].([]any)
					devices[1].(map[string]any)["fabric_zone"] = "Global/Lab/Building"
				}
				config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			key := ctrlclient.ObjectKeyFromObject(config)
			firstID := ""
			for range 3 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
				if mode == "prepare" || mode == "mixed-zone" || mode == "bootstrap-parent" {
					current := journalState(t, a)
					if current == nil || current.Operation == nil {
						t.Fatal("prepared journal disappeared")
					}
					if firstID == "" {
						firstID = current.Operation.ID
					}
					if current.Operation.ID != firstID {
						t.Fatal("restart replaced operation nonce")
					}
				}
			}
			state := journalState(t, a)
			if mode == "prepare" || mode == "mixed-zone" || mode == "bootstrap-parent" {
				if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.TaskID != "" || len(state.Operation.PhysicalGroup.Targets) != 3 {
					t.Fatal("public route did not retain original complete prepared journal")
				}
				if mode == "mixed-zone" && state.Operation.ParentID != "zone" {
					t.Fatal("matching parent was replayed before zone work")
				}
				if mode == "bootstrap-parent" && state.Operation.ParentID != "fabric" {
					t.Fatal("unready zone prepared before source-declared parents")
				}
			} else if state != nil && state.Operation != nil {
				t.Fatal("no-op or refused manifest prepared work")
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, key, &published); err != nil {
				t.Fatal(err)
			}
			reason := "GroupPreparationPending"
			if mode == "noop" {
				reason = "GroupConvergenceVerificationUnavailable"
			}
			if mode == "bad-bool" || mode == "wrong-section" {
				reason = "InvalidBulkOptions"
			}
			if mode == "delete" {
				reason = "UnsupportedConfiguration"
			}
			if mode == "unsupported" {
				reason = "UnsupportedIntent"
			}
			found := false
			for _, c := range published.Status.Conditions {
				if c.Reason == reason {
					found = true
				}
			}
			if !found || published.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || f.posts != 0 {
				t.Fatal("misreported or dispatched incomplete bulk lifecycle", mode)
			}
			var claims ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &claims); err != nil || len(claims.Items) != 0 {
				t.Fatal("preparation claimed devices", err)
			}
		})
	}
}
