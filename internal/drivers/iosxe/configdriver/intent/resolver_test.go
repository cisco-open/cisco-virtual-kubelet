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
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func resolverScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core AddToScheme: %v", err)
	}
	if err := ciskov1.AddToScheme(s); err != nil {
		t.Fatalf("ciskov1 AddToScheme: %v", err)
	}
	if err := configv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("configv1alpha1 AddToScheme: %v", err)
	}
	return s
}

func mkDevice(name string, labels map[string]string) *ciskov1.CiscoDevice {
	return &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "network",
			Labels:    labels,
		},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverXE,
			Address:  "192.0.2.10",
			Username: "admin",
		},
	}
}

func mkDefaults(name string, body string) *configv1alpha1.IOSXEConfigDefaults {
	return &configv1alpha1.IOSXEConfigDefaults{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: configv1alpha1.IOSXEConfigDefaultsSpec{
			Configuration: runtime.RawExtension{Raw: []byte(body)},
		},
	}
}

func mkGroup(name string, selector *metav1.LabelSelector, body string) *configv1alpha1.IOSXEDeviceGroupConfig {
	return &configv1alpha1.IOSXEDeviceGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network"},
		Spec: configv1alpha1.IOSXEDeviceGroupConfigSpec{
			DeviceSelector: selector,
			Configuration:  runtime.RawExtension{Raw: []byte(body)},
		},
	}
}

func mkCR(name, device string, managed []string, inline string) *configv1alpha1.IOSXEConfig {
	return &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network", Generation: 1},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: device},
			ManagedFamilies: managed,
			Source: configv1alpha1.ConfigurationSource{
				Inline: &runtime.RawExtension{Raw: []byte(inline)},
			},
		},
	}
}

func TestResolveScopePrecedence(t *testing.T) {
	t.Parallel()
	device := mkDevice("edge-01", map[string]string{"role": "access-switch"})
	defaults := mkDefaults("default",
		`{"system":{"login_on_failure":true,"mtu":1500}}`)
	group := mkGroup("access-switches",
		&metav1.LabelSelector{MatchLabels: map[string]string{"role": "access-switch"}},
		`{"system":{"mtu":9000},"vlan":{"vlans":[{"id":10,"name":"users"}]}}`)
	cr := mkCR("edge-01", "edge-01",
		[]string{"system", "vlan"},
		`{"system":{"hostname":"edge-01"},"vlan":{"vlans":[{"id":10,"name":"USERS"}]}}`)
	cr.Spec.DeviceGroups = []string{"access-switches"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, defaults, group, cr).
		Build()

	r := &Resolver{Client: c, KeyRules: KeyRules{"vlan.vlans": "id"}}
	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := map[string]any{
		"system": map[string]any{
			"login_on_failure": true,
			"mtu":              float64(9000),
			"hostname":         "edge-01",
		},
		"vlan": map[string]any{
			"vlans": []any{
				map[string]any{"id": float64(10), "name": "USERS"},
			},
		},
	}
	if !reflect.DeepEqual(got.Configuration, want) {
		t.Fatalf("configuration =\n%#v\nwant\n%#v", got.Configuration, want)
	}
	if got.DriftPolicy != configv1alpha1.DriftPolicyRevert {
		t.Errorf("DriftPolicy default = %q, want revert", got.DriftPolicy)
	}
}

func TestResolveRejectsDeviceNotInGroup(t *testing.T) {
	t.Parallel()
	device := mkDevice("core-01", map[string]string{"role": "core"})
	group := mkGroup("access-switches",
		&metav1.LabelSelector{MatchLabels: map[string]string{"role": "access-switch"}},
		`{"system":{}}`)
	cr := mkCR("core-01", "core-01", []string{"system"}, `{}`)
	cr.Spec.DeviceGroups = []string{"access-switches"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, group, cr).
		Build()
	r := &Resolver{Client: c}

	_, err := r.Resolve(context.Background(), cr)
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("got %v, want not-a-member error", err)
	}
}

func TestResolveMissingDevice(t *testing.T) {
	t.Parallel()
	cr := mkCR("x", "does-not-exist", []string{"system"}, `{}`)
	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(cr).
		Build()
	r := &Resolver{Client: c}

	_, err := r.Resolve(context.Background(), cr)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v, want not-found error", err)
	}
}

