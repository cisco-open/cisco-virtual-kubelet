// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	"encoding/json"
	"os"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestConfigurationEffectReceiptSchema(t *testing.T) {
	var canonical []byte
	for _, path := range []string{"../../config/crd/cisco.vk_networkcontrollers.yaml", "../../charts/cisco-virtual-kubelet/crds/cisco.vk_networkcontrollers.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if canonical != nil && string(canonical) != string(raw) {
			t.Fatal("canonical and chart CRDs differ")
		}
		canonical = raw
		var crd apiextensions.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatal(err)
		}
		row := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["configuration"].Properties["actionApplications"].Items.Schema
		field, ok := row.Properties["effectReceipts"]
		if !ok || field.Type != "array" || field.MaxItems == nil || *field.MaxItems != 256 || field.XListType == nil || *field.XListType != "set" || field.Items == nil || field.Items.Schema == nil || field.Items.Schema.Type != "string" || field.Items.Schema.MinLength == nil || *field.Items.Schema.MinLength != 1 || field.Items.Schema.MaxLength == nil || *field.Items.Schema.MaxLength != 512 {
			t.Fatal("related-effect metadata is unbounded or not a set")
		}
		for _, name := range row.Required {
			if name == "effectReceipts" {
				t.Fatal("legacy receipts unexpectedly require related effects")
			}
		}
	}
}

func TestConfigurationEffectReceiptDeepCopyAndLegacyJSON(t *testing.T) {
	row := NetworkControllerConfigurationActionApplication{Section: "sites", Resource: "legacy"}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["effectReceipts"] != nil {
		t.Fatal("legacy JSON acquired new required state")
	}
	row.EffectReceipts = []string{"effect"}
	state := &NetworkControllerConfigurationStatus{ActionApplications: []NetworkControllerConfigurationActionApplication{row}}
	copy := state.DeepCopy()
	copy.ActionApplications[0].EffectReceipts[0] = "changed"
	if state.ActionApplications[0].EffectReceipts[0] != "effect" {
		t.Fatal("generated controller-status copy aliases receipt evidence")
	}
}
