// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"fmt"
	"sigs.k8s.io/yaml"
	"sort"
	"strings"
)

const ModelVersion = "1.5.0"
const ProviderVersion = "0.11.5"

// Document uses the upstream names. Unknown fields are rejected until their
// complete mapping is implemented; silently dropping intent is never parity.
type Document struct {
	SDWAN    SDWAN     `json:"sdwan"`
	Defaults *Defaults `json:"defaults,omitempty"`
}
type TemplateDefaults struct {
	DeviceTypes []string `json:"device_types"`
}
type Defaults struct {
	SDWAN struct {
		EdgeFeatureTemplates map[string]TemplateDefaults `json:"edge_feature_templates"`
		Sites                struct {
			Routers struct {
				ConfigurationGroupDeploy *bool `json:"configuration_group_deploy,omitempty"`
				PolicyGroupDeploy        *bool `json:"policy_group_deploy,omitempty"`
			} `json:"routers,omitempty"`
		} `json:"sites,omitempty"`
	} `json:"sdwan"`
}
type SDWAN struct {
	ManagerVersion       string                     `json:"manager_version,omitempty"`
	ApplicationCatalog   *ApplicationCatalog        `json:"application_catalog,omitempty"`
	NetworkHierarchy     *NetworkHierarchy          `json:"network_hierarchy,omitempty"`
	Settings             *SDWANSettings             `json:"settings,omitempty"`
	Sites                []SDWANSite                `json:"sites,omitempty"`
	EdgeDeviceTemplates  []EdgeDeviceTemplate       `json:"edge_device_templates,omitempty"`
	PolicyObjects        map[string]json.RawMessage `json:"policy_objects,omitempty"`
	ConfigurationGroups  []ConfigurationGroup       `json:"configuration_groups,omitempty"`
	TopologyGroups       []TopologyGroup            `json:"topology_groups,omitempty"`
	PolicyGroups         []PolicyGroup              `json:"policy_groups,omitempty"`
	LocalizedPolicies    *LocalizedPolicies         `json:"localized_policies,omitempty"`
	CentralizedPolicies  *CentralizedPolicies       `json:"centralized_policies,omitempty"`
	SecurityPolicies     *SecurityPolicies          `json:"security_policies,omitempty"`
	FeatureProfiles      map[string]json.RawMessage `json:"feature_profiles,omitempty"`
	EdgeFeatureTemplates map[string]json.RawMessage `json:"edge_feature_templates"`
}
type ApplicationCatalog struct {
	CustomApplications []map[string]any `json:"custom_applications,omitempty"`
}
type Banner struct {
	Name          string    `json:"name"`
	Description   *string   `json:"description"`
	DeviceTypes   *[]string `json:"device_types,omitempty"`
	Login         *string   `json:"login,omitempty"`
	LoginVariable *string   `json:"login_variable,omitempty"`
	MOTD          *string   `json:"motd,omitempty"`
	MOTDVariable  *string   `json:"motd_variable,omitempty"`
}

var defaultBannerDevices = []string{"C8000V", "C8300-1N1S-4T2X", "C8300-1N1S-6T", "C8300-2N2S-6T", "C8300-2N2S-4T2X", "C8500-12X4QC", "C8500-12X", "C8500-20X6C", "C8500L-8S4X", "C8200-1N-4T", "C8200L-1N-4T"}

type ParcelReference struct {
	Kind                   string
	Name, ParcelPath       string
	Collection, ParentName string
	BodyPath               []string
}
type ParcelAncestor struct {
	Path, Name string
}
type Resource struct {
	ParcelRefs                         []ParcelReference
	ClassicDefinitionRefs              []ClassicDefinitionReference
	PolicyListRefs                     []PolicyListReference
	ParcelAncestors                    []ParcelAncestor
	HierarchySites, SiteGroups         []string
	HubSpoke                           *hubSpokeInput
	CustomControlSites                 map[string][]string
	TagDevices                         []string
	DeviceTemplateRefs                 []DeviceTemplateReference
	DevicePolicyRefs                   []DevicePolicyReference
	GroupDevices                       []GroupDeviceTarget
	AttachDevices                      []DeviceAttachmentTarget
	ParcelPath                         string
	ParentParcelPath, ParentParcelName string
	Key, Name, Section, Type           string
	Kind, Collection, ParentName       string
	ProfileRefs                        map[string]string
	Body                               map[string]any
}

