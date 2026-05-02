// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	gvrschema "k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
)

// fakeDynamic builds a fake dynamic client knowing about
// IOSXEConfig list shape so List/Get/Create round-trip cleanly.
func fakeDynamic(t *testing.T, objs ...runtime.Object) *dynfake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[gvrschema.GroupVersionResource]string{
		iosxeConfigGVR: "IOSXEConfigList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func mustList(t *testing.T, ss ...string) types.List {
	t.Helper()
	elems := make([]attr.Value, 0, len(ss))
	for _, s := range ss {
		elems = append(elems, types.StringValue(s))
	}
	out, dgs := types.ListValue(types.StringType, elems)
	if dgs.HasError() {
		t.Fatalf("types.ListValue: %v", dgs)
	}
	return out
}

func TestSplitNamespacedNameWellFormed(t *testing.T) {
	ns, name, ok := splitNamespacedName("network/edge-01")
	if !ok || ns != "network" || name != "edge-01" {
		t.Errorf("got %q/%q ok=%v", ns, name, ok)
	}
}

func TestSplitNamespacedNameRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", "/", "no-slash", "/missing-ns", "missing-name/"} {
		if _, _, ok := splitNamespacedName(raw); ok {
			t.Errorf("splitNamespacedName(%q) returned ok=true", raw)
		}
	}
}

func TestNamespaceForRespectsResourceModelOverProviderDefault(t *testing.T) {
	r := &iosxeConfigResource{defaultNS: "network"}
	m := &IOSXEConfigResourceModel{Namespace: types.StringValue("override")}
	if got := r.namespaceFor(m); got != "override" {
		t.Errorf("got %q, want override", got)
	}
}

func TestNamespaceForFallsBackToProviderDefault(t *testing.T) {
	r := &iosxeConfigResource{defaultNS: "network"}
	m := &IOSXEConfigResourceModel{Namespace: types.StringNull()}
	if got := r.namespaceFor(m); got != "network" {
		t.Errorf("got %q, want network", got)
	}
}

func TestNamespaceForFallsBackToDefault(t *testing.T) {
	r := &iosxeConfigResource{}
	m := &IOSXEConfigResourceModel{Namespace: types.StringNull()}
	if got := r.namespaceFor(m); got != "default" {
		t.Errorf("got %q, want default", got)
	}
}

// dummySink captures diagnostics in tests without pulling in the
// framework's full Diagnostics machinery.
type dummySink struct {
	errs []string
}

func (d *dummySink) AddError(summary, detail string) {
	d.errs = append(d.errs, summary+": "+detail)
}

func TestToUnstructuredAssemblesValidShape(t *testing.T) {
	r := &iosxeConfigResource{}
	m := &IOSXEConfigResourceModel{
		Name:            types.StringValue("edge-01"),
		Namespace:       types.StringValue("network"),
		DeviceRef:       types.StringValue("edge-01"),
		ManagedFamilies: mustList(t, "vlan", "vrf"),
		SourceInline:    types.StringValue(`{"vlan":{"vlans":[{"id":10,"name":"users"}]}}`),
		WriteStartup:    types.BoolValue(true),
	}
	sink := &dummySink{}
	got, err := r.toUnstructured(context.Background(), m, sink)
	if err != nil || sink.errs != nil {
		t.Fatalf("toUnstructured: err=%v sink=%v", err, sink.errs)
	}
	if got.GetAPIVersion() != "config.cisco.vk/v1alpha1" {
		t.Errorf("apiVersion=%q", got.GetAPIVersion())
	}
	families, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "managedFamilies")
	if len(families) != 2 || families[0] != "vlan" || families[1] != "vrf" {
		t.Errorf("managedFamilies=%v", families)
	}
	dr, _, _ := unstructured.NestedString(got.Object, "spec", "deviceRef", "name")
	if dr != "edge-01" {
		t.Errorf("deviceRef.name=%q", dr)
	}
	ws, _, _ := unstructured.NestedBool(got.Object, "spec", "writeStartup")
	if !ws {
		t.Errorf("writeStartup not propagated")
	}
}

func TestToUnstructuredRejectsBadYAML(t *testing.T) {
	r := &iosxeConfigResource{}
	m := &IOSXEConfigResourceModel{
		Name:            types.StringValue("edge-01"),
		DeviceRef:       types.StringValue("edge-01"),
		ManagedFamilies: mustList(t, "vlan"),
		SourceInline:    types.StringValue(":\n   not yaml ::"),
	}
	sink := &dummySink{}
	_, err := r.toUnstructured(context.Background(), m, sink)
	if err == nil || len(sink.errs) == 0 {
		t.Fatalf("expected parse error; got err=%v sink=%v", err, sink.errs)
	}
	if !strings.Contains(sink.errs[0], "parse source_inline failed") {
		t.Errorf("error did not mention source_inline: %v", sink.errs)
	}
}

func TestRefreshFromClusterPullsStatusFields(t *testing.T) {
	got := &unstructured.Unstructured{}
	got.SetUnstructuredContent(map[string]any{
		"status": map[string]any{
			"phase":             "InSync",
			"lastAppliedHash":   "sha256:abc",
			"sourceYangVersion": "1791",
		},
	})
	m := &IOSXEConfigResourceModel{}
	(&iosxeConfigResource{}).refreshFromCluster(context.Background(), m, got)
	if m.Phase.ValueString() != "InSync" {
		t.Errorf("phase=%q", m.Phase.ValueString())
	}
	if m.LastAppliedHash.ValueString() != "sha256:abc" {
		t.Errorf("hash=%q", m.LastAppliedHash.ValueString())
	}
	if m.SourceYangVersion.ValueString() != "1791" {
		t.Errorf("yang=%q", m.SourceYangVersion.ValueString())
	}
}

func TestImportStateSplitsNamespacedID(t *testing.T) {
	// Ensures the import path validates the format up-front so
	// `terraform import iosxeconfig_config.foo bare-name` fails
	// loud rather than silently writing to the default namespace.
	if _, _, ok := splitNamespacedName("network/edge-01"); !ok {
		t.Fatal("well-formed ID rejected")
	}
	if _, _, ok := splitNamespacedName("bare-name"); ok {
		t.Fatal("bare name accepted; expected rejection")
	}
}

// Compile-time guard the framework's diag.Diagnostics also
// implements DiagnosticsSink — so the production AddError path
// uses it transparently.
var _ DiagnosticsSink = (*diag.Diagnostics)(nil)

// Suppress the linter complaint about metav1's only being used for
// future test additions (Get with options, etc.).
var _ = metav1.GetOptions{}
