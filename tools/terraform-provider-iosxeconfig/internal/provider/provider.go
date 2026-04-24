// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package provider hosts the terraform-plugin-framework
// implementation. Phase-8 scaffold; the framework imports are
// commented inline so this file compiles in the standalone module
// without pulling the Hashicorp dependency tree until the next
// iteration wires it up.
package provider

// IOSXEConfigProvider configures the kube client every resource
// shares. Operators set Kubeconfig to a path; an empty value
// follows the same precedence kubectl uses (KUBECONFIG ->
// in-cluster -> $HOME/.kube/config).
//
// Wired up in the next iteration:
//
//	import (
//	  "github.com/hashicorp/terraform-plugin-framework/provider"
//	  "github.com/hashicorp/terraform-plugin-framework/provider/schema"
//	  "github.com/hashicorp/terraform-plugin-framework/types"
//	)
//
//	type IOSXEConfigProvider struct{}
//
//	func (p *IOSXEConfigProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
//	  resp.Schema = schema.Schema{
//	    Attributes: map[string]schema.Attribute{
//	      "kubeconfig": schema.StringAttribute{Optional: true},
//	      "context":    schema.StringAttribute{Optional: true},
//	      "namespace":  schema.StringAttribute{Optional: true,
//	        Description: "default namespace for IOSXEConfig CRs"},
//	    },
//	  }
//	}
//
//	func (p *IOSXEConfigProvider) Resources(_ context.Context) []func() resource.Resource {
//	  return []func() resource.Resource{NewIOSXEConfigResource}
//	}
type IOSXEConfigProvider struct {
	Kubeconfig string
	Context    string
	Namespace  string
}
