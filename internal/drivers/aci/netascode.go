// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package aci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ConfigMapReader is the minimum Kubernetes reader surface needed by the APIC source loader.
type ConfigMapReader interface {
	Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
}

// Intent is the resolved APIC configuration request consumed by the applier.
type Intent struct {
	DeviceName      string
	ManagedFamilies []string
	Configuration   map[string]any
	DriftPolicy     configv1alpha1.DriftPolicy
}

// FamilyResult is the platform-neutral status for one APIC NetAsCode family.
type FamilyResult struct {
	Name    string
	State   string
	Entries int32
	OpCount int32
	Message string
}

// DriftResult captures a single APIC drift item.
type DriftResult struct {
	Family   string
	Path     string
	Desired  string
	Observed string
}

// ApplyResult summarises an APIC NetAsCode reconcile.
type ApplyResult struct {
	FamilyResults []FamilyResult
	Drift         []DriftResult
	Applied       bool
}

type restClient interface {
	Check(context.Context) error
	PostMO(ctx context.Context, dn, class string, attrs map[string]string, children []any) error
	Close() error
}

// NetAsCodeApplier translates Network as Code APIC model fragments into APIC
// managed-object writes. The APIC NaC model is intentionally broad, so the
// writable registry starts with common, low-risk resources and can be extended
// without changing Kubernetes reconciliation.
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
		return fmt.Errorf("apic netascode applier: nil client")
	}
	return a.client.Check(ctx)
}

func (a *NetAsCodeApplier) Apply(ctx context.Context, intent Intent) (ApplyResult, error) {
	if a == nil || a.client == nil {
		return ApplyResult{}, fmt.Errorf("apic netascode applier: nil client")
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
	objects, err := CollectManagedObjects(intent.Configuration, intent.ManagedFamilies)
	if err != nil {
		return ApplyResult{}, err
	}
	byFamily := map[string][]moIntent{}
	for _, obj := range objects {
		byFamily[obj.Family] = append(byFamily[obj.Family], obj)
	}
	out := ApplyResult{FamilyResults: make([]FamilyResult, 0, len(intent.ManagedFamilies))}
	for _, family := range intent.ManagedFamilies {
		items := byFamily[family]
		if len(items) == 0 {
			msg := "no supported writable resources declared"
			if family == "existing" {
				msg = "existing APIC references are read-only"
			}
			out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "InSync", Message: msg})
			continue
		}
		if intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
			out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "Drifted", Entries: int32(len(items)), Message: "report-only: APIC managed objects were planned but not written"})
			for _, item := range items {
				out.Drift = append(out.Drift, DriftResult{Family: family, Path: item.Path, Desired: "declared", Observed: "not checked in report-only mode"})
			}
			continue
		}
		written := 0
		for _, item := range items {
			if err := a.client.PostMO(ctx, item.DN, item.Class, item.Attrs, item.Children); err != nil {
				out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "ApplyError", Entries: int32(len(items)), OpCount: int32(written), Message: err.Error()})
				return out, fmt.Errorf("apply %s %s: %w", item.Path, item.DN, err)
			}
			written++
		}
		out.Applied = out.Applied || written > 0
		out.FamilyResults = append(out.FamilyResults, FamilyResult{Name: family, State: "InSync", Entries: int32(len(items)), OpCount: int32(written)})
	}
	return out, nil
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
		return nil, fmt.Errorf("parse APIC netascode body: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("APIC netascode body top-level must be a mapping, got %T", root)
	}
	if apicRaw, ok := top["apic"]; ok {
		body, ok := apicRaw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("APIC netascode envelope: .apic is %T, want mapping", apicRaw)
		}
		out := copyMap(body)
		if existing, ok := top["existing"].(map[string]any); ok {
			if existingAPIC, ok := existing["apic"].(map[string]any); ok {
				out["existing"] = existingAPIC
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
	"bootstrap":          {},
	"fabric_policies":    {},
	"access_policies":    {},
	"pod_policies":       {},
	"node_policies":      {},
	"interface_policies": {},
	"tenants":            {},
	"existing":           {},
}

var supportedTopLevelSections = map[string]struct{}{
	"name":                              {},
	"version":                           {},
	"existing":                          {},
	"bootstrap":                         {},
	"fabric_policies":                   {},
	"access_policies":                   {},
	"pod_policies":                      {},
	"node_policies":                     {},
	"interface_policies":                {},
	"tenants":                           {},
	"auto_generate_switch_pod_profiles": {},
}

func ValidateManagedFamilies(families []string) error {
	if len(families) == 0 {
		return fmt.Errorf("managedFamilies must contain at least one APIC family")
	}
	for _, fam := range families {
		if _, ok := supportedFamilies[fam]; !ok {
			return fmt.Errorf("unsupported APIC Network as Code family %q", fam)
		}
	}
	return nil
}

func ValidateModel(config map[string]any, families []string) error {
	if config == nil {
		return fmt.Errorf("APIC Network as Code configuration is nil")
	}
	for top := range config {
		if _, ok := supportedTopLevelSections[top]; !ok {
			return fmt.Errorf("unsupported APIC Network as Code top-level section %q", top)
		}
	}
	for _, fam := range families {
		raw, ok := config[fam]
		if !ok {
			continue
		}
		switch fam {
		case "tenants":
			if _, ok := raw.([]any); !ok {
				return fmt.Errorf("APIC family %q must be a list", fam)
			}
		default:
			if _, ok := raw.(map[string]any); !ok {
				return fmt.Errorf("APIC family %q must be a mapping", fam)
			}
		}
	}
	return nil
}

type moIntent struct {
	Family   string
	Path     string
	DN       string
	Class    string
	Attrs    map[string]string
	Children []any
	Priority int
}

func CollectManagedObjects(config map[string]any, families []string) ([]moIntent, error) {
	familySet := map[string]bool{}
	for _, fam := range families {
		familySet[fam] = true
	}
	out := []moIntent{}
	if familySet["access_policies"] {
		items, err := collectAccessPolicyMOs(config)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if familySet["tenants"] {
		items, err := collectTenantMOs(config)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].DN < out[j].DN
	})
	return out, nil
}

