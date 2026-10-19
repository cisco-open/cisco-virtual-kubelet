// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testFabricL2HandoffPrepareFlow(t *testing.T, native *client, objects []ctrlclient.Object, key fabricL2HandoffIntent, existing bool) {
	t.Helper()
	for _, mode := range []string{"valid", "undeclared", "lost-response", "pruned", "preflight-source", "preflight-owner", "preflight-node", "preflight-group", "preflight-phase"} {
		t.Run("prepare-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = native
			for _, obj := range objects {
				copy := obj.DeepCopyObject().(ctrlclient.Object)
				copy.SetResourceVersion("")
				if err := a.statusWriter.Create(ctx, copy); err != nil {
					t.Fatal(err)
				}
			}
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"a","border_types":["LAYER_2"],"l2_handoffs":{"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":400}],"interfaces":["GigabitEthernet1/0/1"]}}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			selected := key
			if mode == "undeclared" {
				selected.device = "absent"
			}
			if mode == "lost-response" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			op, err := a.prepareFabricL2Handoff(ctx, config, source, selected)
			success := mode == "valid" || strings.HasPrefix(mode, "preflight-") || existing && mode != "undeclared"
			if (err == nil) != success {
				t.Fatal("prepare outcome", err)
			}
			state := journalState(t, a)
			if existing || mode == "undeclared" {
				if op != nil || state != nil && state.Operation != nil {
					t.Fatal("no-op or undeclared intent created a journal")
				}
				return
			}
			if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" {
				t.Fatal("uncertainty was not retained")
			}
			if mode != "pruned" && (contract.ValidateGroupJournalIdentity(state.Operation) != nil || len(state.Operation.PhysicalGroup.Targets) != 2) {
				t.Fatal("incomplete durable footprint")
			}
			if mode == "valid" || strings.HasPrefix(mode, "preflight-") {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				checked := current.Status.Configuration.Operation.DeepCopy()
				switch mode {
				case "preflight-source":
					changed := config.DeepCopy()
					changed.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{}}`)
					if err := a.statusWriter.Update(ctx, changed); err != nil {
						t.Fatal(err)
					}
				case "preflight-owner":
					current.Status.Configuration.Owners = nil
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				case "preflight-node":
					var node corev1.Node
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "b"}, &node); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Delete(ctx, &node); err != nil {
						t.Fatal(err)
					}
				case "preflight-group":
					checked.PhysicalGroup.Targets[0].Node.UID = "replacement"
				case "preflight-phase":
					checked.Phase = "Unknown"
				}
				err = a.revalidateFabricL2HandoffOperation(ctx, config, source, selected, checked, "Prepared")
				if (err == nil) != (mode == "valid") {
					t.Fatal("preflight result", err)
				}
				if mode == "valid" {
					current.Status.Configuration.Operation.Phase = "Submitting"
					at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
					current.Status.Configuration.Operation.SubmittedAt = &at
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
					if err := a.revalidateFabricL2HandoffOperation(ctx, config, source, selected, current.Status.Configuration.Operation, "Submitting"); err != nil {
						t.Fatal(err)
					}
					if err := a.revalidateFabricL2HandoffOperation(ctx, config, source, selected, current.Status.Configuration.Operation, "Prepared"); err == nil {
						t.Fatal("submitted operation accepted as prepared")
					}
				}
			}
			if _, err := a.prepareFabricL2Handoff(ctx, config, source, selected); err == nil {
				t.Fatal("restart replaced an existing operation")
			}
		})
	}
}
