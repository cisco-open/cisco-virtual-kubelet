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
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gvrschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// iosxeConfigGVR is the dynamic-resource handle for IOSXEConfig.
// Mirrors the cisco-vk-config-lint cluster loader so the two
// authoring surfaces agree on what they're talking to.
var iosxeConfigGVR = gvrschema.GroupVersionResource{
	Group:    "config.cisco.vk",
	Version:  "v1alpha1",
	Resource: "iosxeconfigs",
}

const ResourceMetadataName = "iosxeconfig_config"

// NewIOSXEConfigResource is what the provider's Resources() hands
// the framework. Each call returns a fresh resource value; the
// framework calls Configure on it before any CRUD method runs.
func NewIOSXEConfigResource() resource.Resource {
	return &iosxeConfigResource{}
}

type iosxeConfigResource struct {
	client    dynamic.Interface
	defaultNS string
}

// IOSXEConfigResourceModel is the Terraform-side projection of
// IOSXEConfig.spec. Optional fields surface as types.String /
// types.Bool null when unset so plans don't churn.
type IOSXEConfigResourceModel struct {
	Name              types.String `tfsdk:"name"`
	Namespace         types.String `tfsdk:"namespace"`
	DeviceRef         types.String `tfsdk:"device_ref"`
	ManagedFamilies   types.List   `tfsdk:"managed_families"`
	DriftPolicy       types.String `tfsdk:"drift_policy"`
	SourceInline      types.String `tfsdk:"source_inline"`
	WriteStartup      types.Bool   `tfsdk:"write_startup"`
	PruneOnRelinquish types.Bool   `tfsdk:"prune_on_relinquish"`

	Phase             types.String `tfsdk:"phase"`
	LastAppliedHash   types.String `tfsdk:"last_applied_hash"`
	SourceYangVersion types.String `tfsdk:"source_yang_version"`
}

func (r *iosxeConfigResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ResourceMetadataName
}

func (r *iosxeConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("internal: bad provider data type",
			fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return
	}
	r.client = pd.Dynamic
	r.defaultNS = string(pd.Default)
}

