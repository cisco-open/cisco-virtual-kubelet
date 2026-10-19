// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"sigs.k8s.io/yaml"
)

func TestWirelessSSIDHistoryGeneratedSchemaAndOldSchemaPruning(t *testing.T) {
	var operationSchema apiextensionsv1.JSONSchemaProps
	for _, path := range []string{"../../../config/crd/cisco.vk_networkcontrollers.yaml", "../../../charts/cisco-virtual-kubelet/crds/cisco.vk_networkcontrollers.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatal(err)
		}
		root := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
		count := 0
		var check func(apiextensionsv1.JSONSchemaProps)
		check = func(schema apiextensionsv1.JSONSchemaProps) {
			for key, child := range schema.Properties {
				if key == "ssidCredentialHistory" {
					count++
					if child.MaxItems == nil || *child.MaxItems != 5 || child.XListType == nil || *child.XListType != "map" || !reflect.DeepEqual(child.XListMapKeys, []string{"priority"}) || child.Items == nil || child.Items.Schema == nil {
						t.Fatal("invalid history collection schema", path)
					}
					item := child.Items.Schema
					if !reflect.DeepEqual(item.Required, []string{"priority", "version"}) || item.Properties["priority"].Maximum == nil || *item.Properties["priority"].Maximum != 4 || item.Properties["version"].Minimum == nil || *item.Properties["version"].Minimum != 1 {
						t.Fatal("invalid history item schema", path)
					}
				}
				check(child)
			}
			if schema.Items != nil && schema.Items.Schema != nil {
				check(*schema.Items.Schema)
			}
		}
		check(*root)
		// The retained metadata handoff embeds the original operation too.
		if count != 5 {
			t.Fatal("history missing from embedded operation/receipt schemas", path, count)
		}
		current := root.Properties["status"].Properties["configuration"].Properties["operation"]
		handoff := root.Properties["status"].Properties["configuration"].Properties["metadataHandoff"].Properties["operation"]
		// Field comments differ; the actual structural and validation schema must not.
		handoff.Description = current.Description
		if !reflect.DeepEqual(current, handoff) {
			t.Fatal("metadata handoff operation schema lost journal fields", path)
		}
		if operationSchema.Type != "" && !reflect.DeepEqual(operationSchema, current) {
			t.Fatal("chart schema drift")
		}
		operationSchema = current
	}
	history := []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 4, Version: 3}}
	versions := []ciskov1.NetworkControllerConfigurationCredentialVersion{{Key: "passphrase", Path: "/ssids/0/passphrase", Version: 1, SourceAlias: "main", SecretName: "keys", SecretKey: "main"}}
	op := ciskov1.NetworkControllerConfigurationOperation{Section: "wireless", Resource: ssidHistoryPrefix(history) + ":global:CVK", CredentialVersions: versions, SSIDCredentialHistory: history}
	// Simulate an old CRD that otherwise preserves version metadata.
	delete(operationSchema.Properties, "ssidCredentialHistory")
	var converted apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&operationSchema, &converted, nil); err != nil {
		t.Fatal(err)
	}
	structural, err := structuralschema.NewStructural(&converted)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(op)
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	pruning.Prune(object, structural, true)
	raw, _ = json.Marshal(object)
	var pruned ciskov1.NetworkControllerConfigurationOperation
	if err := json.Unmarshal(raw, &pruned); err != nil {
		t.Fatal(err)
	}
	if len(pruned.SSIDCredentialHistory) != 0 || len(pruned.CredentialVersions) != 1 {
		t.Fatal("fixture did not isolate history pruning")
	}
	if err := validateSSIDCredentialHistory(pruned.Section, pruned.Resource, pruned.CredentialVersions, pruned.SSIDCredentialHistory); err == nil {
		t.Fatal("old schema silently lost retired history")
	}

	a, config := journalFixture(t)
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pruned.ID = "0123456789abcdef0123456789abcdef"
	pruned.Phase = "Verified"
	pruned.ConfigUID = "owner"
	pruned.TargetID = "ssid-id"
	nc.Status.Configuration = &ciskov1.NetworkControllerConfigurationStatus{Operation: &pruned, CredentialApplications: []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: pruned.Section, Resource: pruned.Resource, ConfigUID: pruned.ConfigUID, TargetID: pruned.TargetID, OperationID: pruned.ID, Versions: versions}}}
	if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(context.Background(), pruned.ID); err == nil {
		t.Fatal("pruned operation and receipt released slot")
	}
	if journalState(t, a).Operation == nil {
		t.Fatal("retained history fence lost")
	}
	nc, err = a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.Phase = "Prepared"
	if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(context.Background(), config, nil, pruned.ID); !errors.Is(err, errConfigurationSecret) {
		t.Fatal("pruned history received submission permission", err)
	}
	if _, err := a.prepareConfiguration(context.Background(), config, nil, "wireless", op.Resource, "Update"); !errors.Is(err, errConfigurationSecret) {
		t.Fatal("history marker prepared without binding", err)
	}

}
