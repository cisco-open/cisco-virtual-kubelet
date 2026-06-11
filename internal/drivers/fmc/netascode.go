// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

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

// ConfigMapReader is the minimum Kubernetes reader surface needed by the FMC source loader.
type ConfigMapReader interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
}

// Intent is the resolved FMC configuration request consumed by the applier.
type Intent struct {
	DeviceName      string
	ManagedFamilies []string
	Configuration   map[string]any
	DriftPolicy     configv1alpha1.DriftPolicy
}

// FamilyResult is the platform-neutral status for one FMC NetAsCode family.
type FamilyResult struct {
	Name    string
	State   string
	Entries int32
	OpCount int32
	Message string
}

// DriftResult captures a single FMC drift item.
type DriftResult struct {
	Family   string
	Path     string
	Desired  string
	Observed string
}

// ApplyResult summarises an FMC NetAsCode reconcile.
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
	ListItems(ctx context.Context, path string) ([]map[string]any, error)
	DomainUUIDForName(ctx context.Context, name string) (string, error)
	Close() error
}

// NetAsCodeApplier translates Network as Code FMC model fragments into FMC REST
// upserts. The resource catalog is data-driven so new FMC model pages can be
// enabled by adding endpoint metadata without touching the Kubernetes reconciler.
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
		return fmt.Errorf("fmc netascode applier: nil client")
	}
	return a.client.Check(ctx)
}

func (a *NetAsCodeApplier) Apply(ctx context.Context, intent Intent) (ApplyResult, error) {
	if a == nil || a.client == nil {
		return ApplyResult{}, fmt.Errorf("fmc netascode applier: nil client")
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
			msg := "no supported writable resources declared"
			if family == "existing" {
				msg = "existing FMC references are read-only"
			}
			if family == "nac_configuration" {
				msg = "nac_configuration processed as controller options"
			}
			out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "InSync", Message: msg})
			continue
		}
		if intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
			out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "Drifted", Entries: int32(len(items)), Message: "report-only: FMC resources were planned but not written"})
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
	endpoint, err := a.endpoint(ctx, item)
	if err != nil {
		return err
	}
	body := normaliseBody(item.Def, item.Body)
	body["name"] = item.Name
	if item.Def.Type != "" {
		body["type"] = item.Def.Type
	}
	id, err := a.lookupIDByName(ctx, endpoint, item.Name)
	if err != nil {
		return err
	}
	if id == "" {
		_, err = a.client.Post(ctx, endpoint, body)
		if err != nil {
			return fmt.Errorf("create %s %q: %w", item.Path, item.Name, err)
		}
		return nil
	}
	body["id"] = id
	_, err = a.client.Put(ctx, strings.TrimRight(endpoint, "/")+"/"+url.PathEscape(id), body)
	if err != nil {
		return fmt.Errorf("update %s %q: %w", item.Path, item.Name, err)
	}
	return nil
}

func (a *NetAsCodeApplier) endpoint(ctx context.Context, item resourceIntent) (string, error) {
	domainUUID, err := a.client.DomainUUIDForName(ctx, item.DomainName)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(item.Def.Endpoint, "{domainUUID}", url.PathEscape(domainUUID)), nil
}

