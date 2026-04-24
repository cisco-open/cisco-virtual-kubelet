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

// TestExpandTemplateRoutesCLIToOtherEntrypoint verifies the data-model
// entrypoint refuses CLI-type templates with a clear pointer to
// the CLI entrypoint. Replaces an earlier test that asserted "cli
// not yet supported" — CLI is now supported, via ExpandCLITemplate.
func TestExpandTemplateRoutesCLIToOtherEntrypoint(t *testing.T) {
	tpl := mkTemplate("t", `hostname {{ .hostname }}`,
		configv1alpha1.TemplateParameter{Name: "hostname", Type: configv1alpha1.TemplateParameterString, Required: true},
	)
	tpl.Spec.Type = configv1alpha1.CLITemplate

	_, err := ExpandTemplate(tpl, map[string]string{"hostname": "edge-01"})
	if err == nil || !strings.Contains(err.Error(), "use ExpandCLITemplate") {
		t.Fatalf("got %v, want 'use ExpandCLITemplate' pointer", err)
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

// ----- CLI template render tests (ExpandCLITemplate) -----

func TestExpandCLITemplateRendersParams(t *testing.T) {
	tpl := &configv1alpha1.IOSXETemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "lo", Namespace: "network"},
		Spec: configv1alpha1.IOSXETemplateSpec{
			Type: configv1alpha1.CLITemplate,
			Parameters: []configv1alpha1.TemplateParameter{
				{Name: "id", Type: configv1alpha1.TemplateParameterInt, Required: true},
				{Name: "ip", Type: configv1alpha1.TemplateParameterIPv4, Required: true},
			},
			// Body is a YAML string — the RawExtension
			// decoding accepts plain JSON string literals too;
			// we use the YAML literal block shape here for
			// readability.
			Configuration: runtime.RawExtension{Raw: []byte(
				`"interface Loopback{{ .id }}\n ip address {{ .ip }} 255.255.255.255\n no shutdown"`)},
		},
	}
	got, err := ExpandCLITemplate(tpl, map[string]string{"id": "100", "ip": "10.0.0.1"})
	if err != nil {
		t.Fatalf("ExpandCLITemplate: %v", err)
	}
	want := "interface Loopback100\n ip address 10.0.0.1 255.255.255.255\n no shutdown"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestExpandCLITemplateRejectsDataModelType(t *testing.T) {
	tpl := mkTemplate("t", `{"k":"v"}`)
	// spec.type unset — defaults to data-model.
	_, err := ExpandCLITemplate(tpl, nil)
	if err == nil || !strings.Contains(err.Error(), "expected cli") {
		t.Fatalf("got %v, want expected-cli error", err)
	}
}

func TestExpandCLITemplateMappingShape(t *testing.T) {
	// {"cli": "..."} body is the structured alternative to a
	// bare string; useful when operators want metadata alongside
	// the CLI without a breaking change later.
	tpl := &configv1alpha1.IOSXETemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "lo", Namespace: "network"},
		Spec: configv1alpha1.IOSXETemplateSpec{
			Type: configv1alpha1.CLITemplate,
			Configuration: runtime.RawExtension{Raw: []byte(
				`{"cli": "hostname edge-01"}`)},
		},
	}
	got, err := ExpandCLITemplate(tpl, nil)
	if err != nil {
		t.Fatalf("ExpandCLITemplate: %v", err)
	}
	if got != "hostname edge-01" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandCLITemplateEmptyBodyRejected(t *testing.T) {
	tpl := &configv1alpha1.IOSXETemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "network"},
		Spec: configv1alpha1.IOSXETemplateSpec{
			Type:          configv1alpha1.CLITemplate,
			Configuration: runtime.RawExtension{},
		},
	}
	_, err := ExpandCLITemplate(tpl, nil)
	if err == nil || !strings.Contains(err.Error(), "empty configuration body") {
		t.Fatalf("got %v", err)
	}
}

// The original "cli rejected as not yet supported" test is
// obsolete now that CLI templates work. Replace with a test that
// pins the correct handoff between ExpandTemplate (data-model)
// and ExpandCLITemplate (cli).
func TestExpandTemplateCLITypeRoutesToCLIExpander(t *testing.T) {
	tpl := mkTemplate("t", `{"k":"v"}`)
	tpl.Spec.Type = configv1alpha1.CLITemplate

	_, err := ExpandTemplate(tpl, nil)
	if err == nil || !strings.Contains(err.Error(), "use ExpandCLITemplate") {
		t.Fatalf("got %v, want 'use ExpandCLITemplate' pointer", err)
	}
}
