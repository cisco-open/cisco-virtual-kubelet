// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	schema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func TestConfigurationGroupCRDPreservation(t *testing.T) {
	paths := []string{"../../config/crd/cisco.vk_networkcontrollers.yaml", "../../charts/cisco-virtual-kubelet/crds/cisco.vk_networkcontrollers.yaml"}
	var baseline []byte
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if baseline != nil && string(raw) != string(baseline) {
			t.Fatal("canonical and Helm schemas differ")
		}
		baseline = raw
		var crd v1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatal(err)
		}
		configuration := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["configuration"]
		operations := map[string]v1.JSONSchemaProps{"active": configuration.Properties["operation"], "completed": configuration.Properties["actionApplications"].Items.Schema.Properties["operation"]}
		for name, operation := range operations {
			t.Run(path+"/"+name, func(t *testing.T) {
				var internal apiextensions.JSONSchemaProps
				if err := v1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&operation, &internal, nil); err != nil {
					t.Fatal(err)
				}
				structural, err := schema.NewStructural(&internal)
				if err != nil {
					t.Fatal(err)
				}
				validator := cel.NewValidator(structural, false, 10_000_000)
				if validator == nil {
					t.Fatal("missing scope validation")
				}
				for _, mode := range []string{"group", "legacy", "mixed"} {
					group := map[string]interface{}{"version": "controller-configuration-group-identity-v1", "digest": strings.Repeat("a", 64), "targets": []interface{}{map[string]interface{}{"inventoryID": "inventory", "physicalIdentity": "serial", "device": map[string]interface{}{"name": "device", "uid": "device-uid"}, "node": map[string]interface{}{"name": "node", "uid": "node-uid"}}}}
					obj := map[string]interface{}{"physicalGroup": group, "phase": "Prepared"}
					if mode == "legacy" {
						delete(obj, "physicalGroup")
					}
					if mode != "group" {
						obj["targetSerial"] = "serial"
					}
					expected, err := yaml.Marshal(obj)
					if err != nil {
						t.Fatal(err)
					}
					pruning.Prune(obj, structural, false)
					after, err := yaml.Marshal(obj)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(expected, after) {
						t.Fatalf("journal fields pruned: %s", after)
					}
					errs, _ := validator.Validate(context.Background(), field.NewPath("operation"), structural, obj, nil, 10_000_000)
					if (len(errs) == 0) != (mode != "mixed") {
						t.Fatalf("%s: %v", mode, errs)
					}
				}
			})
		}
	}
}
