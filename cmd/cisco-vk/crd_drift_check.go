// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextcs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// expectedCRDFields is the per-CRD set of fields this binary requires
// at runtime. When a field is missing on the cluster's installed CRD
// the operator hasn't applied the chart's latest CRDs after a helm
// upgrade — Helm only applies files under crds/ on first install, so
// CRD-additive changes have to be patched manually.
//
// Adding a new entry here is the canonical signal that a release
// requires a CRD bump. Keep entries in (CRD-name, list-of-spec-or-
// status fields) form; the drift check inspects
// .spec.versions[0].schema.openAPIV3Schema.properties.{spec,status}
// .properties[<field>].
var expectedCRDFields = map[string][]crdField{
	"iosxeconfigs.config.cisco.vk": {
		{section: "spec", name: "confirmTimeoutSeconds"}, // Wave 10.2
		{section: "spec", name: "atomicReplace"},         // Wave 10.3
	},
	"iosxediagnostics.config.cisco.vk": {
		{section: "spec", name: "outputSink"},      // Phase D ConfigMap sink
		{section: "status", name: "commandCount"},  // commands printer column
		{section: "status", name: "nextCapture"},   // schedule printer column
	},
}

type crdField struct {
	section string // "spec" or "status"
	name    string
}

// checkCRDFieldDrift queries the live API server for each CRD in
// expectedCRDFields and returns a human-readable description of any
// missing fields. Returns "" when all expected fields are present.
//
// Non-fatal — the caller logs a warning and proceeds. Operators on
// stale clusters get a clear pointer to the fix
// (`kubectl apply -f charts/.../crds/`) without the manager binary
// refusing to start, which would be over-aggressive for a
// drift-not-error condition.
func checkCRDFieldDrift(cfg *rest.Config) string {
	cli, err := apiextcs.NewForConfig(cfg)
	if err != nil {
		// Discovery itself failed — surface as best-effort, don't
		// block startup.
		return ""
	}
	ctx := context.Background()
	var missing []string
	for crdName, expected := range expectedCRDFields {
		crd, err := cli.ApiextensionsV1().CustomResourceDefinitions().
			Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				missing = append(missing, fmt.Sprintf("CRD %q not installed", crdName))
				continue
			}
			// Permission or transport error — best-effort skip.
			continue
		}
		if len(crd.Spec.Versions) == 0 {
			continue
		}
		schema := crd.Spec.Versions[0].Schema
		if schema == nil || schema.OpenAPIV3Schema == nil {
			continue
		}
		root := schema.OpenAPIV3Schema
		for _, field := range expected {
			if !hasField(root, field.section, field.name) {
				missing = append(missing,
					fmt.Sprintf("CRD %q missing %s.%s", crdName, field.section, field.name))
			}
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, "; ")
}

func hasField(root *apiextv1.JSONSchemaProps, section, name string) bool {
	if root == nil {
		return false
	}
	parent, ok := root.Properties[section]
	if !ok {
		return false
	}
	_, has := parent.Properties[name]
	return has
}