func (a *NetAsCodeApplier) lookupIDByName(ctx context.Context, endpoint, name string) (string, error) {
	items, err := a.client.ListItems(ctx, endpoint)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		itemName, _ := item["name"].(string)
		if strings.EqualFold(itemName, name) {
			id, _ := item["id"].(string)
			return id, nil
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
		return nil, fmt.Errorf("parse FMC netascode body: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("FMC netascode body top-level must be a mapping, got %T", root)
	}
	if fmcRaw, ok := top["fmc"]; ok {
		body, ok := fmcRaw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("FMC netascode envelope: .fmc is %T, want mapping", fmcRaw)
		}
		out := copyMap(body)
		if existing, ok := top["existing"].(map[string]any); ok {
			if existingFMC, ok := existing["fmc"].(map[string]any); ok {
				out["existing"] = existingFMC
			}
		}
		return out, nil
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
	"nac_configuration": {},
	"existing":          {},
	"system":            {},
	"domains":           {},
	"devices":           {},
	"objects":           {},
	"policies":          {},
	"vpns":              {},
	"integrations":      {},
}

var supportedTopLevelSections = map[string]struct{}{
	"name":              {},
	"version":           {},
	"nac_configuration": {},
	"existing":          {},
	"system":            {},
	"domains":           {},
	"devices":           {},
	"objects":           {},
	"policies":          {},
	"vpns":              {},
	"integrations":      {},
}

func ValidateManagedFamilies(families []string) error {
	if len(families) == 0 {
		return fmt.Errorf("managedFamilies must contain at least one FMC family")
	}
	for _, fam := range families {
		if _, ok := supportedFamilies[fam]; !ok {
			return fmt.Errorf("unsupported FMC NetAsCode family %q", fam)
		}
	}
	return nil
}

func ValidateModel(config map[string]any, families []string) error {
	if config == nil {
		return fmt.Errorf("FMC NetAsCode configuration is nil")
	}
	for top := range config {
		if _, ok := supportedTopLevelSections[top]; !ok {
			return fmt.Errorf("unsupported FMC NetAsCode top-level section %q", top)
		}
	}
	for _, fam := range families {
		if _, ok := config[fam]; !ok {
			continue
		}
		switch fam {
		case "domains":
			if _, ok := config[fam].([]any); !ok {
				return fmt.Errorf("FMC family %q must be a list", fam)
			}
		default:
			if _, ok := config[fam].(map[string]any); !ok {
				return fmt.Errorf("FMC family %q must be a mapping", fam)
			}
		}
	}
	return nil
}

type resourceDef struct {
	Path     []string
	Family   string
	Endpoint string
	Type     string
	Aliases  map[string]string
	Priority int
}

type resourceIntent struct {
	Family     string
	DomainName string
	Path       string
	Def        resourceDef
	Name       string
	Body       map[string]any
}

var resourceRegistry = []resourceDef{
	{Path: []string{"objects", "hosts"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/hosts", Type: "Host", Aliases: map[string]string{"ip": "value"}, Priority: 10},
	{Path: []string{"objects", "networks"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/networks", Type: "Network", Aliases: map[string]string{"prefix": "value"}, Priority: 20},
	{Path: []string{"objects", "ranges"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/ranges", Type: "Range", Priority: 30},
	{Path: []string{"objects", "fqdns"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/fqdns", Type: "FQDN", Aliases: map[string]string{"fqdn": "value"}, Priority: 40},
	{Path: []string{"objects", "urls"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/urls", Type: "Url", Priority: 45},
	{Path: []string{"objects", "network_groups"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/networkgroups", Type: "NetworkGroup", Priority: 80},
	{Path: []string{"objects", "port_groups"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/portobjectgroups", Type: "PortObjectGroup", Priority: 90},
	{Path: []string{"objects", "security_zones"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/securityzones", Type: "SecurityZone", Priority: 100},
	{Path: []string{"objects", "vlan_tags"}, Family: "objects", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/object/vlantags", Type: "VlanTag", Priority: 110},
	{Path: []string{"policies", "access_control_policies"}, Family: "policies", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/policy/accesspolicies", Type: "AccessPolicy", Priority: 200},
	{Path: []string{"policies", "prefilter_policies"}, Family: "policies", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/policy/prefilterpolicies", Type: "PrefilterPolicy", Priority: 210},
	{Path: []string{"policies", "ftd_nat_policies"}, Family: "policies", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/policy/ftdnatpolicies", Type: "FTDNatPolicy", Priority: 220},
	{Path: []string{"policies", "health_policies"}, Family: "policies", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/policy/healthpolicies", Type: "HealthPolicy", Priority: 230},
	{Path: []string{"integrations", "ad_ldap_realms"}, Family: "integrations", Endpoint: "/api/fmc_config/v1/domain/{domainUUID}/integration/realms", Type: "Realm", Priority: 300},
}

func CollectResources(config map[string]any, families []string) ([]resourceIntent, error) {
	familySet := map[string]bool{}
	for _, fam := range families {
		familySet[fam] = true
	}
	out := []resourceIntent{}
	domains, ok, err := domainsFromConfig(config)
	if err != nil {
		return nil, err
	}
	if !ok {
		domains = []domainModel{{Name: defaultFMCDomainName, Body: config}}
	}
	for _, domain := range domains {
		for _, def := range resourceRegistry {
			if !familySet[def.Family] {
				continue
			}
			items, ok, err := listAtPath(domain.Body, def.Path)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			for idx, item := range items {
				body, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("FMC %s[%d] must be a mapping", strings.Join(def.Path, "."), idx)
				}
				name, _ := body["name"].(string)
				if strings.TrimSpace(name) == "" {
					return nil, fmt.Errorf("FMC %s[%d].name is required", strings.Join(def.Path, "."), idx)
				}
				out = append(out, resourceIntent{Family: def.Family, DomainName: domain.Name, Path: "domains." + domain.Name + "." + strings.Join(def.Path, "."), Def: def, Name: name, Body: body})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		if out[i].Def.Priority != out[j].Def.Priority {
			return out[i].Def.Priority < out[j].Def.Priority
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

type domainModel struct {
	Name string
	Body map[string]any
}

func domainsFromConfig(config map[string]any) ([]domainModel, bool, error) {
	raw, ok := config["domains"]
	if !ok {
		return nil, false, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, true, fmt.Errorf("FMC domains must be a list")
	}
	out := make([]domainModel, 0, len(items))
	for idx, item := range items {
		body, ok := item.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("FMC domains[%d] must be a mapping", idx)
		}
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			name = defaultFMCDomainName
		}
		out = append(out, domainModel{Name: name, Body: body})
	}
	return out, true, nil
}

func listAtPath(root map[string]any, path []string) ([]any, bool, error) {
	var cur any = root
	for _, part := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("FMC path %s parent is %T, want mapping", strings.Join(path, "."), cur)
		}
		v, ok := m[part]
		if !ok {
			return nil, false, nil
		}
		cur = v
	}
	items, ok := cur.([]any)
	if !ok {
		return nil, false, fmt.Errorf("FMC path %s must be a list", strings.Join(path, "."))
	}
	return items, true, nil
}

func normaliseBody(def resourceDef, in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		if k == "id" || k == "metadata" || k == "links" {
			continue
		}
		mapped := k
		if alias, ok := def.Aliases[k]; ok {
			mapped = alias
		} else {
			mapped = toFMCFieldName(k)
		}
		out[mapped] = normaliseValue(v)
	}
	return out
}

func normaliseValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return normaliseBody(resourceDef{}, t)
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

func toFMCFieldName(in string) string {
	switch in {
	case "domain_uuid":
		return "domainUuid"
	case "access_policy":
		return "accessPolicy"
	case "health_policy":
		return "healthPolicy"
	}
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

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