func (r *iosxeConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Authors a config.cisco.vk/v1alpha1/IOSXEConfig CR. The cluster's per-device cisco-vk pod (or aggregator) does the device work.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"namespace": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"device_ref": schema.StringAttribute{
				Required: true,
			},
			"managed_families": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
			},
			"drift_policy": schema.StringAttribute{
				Optional: true,
			},
			"source_inline": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Netascode YAML body. Ride-along ConfigMap support arrives in a follow-up.",
			},
			"write_startup": schema.BoolAttribute{
				Optional: true,
			},
			"prune_on_relinquish": schema.BoolAttribute{
				Optional: true,
			},
			"phase": schema.StringAttribute{
				Computed: true,
			},
			"last_applied_hash": schema.StringAttribute{
				Computed: true,
			},
			"source_yang_version": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (r *iosxeConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan IOSXEConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ns := r.namespaceFor(&plan)
	plan.Namespace = types.StringValue(ns)

	obj, err := r.toUnstructured(ctx, &plan, &resp.Diagnostics)
	if err != nil {
		return
	}
	created, err := r.client.Resource(iosxeConfigGVR).Namespace(ns).
		Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("create IOSXEConfig failed", err.Error())
		return
	}
	r.refreshFromCluster(ctx, &plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *iosxeConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state IOSXEConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ns := r.namespaceFor(&state)
	got, err := r.client.Resource(iosxeConfigGVR).Namespace(ns).
		Get(ctx, state.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CR went away outside Terraform — drop it from state.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("read IOSXEConfig failed", err.Error())
		return
	}
	r.refreshFromCluster(ctx, &state, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *iosxeConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan IOSXEConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ns := r.namespaceFor(&plan)
	plan.Namespace = types.StringValue(ns)

	obj, err := r.toUnstructured(ctx, &plan, &resp.Diagnostics)
	if err != nil {
		return
	}
	// Carry the existing resourceVersion forward so concurrent
	// edits surface as a clean Conflict instead of overwriting.
	current, getErr := r.client.Resource(iosxeConfigGVR).Namespace(ns).
		Get(ctx, plan.Name.ValueString(), metav1.GetOptions{})
	if getErr != nil {
		resp.Diagnostics.AddError("update prefetch failed", getErr.Error())
		return
	}
	obj.SetResourceVersion(current.GetResourceVersion())

	updated, err := r.client.Resource(iosxeConfigGVR).Namespace(ns).
		Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("update IOSXEConfig failed", err.Error())
		return
	}
	r.refreshFromCluster(ctx, &plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *iosxeConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state IOSXEConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ns := r.namespaceFor(&state)
	if err := r.client.Resource(iosxeConfigGVR).Namespace(ns).
		Delete(ctx, state.Name.ValueString(), metav1.DeleteOptions{}); err != nil &&
		!apierrors.IsNotFound(err) {
		resp.Diagnostics.AddError("delete IOSXEConfig failed", err.Error())
	}
}

// ImportState lets `terraform import iosxeconfig_config.foo
// <namespace>/<name>` adopt an existing CR.
func (r *iosxeConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ns, name, ok := splitNamespacedName(req.ID)
	if !ok {
		resp.Diagnostics.AddError("invalid import ID",
			"expected '<namespace>/<name>'")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), ns)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

// namespaceFor picks the resource's effective namespace —
// model.Namespace if set, otherwise the provider default,
// otherwise "default".
func (r *iosxeConfigResource) namespaceFor(m *IOSXEConfigResourceModel) string {
	if !m.Namespace.IsNull() && m.Namespace.ValueString() != "" {
		return m.Namespace.ValueString()
	}
	if r.defaultNS != "" {
		return r.defaultNS
	}
	return "default"
}

// toUnstructured assembles the wire CR. The HCL `source_inline` is
// the operator-facing netascode YAML body; we decode it once and
// embed under spec.source.inline as a structured map.
func (r *iosxeConfigResource) toUnstructured(_ context.Context, m *IOSXEConfigResourceModel, diag DiagnosticsSink) (*unstructured.Unstructured, error) {
	var inline map[string]any
	if err := yaml.Unmarshal([]byte(m.SourceInline.ValueString()), &inline); err != nil {
		diag.AddError("parse source_inline failed",
			"the value must be valid YAML/JSON: "+err.Error())
		return nil, err
	}

	families, err := stringSliceFromList(m.ManagedFamilies)
	if err != nil {
		diag.AddError("decode managed_families failed", err.Error())
		return nil, err
	}

	spec := map[string]any{
		"deviceRef":       map[string]any{"name": m.DeviceRef.ValueString()},
		"managedFamilies": stringsToAny(families),
		"source":          map[string]any{"inline": inline},
	}
	if !m.DriftPolicy.IsNull() && m.DriftPolicy.ValueString() != "" {
		spec["driftPolicy"] = m.DriftPolicy.ValueString()
	}
	if !m.WriteStartup.IsNull() {
		spec["writeStartup"] = m.WriteStartup.ValueBool()
	}
	if !m.PruneOnRelinquish.IsNull() {
		spec["pruneOnRelinquish"] = m.PruneOnRelinquish.ValueBool()
	}
	out := &unstructured.Unstructured{}
	out.SetUnstructuredContent(map[string]any{
		"apiVersion": "config.cisco.vk/v1alpha1",
		"kind":       "IOSXEConfig",
		"metadata": map[string]any{
			"name":      m.Name.ValueString(),
			"namespace": r.namespaceFor(m),
		},
		"spec": spec,
	})
	return out, nil
}

// refreshFromCluster copies the CR's status fields onto the model
// so the computed attributes (phase, last_applied_hash,
// source_yang_version) reflect what the cluster knows.
func (r *iosxeConfigResource) refreshFromCluster(_ context.Context, m *IOSXEConfigResourceModel, got *unstructured.Unstructured) {
	status, _, _ := unstructured.NestedMap(got.Object, "status")
	m.Phase = stringFromMap(status, "phase")
	m.LastAppliedHash = stringFromMap(status, "lastAppliedHash")
	m.SourceYangVersion = stringFromMap(status, "sourceYangVersion")
}

// DiagnosticsSink is the narrow surface toUnstructured needs.
// Pulled to a local interface so the test-only buildUnstructured
// path can use it without importing terraform's diag types.
type DiagnosticsSink interface {
	AddError(summary, detail string)
}

func stringFromMap(m map[string]any, key string) types.String {
	if m == nil {
		return types.StringNull()
	}
	v, ok := m[key]
	if !ok {
		return types.StringNull()
	}
	s, ok := v.(string)
	if !ok {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func stringSliceFromList(in types.List) ([]string, error) {
	if in.IsNull() || in.IsUnknown() {
		return nil, nil
	}
	out := make([]string, 0, len(in.Elements()))
	for _, e := range in.Elements() {
		v, ok := e.(types.String)
		if !ok {
			return nil, fmt.Errorf("element %v is not a string", e)
		}
		out = append(out, v.ValueString())
	}
	return out, nil
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func splitNamespacedName(id string) (string, string, bool) {
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			return id[:i], id[i+1:], i > 0 && i < len(id)-1
		}
	}
	return "", "", false
}

// Compile-time guards.
var (
	_ resource.Resource                = (*iosxeConfigResource)(nil)
	_ resource.ResourceWithConfigure   = (*iosxeConfigResource)(nil)
	_ resource.ResourceWithImportState = (*iosxeConfigResource)(nil)
)

// Anchor the encoding/json import — used in tests + by future
// fields we expect to surface (computed status payloads).
var _ = json.Marshal