func TestResolveWithTemplateExpansion(t *testing.T) {
	t.Parallel()
	device := mkDevice("edge-01", nil)
	tpl := &configv1alpha1.IOSXETemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "uplink", Namespace: "network"},
		Spec: configv1alpha1.IOSXETemplateSpec{
			Parameters: []configv1alpha1.TemplateParameter{
				{Name: "iface", Type: configv1alpha1.TemplateParameterString, Required: true},
			},
			Configuration: runtime.RawExtension{Raw: []byte(
				`{"interface_ethernet":{"interfaces":[{"name":"{{ .iface }}"}]}}`,
			)},
		},
	}
	cr := mkCR("edge-01", "edge-01", []string{"interface_ethernet"}, `{}`)
	cr.Spec.TemplateRefs = []configv1alpha1.TemplateRef{{
		Name:   "uplink",
		Values: &runtime.RawExtension{Raw: []byte(`{"iface":"0/0/0"}`)},
	}}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, tpl, cr).
		Build()
	r := &Resolver{Client: c}

	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ifaces := got.Configuration["interface_ethernet"].(map[string]any)["interfaces"].([]any)
	name := ifaces[0].(map[string]any)["name"]
	if name != "0/0/0" {
		t.Fatalf("expanded name = %v, want 0/0/0", name)
	}
}

func TestResolveInterfaceGroupExpansion(t *testing.T) {
	t.Parallel()
	device := mkDevice("edge-01", map[string]string{"role": "access-switch"})
	group := &configv1alpha1.IOSXEInterfaceGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "access-uplinks", Namespace: "network"},
		Spec: configv1alpha1.IOSXEInterfaceGroupConfigSpec{
			DeviceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "access-switch"},
			},
			InterfaceSelector: []configv1alpha1.InterfaceMatch{
				{Type: "GigabitEthernet", Name: "0/0/0"},
				{Type: "GigabitEthernet", Name: "0/0/1"},
			},
			Configuration: runtime.RawExtension{Raw: []byte(
				`{"interface_ethernet":{"interfaces":[{"description":"uplink","shutdown":false}]}}`,
			)},
		},
	}
	cr := mkCR("edge-01", "edge-01", []string{"interface_ethernet"}, `{}`)
	cr.Spec.InterfaceGroups = []string{"access-uplinks"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, group, cr).
		Build()
	r := &Resolver{Client: c}

	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ifaces := got.Configuration["interface_ethernet"].(map[string]any)["interfaces"].([]any)
	if len(ifaces) != 2 {
		t.Fatalf("got %d projected interfaces, want 2:\n%#v", len(ifaces), ifaces)
	}
	// Every projected entry must carry both type and name from the
	// selector.
	seen := map[string]bool{}
	for _, e := range ifaces {
		m := e.(map[string]any)
		key := m["type"].(string) + "/" + m["name"].(string)
		seen[key] = true
		if m["description"] != "uplink" {
			t.Errorf("entry %s missing inherited description: %#v", key, m)
		}
	}
	if !seen["GigabitEthernet/0/0/0"] || !seen["GigabitEthernet/0/0/1"] {
		t.Errorf("missing expected projections: %v", seen)
	}
}

func TestResolveInterfaceGroupNamePatternMatchesDeclared(t *testing.T) {
	t.Parallel()
	// The CR declares three GigabitEthernet interfaces (0/0/0, 0/0/1,
	// 0/0/2). A pattern-based group targets "0/0/[01]" — it should
	// project onto the first two and leave the third alone, *and*
	// preserve any per-interface attributes from the source body
	// (description on 0/0/2 must survive).
	device := mkDevice("edge-01", map[string]string{"role": "access-switch"})
	group := &configv1alpha1.IOSXEInterfaceGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "uplinks-pattern", Namespace: "network"},
		Spec: configv1alpha1.IOSXEInterfaceGroupConfigSpec{
			DeviceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "access-switch"},
			},
			InterfaceSelector: []configv1alpha1.InterfaceMatch{
				{Type: "GigabitEthernet", NamePattern: "0/0/[01]"},
			},
			Configuration: runtime.RawExtension{Raw: []byte(
				`{"interface_ethernet":{"interfaces":[{"description":"uplink"}]}}`,
			)},
		},
	}
	source := `{"interface_ethernet":{"interfaces":[
		{"type":"GigabitEthernet","name":"0/0/0"},
		{"type":"GigabitEthernet","name":"0/0/1"},
		{"type":"GigabitEthernet","name":"0/0/2","description":"server"}
	]}}`
	cr := mkCR("edge-01", "edge-01", []string{"interface_ethernet"}, source)
	cr.Spec.InterfaceGroups = []string{"uplinks-pattern"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, group, cr).
		Build()
	r := &Resolver{Client: c}

	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ifaces := got.Configuration["interface_ethernet"].(map[string]any)["interfaces"].([]any)
	if len(ifaces) != 3 {
		t.Fatalf("got %d interfaces, want 3 (the declared set):\n%#v", len(ifaces), ifaces)
	}
	desc := map[string]string{}
	for _, e := range ifaces {
		m := e.(map[string]any)
		name := m["name"].(string)
		if d, ok := m["description"].(string); ok {
			desc[name] = d
		}
	}
	if desc["0/0/0"] != "uplink" || desc["0/0/1"] != "uplink" {
		t.Errorf("pattern projection missing on matched interfaces: %#v", desc)
	}
	if desc["0/0/2"] != "server" {
		t.Errorf("non-matching interface lost its source description: %#v", desc)
	}
}

