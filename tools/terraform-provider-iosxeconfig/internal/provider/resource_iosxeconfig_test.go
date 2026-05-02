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
	"encoding/json"
	"reflect"
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
	ktesting "k8s.io/client-go/testing"
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
		"metadata": map[string]any{
			"generation": int64(3),
		},
		"status": map[string]any{
			"observedGeneration": int64(3),
			"phase":              "InSync",
			"lastAppliedHash":    "sha256:abc",
			"sourceYangVersion":  "1791",
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

func TestUpdatePreservesControllerMetadata(t *testing.T) {
	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion("config.cisco.vk/v1alpha1")
	existing.SetKind("IOSXEConfig")
	existing.SetName("edge-01")
	existing.SetNamespace("network")
	existing.SetFinalizers([]string{"config.cisco.vk/lease-cleanup"})
	existing.SetLabels(map[string]string{"controller.cisco.vk/owned": "true"})
	existing.SetAnnotations(map[string]string{"controller.cisco.vk/annotation": "keep"})
	existing.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "cisco.vk/v1alpha1",
		Kind:       "CiscoDevice",
		Name:       "edge-01",
		UID:        "11111111-1111-1111-1111-111111111111",
	}})
	if err := unstructured.SetNestedField(existing.Object, map[string]any{"name": "edge-01"}, "spec", "deviceRef"); err != nil {
		t.Fatalf("seed spec.deviceRef: %v", err)
	}
	if err := unstructured.SetNestedStringSlice(existing.Object, []string{"vlan"}, "spec", "managedFamilies"); err != nil {
		t.Fatalf("seed spec.managedFamilies: %v", err)
	}

	r := &iosxeConfigResource{
		client: fakeDynamic(t, existing),
	}
	stored := existing.DeepCopy()
	r.client.(*dynfake.FakeDynamicClient).PrependReactor("patch", "iosxeconfigs", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction)
		var payload map[string]any
		if err := json.Unmarshal(patch.GetPatch(), &payload); err != nil {
			return true, nil, err
		}
		if spec, ok := payload["spec"]; ok {
			stored.Object["spec"] = spec
		}
		if apiVersion, ok := payload["apiVersion"].(string); ok {
			stored.SetAPIVersion(apiVersion)
		}
		if kind, ok := payload["kind"].(string); ok {
			stored.SetKind(kind)
		}
		return true, stored.DeepCopy(), nil
	})
	model := &IOSXEConfigResourceModel{
		Name:            types.StringValue("edge-01"),
		Namespace:       types.StringValue("network"),
		DeviceRef:       types.StringValue("edge-01"),
		ManagedFamilies: mustList(t, "vlan", "vrf"),
		SourceInline:    types.StringValue(`{"vlan":{"vlans":[{"id":10,"name":"users"}]}}`),
	}
	sink := &dummySink{}
	obj, err := r.toUnstructured(context.Background(), model, sink)
	if err != nil || sink.errs != nil {
		t.Fatalf("toUnstructured: err=%v sink=%v", err, sink.errs)
	}
	got, err := r.applyIOSXEConfig(context.Background(), "network", obj)
	if err != nil {
		t.Fatalf("applyIOSXEConfig: %v", err)
	}
	if !reflect.DeepEqual(got.GetFinalizers(), existing.GetFinalizers()) {
		t.Fatalf("finalizers=%v, want %v", got.GetFinalizers(), existing.GetFinalizers())
	}
	if !reflect.DeepEqual(got.GetOwnerReferences(), existing.GetOwnerReferences()) {
		t.Fatalf("ownerReferences=%v, want %v", got.GetOwnerReferences(), existing.GetOwnerReferences())
	}
	if got.GetLabels()["controller.cisco.vk/owned"] != "true" {
		t.Fatalf("labels=%v, want controller label preserved", got.GetLabels())
	}
	if got.GetAnnotations()["controller.cisco.vk/annotation"] != "keep" {
		t.Fatalf("annotations=%v, want controller annotation preserved", got.GetAnnotations())
	}
	families, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "managedFamilies")
	if !reflect.DeepEqual(families, []string{"vlan", "vrf"}) {
		t.Fatalf("managedFamilies=%v, want updated Terraform spec", families)
	}
}

func TestUpdateSuppressesStaleStatus(t *testing.T) {
	got := &unstructured.Unstructured{}
	got.SetUnstructuredContent(map[string]any{
		"metadata": map[string]any{
			"generation": int64(8),
		},
		"status": map[string]any{
			"observedGeneration": int64(7),
			"phase":              "InSync",
			"lastAppliedHash":    "sha256:stale",
			"sourceYangVersion":  "1791",
		},
	})
	model := &IOSXEConfigResourceModel{}
	(&iosxeConfigResource{}).refreshFromCluster(context.Background(), model, got)
	if model.Phase.ValueString() != reconcilePendingPhase {
		t.Fatalf("phase=%q, want %q", model.Phase.ValueString(), reconcilePendingPhase)
	}
	if !model.LastAppliedHash.IsNull() {
		t.Fatalf("lastAppliedHash=%q, want null while observedGeneration is stale", model.LastAppliedHash.ValueString())
	}

	if err := unstructured.SetNestedField(got.Object, int64(8), "status", "observedGeneration"); err != nil {
		t.Fatalf("set observedGeneration current: %v", err)
	}
	(&iosxeConfigResource{}).refreshFromCluster(context.Background(), model, got)
	if model.Phase.ValueString() != "InSync" {
		t.Fatalf("phase after observedGeneration catches up=%q, want InSync", model.Phase.ValueString())
	}
	if model.LastAppliedHash.ValueString() != "sha256:stale" {
		t.Fatalf("lastAppliedHash after observedGeneration catches up=%q", model.LastAppliedHash.ValueString())
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
