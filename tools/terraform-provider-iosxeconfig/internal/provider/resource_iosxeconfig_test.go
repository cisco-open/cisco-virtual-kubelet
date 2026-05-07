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
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

func newResourceState(t *testing.T) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var schemaResp frameworkresource.SchemaResponse
	(&iosxeConfigResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("Schema diagnostics: %v", schemaResp.Diagnostics)
	}
	return tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
}

func newResourcePlan(t *testing.T) tfsdk.Plan {
	t.Helper()
	ctx := context.Background()
	var schemaResp frameworkresource.SchemaResponse
	(&iosxeConfigResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("Schema diagnostics: %v", schemaResp.Diagnostics)
	}
	return tfsdk.Plan{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
}

func stateForModel(t *testing.T, model *IOSXEConfigResourceModel) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	state := newResourceState(t)
	if diags := state.Set(ctx, model); diags.HasError() {
		t.Fatalf("state.Set diagnostics: %v", diags)
	}
	return state
}

func planForModel(t *testing.T, model *IOSXEConfigResourceModel) tfsdk.Plan {
	t.Helper()
	ctx := context.Background()
	plan := newResourcePlan(t)
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatalf("plan.Set diagnostics: %v", diags)
	}
	return plan
}

func readResourceForTest(t *testing.T, r *iosxeConfigResource, model *IOSXEConfigResourceModel) IOSXEConfigResourceModel {
	t.Helper()
	ctx := context.Background()
	req := frameworkresource.ReadRequest{State: stateForModel(t, model)}
	resp := frameworkresource.ReadResponse{State: newResourceState(t)}
	r.Read(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", resp.Diagnostics)
	}
	var out IOSXEConfigResourceModel
	if diags := resp.State.Get(ctx, &out); diags.HasError() {
		t.Fatalf("read state diagnostics: %v", diags)
	}
	return out
}

func createResourceForTest(t *testing.T, r *iosxeConfigResource, model *IOSXEConfigResourceModel) IOSXEConfigResourceModel {
	t.Helper()
	ctx := context.Background()
	req := frameworkresource.CreateRequest{Plan: planForModel(t, model)}
	resp := frameworkresource.CreateResponse{State: newResourceState(t)}
	r.Create(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}
	var out IOSXEConfigResourceModel
	if diags := resp.State.Get(ctx, &out); diags.HasError() {
		t.Fatalf("create state diagnostics: %v", diags)
	}
	return out
}

func updateResourceForTest(t *testing.T, r *iosxeConfigResource, model *IOSXEConfigResourceModel) IOSXEConfigResourceModel {
	t.Helper()
	ctx := context.Background()
	req := frameworkresource.UpdateRequest{
		Plan:  planForModel(t, model),
		State: stateForModel(t, validIOSXEConfigModel(t, model.Name.ValueString(), model.Namespace.ValueString())),
	}
	resp := frameworkresource.UpdateResponse{State: newResourceState(t)}
	r.Update(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update diagnostics: %v", resp.Diagnostics)
	}
	var out IOSXEConfigResourceModel
	if diags := resp.State.Get(ctx, &out); diags.HasError() {
		t.Fatalf("update state diagnostics: %v", diags)
	}
	return out
}

