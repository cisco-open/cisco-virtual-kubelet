// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	"os"
	"testing"

	extensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"sigs.k8s.io/yaml"
)

func TestCreatedResourceReceiptSchemaPreservation(t *testing.T) {
	for _, path := range []string{"../../config/crd/cisco.vk_networkcontrollers.yaml", "../../charts/cisco-virtual-kubelet/crds/cisco.vk_networkcontrollers.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var crd v1.CustomResourceDefinition
		if yaml.Unmarshal(raw, &crd) != nil {
			t.Fatal("invalid CRD")
		}
		count := 0
		var visit func(v1.JSONSchemaProps)
		visit = func(node v1.JSONSchemaProps) {
			if _, ok := node.Properties["taskID"]; ok {
				field, exists := node.Properties["createdResourceID"]
				if !exists || field.MaxLength == nil || *field.MaxLength != 128 {
					t.Fatal("missing bounded created UUID")
				}
				var internal extensions.JSONSchemaProps
				if err := v1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&node, &internal, nil); err != nil {
					t.Fatal(err)
				}
				structure, err := schema.NewStructural(&internal)
				if err != nil {
					t.Fatal(err)
				}
				object := map[string]any{"createdResourceID": "native-id"}
				pruning.Prune(object, structure, true)
				if object["createdResourceID"] != "native-id" {
					t.Fatal("native receipt pruned")
				}
				count++
			}
			for _, child := range node.Properties {
				visit(child)
			}
			if node.Items != nil && node.Items.Schema != nil {
				visit(*node.Items.Schema)
			}
		}
		visit(*crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
		// The retained metadata handoff contains the fourth typed operation copy.
		if count != 4 {
			t.Fatalf("checked %d journal/receipt schemas", count)
		}
	}
	op := &NetworkControllerConfigurationOperation{CreatedResourceID: "native-id"}
	if op.DeepCopy().CreatedResourceID != "native-id" {
		t.Fatal("copy lost receipt")
	}
}
