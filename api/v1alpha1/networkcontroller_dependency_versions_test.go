// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"reflect"
	"testing"
	"time"

	extensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"sigs.k8s.io/yaml"
)

func TestDependencyVersionsSchemaPreservation(t *testing.T) {
	var canonical []byte
	for _, path := range []string{"../../config/crd/cisco.vk_networkcontrollers.yaml", "../../charts/cisco-virtual-kubelet/crds/cisco.vk_networkcontrollers.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if canonical != nil && string(canonical) != string(raw) {
			t.Fatal("Helm schema drift")
		}
		canonical = raw
		var crd v1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatal(err)
		}
		config := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["configuration"]
		for _, operation := range []v1.JSONSchemaProps{config.Properties["operation"], config.Properties["actionApplications"].Items.Schema.Properties["operation"]} {
			prepared := operation.Properties["preparedAt"]
			if prepared.Type != "string" || prepared.Format != "date-time" {
				t.Fatal("missing persisted preparation timestamp schema")
			}
			var operationSchema extensions.JSONSchemaProps
			if err := v1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&operation, &operationSchema, nil); err != nil {
				t.Fatal(err)
			}
			operationStructure, err := schema.NewStructural(&operationSchema)
			if err != nil {
				t.Fatal(err)
			}
			preparation := map[string]any{"preparedAt": "2026-10-14T12:00:00Z", "id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "phase": "Prepared"}
			pruning.Prune(preparation, operationStructure, true)
			if preparation["preparedAt"] != "2026-10-14T12:00:00Z" {
				t.Fatal("preparation timestamp pruned")
			}
			deps := operation.Properties["dependencyVersions"]
			entries := deps.Properties["entries"]
			if entries.MaxItems == nil || *entries.MaxItems != 1001 || entries.MinItems == nil || *entries.MinItems != 2 || entries.XListType == nil || *entries.XListType != "atomic" {
				t.Fatal("missing bounded ordered graph schema")
			}
			var internal extensions.JSONSchemaProps
			if err := v1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&deps, &internal, nil); err != nil {
				t.Fatal(err)
			}
			structure, err := schema.NewStructural(&internal)
			if err != nil {
				t.Fatal(err)
			}
			object := map[string]any{"kind": "template-graph-v1", "entries": []any{map[string]any{"id": "parent", "versionID": "parent-v2", "version": float64(2)}, map[string]any{"id": "member", "versionID": "member-v4", "version": float64(4)}}}
			encoded, _ := json.Marshal(object)
			pruning.Prune(object, structure, true)
			actual, _ := json.Marshal(object)
			if string(encoded) != string(actual) {
				t.Fatal("recovery version bindings pruned")
			}
		}
	}
	op := &NetworkControllerConfigurationOperation{PreparedAt: &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}, DependencyVersions: &NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1", Entries: []NetworkControllerConfigurationDependencyVersion{{ID: "parent", VersionID: "parent-v2", Version: 2}, {ID: "member", VersionID: "member-v4", Version: 4}}}}
	copy := op.DeepCopy()
	if !reflect.DeepEqual(op, copy) {
		t.Fatal("copy lost graph")
	}
	copy.PreparedAt.Time = copy.PreparedAt.Add(time.Second)
	if copy.PreparedAt.Equal(op.PreparedAt) {
		t.Fatal("copy shares preparation timestamp")
	}
	copy.DependencyVersions.Entries[1].Version = 99
	if op.DependencyVersions.Entries[1].Version != 4 {
		t.Fatal("copy shares graph backing array")
	}
}
