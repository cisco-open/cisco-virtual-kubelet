// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ConfigMapReader is the minimum Kubernetes reader surface needed by the ISE
// source loader.
type ConfigMapReader interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
}

// Intent is the resolved ISE configuration request consumed by the applier.
type Intent struct {
	DeviceName      string
	ManagedFamilies []string
	Configuration   map[string]any
	DriftPolicy     configv1alpha1.DriftPolicy
}

// FamilyResult is the platform-neutral status for one ISE NetAsCode family.
type FamilyResult struct {
	Name    string
	State   string
	Entries int32
	OpCount int32
	Message string
}

// DriftResult captures a single ISE drift item.
type DriftResult struct {
	Family   string
	Path     string
	Desired  string
	Observed string
}

// ApplyResult summarises an ISE NetAsCode reconcile.
type ApplyResult struct {
	FamilyResults []FamilyResult
	Drift         []DriftResult
	Applied       bool
}

type restClient interface {
	Check(ctx context.Context) error
	Get(ctx context.Context, path string) ([]byte, error)
	Post(ctx context.Context, path string, body any) ([]byte, error)
	Put(ctx context.Context, path string, body any) ([]byte, error)
	Close() error
}

// NetAsCodeApplier translates Network as Code ISE model fragments into ISE
// ERS upserts. It deliberately keeps the resource registry data-driven so new
// ISE model pages can be added without changing the reconciler.
type NetAsCodeApplier struct {
	client restClient
}

func NewNetAsCodeApplier(spec *v1alpha1.DeviceSpec, password string) (*NetAsCodeApplier, error) {
	client, err := NewAPIClientFromSpec(spec, password)
	if err != nil {
		return nil, err
	}
	return &NetAsCodeApplier{client: client}, nil
}

func NewNetAsCodeApplierWithClient(client restClient) *NetAsCodeApplier {
	return &NetAsCodeApplier{client: client}
}

func (a *NetAsCodeApplier) Close() error {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.Close()
}

func (a *NetAsCodeApplier) Health(ctx context.Context) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("ise netascode applier: nil client")
	}
	return a.client.Check(ctx)
}

func (a *NetAsCodeApplier) Apply(ctx context.Context, intent Intent) (ApplyResult, error) {
	if a == nil || a.client == nil {
		return ApplyResult{}, fmt.Errorf("ise netascode applier: nil client")
	}
	if err := ValidateManagedFamilies(intent.ManagedFamilies); err != nil {
		return ApplyResult{}, err
	}
	if err := ValidateModel(intent.Configuration, intent.ManagedFamilies); err != nil {
		return ApplyResult{}, err
	}
	if err := a.client.Check(ctx); err != nil {
		return ApplyResult{}, err
	}
	resources, err := CollectResources(intent.Configuration, intent.ManagedFamilies)
	if err != nil {
		return ApplyResult{}, err
	}
	byFamily := map[string][]resourceIntent{}
	for _, res := range resources {
		byFamily[res.Family] = append(byFamily[res.Family], res)
	}
	out := ApplyResult{FamilyResults: make([]FamilyResult, 0, len(intent.ManagedFamilies))}
	for _, family := range intent.ManagedFamilies {
		items := byFamily[family]
		if len(items) == 0 {
			out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "InSync", Message: "no supported resources declared"})
			continue
		}
		if intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
			out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "Drifted", Entries: int32(len(items)), Message: "report-only: ISE resources were planned but not written"})
			for _, item := range items {
				out.Drift = append(out.Drift, DriftResult{Family: family, Path: item.Path, Desired: "declared", Observed: "not checked in report-only mode"})
			}
			continue
		}
		written := 0
		for _, item := range items {
			if err := a.upsert(ctx, item); err != nil {
				out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "ApplyError", Entries: int32(len(items)), OpCount: int32(written), Message: err.Error()})
				return out, err
			}
			written++
		}
		out.Applied = out.Applied || written > 0
		out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "InSync", Entries: int32(len(items)), OpCount: int32(written)})
	}
	return out, nil
}

