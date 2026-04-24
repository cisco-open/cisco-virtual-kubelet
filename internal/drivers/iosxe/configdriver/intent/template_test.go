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

package intent

import (
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func mkTemplate(name, body string, params ...configv1alpha1.TemplateParameter) *configv1alpha1.IOSXETemplate {
	return &configv1alpha1.IOSXETemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network"},
		Spec: configv1alpha1.IOSXETemplateSpec{
			Parameters:    params,
			Configuration: runtime.RawExtension{Raw: []byte(body)},
		},
	}
}

func TestExpandTemplateHappyPath(t *testing.T) {
	tpl := mkTemplate("uplink",
		`{"interface_ethernet":{"interfaces":[{"name":"{{ .interface }}","description":"{{ .description }}"}]}}`,
		configv1alpha1.TemplateParameter{Name: "interface", Type: configv1alpha1.TemplateParameterString, Required: true},
		configv1alpha1.TemplateParameter{Name: "description", Type: configv1alpha1.TemplateParameterString, Default: "Uplink"},
	)
	got, err := ExpandTemplate(tpl, map[string]string{"interface": "0/0/0"})
	if err != nil {
		t.Fatalf("ExpandTemplate: %v", err)
	}
	want := map[string]any{
		"interface_ethernet": map[string]any{
			"interfaces": []any{
				map[string]any{"name": "0/0/0", "description": "Uplink"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestExpandTemplateMissingRequired(t *testing.T) {
	tpl := mkTemplate("uplink",
		`{"x":"{{ .interface }}"}`,
		configv1alpha1.TemplateParameter{Name: "interface", Type: configv1alpha1.TemplateParameterString, Required: true},
	)
	_, err := ExpandTemplate(tpl, nil)
	if err == nil || !strings.Contains(err.Error(), "required parameter") {
		t.Fatalf("got %v, want required-parameter error", err)
	}
}

func TestExpandTemplateTypeValidation(t *testing.T) {
	cases := []struct {
		name  string
		pType configv1alpha1.TemplateParameterType
		value string
		ok    bool
	}{
		{"int-ok", configv1alpha1.TemplateParameterInt, "1500", true},
		{"int-bad", configv1alpha1.TemplateParameterInt, "fifteen", false},
		{"ipv4-ok", configv1alpha1.TemplateParameterIPv4, "10.0.0.1", true},
		{"ipv4-bad-v6", configv1alpha1.TemplateParameterIPv4, "::1", false},
		{"ipv6-ok", configv1alpha1.TemplateParameterIPv6, "2001:db8::1", true},
		{"cidr-ok", configv1alpha1.TemplateParameterCIDR, "10.0.0.0/24", true},
		{"cidr-bad", configv1alpha1.TemplateParameterCIDR, "10.0.0.0", false},
		{"bool-ok", configv1alpha1.TemplateParameterBool, "true", true},
		{"bool-bad", configv1alpha1.TemplateParameterBool, "yes-maybe", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := mkTemplate("t",
				`{"k":"{{ .v }}"}`,
				configv1alpha1.TemplateParameter{Name: "v", Type: tc.pType, Required: true},
			)
			_, err := ExpandTemplate(tpl, map[string]string{"v": tc.value})
			if (err == nil) != tc.ok {
				t.Fatalf("value=%q err=%v, want ok=%v", tc.value, err, tc.ok)
			}
		})
	}
}

func TestExpandTemplateUnknownParameter(t *testing.T) {
	tpl := mkTemplate("t", `{"k":"v"}`)
	_, err := ExpandTemplate(tpl, map[string]string{"extra": "1"})
	if err == nil || !strings.Contains(err.Error(), "undeclared parameter") {
		t.Fatalf("got %v, want undeclared-parameter error", err)
	}
}

func TestExpandTemplateMissingKeyFailsLoud(t *testing.T) {
	// A leaf referring to a parameter the declared-and-resolved map
	// doesn't contain must error. Without missingkey=error, text/template
	// would silently substitute "<no value>", which is a worst-case
	// failure mode at the device.
	tpl := mkTemplate("t",
		`{"k":"{{ .nope }}"}`,
		configv1alpha1.TemplateParameter{Name: "maybe", Type: configv1alpha1.TemplateParameterString, Default: "x"},
	)
	_, err := ExpandTemplate(tpl, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v, want missingkey error referencing 'nope'", err)
	}
}

func TestExpandTemplateNoDelimsIsIdentity(t *testing.T) {
	tpl := mkTemplate("t", `{"k":"literal"}`)
	got, err := ExpandTemplate(tpl, nil)
	if err != nil {
		t.Fatalf("ExpandTemplate: %v", err)
	}
	if got["k"] != "literal" {
		t.Fatalf("got %v", got)
	}
}

func TestExpandTemplateDefaultsToDataModel(t *testing.T) {
	// spec.type unset should behave as data-model (backward compat;
	// existing templates authored before the field was introduced
	// keep working without a CR edit).
	tpl := mkTemplate("t", `{"k":"literal"}`)
	got, err := ExpandTemplate(tpl, nil)
	if err != nil {
		t.Fatalf("ExpandTemplate: %v", err)
	}
	if got["k"] != "literal" {
		t.Fatalf("got %v", got)
	}
}

func TestExpandTemplateCLITypeRejected(t *testing.T) {
	tpl := mkTemplate("t", `hostname {{ .hostname }}`,
		configv1alpha1.TemplateParameter{Name: "hostname", Type: configv1alpha1.TemplateParameterString, Required: true},
	)
	tpl.Spec.Type = configv1alpha1.CLITemplate

	_, err := ExpandTemplate(tpl, map[string]string{"hostname": "edge-01"})
	if err == nil || !strings.Contains(err.Error(), "spec.type=cli is not yet supported") {
		t.Fatalf("got %v, want cli-not-supported error", err)
	}
	// Make sure the error points operators at the feedback doc so the
	// gap is discoverable without grepping source.
	if !strings.Contains(err.Error(), "feedback 3b") {
		t.Errorf("error message should reference feedback 3b for findability:\n%s", err.Error())
	}
}

func TestExpandTemplateExplicitDataModelType(t *testing.T) {
	// Explicit spec.type=data-model must behave identically to the
	// default empty value.
	tpl := mkTemplate("t", `{"k":"literal"}`)
	tpl.Spec.Type = configv1alpha1.DataModelTemplate

	got, err := ExpandTemplate(tpl, nil)
	if err != nil {
		t.Fatalf("ExpandTemplate: %v", err)
	}
	if got["k"] != "literal" {
		t.Fatalf("got %v", got)
	}
}

func TestExpandTemplateUnknownTypeRejected(t *testing.T) {
	// kubebuilder enum validation catches this at the API server, but
	// the expander's defence-in-depth check keeps unit tests and
	// in-process callers honest.
	tpl := mkTemplate("t", `{"k":"v"}`)
	tpl.Spec.Type = configv1alpha1.TemplateKind("nonsense")

	_, err := ExpandTemplate(tpl, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown spec.type") {
		t.Fatalf("got %v, want unknown-type error", err)
	}
}