func collectTenantMOs(config map[string]any) ([]moIntent, error) {
	raw, ok := config["tenants"]
	if !ok {
		return nil, nil
	}
	tenants, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("APIC tenants must be a list")
	}
	out := []moIntent{}
	for tidx, item := range tenants {
		tenant, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("APIC tenants[%d] must be a mapping", tidx)
		}
		if managed, ok := tenant["managed"].(bool); ok && !managed {
			continue
		}
		name := stringField(tenant, "name")
		if name == "" {
			return nil, fmt.Errorf("APIC tenants[%d].name is required", tidx)
		}
		tnDN := "uni/tn-" + name
		out = append(out, moIntent{
			Family:   "tenants",
			Path:     fmt.Sprintf("tenants.%s", name),
			DN:       tnDN,
			Class:    "fvTenant",
			Attrs:    attrsFromModel(tenant, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias"),
			Priority: 100,
		})
		vrfs, err := tenantList(tenant, "vrfs", tidx)
		if err != nil {
			return nil, err
		}
		for vidx, vrf := range vrfs {
			vname := stringField(vrf, "name")
			if vname == "" {
				return nil, fmt.Errorf("APIC tenants[%d].vrfs[%d].name is required", tidx, vidx)
			}
			out = append(out, moIntent{
				Family:   "tenants",
				Path:     fmt.Sprintf("tenants.%s.vrfs.%s", name, vname),
				DN:       tnDN + "/ctx-" + vname,
				Class:    "fvCtx",
				Attrs:    attrsFromModel(vrf, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias", "pcEnfPref", "pcEnfDir"),
				Priority: 200,
			})
		}
		bds, err := tenantList(tenant, "bridge_domains", tidx)
		if err != nil {
			return nil, err
		}
		for bidx, bd := range bds {
			bname := stringField(bd, "name")
			if bname == "" {
				return nil, fmt.Errorf("APIC tenants[%d].bridge_domains[%d].name is required", tidx, bidx)
			}
			vrf := stringField(bd, "vrf")
			if vrf == "" {
				return nil, fmt.Errorf("APIC tenants[%d].bridge_domains[%d].vrf is required", tidx, bidx)
			}
			attrs := attrsFromModel(bd, map[string]string{
				"description":                "descr",
				"alias":                      "nameAlias",
				"arp_flooding":               "arpFlood",
				"unicast_routing":            "unicastRoute",
				"unknown_unicast":            "unkMacUcastAct",
				"unknown_ipv4_multicast":     "unkMcastAct",
				"multi_destination_flooding": "multiDstPktAct",
				"limit_ip_learn_to_subnets":  "limitIpLearnToSubnets",
				"ip_dataplane_learning":      "ipLearning",
				"advertise_host_routes":      "hostBasedRouting",
				"clear_remote_mac_entries":   "epClear",
				"endpoint_retention_policy":  "tnFvEpRetPolName",
				"igmp_interface_policy":      "tnIgmpIfPolName",
				"igmp_snooping_policy":       "tnIgmpSnoopPolName",
				"nd_interface_policy":        "tnNdIfPolName",
				"l3_multicast":               "mcastAllow",
				"multicast_arp_drop":         "mcastARPDrop",
			}, "name", "descr", "nameAlias", "mac", "vmac", "arpFlood", "unicastRoute", "unkMacUcastAct", "unkMcastAct", "multiDstPktAct", "limitIpLearnToSubnets", "ipLearning", "hostBasedRouting", "epClear", "tnFvEpRetPolName", "tnIgmpIfPolName", "tnIgmpSnoopPolName", "tnNdIfPolName", "mcastAllow", "mcastARPDrop")
			out = append(out, moIntent{
				Family:   "tenants",
				Path:     fmt.Sprintf("tenants.%s.bridge_domains.%s", name, bname),
				DN:       tnDN + "/BD-" + bname,
				Class:    "fvBD",
				Attrs:    attrs,
				Children: []any{apicChild("fvRsCtx", map[string]string{"tnFvCtxName": vrf})},
				Priority: 300,
			})
			subnets, err := listField(bd, "subnets")
			if err != nil {
				return nil, fmt.Errorf("APIC tenants[%d].bridge_domains[%d].subnets: %w", tidx, bidx, err)
			}
			for sidx, subnet := range subnets {
				ip := stringField(subnet, "ip")
				if ip == "" {
					return nil, fmt.Errorf("APIC tenants[%d].bridge_domains[%d].subnets[%d].ip is required", tidx, bidx, sidx)
				}
				sattrs := attrsFromModel(subnet, map[string]string{
					"description": "descr",
					"primary_ip":  "preferred",
				}, "ip", "descr", "scope", "ctrl", "preferred", "virtual")
				mergeSubnetScope(sattrs, subnet)
				out = append(out, moIntent{
					Family:   "tenants",
					Path:     fmt.Sprintf("tenants.%s.bridge_domains.%s.subnets.%s", name, bname, ip),
					DN:       tnDN + "/BD-" + bname + "/subnet-[" + ip + "]",
					Class:    "fvSubnet",
					Attrs:    sattrs,
					Priority: 350,
				})
			}
		}
		apps, err := tenantList(tenant, "application_profiles", tidx)
		if err != nil {
			return nil, err
		}
		for aidx, app := range apps {
			aname := stringField(app, "name")
			if aname == "" {
				return nil, fmt.Errorf("APIC tenants[%d].application_profiles[%d].name is required", tidx, aidx)
			}
			appDN := tnDN + "/ap-" + aname
			out = append(out, moIntent{Family: "tenants", Path: fmt.Sprintf("tenants.%s.application_profiles.%s", name, aname), DN: appDN, Class: "fvAp", Attrs: attrsFromModel(app, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias"), Priority: 400})
			epgs, err := listField(app, "endpoint_groups")
			if err != nil {
				return nil, fmt.Errorf("APIC tenants[%d].application_profiles[%d].endpoint_groups: %w", tidx, aidx, err)
			}
			for eidx, epg := range epgs {
				ename := stringField(epg, "name")
				if ename == "" {
					return nil, fmt.Errorf("APIC tenants[%d].application_profiles[%d].endpoint_groups[%d].name is required", tidx, aidx, eidx)
				}
				children := []any{}
				if bd := stringField(epg, "bridge_domain"); bd != "" {
					children = append(children, apicChild("fvRsBd", map[string]string{"tnFvBDName": bd}))
				}
				out = append(out, moIntent{Family: "tenants", Path: fmt.Sprintf("tenants.%s.application_profiles.%s.endpoint_groups.%s", name, aname, ename), DN: appDN + "/epg-" + ename, Class: "fvAEPg", Attrs: attrsFromModel(epg, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias"), Children: children, Priority: 450})
			}
		}
		filters, err := tenantList(tenant, "filters", tidx)
		if err != nil {
			return nil, err
		}
		for fidx, filter := range filters {
			fname := stringField(filter, "name")
			if fname == "" {
				return nil, fmt.Errorf("APIC tenants[%d].filters[%d].name is required", tidx, fidx)
			}
			filterDN := tnDN + "/flt-" + fname
			out = append(out, moIntent{Family: "tenants", Path: fmt.Sprintf("tenants.%s.filters.%s", name, fname), DN: filterDN, Class: "vzFilter", Attrs: attrsFromModel(filter, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias"), Priority: 500})
			entries, err := listField(filter, "entries")
			if err != nil {
				return nil, fmt.Errorf("APIC tenants[%d].filters[%d].entries: %w", tidx, fidx, err)
			}
			for eidx, entry := range entries {
				ename := stringField(entry, "name")
				if ename == "" {
					return nil, fmt.Errorf("APIC tenants[%d].filters[%d].entries[%d].name is required", tidx, fidx, eidx)
				}
				out = append(out, moIntent{Family: "tenants", Path: fmt.Sprintf("tenants.%s.filters.%s.entries.%s", name, fname, ename), DN: filterDN + "/e-" + ename, Class: "vzEntry", Attrs: attrsFromModel(entry, map[string]string{"description": "descr", "ether_type": "etherT", "protocol": "prot", "source_from_port": "sFromPort", "source_to_port": "sToPort", "destination_from_port": "dFromPort", "destination_to_port": "dToPort"}, "name", "descr", "etherT", "prot", "sFromPort", "sToPort", "dFromPort", "dToPort"), Priority: 520})
			}
		}
		contracts, err := tenantList(tenant, "contracts", tidx)
		if err != nil {
			return nil, err
		}
		for cidx, contract := range contracts {
			cname := stringField(contract, "name")
			if cname == "" {
				return nil, fmt.Errorf("APIC tenants[%d].contracts[%d].name is required", tidx, cidx)
			}
			contractDN := tnDN + "/brc-" + cname
			out = append(out, moIntent{Family: "tenants", Path: fmt.Sprintf("tenants.%s.contracts.%s", name, cname), DN: contractDN, Class: "vzBrCP", Attrs: attrsFromModel(contract, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias", "scope"), Priority: 600})
			subjects, err := listField(contract, "subjects")
			if err != nil {
				return nil, fmt.Errorf("APIC tenants[%d].contracts[%d].subjects: %w", tidx, cidx, err)
			}
			for sidx, subject := range subjects {
				sname := stringField(subject, "name")
				if sname == "" {
					return nil, fmt.Errorf("APIC tenants[%d].contracts[%d].subjects[%d].name is required", tidx, cidx, sidx)
				}
				children := []any{}
				for _, fname := range stringListField(subject, "filters") {
					children = append(children, apicChild("vzRsSubjFiltAtt", map[string]string{"tnVzFilterName": fname}))
				}
				out = append(out, moIntent{Family: "tenants", Path: fmt.Sprintf("tenants.%s.contracts.%s.subjects.%s", name, cname, sname), DN: contractDN + "/subj-" + sname, Class: "vzSubj", Attrs: attrsFromModel(subject, map[string]string{"description": "descr", "alias": "nameAlias"}, "name", "descr", "nameAlias"), Children: children, Priority: 620})
			}
		}
	}
	return out, nil
}

func collectAccessPolicyMOs(config map[string]any) ([]moIntent, error) {
	raw, ok := config["access_policies"]
	if !ok {
		return nil, nil
	}
	body, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("APIC access_policies must be a mapping")
	}
	pools, err := listField(body, "vlan_pools")
	if err != nil {
		return nil, fmt.Errorf("APIC access_policies.vlan_pools: %w", err)
	}
	out := []moIntent{}
	for pidx, pool := range pools {
		name := stringField(pool, "name")
		if name == "" {
			return nil, fmt.Errorf("APIC access_policies.vlan_pools[%d].name is required", pidx)
		}
		allocMode := firstNonEmpty(stringField(pool, "allocation"), stringField(pool, "alloc_mode"), stringField(pool, "allocMode"), "static")
		dn := "uni/infra/vlanns-[" + name + "]-" + allocMode
		out = append(out, moIntent{Family: "access_policies", Path: fmt.Sprintf("access_policies.vlan_pools.%s", name), DN: dn, Class: "fvnsVlanInstP", Attrs: map[string]string{"name": name, "allocMode": allocMode}, Priority: 100})
		ranges, err := listField(pool, "ranges")
		if err != nil {
			return nil, fmt.Errorf("APIC access_policies.vlan_pools[%d].ranges: %w", pidx, err)
		}
		for ridx, rng := range ranges {
			from := vlanEncap(rng["from"])
			to := vlanEncap(rng["to"])
			if from == "" || to == "" {
				return nil, fmt.Errorf("APIC access_policies.vlan_pools[%d].ranges[%d].from/to are required", pidx, ridx)
			}
			rangeMode := firstNonEmpty(stringField(rng, "allocation"), stringField(rng, "alloc_mode"), stringField(rng, "allocMode"), "static")
			out = append(out, moIntent{
				Family:   "access_policies",
				Path:     fmt.Sprintf("access_policies.vlan_pools.%s.ranges.%s-%s", name, from, to),
				DN:       dn + "/from-[" + from + "]-to-[" + to + "]",
				Class:    "fvnsEncapBlk",
				Attrs:    map[string]string{"from": from, "to": to, "allocMode": rangeMode},
				Priority: 120,
			})
		}
	}
	return out, nil
}

func tenantList(tenant map[string]any, key string, tenantIndex int) ([]map[string]any, error) {
	items, err := listField(tenant, key)
	if err != nil {
		return nil, fmt.Errorf("APIC tenants[%d].%s: %w", tenantIndex, key, err)
	}
	return items, nil
}

func listField(root map[string]any, key string) ([]map[string]any, error) {
	raw, ok := root[key]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be a list")
	}
	out := make([]map[string]any, 0, len(items))
	for idx, item := range items {
		body, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("[%d] must be a mapping", idx)
		}
		out = append(out, body)
	}
	return out, nil
}

func attrsFromModel(body map[string]any, aliases map[string]string, allowed ...string) map[string]string {
	allow := map[string]bool{}
	for _, k := range allowed {
		allow[k] = true
	}
	out := map[string]string{}
	for key, raw := range body {
		mapped := key
		if alias, ok := aliases[key]; ok {
			mapped = alias
		} else {
			mapped = toAPICFieldName(key)
		}
		if !allow[mapped] {
			continue
		}
		if val := apicString(raw); val != "" {
			out[mapped] = val
		}
	}
	return out
}

func stringField(body map[string]any, key string) string {
	return apicString(body[key])
}

func stringListField(body map[string]any, key string) []string {
	raw, ok := body[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := apicString(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func apicString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "yes"
		}
		return "no"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(raw))
	}
}

func toAPICFieldName(in string) string {
	switch in {
	case "name_alias":
		return "nameAlias"
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

func apicChild(class string, attrs map[string]string) map[string]any {
	return map[string]any{
		class: map[string]any{
			"attributes": attrs,
		},
	}
}

func mergeSubnetScope(attrs map[string]string, subnet map[string]any) {
	scopes := []string{}
	if boolField(subnet, "public") {
		scopes = append(scopes, "public")
	}
	if boolField(subnet, "shared") {
		scopes = append(scopes, "shared")
	}
	if len(scopes) > 0 {
		attrs["scope"] = strings.Join(scopes, ",")
	}
	ctrl := []string{}
	if boolField(subnet, "nd_ra_prefix") {
		ctrl = append(ctrl, "nd")
	}
	if boolField(subnet, "no_default_gateway") {
		ctrl = append(ctrl, "no-default-gateway")
	}
	if len(ctrl) > 0 {
		attrs["ctrl"] = strings.Join(ctrl, ",")
	}
}

func boolField(body map[string]any, key string) bool {
	v, _ := body[key].(bool)
	return v
}

func vlanEncap(raw any) string {
	s := apicString(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "vlan-") {
		return s
	}
	return "vlan-" + s
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