func TestResolveInterfaceGroupNamePatternNoMatchesIsNoOp(t *testing.T) {
	t.Parallel()
	// Pattern that matches nothing in the resolved intent must not
	// fail — operators commonly target a regex hopeful no-op-it
	// before adding interfaces. Resolution stays clean.
	device := mkDevice("edge-01", map[string]string{"role": "access-switch"})
	group := &configv1alpha1.IOSXEInterfaceGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "no-matches", Namespace: "network"},
		Spec: configv1alpha1.IOSXEInterfaceGroupConfigSpec{
			DeviceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "access-switch"},
			},
			InterfaceSelector: []configv1alpha1.InterfaceMatch{
				{Type: "GigabitEthernet", NamePattern: "9/9/.*"},
			},
			Configuration: runtime.RawExtension{Raw: []byte(
				`{"interface_ethernet":{"interfaces":[{"description":"never"}]}}`,
			)},
		},
	}
	source := `{"interface_ethernet":{"interfaces":[
		{"type":"GigabitEthernet","name":"0/0/0"}
	]}}`
	cr := mkCR("edge-01", "edge-01", []string{"interface_ethernet"}, source)
	cr.Spec.InterfaceGroups = []string{"no-matches"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, group, cr).
		Build()
	r := &Resolver{Client: c}
	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ifaces := got.Configuration["interface_ethernet"].(map[string]any)["interfaces"].([]any)
	for _, e := range ifaces {
		if e.(map[string]any)["description"] == "never" {
			t.Fatalf("pattern projected onto unintended interface: %#v", e)
		}
	}
}

func TestResolveInterfaceGroupNamePatternAnchoredOnBothEnds(t *testing.T) {
	t.Parallel()
	// "0/0/0" must not match "0/0/0Bar" or "X0/0/0" — the resolver
	// anchors the operator's pattern with ^…$ so accidental
	// substring matches don't sneak past.
	device := mkDevice("edge-01", map[string]string{"role": "access-switch"})
	group := &configv1alpha1.IOSXEInterfaceGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "anchor-test", Namespace: "network"},
		Spec: configv1alpha1.IOSXEInterfaceGroupConfigSpec{
			DeviceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "access-switch"},
			},
			InterfaceSelector: []configv1alpha1.InterfaceMatch{
				{Type: "GigabitEthernet", NamePattern: "0/0/0"},
			},
			Configuration: runtime.RawExtension{Raw: []byte(
				`{"interface_ethernet":{"interfaces":[{"description":"uplink"}]}}`,
			)},
		},
	}
	source := `{"interface_ethernet":{"interfaces":[
		{"type":"GigabitEthernet","name":"0/0/0"},
		{"type":"GigabitEthernet","name":"0/0/0Bar"}
	]}}`
	cr := mkCR("edge-01", "edge-01", []string{"interface_ethernet"}, source)
	cr.Spec.InterfaceGroups = []string{"anchor-test"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, group, cr).
		Build()
	r := &Resolver{Client: c}
	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ifaces := got.Configuration["interface_ethernet"].(map[string]any)["interfaces"].([]any)
	for _, e := range ifaces {
		m := e.(map[string]any)
		if m["name"] == "0/0/0Bar" && m["description"] == "uplink" {
			t.Fatalf("unanchored pattern leaked onto sibling: %#v", m)
		}
	}
}

