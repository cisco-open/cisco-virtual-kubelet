// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func retainExtranetCreationFixture(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, want fabricExtranetIntent) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, targets, err := a.client.extranetManagedTargets(ctx, a.statusReader, config.Namespace, want)
	if err != nil {
		t.Fatal(err)
	}
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "fabric", Resource: "extranet-create:" + strings.Repeat("b", 64), Action: "Create", Phase: "Prepared"}
	op, err = contract.WithGroupIdentity(op, targets)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	op.Phase = "Verified"
	op.TaskID = "created-task"
	op.SubmittedAt = &metav1.Time{Time: now.Add(-time.Hour)}
	prefix, _ := extranetReceiptPrefix(want.policy.Name)
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: prefix + "policy", Operation: *op, VerifiedAt: &metav1.Time{Time: now.Add(-time.Minute)}}}}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
}

func TestExtranetDurablePreparation(t *testing.T) {
	for _, mode := range []string{"create", "update", "remote-vn", "configmap", "converged", "undeclared", "lost-ack", "pruned", "changed-source", "changed-node", "changed-native", "changed-policy-baseline", "wrong-phase", "wrong-group", "submitting", "later-unready", "later-unowned", "prerequisite-ready", "prerequisite-pending", "unknown-collection", "unknown-section", "unscoped"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nativeMode := "valid"
			if mode == "update" {
				nativeMode = "expansion"
			}
			if mode == "changed-policy-baseline" {
				nativeMode = "expansion-policy-drift"
			}
			if mode == "later-unowned" {
				nativeMode = "expansion"
			}
			if mode == "converged" {
				nativeMode = "existing"
			}
			if mode == "remote-vn" {
				nativeMode = "remote-vn"
			}
			a, config, want, writes, late := extranetManagedFixture(t, nativeMode)
			config.Spec.Source.Inline.Raw = []byte(extranetModel)
			var root map[string]any
			if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &root); err != nil {
				t.Fatal(err)
			}
			cc := root["catalyst_center"].(map[string]any)
			fabric := cc["fabric"].(map[string]any)
			selected := "Shared"
			switch mode {
			case "later-unready":
				rows := fabric["extranet_policies"].([]any)
				rows = append(rows, map[string]any{"name": "ZZ", "provider_virtual_network": "Services", "subscriber_virtual_networks": []string{"Corp"}, "fabric_sites": []string{"Global/Missing"}})
				fabric["extranet_policies"] = rows
			case "later-unowned":
				first := map[string]any{"name": "First", "provider_virtual_network": "Services", "subscriber_virtual_networks": []string{"Corp", "Guest"}, "fabric_sites": []string{"Global/Lab"}}
				fabric["extranet_policies"] = append([]any{first}, fabric["extranet_policies"].([]any)...)
				selected = "First"
			case "prerequisite-ready":
				fabric["l3_virtual_networks"] = []any{map[string]any{"name": "Services"}}
			case "prerequisite-pending":
				fabric["l3_virtual_networks"] = []any{map[string]any{"name": "NotYet"}}
			case "unknown-collection":
				fabric["future"] = []any{}
			case "unknown-section":
				cc["wireless"] = map[string]any{}
			case "unscoped":
				delete(fabric["extranet_policies"].([]any)[0].(map[string]any), "fabric_sites")
			}
			config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if mode == "configmap" {
				cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "extranet-model", Namespace: config.Namespace, UID: "extranet-source"}, Data: map[string]string{"model.yaml": string(config.Spec.Source.Inline.Raw)}}
				if err := a.statusWriter.Create(ctx, cm); err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "update" || mode == "changed-policy-baseline" {
				retainExtranetCreationFixture(t, a, config, want)
			}
			if mode == "lost-ack" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			if mode == "undeclared" {
				selected = "Missing"
			}
			op, err := a.prepareExtranetPolicy(ctx, config, source, selected)
			beforeRefused := mode == "converged" || mode == "undeclared" || mode == "later-unready" || mode == "later-unowned" || mode == "prerequisite-pending" || mode == "unknown-collection" || mode == "unknown-section" || mode == "unscoped"
			if beforeRefused {
				if mode == "converged" {
					if err != nil || op != nil {
						t.Fatal("converged result", err)
					}
				} else if err == nil || op != nil {
					t.Fatal("invalid whole model prepared", err)
				}
				state := journalState(t, a)
				if state != nil && state.Operation != nil {
					t.Fatal("failed preflight created journal")
				}
				if writes.Load() != 0 {
					t.Fatal("preflight wrote native state")
				}
				return
			}
			if mode != "lost-ack" && mode != "pruned" && err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.TaskID != "" || state.Operation.SubmittedAt != nil {
				t.Fatal("missing retained preparation", err, state)
			}
			current := state.Operation.DeepCopy()
			if mode == "pruned" {
				if a.revalidateExtranetOperation(ctx, config, source, selected, current, "Prepared") == nil {
					t.Fatal("pruned group accepted")
				}
			} else {
				expected := 2
				if mode == "remote-vn" {
					expected = 3
				}
				if contract.ValidateGroupJournalIdentity(current) != nil || len(current.PhysicalGroup.Targets) != expected {
					t.Fatal("wrong group identity")
				}
				if mode == "update" || mode == "changed-policy-baseline" {
					if current.Action != "Update" || current.TargetID != "policy" || !strings.HasPrefix(current.Resource, "extranet-update-v2:") {
						t.Fatal("missing old-policy binding")
					}
				} else if current.Action != "Create" || current.TargetID != "" || current.ParentID != "" {
					t.Fatal("invented native anchor")
				}
				switch mode {
				case "changed-source":
					revised := config.DeepCopy()
					revised.Generation++
					if err := a.statusWriter.Update(ctx, revised); err != nil {
						t.Fatal(err)
					}
				case "changed-node":
					var node core.Node
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "peer"}, &node); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Delete(ctx, &node); err != nil {
						t.Fatal(err)
					}
					node.UID = "replacement"
					node.ResourceVersion = ""
					if err := a.statusWriter.Create(ctx, &node); err != nil {
						t.Fatal(err)
					}
				case "changed-native", "changed-policy-baseline":
					late.Store(true)
				case "wrong-phase":
					current.Phase = "Accepted"
				case "wrong-group":
					current.PhysicalGroup.Targets[0].Node.UID = "replacement"
				case "submitting":
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					nc.Status.Configuration.Operation.Phase = "Submitting"
					nc.Status.Configuration.Operation.SubmittedAt = &metav1.Time{Time: time.Now()}
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
					current = nc.Status.Configuration.Operation.DeepCopy()
				}
				phase := "Prepared"
				if mode == "submitting" {
					phase = "Submitting"
				}
				restarted := *a
				valid := mode == "create" || mode == "update" || mode == "remote-vn" || mode == "configmap" || mode == "lost-ack" || mode == "submitting" || mode == "prerequisite-ready"
				if err := restarted.revalidateExtranetOperation(ctx, config, source, selected, current, phase); (err == nil) != valid {
					t.Fatal("exact revalidation", err)
				}
			}
			if a.startConfiguration(ctx, config, source, current.ID) == nil {
				t.Fatal("group fell through metadata dispatch")
			}
			if _, err := a.prepareExtranetPolicy(ctx, config, source, selected); err == nil {
				t.Fatal("retained preparation replaced")
			}
			if writes.Load() != 0 {
				t.Fatal("preparation/revalidation mutated native state")
			}
			var rows ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &rows); err != nil || len(rows.Items) != 0 {
				t.Fatal("preparation fabricated reservations", err)
			}
		})
	}
}

