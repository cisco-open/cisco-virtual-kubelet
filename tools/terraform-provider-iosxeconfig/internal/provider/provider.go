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
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// New returns a fresh provider instance. Used by main.go's Serve
// call and by the acceptance-test framework.
func New() provider.Provider {
	return &iosxeConfigProvider{}
}

// iosxeConfigProvider configures the kube client every resource
// shares. Empty Kubeconfig follows the same precedence kubectl
// uses (KUBECONFIG → in-cluster → $HOME/.kube/config), so a
// .tf written without a provider block works in both shapes.
type iosxeConfigProvider struct{}

// providerData is the per-process configuration the provider
// hands to each resource via Configure. Carrying just the dynamic
// client (rather than a *rest.Config) means resources don't need
// to re-build the same client per-Resource.
type providerData struct {
	Dynamic            dynamic.Interface
	Default            Namespace
	WaitForConvergence bool
}

// Namespace is a small typed-string so a refactor that adds
// per-resource namespace fields can rebind from the provider
// default without touching every CRUD method.
type Namespace string

func (p *iosxeConfigProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "iosxeconfig"
	resp.Version = "0.1.0"
}

func (p *iosxeConfigProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Authoring-side provider for IOSXEConfig CRs in a Kubernetes cluster.

The provider does NOT run the cisco-virtual-kubelet config driver — it only
writes IOSXEConfig CRs against the cluster's API. The per-device cisco-vk pod
(or the aggregator's in-process reconciler) does the device work.`,
		Attributes: map[string]schema.Attribute{
			"kubeconfig": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to a kubeconfig. Empty falls back to $KUBECONFIG → in-cluster → $HOME/.kube/config.",
			},
			"context": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Kubeconfig context name. Empty uses current-context.",
			},
			"namespace": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Default namespace for IOSXEConfig CRs whose `namespace` attribute is not set.",
			},
			"wait_for_convergence": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, Create and Update wait for status.observedGeneration to catch metadata.generation before storing computed status fields.",
			},
		},
	}
}

type providerModel struct {
	Kubeconfig         types.String `tfsdk:"kubeconfig"`
	Context            types.String `tfsdk:"context"`
	Namespace          types.String `tfsdk:"namespace"`
	WaitForConvergence types.Bool   `tfsdk:"wait_for_convergence"`
}

func (p *iosxeConfigProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	restCfg, err := loadRESTConfig(cfg.Kubeconfig.ValueString(), cfg.Context.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("kubeconfig load failed",
			"the provider could not assemble a Kubernetes REST config: "+err.Error())
		return
	}
	dynClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		resp.Diagnostics.AddError("dynamic client construction failed", err.Error())
		return
	}
	pd := &providerData{
		Dynamic:            dynClient,
		Default:            Namespace(cfg.Namespace.ValueString()),
		WaitForConvergence: !cfg.WaitForConvergence.IsNull() && cfg.WaitForConvergence.ValueBool(),
	}
	resp.DataSourceData = pd
	resp.ResourceData = pd
}

func (p *iosxeConfigProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func (p *iosxeConfigProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewIOSXEConfigResource}
}

// loadRESTConfig follows the same precedence kubectl uses and
// matches the cisco-vk-config-lint cluster-mode loader. Explicit
// kubeconfig path > $KUBECONFIG > in-cluster > $HOME/.kube/config.
func loadRESTConfig(kubeconfig, context string) (*rest.Config, error) {
	if kubeconfig != "" {
		if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
			return nil, fmt.Errorf("kubeconfig %q does not exist", kubeconfig)
		}
		overrides := &clientcmd.ConfigOverrides{}
		if context != "" {
			overrides.CurrentContext = context
		}
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			overrides,
		).ClientConfig()
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		loadingRules.ExplicitPath = env
		overrides := &clientcmd.ConfigOverrides{}
		if context != "" {
			overrides.CurrentContext = context
		}
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		overrides.CurrentContext = context
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

// Compile-time guard — keep us honest about which framework
// interfaces this provider satisfies.
var _ provider.Provider = (*iosxeConfigProvider)(nil)