func TestResolveSecretRefMergesIntoFamily(t *testing.T) {
	t.Parallel()
	// SecretRefs are the path for credentials that must not live
	// in a ConfigMap or git-tracked YAML. The resolver loads the
	// Secret, parses the named key as a YAML/JSON snippet, and
	// merges it under the family root. Secret material must win
	// against any placeholder in the per-device source — that's
	// the whole point of the feature.
	device := mkDevice("edge-01", nil)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bgp-creds", Namespace: "network"},
		Data: map[string][]byte{
			"bgp.yaml": []byte(`{"neighbors":[{"asn":65001,"password":"realsecret"}]}`),
		},
	}
	source := `{"bgp":{"neighbors":[{"asn":65001,"password":"placeholder"}]}}`
	cr := mkCR("edge-01", "edge-01", []string{"bgp"}, source)
	cr.Spec.SecretRefs = []configv1alpha1.FamilySecretRef{{
		Family: "bgp",
		Name:   "bgp-creds",
		Key:    "bgp.yaml",
	}}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, secret, cr).
		Build()
	r := &Resolver{Client: c, KeyRules: KeyRules{}}

	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	bgp, ok := got.Configuration["bgp"].(map[string]any)
	if !ok {
		t.Fatalf("no bgp family: %#v", got.Configuration)
	}
	neighbors, ok := bgp["neighbors"].([]any)
	if !ok || len(neighbors) == 0 {
		t.Fatalf("no neighbors: %#v", bgp)
	}
	first := neighbors[0].(map[string]any)
	if pw, _ := first["password"].(string); pw != "realsecret" {
		t.Errorf("secret value did not win against placeholder: password=%q", pw)
	}
}

func TestResolveSecretRefRejectsUnmanagedFamily(t *testing.T) {
	t.Parallel()
	// A typo in spec.secretRefs[].family must fail loud — silent
	// no-ops would leave credentials out of the apply and the
	// operator would get an unauthenticated session that's not
	// obviously the result of a typo.
	device := mkDevice("edge-01", nil)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "network"},
		Data:       map[string][]byte{"x.yaml": []byte(`{}`)},
	}
	cr := mkCR("edge-01", "edge-01", []string{"bgp"}, `{}`)
	cr.Spec.SecretRefs = []configv1alpha1.FamilySecretRef{{
		Family: "snmp_server", // not in managedFamilies
		Name:   "creds",
		Key:    "x.yaml",
	}}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, secret, cr).
		Build()
	r := &Resolver{Client: c}

	_, err := r.Resolve(context.Background(), cr)
	if err == nil || !strings.Contains(err.Error(), "not in managedFamilies") {
		t.Fatalf("got %v, want managed-families rejection", err)
	}
}

func TestResolveSecretRefMissingKeyFailsLoud(t *testing.T) {
	t.Parallel()
	// Missing key inside the Secret must error — better to halt
	// the reconcile than apply a config that's quietly missing
	// the credential it referenced.
	device := mkDevice("edge-01", nil)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "network"},
		Data:       map[string][]byte{"other": []byte(`{}`)},
	}
	cr := mkCR("edge-01", "edge-01", []string{"bgp"}, `{}`)
	cr.Spec.SecretRefs = []configv1alpha1.FamilySecretRef{{
		Family: "bgp",
		Name:   "creds",
		Key:    "missing",
	}}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, secret, cr).
		Build()
	r := &Resolver{Client: c}

	_, err := r.Resolve(context.Background(), cr)
	if err == nil || !strings.Contains(err.Error(), `key "missing"`) {
		t.Fatalf("got %v, want missing-key error", err)
	}
}

func TestResolveInterfaceGroupSkipsNonMemberDevice(t *testing.T) {
	t.Parallel()
	// Device doesn't match the selector — group should be silently
	// skipped, not fail resolution.
	device := mkDevice("core-01", map[string]string{"role": "core"})
	group := &configv1alpha1.IOSXEInterfaceGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "access-uplinks", Namespace: "network"},
		Spec: configv1alpha1.IOSXEInterfaceGroupConfigSpec{
			DeviceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "access-switch"},
			},
			InterfaceSelector: []configv1alpha1.InterfaceMatch{
				{Type: "GigabitEthernet", Name: "0/0/0"},
			},
			Configuration: runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}
	cr := mkCR("core-01", "core-01", []string{"interface_ethernet"}, `{}`)
	cr.Spec.InterfaceGroups = []string{"access-uplinks"}

	c := fake.NewClientBuilder().
		WithScheme(resolverScheme(t)).
		WithObjects(device, group, cr).
		Build()
	r := &Resolver{Client: c}

	// Should resolve without error; no interface_ethernet entries
	// contributed by the group.
	got, err := r.Resolve(context.Background(), cr)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fam, ok := got.Configuration["interface_ethernet"].(map[string]any); ok {
		if ifs, ok := fam["interfaces"].([]any); ok && len(ifs) > 0 {
			t.Fatalf("non-member device received projections: %#v", ifs)
		}
	}
}