func Compile(data []byte) ([]Resource, error) {
	if len(data) > 1<<20 {
		return nil, errors.New("SD-WAN document exceeds size limit")
	}
	var doc Document
	if err := yaml.UnmarshalStrict(data, &doc); err != nil {
		return nil, errors.New("invalid or unsupported SD-WAN model fields")
	}

	defaults := defaultBannerDevices
	if doc.Defaults != nil && doc.Defaults.SDWAN.EdgeFeatureTemplates["banner_templates"].DeviceTypes != nil {
		defaults = doc.Defaults.SDWAN.EdgeFeatureTemplates["banner_templates"].DeviceTypes
	}
	seen := map[string]bool{}
	out := []Resource{}
	var banners []Banner
	if raw, exists := doc.SDWAN.EdgeFeatureTemplates["banner_templates"]; exists {
		if err := yaml.UnmarshalStrict(raw, &banners); err != nil {
			return nil, errors.New("invalid banner template fields")
		}
	}
	for _, b := range banners {
		if b.Name == "" || len(b.Name) > 128 || strings.TrimSpace(b.Name) != b.Name || b.Description == nil {
			return nil, errors.New("banner requires a name and description")
		}
		if seen[b.Name] {
			return nil, errors.New("duplicate SD-WAN resource name")
		}
		seen[b.Name] = true
		devices := defaults
		if b.DeviceTypes != nil {
			devices = *b.DeviceTypes
		}
		if len(devices) == 0 {
			return nil, errors.New("banner requires device_types")
		}
		mapped := []string{}
		ds := map[string]bool{}
		for _, d := range devices {
			if d == "" {
				return nil, errors.New("invalid device type")
			}
			v := "vedge-" + d
			if !ds[v] {
				mapped = append(mapped, v)
				ds[v] = true
			}
		}
		sort.Strings(mapped)
		body := map[string]any{"deviceType": mapped, "factoryDefault": false, "templateDescription": *b.Description, "templateMinVersion": "15.0.0", "templateName": b.Name, "templateType": "cisco_banner", "templateDefinition": map[string]any{"login": bannerValue(b.Login, b.LoginVariable), "motd": bannerValue(b.MOTD, b.MOTDVariable)}}
		out = append(out, Resource{Key: "edge_feature_templates/banner_templates/" + b.Name, Name: b.Name, Section: "edge_feature_templates", Type: "cisco_banner", Body: body})
	}
	overrides := map[string][]string{}
	if doc.Defaults != nil {
		for collection, definition := range doc.Defaults.SDWAN.EdgeFeatureTemplates {
			if collection != "banner_templates" {
				if _, err := compileClassicTemplates(collection, nil, nil); err != nil {
					return nil, err
				}
			}
			overrides[collection] = definition.DeviceTypes
		}
	}
	for collection, raw := range doc.SDWAN.EdgeFeatureTemplates {
		if collection == "banner_templates" {
			continue
		}
		var input []map[string]any
		if err := yaml.UnmarshalStrict(raw, &input); err != nil {
			return nil, errors.New("invalid classic template fields")
		}
		resources, err := compileClassicTemplates(collection, input, overrides[collection])
		if err != nil {
			return nil, err
		}
		out = append(out, resources...)
	}
	hierarchy, err := compileHierarchy(doc.SDWAN.NetworkHierarchy)
	if err != nil {
		return nil, err
	}
	out = append(out, hierarchy...)
	settings, err := compileSDWANSettings(doc.SDWAN.Settings)
	if err != nil {
		return nil, err
	}
	out = append(out, settings...)
	tags, err := compileTags(doc.SDWAN.Sites)
	if err != nil {
		return nil, err
	}
	out = append(out, tags...)
	deviceTemplates, err := compileDeviceTemplates(doc.SDWAN.EdgeDeviceTemplates)
	if err != nil {
		return nil, err
	}
	out = append(out, deviceTemplates...)
	attachments, err := compileDeviceAttachments(doc.SDWAN.Sites, doc.SDWAN.EdgeDeviceTemplates)
	if err != nil {
		return nil, err
	}
	out = append(out, attachments...)
	if doc.SDWAN.NetworkHierarchy != nil {
		collectors, err := compileHierarchyCollectors(doc.SDWAN.NetworkHierarchy.Collectors)
		if err != nil {
			return nil, err
		}
		out = append(out, collectors...)
	}
	policies, err := compilePolicyObjects(doc.SDWAN.PolicyObjects)
	if err != nil {
		return nil, err
	}
	out = append(out, policies...)
	customApps, err := compileCustomApplications(doc.SDWAN.ApplicationCatalog)
	if err != nil {
		return nil, err
	}
	out = append(out, customApps...)
	profiles, err := compileProfiles(doc.SDWAN.FeatureProfiles, doc.SDWAN.ManagerVersion)
	if err != nil {
		return nil, err
	}
	out = append(out, profiles...)
	if err := prepareMeshPolicies(out, doc.SDWAN.ManagerVersion); err != nil {
		return nil, err
	}
	if err := prepareHubSpokePolicies(out, doc.SDWAN.ManagerVersion); err != nil {
		return nil, err
	}
	groups, err := compileGroups(doc.SDWAN.ConfigurationGroups, profiles)
	if err != nil {
		return nil, err
	}
	out = append(out, groups...)
	topologyGroups, err := compileTopologyGroups(doc.SDWAN.TopologyGroups, profiles)
	if err != nil {
		return nil, err
	}
	out = append(out, topologyGroups...)
	policyGroups, err := compilePolicyGroups(doc.SDWAN.PolicyGroups, profiles)
	if err != nil {
		return nil, err
	}
	out = append(out, policyGroups...)
	groupDeployments, err := compileGroupDeployments(doc.SDWAN.Sites, doc.SDWAN.ConfigurationGroups, doc.SDWAN.PolicyGroups, doc.Defaults)
	if err != nil {
		return nil, err
	}
	out = append(out, groupDeployments...)
	localized, err := compileLocalizedPolicies(doc.SDWAN.LocalizedPolicies)
	if err != nil {
		return nil, err
	}
	out = append(out, localized...)
	centralized, err := compileCentralizedPolicies(doc.SDWAN.CentralizedPolicies)
	if err != nil {
		return nil, err
	}
	out = append(out, centralized...)
	security, err := compileSecurityPolicies(doc.SDWAN.SecurityPolicies)
	if err != nil {
		return nil, err
	}
	out = append(out, security...)
	policyProfile := ""
	for _, r := range out {
		if r.Kind == "profile" && r.Type == "policy-object" {
			policyProfile = r.Name
		}
	}
	for i := range out {
		for j := range out[i].ParcelRefs {
			if out[i].ParcelRefs[j].ParentName == "$policy_objects" {
				out[i].ParcelRefs[j].ParentName = policyProfile
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("SD-WAN document has no supported resources")
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Kind == "activation") != (out[j].Kind == "activation") {
			return out[j].Kind == "activation"
		}
		if (out[i].Kind == "hierarchy") != (out[j].Kind == "hierarchy") {
			return out[i].Kind == "hierarchy"
		}
		if (out[i].Kind == "group") != (out[j].Kind == "group") {
			return out[j].Kind == "group"
		}
		if out[i].ParentName == "" && out[j].ParentName != "" {
			return true
		}
		if out[i].ParentName != "" && out[j].ParentName == "" {
			return false
		}
		if (len(out[i].ParcelRefs) == 0) != (len(out[j].ParcelRefs) == 0) {
			return len(out[i].ParcelRefs) == 0
		}
		return out[i].Key < out[j].Key
	})
	out, err = orderParcelDependencies(out)
	if err != nil {
		return nil, err
	}
	if len(out) > 512 {
		return nil, errors.New("SD-WAN document exceeds 512 resource limit")
	}
	counts := map[string]int{}
	classicNames := map[string]bool{}
	for _, r := range out {
		if r.Kind == "" {
			if classicNames[r.Name] {
				return nil, errors.New("duplicate classic template name across collections")
			}
			classicNames[r.Name] = true
		}
		counts[r.Section]++
		if counts[r.Section] > 128 {
			return nil, errors.New("SD-WAN section exceeds journal capacity")
		}
	}
	return out, nil
}
func bannerValue(value, variable *string) map[string]any {
	result := map[string]any{"vipObjectType": "object", "vipType": "ignore"}
	if variable != nil {
		result["vipType"] = "variableName"
		result["vipVariableName"] = *variable
	} else if value != nil {
		result["vipType"] = "constant"
		result["vipValue"] = *value
	}
	return result
}
func normalized(body map[string]any) (string, error) {
	result := map[string]any{}
	if body["templateType"] == "cisco_sig_credentials" {
		global, ok := body["isGlobal"].(bool)
		if !ok {
			return "", ErrResponse
		}
		result["isGlobal"] = global
	}
	for _, key := range []string{"deviceType", "templateDescription", "templateName", "templateType", "templateDefinition"} {
		v, ok := body[key]
		if !ok {
			return "", ErrResponse
		}
		result[key] = v
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", ErrResponse
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", ErrResponse
	}
	a, ok := m["deviceType"].([]any)
	if !ok {
		return "", ErrResponse
	}
	s := []string{}
	for _, v := range a {
		x, ok := v.(string)
		if !ok {
			return "", ErrResponse
		}
		s = append(s, x)
	}
	sort.Strings(s)
	m["deviceType"] = s
	// Manager may return non-semantic editor metadata inside feature leaves.
	def, ok := m["templateDefinition"].(map[string]any)
	if !ok {
		return "", ErrResponse
	}
	normalizedDefinition, err := normalizeVIP(def)
	if err != nil {
		return "", err
	}
	m["templateDefinition"] = normalizedDefinition
	raw, err = json.Marshal(m)
	return string(raw), err
}
func Equal(r Resource, observed map[string]any) (bool, error) {
	if r.Kind != "" {
		return equalModern(r, observed)
	}
	a, e := normalized(r.Body)
	if e != nil {
		return false, e
	}
	b, e := normalized(observed)
	return a == b, e
}
func (r Resource) String() string { return fmt.Sprintf("%s resource", r.Type) }
