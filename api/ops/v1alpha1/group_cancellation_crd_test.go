// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	"context"
	"os"
	"testing"

	internal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	schema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func TestGroupCancellationStatusCEL(t *testing.T) {
	raw, err := os.ReadFile(generatedOpsCRDPath("ops.cisco.vk_controllerconfigurationreservations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd v1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	status := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"]
	var converted internal.JSONSchemaProps
	if err := v1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&status, &converted, nil); err != nil {
		t.Fatal(err)
	}
	structural, err := schema.NewStructural(&converted)
	if err != nil {
		t.Fatal(err)
	}
	validator := cel.NewValidator(structural, false, 10_000_000)
	if validator == nil {
		t.Fatal("missing cancellation rules")
	}
	grant := func() map[string]interface{} {
		return map[string]interface{}{"phase": "Held", "leaseNamespace": "leases", "lease": map[string]interface{}{"name": "lease", "uid": "lease-uid"}, "holder": "holder", "leaseTransitions": int64(1), "grantedAt": "2026-10-13T11:59:58Z"}
	}
	base := map[string]interface{}{"groupCancellation": map[string]interface{}{"operationID": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "operationDigest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "requestedAt": "2026-10-13T12:00:00Z", "acknowledgedAt": "2026-10-13T12:00:01Z"}}
	for _, mode := range []string{"unchanged", "new-grant", "new-claim", "changed-ack", "removed-ack", "completion", "settled-ungranted", "removed-grant", "removed-claim", "released-claimed", "changed-grant", "reopened-grant"} {
		old := runtime.DeepCopyJSON(base)
		current := runtime.DeepCopyJSON(base)
		switch mode {
		case "new-grant":
			current["grant"] = grant()
		case "new-claim":
			old["grant"] = grant()
			current["grant"] = grant()
			current["claim"] = map[string]interface{}{"operationID": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		case "changed-ack":
			current["groupCancellation"].(map[string]interface{})["acknowledgedAt"] = "2026-10-13T12:00:02Z"
		case "removed-ack":
			delete(current, "groupCancellation")
		case "completion":
			current["completion"] = map[string]interface{}{"operationID": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		case "changed-grant", "reopened-grant":
			old["grant"] = grant()
			current["grant"] = grant()
			if mode == "changed-grant" {
				current["grant"].(map[string]interface{})["holder"] = "other"
			} else {
				old["grant"].(map[string]interface{})["phase"] = "Released"
			}
		case "removed-grant":
			old["grant"] = grant()
		case "removed-claim", "released-claimed":
			old["grant"] = grant()
			old["claim"] = map[string]interface{}{"operationID": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "operationDigest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "claimedAt": "2026-10-13T11:59:59Z"}
			current = runtime.DeepCopyJSON(old)
			if mode == "removed-claim" {
				delete(current, "claim")
			} else {
				current["grant"].(map[string]interface{})["phase"] = "Released"
				current["managerSettledAt"] = "2026-10-13T12:00:03Z"
			}
		case "settled-ungranted":
			current["managerSettledAt"] = "2026-10-13T12:00:03Z"
		}
		errs, _ := validator.Validate(context.Background(), field.NewPath("status"), structural, current, old, 10_000_000)
		valid := mode == "unchanged" || mode == "settled-ungranted" || mode == "released-claimed"
		if (len(errs) == 0) != valid {
			t.Fatalf("%s: %v", mode, errs)
		}
	}
}
