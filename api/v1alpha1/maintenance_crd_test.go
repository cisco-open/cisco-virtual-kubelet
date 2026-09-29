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
	crdDir := filepath.Join("..", "..", "config", "crd")
	if override := os.Getenv("CVK_TEST_CRD_DIR"); override != "" {
		crdDir = override
	}
	raw, err := os.ReadFile(filepath.Join(crdDir, "cisco.vk_ciscodevices.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	session := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["maintenanceSession"]
	if len(session.Properties["phase"].Enum) != 4 {
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
	for _, name := range []string{"unchanged", "revision advances", "revision regresses", "active token changes", "active operation namespace changes", "active operation name changes", "active Lease namespace changes", "active Lease name changes", "active Lease changes", "active acknowledgement changes", "ack missing", "next settled session", "same settled session regresses", "legacy recovery denied", "drain recovery", "drain promotion", "drain holder-only change denied", "mixed protocol fields denied"} {
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
			case "active operation namespace changes":
				current["operation"].(map[string]interface{})["namespace"] = "other"
			case "active operation name changes":
				current["operation"].(map[string]interface{})["name"] = "other"
			case "active Lease namespace changes":
				current["lease"].(map[string]interface{})["namespace"] = "other"
			case "active Lease name changes":
				current["lease"].(map[string]interface{})["name"] = "other"
			case "active Lease changes":
				current["lease"].(map[string]interface{})["uid"] = "replacement"
			case "active acknowledgement changes":
				current["acknowledgedAt"] = "2026-09-11T00:00:02Z"
			case "ack missing":
				delete(current, "acknowledgedAt")
			case "next settled session":
				old["phase"] = "Settled"
				current["sessionToken"] = "session-token-0002"
				current["operation"].(map[string]interface{})["uid"] = "next-leaf-uid"
				current["lease"].(map[string]interface{})["uid"] = "next-lease-uid"
				current["requestedAt"] = "2026-09-12T00:00:00Z"
				current["acknowledgedAt"] = "2026-09-12T00:00:01Z"
				current["controlRevision"] = int64(1)
				allowed = true
			case "same settled session regresses":
				old["phase"] = "Settled"
				current["controlRevision"] = int64(1)
			case "legacy recovery denied":
				current["phase"] = "Recovering"
			case "drain recovery":
				for _, session := range []map[string]interface{}{old, current} {
					session["protocolVersion"] = "pdb-drain-v1"
					session["purpose"] = "WorkloadDrain"
					session["sessionToken"] = "11111111-1111-4111-8111-111111111111"
					session["lease"].(map[string]interface{})["holder"] = "software-drain/leaf-uid"
				}
				current["phase"] = "Recovering"
				allowed = true
			case "drain promotion":
				for _, session := range []map[string]interface{}{old, current} {
					session["protocolVersion"] = "pdb-drain-v1"
					session["purpose"] = "WorkloadDrain"
					session["sessionToken"] = "11111111-1111-4111-8111-111111111111"
					session["lease"].(map[string]interface{})["holder"] = "software-drain/leaf-uid"
				}
				current["purpose"] = "SoftwareMutation"
				current["lease"].(map[string]interface{})["holder"] = "software-upgrade/leaf-uid"
				allowed = true
			case "drain holder-only change denied":
				for _, session := range []map[string]interface{}{old, current} {
					session["protocolVersion"] = "pdb-drain-v1"
					session["purpose"] = "WorkloadDrain"
					session["sessionToken"] = "11111111-1111-4111-8111-111111111111"
					session["lease"].(map[string]interface{})["holder"] = "software-drain/leaf-uid"
				}
				current["lease"].(map[string]interface{})["holder"] = "software-upgrade/leaf-uid"
			case "mixed protocol fields denied":
				current["protocolVersion"] = "pdb-drain-v1"
			}
			errs, _ := validator.Validate(context.Background(), field.NewPath("status", "maintenanceSession"), structural, current, old, 10_000_000)
			if (len(errs) == 0) != allowed {
				t.Fatalf("allowed=%v validation=%v", allowed, errs)
			}
		})
	}
}
