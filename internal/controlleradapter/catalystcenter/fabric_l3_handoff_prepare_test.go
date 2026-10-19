// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testFabricL3IPHandoffPrepareFlow(t *testing.T, native *client, objects []ctrlclient.Object, key fabricL3IPHandoffIntent, existing bool) {
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
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"a","border_types":["LAYER_3"],"local_autonomous_system_number":65000,"default_exit":true,"import_external_routes":true,"l3_handoffs":[{"name":"Transit","interfaces":[{"name":"GigabitEthernet1/0/1","virtual_networks":[{"name":"Users","vlan":300,"local_ip_address":"192.0.2.1/30","peer_ip_address":"192.0.2.2/30"}]}]}]}]}}}`)
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
			op, err := a.prepareFabricL3IPHandoff(ctx, config, source, selected)
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
				err = a.revalidateFabricL3IPHandoffOperation(ctx, config, source, selected, checked, "Prepared")
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
					if err := a.revalidateFabricL3IPHandoffOperation(ctx, config, source, selected, current.Status.Configuration.Operation, "Submitting"); err != nil {
						t.Fatal(err)
					}
					if err := a.revalidateFabricL3IPHandoffOperation(ctx, config, source, selected, current.Status.Configuration.Operation, "Prepared"); err == nil {
						t.Fatal("submitted operation accepted as prepared")
					}
				}
			}
			if _, err := a.prepareFabricL3IPHandoff(ctx, config, source, selected); err == nil {
				t.Fatal("restart replaced an existing operation")
			}
		})
	}
}

// Optional MSS is a value in the model, not a Go pointer identity. The selector
// selects one declared resource; the public sequencer schedules every family.
func TestFabricL3IPHandoffDeclaredSelection(t *testing.T) {
	for _, mode := range []string{"mss-value", "changed-mss", "mixed", "nil-source"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["BORDER_NODE"]}]},"fabric":{"border_devices":[{"name":"a","border_types":["LAYER_2","LAYER_3"],"local_autonomous_system_number":65000,"default_exit":true,"import_external_routes":true,"l3_handoffs":[{"name":"Transit","interfaces":[{"name":"GigabitEthernet1/0/1","virtual_networks":[{"name":"Users","vlan":300,"tcp_mss_adjustment":1200}]}]}]}]}}}`)
			if mode == "mixed" {
				var root map[string]any
				if err := json.Unmarshal(raw, &root); err != nil {
					t.Fatal(err)
				}
				border := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)["border_devices"].([]any)[0].(map[string]any)
				border["l2_handoffs"] = map[string]any{"l2_without_anycast_gateway": map[string]any{"vlans": []any{map[string]any{"name": "IoT", "external_vlan": 400}}, "interfaces": []any{"GigabitEthernet1/0/1"}}}
				var err error
				raw, err = json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, _, intents, err := parseFabricHandoffDeployment(raw)
			if err != nil || len(intents) != 1 {
				t.Fatal("fixture", err)
			}
			key := intents[0].handoff
			mss := 1200
			if mode == "changed-mss" {
				mss = 1201
			}
			key.mss = &mss
			source := &configSourceSnapshot{raw: raw}
			if mode == "nil-source" {
				source = nil
			}
			got, err := declaredFabricL3IPHandoff(source, key)
			if mode == "mss-value" || mode == "mixed" {
				if err != nil || got == nil {
					t.Fatal("value-equivalent MSS rejected", err)
				}
			} else if err == nil || got != nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
}