func (a *NetAsCodeApplier) upsert(ctx context.Context, item resourceIntent) error {
	id, err := a.lookupIDByName(ctx, item.Endpoint, item.Name)
	if err != nil {
		return err
	}
	payload := map[string]any{item.Entity: item.Body}
	if id == "" {
		_, err = a.client.Post(ctx, item.Endpoint, payload)
		if err != nil {
			return fmt.Errorf("create %s %q: %w", item.Path, item.Name, err)
		}
		return nil
	}
	_, err = a.client.Put(ctx, strings.TrimRight(item.Endpoint, "/")+"/"+url.PathEscape(id), payload)
	if err != nil {
		return fmt.Errorf("update %s %q: %w", item.Path, item.Name, err)
	}
	return nil
}

func (a *NetAsCodeApplier) lookupIDByName(ctx context.Context, endpoint, name string) (string, error) {
	raw, err := a.client.Get(ctx, strings.TrimRight(endpoint, "/")+"?filter=name.EQ."+url.QueryEscape(name))
	if err != nil {
		return "", err
	}
	var sr struct {
		SearchResult struct {
			Resources []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"resources"`
		} `json:"SearchResult"`
	}
	if err := json.Unmarshal(raw, &sr); err != nil {
		return "", fmt.Errorf("parse ISE search result for %s %q: %w", endpoint, name, err)
	}
	for _, res := range sr.SearchResult.Resources {
		if strings.EqualFold(res.Name, name) || res.Name == "" {
			return res.ID, nil
		}
	}
	return "", nil
}

func LoadSource(ctx context.Context, r ConfigMapReader, ns, deviceName string, src configv1alpha1.ConfigurationSource) (map[string]any, error) {
	inlineSet := src.Inline != nil && len(src.Inline.Raw) > 0
	configMapSet := src.ConfigMapRef != nil && src.ConfigMapRef.Name != ""
	if inlineSet == configMapSet {
		return nil, fmt.Errorf("spec.source: exactly one of inline or configMapRef must be set")
	}
	var raw []byte
	if inlineSet {
		raw = src.Inline.Raw
	} else {
		var cm corev1.ConfigMap
		key := types.NamespacedName{Namespace: ns, Name: src.ConfigMapRef.Name}
		if err := r.Get(ctx, key, &cm); err != nil {
			return nil, fmt.Errorf("get ConfigMap %s/%s: %w", ns, src.ConfigMapRef.Name, err)
		}
		body, ok := cm.Data[src.ConfigMapRef.Key]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %s/%s does not contain key %q", ns, src.ConfigMapRef.Name, src.ConfigMapRef.Key)
		}
		raw = []byte(body)
	}
	return DecodeNetAsCodeBody(raw, deviceName)
}

func DecodeNetAsCodeBody(raw []byte, deviceName string) (map[string]any, error) {
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse ISE netascode body: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ISE netascode body top-level must be a mapping, got %T", root)
	}
	if ise, ok := top["ise"]; ok {
		body, ok := ise.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ISE netascode envelope: .ise is %T, want mapping", ise)
		}
		return body, nil
	}
	return top, nil
}

func CanonicalHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

var supportedFamilies = map[string]struct{}{
	"identity_management":   {},
	"network_resources":     {},
	"network_access":        {},
	"device_administration": {},
	"trust_sec":             {},
	"system":                {},
}

func ValidateManagedFamilies(families []string) error {
	if len(families) == 0 {
		return fmt.Errorf("managedFamilies must contain at least one ISE family")
	}
	for _, fam := range families {
		if _, ok := supportedFamilies[fam]; !ok {
			return fmt.Errorf("unsupported ISE NetAsCode family %q", fam)
		}
	}
	return nil
}

func ValidateModel(config map[string]any, families []string) error {
	if config == nil {
		return fmt.Errorf("ISE NetAsCode configuration is nil")
	}
	for top := range config {
		if _, ok := supportedFamilies[top]; !ok {
			return fmt.Errorf("unsupported ISE NetAsCode top-level section %q", top)
		}
	}
	for _, fam := range families {
		if _, ok := config[fam]; !ok {
			continue
		}
		if _, ok := config[fam].(map[string]any); !ok {
			return fmt.Errorf("ISE family %q must be a mapping", fam)
		}
	}
	return nil
}

type resourceDef struct {
	Path     []string
	Family   string
	Endpoint string
	Entity   string
}

type resourceIntent struct {
	Family   string
	Path     string
	Endpoint string
	Entity   string
	Name     string
	Body     map[string]any
}

var resourceRegistry = []resourceDef{
	{Path: []string{"identity_management", "endpoint_identity_groups"}, Family: "identity_management", Endpoint: "/ers/config/endpointgroup", Entity: "EndPointGroup"},
	{Path: []string{"identity_management", "endpoints"}, Family: "identity_management", Endpoint: "/ers/config/endpoint", Entity: "ERSEndPoint"},
	{Path: []string{"identity_management", "internal_users"}, Family: "identity_management", Endpoint: "/ers/config/internaluser", Entity: "InternalUser"},
	{Path: []string{"identity_management", "user_identity_groups"}, Family: "identity_management", Endpoint: "/ers/config/identitygroup", Entity: "IdentityGroup"},
	{Path: []string{"network_resources", "network_device_groups"}, Family: "network_resources", Endpoint: "/ers/config/networkdevicegroup", Entity: "NetworkDeviceGroup"},
	{Path: []string{"network_resources", "network_devices"}, Family: "network_resources", Endpoint: "/ers/config/networkdevice", Entity: "NetworkDevice"},
	{Path: []string{"network_access", "policy_elements", "allowed_protocols"}, Family: "network_access", Endpoint: "/ers/config/allowedprotocols", Entity: "AllowedProtocols"},
	{Path: []string{"network_access", "policy_elements", "authorization_profiles"}, Family: "network_access", Endpoint: "/ers/config/authorizationprofile", Entity: "AuthorizationProfile"},
	{Path: []string{"network_access", "policy_elements", "downloadable_acls"}, Family: "network_access", Endpoint: "/ers/config/downloadableacl", Entity: "DownloadableAcl"},
	{Path: []string{"device_administration", "policy_elements", "allowed_protocols"}, Family: "device_administration", Endpoint: "/ers/config/deviceadminallowedprotocols", Entity: "DeviceAdminAllowedProtocols"},
	{Path: []string{"device_administration", "policy_elements", "tacacs_command_sets"}, Family: "device_administration", Endpoint: "/ers/config/tacacscommandsets", Entity: "TacacsCommandSets"},
	{Path: []string{"device_administration", "policy_elements", "tacacs_profiles"}, Family: "device_administration", Endpoint: "/ers/config/tacacsprofile", Entity: "TacacsProfile"},
	{Path: []string{"trust_sec", "security_groups"}, Family: "trust_sec", Endpoint: "/ers/config/sgt", Entity: "Sgt"},
	{Path: []string{"trust_sec", "security_group_acls"}, Family: "trust_sec", Endpoint: "/ers/config/sgacl", Entity: "Sgacl"},
	{Path: []string{"system", "repositories"}, Family: "system", Endpoint: "/ers/config/repository", Entity: "Repository"},
}

func CollectResources(config map[string]any, families []string) ([]resourceIntent, error) {
	familySet := map[string]bool{}
	for _, fam := range families {
		familySet[fam] = true
	}
	out := []resourceIntent{}
	for _, def := range resourceRegistry {
		if !familySet[def.Family] {
			continue
		}
		items, ok, err := listAtPath(config, def.Path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for idx, item := range items {
			body, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ISE %s[%d] must be a mapping", strings.Join(def.Path, "."), idx)
			}
			name, _ := body["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("ISE %s[%d].name is required", strings.Join(def.Path, "."), idx)
			}
			out = append(out, resourceIntent{Family: def.Family, Path: strings.Join(def.Path, "."), Endpoint: def.Endpoint, Entity: def.Entity, Name: name, Body: normaliseBody(body)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func listAtPath(root map[string]any, path []string) ([]any, bool, error) {
	var cur any = root
	for _, part := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("ISE path %s parent is %T, want mapping", strings.Join(path, "."), cur)
		}
		v, ok := m[part]
		if !ok {
			return nil, false, nil
		}
		cur = v
	}
	items, ok := cur.([]any)
	if !ok {
		return nil, false, fmt.Errorf("ISE path %s must be a list", strings.Join(path, "."))
	}
	return items, true, nil
}

func normaliseBody(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[toISEFieldName(k)] = normaliseValue(v)
	}
	return out
}

func normaliseValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return normaliseBody(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = normaliseValue(t[i])
		}
		return out
	default:
		return v
	}
}

func toISEFieldName(in string) string {
	parts := strings.Split(in, "_")
	if len(parts) == 1 {
		return in
	}
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
