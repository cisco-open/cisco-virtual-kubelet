// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func TestMaintenanceSessionCRDTransitionValidation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "cisco.vk_ciscodevices.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	session := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["maintenanceSession"]
	if len(session.Properties["phase"].Enum) != 3 {
		t.Error("maintenance CRD publishes phases the manager never writes")
	}
	var schema apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&session, &schema, nil); err != nil {
		t.Fatal(err)
	}
	structural, err := structuralschema.NewStructural(&schema)
	if err != nil {
		t.Fatal(err)
	}
	validator := cel.NewValidator(structural, false, 10_000_000)
	if validator == nil {
		t.Fatal("maintenance CRD lacks transition validation")
	}

	newSession := func() map[string]interface{} {
		return map[string]interface{}{
			"phase": "Active", "sessionToken": "session-token-0001", "deviceUID": "device-uid", "nodeName": "node", "nodeUID": "node-uid",
			"lease":       map[string]interface{}{"namespace": "edge", "name": "canonical", "uid": "lease-uid", "holder": "upgrade/leaf-uid"},
			"operation":   map[string]interface{}{"namespace": "edge", "name": "upgrade", "uid": "leaf-uid"},
			"requestedAt": "2026-09-11T00:00:00Z", "acknowledgedAt": "2026-09-11T00:00:01Z", "controlRevision": int64(7),
		}
	}
	for _, name := range []string{"unchanged", "revision advances", "revision regresses", "active token changes", "active Lease changes", "ack missing", "next settled session", "same settled session regresses"} {
		t.Run(name, func(t *testing.T) {
			old, current := newSession(), newSession()
			allowed := false
			switch name {
			case "unchanged":
				allowed = true
			case "revision advances":
				current["controlRevision"] = int64(8)
				allowed = true
			case "revision regresses":
				current["controlRevision"] = int64(6)
			case "active token changes":
				current["sessionToken"] = "session-token-0002"
			case "active Lease changes":
				current["lease"].(map[string]interface{})["uid"] = "replacement"
			case "ack missing":
				delete(current, "acknowledgedAt")
			case "next settled session":
				old["phase"] = "Settled"
				current["sessionToken"] = "session-token-0002"
				current["operation"].(map[string]interface{})["uid"] = "next-leaf-uid"
				current["controlRevision"] = int64(1)
				allowed = true
			case "same settled session regresses":
				old["phase"] = "Settled"
				current["controlRevision"] = int64(1)
			}
			errs, _ := validator.Validate(context.Background(), field.NewPath("status", "maintenanceSession"), structural, current, old, 10_000_000)
			if (len(errs) == 0) != allowed {
				t.Fatalf("allowed=%v validation=%v", allowed, errs)
			}
		})
	}
}
