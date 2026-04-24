// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provider

// IOSXEConfigResourceModel is the Terraform-side projection of
// IOSXEConfig.spec. Operators author this in HCL; the resource's
// Create/Update/Delete map it back to a structured CR via the
// dynamic client.
//
// The shape is a deliberately narrow subset of the full CRD —
// every field exposed here is one operators commonly author.
// Niche fields (driftDetectInterval, transactional, secretRefs)
// can be added without breaking the schema; new fields ship
// Optional so plans against older state stay clean.
//
// Phase-8 scaffold; CRUD wiring lands when the framework imports
// land alongside it.
type IOSXEConfigResourceModel struct {
	// Required identifying fields.
	Name      string `tfsdk:"name"`
	Namespace string `tfsdk:"namespace"`

	// Spec mirror.
	DeviceRef          string   `tfsdk:"device_ref"`
	ManagedFamilies    []string `tfsdk:"managed_families"`
	DriftPolicy        string   `tfsdk:"drift_policy"`
	SourceInline       string   `tfsdk:"source_inline"`
	SourceConfigMap    string   `tfsdk:"source_configmap"`
	SourceConfigMapKey string   `tfsdk:"source_configmap_key"`
	WriteStartup       bool     `tfsdk:"write_startup"`
	PruneOnRelinquish  bool     `tfsdk:"prune_on_relinquish"`

	// Computed status output.
	Phase             string `tfsdk:"phase"`
	LastAppliedHash   string `tfsdk:"last_applied_hash"`
	SourceYangVersion string `tfsdk:"source_yang_version"`
}

// ResourceMetadataName is the Terraform resource type name.
// Convention: <provider>_<resource>; the provider is iosxeconfig.
const ResourceMetadataName = "iosxeconfig_config"

// IOSXEConfigResource is the resource implementation. The CRUD
// methods are stubbed pending the framework dependency wire-up.
//
// Wired up in the next iteration:
//
//	import (
//	  "github.com/hashicorp/terraform-plugin-framework/resource"
//	  "github.com/hashicorp/terraform-plugin-framework/resource/schema"
//	  "k8s.io/client-go/dynamic"
//	)
//
//	type IOSXEConfigResource struct {
//	  client dynamic.Interface
//	}
//
//	func NewIOSXEConfigResource() resource.Resource {
//	  return &IOSXEConfigResource{}
//	}
//
//	func (r *IOSXEConfigResource) Schema(...) { ... }
//	func (r *IOSXEConfigResource) Create(ctx, req, resp) { /* CR put */ }
//	func (r *IOSXEConfigResource) Read(ctx, req, resp)   { /* CR get */ }
//	func (r *IOSXEConfigResource) Update(ctx, req, resp) { /* CR patch */ }
//	func (r *IOSXEConfigResource) Delete(ctx, req, resp) { /* CR delete */ }
type IOSXEConfigResource struct{}