func TestExtranetPreparedResourceAndAnchors(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, action := range []string{"Create", "Update", "Delete"} {
		for _, resource := range []string{"extranet-create:" + digest, "extranet-create:pruned", "extranet-update:policy:" + digest, "extranet-update-v2:policy:" + digest + ":" + digest, "extranet-update-v2:policy:" + digest, "extranet-update-v2:bad/id:" + digest + ":" + digest, "extranet-update-v2:policy:" + strings.ToUpper(digest) + ":" + digest} {
			if !isReservedConfigurationGroupResource("fabric", resource) {
				t.Fatal("unreserved prefix", resource)
			}
			valid := action == "Create" && resource == "extranet-create:"+digest || action == "Update" && resource == "extranet-update-v2:policy:"+digest+":"+digest
			if validConfigurationGroupAction("fabric", resource, action) != valid {
				t.Fatal("invalid resource classification", resource, action)
			}
			if validConfigurationGroupAction("inventory", resource, action) {
				t.Fatal("wrong section accepted")
			}
		}
	}
	ctx := context.Background()
	for _, mode := range []string{"create", "missing-group", "invented-create-id", "invented-parent", "legacy-update", "wrong-update-id", "other-group-missing-parent"} {
		t.Run(mode, func(t *testing.T) {
			a, config, want, _, _ := extranetManagedFixture(t, "valid")
			config.Spec.Source.Inline.Raw = []byte(extranetModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			_, targets, err := a.client.extranetManagedTargets(ctx, a.statusReader, config.Namespace, want)
			if err != nil {
				t.Fatal(err)
			}
			resource, action := "extranet-create:"+digest, "Create"
			identity := configurationResourceIdentity{groupTargets: targets}
			switch mode {
			case "missing-group":
				identity.groupTargets = nil
			case "invented-create-id":
				identity.target = "invented"
			case "invented-parent":
				identity.parent = "invented"
			case "legacy-update":
				resource = "extranet-update:policy:" + digest
				action = "Update"
				identity.target = "policy"
			case "wrong-update-id":
				resource = "extranet-update-v2:policy:" + digest + ":" + digest
				action = "Update"
				identity.target = "other"
			case "other-group-missing-parent":
				resource = "fabric-port-channel-create:" + digest
				identity.target = "edge-id"
			}
			op, err := a.prepareConfiguration(ctx, config, source, "fabric", resource, action, identity)
			if mode == "create" {
				if err != nil || op == nil {
					t.Fatal(err)
				}
			} else if err == nil || op != nil {
				t.Fatal("invalid anchor exception accepted", mode)
			}
		})
	}
}
