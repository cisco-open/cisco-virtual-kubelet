// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnvtestSDATransitJournalPreparation(t *testing.T) {
	for _, mode := range []string{"source-bound", "alias-bound", "whole-source-bound", "whole-source-invalid", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml", "../../../config/crd/cisco.vk_ciscodevices.yaml")
			a, config, want, f, physical := sdaTransitJournalFixture(t)
			if mode == "whole-source-bound" || mode == "whole-source-invalid" {
				config.Spec.Source.Inline.Raw = sdaTransitMultiSourceDocument(f)
				wants, err := parseSDATransitIntents(config.Spec.Source.Inline.Raw)
				if err != nil {
					t.Fatal(err)
				}
				want = wants[0]
				if mode == "whole-source-invalid" {
					f.unprovisionedIDs = map[string]bool{"edge-1": true}
				}
			}
			if mode == "alias-bound" {
				f.devices[0].Hostname, f.devices[1].Hostname = "edge0.lab.example", "edge1.lab.example"
				model, _ := sdaTransitAliasModel(f.devices)
				config.Spec.Source.Inline.Raw, _ = json.Marshal(model)
				wants, err := parseSDATransitIntents(config.Spec.Source.Inline.Raw)
				if err != nil {
					t.Fatal(err)
				}
				want = wants[0]
			}
			installWirelessFeatureQualificationAPI(t, api, "sda-transit-journal")(a, config)
			for _, original := range physical {
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: original.node.Name}}
				if err := api.Create(ctx, node); err != nil {
					t.Fatal(err)
				}
				node.Status.NodeInfo.MachineID = original.node.Status.NodeInfo.MachineID
				if err := api.Status().Update(ctx, node); err != nil {
					t.Fatal(err)
				}
				d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: original.device.Name}, Spec: original.device.Spec}
				if err := api.Create(ctx, d); err != nil {
					t.Fatal(err)
				}
				d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(d.UID), NodeName: node.Name, NodeUID: string(node.UID), PhysicalIdentity: d.Spec.PhysicalIdentity}
				if err := api.Status().Update(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "sda-source"}, Data: map[string]string{"model.json": string(config.Spec.Source.Inline.Raw)}}
			if err := api.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline = nil
			config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
			if err := api.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, api, config)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "pruned-group" {
				a.statusWriter = prunePreparedGroupClient{api}
			}
			op, err := a.prepareSDATransit(ctx, config, source, want)
			state := journalState(t, a)
			if mode == "whole-source-invalid" {
				if err == nil || op != nil || state != nil || f.posts != 0 {
					t.Fatal("invalid later dependency persisted preparation", err)
				}
				return
			}
			if mode == "pruned-group" {
				if err == nil || op != nil || state.Operation == nil || state.Operation.PhysicalGroup != nil || state.Operation.Phase != "Prepared" {
					t.Fatal("pruned preparation not retained", err)
				}
				if err := a.finishConfiguration(ctx, state.Operation.ID); !errors.Is(err, errConfigurationDeviceAdmission) {
					t.Fatal("pruned group entered legacy finish", err)
				}
			} else {
				count := 2
				if mode == "whole-source-bound" {
					count = 1
				}
				if err != nil || op == nil || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != count || op.SourceUID != string(cm.UID) || op.SourceVersion != cm.ResourceVersion || len(state.Owners) != 1 {
					t.Fatal("source/owner/group persistence incomplete", err)
				}
				before := state.Operation.DeepCopy()
				if err := a.revalidateSDATransitOperation(ctx, config, source, want, op, "Prepared"); err != nil {
					t.Fatal(err)
				}
				if mode == "alias-bound" {
					var model map[string]any
					if json.Unmarshal([]byte(cm.Data["model.json"]), &model) != nil {
						t.Fatal("alias fixture source")
					}
					row := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
					row["fqdn_name"] = "changed.lab.example"
					raw, _ := json.Marshal(model)
					cm.Data["model.json"] = string(raw)
				} else {
					cm.Data["revision"] = "changed"
				}
				if err := api.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
				if err := a.revalidateSDATransitOperation(ctx, config, source, want, op, "Prepared"); err == nil {
					t.Fatal("ConfigMap resourceVersion change admitted")
				}
				if !reflect.DeepEqual(before, journalState(t, a).Operation) {
					t.Fatal("source refusal rewrote journal")
				}
			}
			if f.posts != 0 {
				t.Fatal("API preparation or source drift submitted product request")
			}
		})
	}
}