func newApplyResourceForTest(t *testing.T) *iosxeConfigResource {
	t.Helper()
	r := &iosxeConfigResource{
		client: fakeDynamic(t),
	}
	r.client.(*dynfake.FakeDynamicClient).PrependReactor("patch", "iosxeconfigs", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction)
		var payload map[string]any
		if err := json.Unmarshal(patch.GetPatch(), &payload); err != nil {
			return true, nil, err
		}
		obj := &unstructured.Unstructured{}
		obj.SetUnstructuredContent(payload)
		obj.SetGeneration(1)
		if err := unstructured.SetNestedMap(obj.Object, map[string]any{
			"observedGeneration": int64(1),
			"phase":              "InSync",
		}, "status"); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	return r
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

func mustMap(t *testing.T, values map[string]string) types.Map {
	t.Helper()
	elems := make(map[string]attr.Value, len(values))
	for k, v := range values {
		elems[k] = types.StringValue(v)
	}
	out, dgs := types.MapValue(types.StringType, elems)
	if dgs.HasError() {
		t.Fatalf("types.MapValue: %v", dgs)
	}
	return out
}

func assertKnownEmptyMap(t *testing.T, name string, got types.Map) {
	t.Helper()
	if got.IsNull() {
		t.Fatalf("%s is null, want known empty map", name)
	}
	if got.IsUnknown() {
		t.Fatalf("%s is unknown, want known empty map", name)
	}
	if len(got.Elements()) != 0 {
		t.Fatalf("%s=%v, want empty map", name, got.Elements())
	}
}

func mustStringMapValue(t *testing.T, in types.Map) map[string]string {
	t.Helper()
	out, err := stringMapFromMap(in)
	if err != nil {
		t.Fatalf("stringMapFromMap: %v", err)
	}
	if out == nil {
		return map[string]string{}
	}
	return out
}

func newIOSXEConfigObject(t *testing.T, name, namespace string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("config.cisco.vk/v1alpha1")
	obj.SetKind("IOSXEConfig")
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetGeneration(1)
	if err := unstructured.SetNestedField(obj.Object, map[string]any{"name": name}, "spec", "deviceRef"); err != nil {
		t.Fatalf("seed spec.deviceRef: %v", err)
	}
	if err := unstructured.SetNestedStringSlice(obj.Object, []string{"vlan"}, "spec", "managedFamilies"); err != nil {
		t.Fatalf("seed spec.managedFamilies: %v", err)
	}
	if err := unstructured.SetNestedMap(obj.Object, map[string]any{
		"observedGeneration": int64(1),
		"phase":              "InSync",
	}, "status"); err != nil {
		t.Fatalf("seed status: %v", err)
	}
	return obj
}

func validIOSXEConfigModel(t *testing.T, name, namespace string) *IOSXEConfigResourceModel {
	t.Helper()
	return &IOSXEConfigResourceModel{
		Name:            types.StringValue(name),
		Namespace:       types.StringValue(namespace),
		DeviceRef:       types.StringValue(name),
		ManagedFamilies: mustList(t, "vlan"),
		SourceInline:    types.StringValue(`{"vlan":{"vlans":[{"id":10,"name":"users"}]}}`),
		Labels:          types.MapNull(types.StringType),
		Annotations:     types.MapNull(types.StringType),
	}
}

func TestCreateOmittedMetadataReturnsKnownEmpty(t *testing.T) {
	r := newApplyResourceForTest(t)
	model := validIOSXEConfigModel(t, "edge-create", "network")
	model.Labels = types.MapUnknown(types.StringType)
	model.Annotations = types.MapUnknown(types.StringType)

	got := createResourceForTest(t, r, model)
	assertKnownEmptyMap(t, "labels", got.Labels)
	assertKnownEmptyMap(t, "annotations", got.Annotations)
}

func TestUpdateOmittedMetadataReturnsKnownEmpty(t *testing.T) {
	r := newApplyResourceForTest(t)
	model := validIOSXEConfigModel(t, "edge-update", "network")
	model.Labels = types.MapUnknown(types.StringType)
	model.Annotations = types.MapUnknown(types.StringType)

	got := updateResourceForTest(t, r, model)
	assertKnownEmptyMap(t, "labels", got.Labels)
	assertKnownEmptyMap(t, "annotations", got.Annotations)
}

func TestCreateExplicitEmptyMapsRemainStable(t *testing.T) {
	r := newApplyResourceForTest(t)
	model := validIOSXEConfigModel(t, "edge-empty", "network")
	model.Labels = mustMap(t, map[string]string{})
	model.Annotations = mustMap(t, map[string]string{})

	got := createResourceForTest(t, r, model)
	assertKnownEmptyMap(t, "labels", got.Labels)
	assertKnownEmptyMap(t, "annotations", got.Annotations)
}

func TestCreateConfiguredMetadataPreserved(t *testing.T) {
	r := newApplyResourceForTest(t)
	model := validIOSXEConfigModel(t, "edge-metadata", "network")
	model.Labels = mustMap(t, map[string]string{"tf": "yes"})

	got := createResourceForTest(t, r, model)
	if labels := mustStringMapValue(t, got.Labels); !reflect.DeepEqual(labels, map[string]string{"tf": "yes"}) {
		t.Fatalf("labels=%v, want configured labels", labels)
	}
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

func TestReadDoesNotPullControllerLabelsIntoState(t *testing.T) {
	existing := newIOSXEConfigObject(t, "edge-01", "network")
	existing.SetLabels(map[string]string{"tf": "yes", "ops/managed": "true"})
	r := &iosxeConfigResource{
		client: fakeDynamic(t, existing),
	}

	model := validIOSXEConfigModel(t, "edge-01", "network")
	model.Labels = mustMap(t, map[string]string{"tf": "yes"})
	got := readResourceForTest(t, r, model)
	if labels := mustStringMapValue(t, got.Labels); !reflect.DeepEqual(labels, map[string]string{"tf": "yes"}) {
		t.Fatalf("labels=%v, want only Terraform-configured labels", labels)
	}
}

func TestUpdatePreservesControllerMetadata(t *testing.T) {
	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion("config.cisco.vk/v1alpha1")
	existing.SetKind("IOSXEConfig")
	existing.SetName("edge-01")
	existing.SetNamespace("network")
	existing.SetFinalizers([]string{"config.cisco.vk/lease-cleanup"})
	existing.SetLabels(map[string]string{"cisco.vk/managed-by-controller": "true"})
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
	if err := unstructured.SetNestedMap(existing.Object, map[string]any{
		"observedGeneration": int64(3),
		"phase":              "InSync",
	}, "status"); err != nil {
		t.Fatalf("seed status: %v", err)
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
		if _, ok := payload["status"]; ok {
			t.Fatalf("apply payload included status: %v", payload["status"])
		}
		metadata, ok := payload["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("apply payload metadata=%T, want map[string]any", payload["metadata"])
		}
		for _, forbidden := range []string{"finalizers", "ownerReferences", "managedFields", "labels", "annotations"} {
			if _, ok := metadata[forbidden]; ok {
				t.Fatalf("apply payload metadata included %s: %v", forbidden, metadata[forbidden])
			}
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
	if got.GetLabels()["cisco.vk/managed-by-controller"] != "true" {
		t.Fatalf("labels=%v, want controller label preserved", got.GetLabels())
	}
	if got.GetAnnotations()["controller.cisco.vk/annotation"] != "keep" {
		t.Fatalf("annotations=%v, want controller annotation preserved", got.GetAnnotations())
	}
	phase, found, err := unstructured.NestedString(got.Object, "status", "phase")
	if err != nil || !found || phase != "InSync" {
		t.Fatalf("status.phase=%q found=%v err=%v, want preserved InSync", phase, found, err)
	}
	families, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "managedFamilies")
	if !reflect.DeepEqual(families, []string{"vlan", "vrf"}) {
		t.Fatalf("managedFamilies=%v, want updated Terraform spec", families)
	}
}

func TestUpdatePreservesNonTerraformLabels(t *testing.T) {
	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion("config.cisco.vk/v1alpha1")
	existing.SetKind("IOSXEConfig")
	existing.SetName("edge-01")
	existing.SetNamespace("network")
	existing.SetLabels(map[string]string{
		"foo": "bar",
		"tf":  "yes",
	})
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
		metadata, ok := payload["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("apply payload metadata=%T, want map[string]any", payload["metadata"])
		}
		rawLabels, ok := metadata["labels"].(map[string]any)
		if !ok {
			t.Fatalf("apply payload labels=%T, want map[string]any", metadata["labels"])
		}
		if len(rawLabels) != 1 || rawLabels["tf"] != "updated" {
			t.Fatalf("apply payload labels=%v, want only Terraform-owned tf=updated", rawLabels)
		}
		labels := stored.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		for k, v := range rawLabels {
			s, ok := v.(string)
			if !ok {
				t.Fatalf("label %s=%T, want string", k, v)
			}
			labels[k] = s
		}
		stored.SetLabels(labels)
		if spec, ok := payload["spec"]; ok {
			stored.Object["spec"] = spec
		}
		return true, stored.DeepCopy(), nil
	})

	model := &IOSXEConfigResourceModel{
		Name:            types.StringValue("edge-01"),
		Namespace:       types.StringValue("network"),
		DeviceRef:       types.StringValue("edge-01"),
		ManagedFamilies: mustList(t, "vlan"),
		SourceInline:    types.StringValue(`{"vlan":{"vlans":[{"id":10,"name":"users"}]}}`),
		Labels:          mustMap(t, map[string]string{"tf": "updated"}),
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
	if got.GetLabels()["foo"] != "bar" {
		t.Fatalf("labels=%v, want non-Terraform label foo=bar preserved", got.GetLabels())
	}
	if got.GetLabels()["tf"] != "updated" {
		t.Fatalf("labels=%v, want Terraform label tf=updated", got.GetLabels())
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

func TestImportPopulatesExistingMetadata(t *testing.T) {
	ctx := context.Background()
	existing := newIOSXEConfigObject(t, "edge-import", "network")
	existing.SetLabels(map[string]string{"imported": "true"})
	existing.SetAnnotations(map[string]string{"imp": "ann"})
	r := &iosxeConfigResource{
		client: fakeDynamic(t, existing),
	}

	importResp := frameworkresource.ImportStateResponse{State: newResourceState(t)}
	r.ImportState(ctx, frameworkresource.ImportStateRequest{ID: "network/edge-import"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", importResp.Diagnostics)
	}
	var importedName, importedNamespace types.String
	if diags := importResp.State.GetAttribute(ctx, path.Root("name"), &importedName); diags.HasError() {
		t.Fatalf("imported name diagnostics: %v", diags)
	}
	if diags := importResp.State.GetAttribute(ctx, path.Root("namespace"), &importedNamespace); diags.HasError() {
		t.Fatalf("imported namespace diagnostics: %v", diags)
	}

	model := validIOSXEConfigModel(t, importedName.ValueString(), importedNamespace.ValueString())
	got := readResourceForTest(t, r, model)
	if labels := mustStringMapValue(t, got.Labels); !reflect.DeepEqual(labels, map[string]string{"imported": "true"}) {
		t.Fatalf("labels=%v, want imported cluster labels", labels)
	}
	if annotations := mustStringMapValue(t, got.Annotations); !reflect.DeepEqual(annotations, map[string]string{"imp": "ann"}) {
		t.Fatalf("annotations=%v, want imported cluster annotations", annotations)
	}
}

// Compile-time guard the framework's diag.Diagnostics also
// implements DiagnosticsSink — so the production AddError path
// uses it transparently.
var _ DiagnosticsSink = (*diag.Diagnostics)(nil)

// Suppress the linter complaint about metav1's only being used for
// future test additions (Get with options, etc.).
var _ = metav1.GetOptions{}
