// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"reflect"
	"sigs.k8s.io/yaml"
	"strings"
)

type FeatureProfile struct {
	OSPF                  []map[string]any             `json:"ospf_features,omitempty"`
	BGP                   []map[string]any             `json:"bgp_features,omitempty"`
	OSPFv3IPv4            []map[string]any             `json:"ospfv3_ipv4_features,omitempty"`
	OSPFv3IPv6            []map[string]any             `json:"ospfv3_ipv6_features,omitempty"`
	EIGRP                 []map[string]any             `json:"eigrp_features,omitempty"`
	Switchports           []map[string]any             `json:"switchport_features,omitempty"`
	Multicasts            []map[string]any             `json:"multicast_features,omitempty"`
	IPv4DeviceAccess      map[string]any               `json:"ipv4_device_access_policy,omitempty"`
	IPv6DeviceAccess      map[string]any               `json:"ipv6_device_access_policy,omitempty"`
	MeshPolicies          []map[string]any             `json:"mesh_policies,omitempty"`
	CustomPolicies        []map[string]any             `json:"custom_policies,omitempty"`
	QoSPolicies           []map[string]any             `json:"qos_policies,omitempty"`
	TrafficPolicies       []map[string]any             `json:"traffic_policies,omitempty"`
	NGFWPolicies          []map[string]any             `json:"policies,omitempty"`
	HubSpokePolicies      []map[string]any             `json:"hub_spoke_policies,omitempty"`
	IPv4ACLs              []map[string]any             `json:"ipv4_acls,omitempty"`
	IPv6ACLs              []map[string]any             `json:"ipv6_acls,omitempty"`
	RoutePolicies         []map[string]any             `json:"route_policies,omitempty"`
	ManagementVPN         map[string]any               `json:"management_vpn,omitempty"`
	WANVPN                map[string]any               `json:"wan_vpn,omitempty"`
	LANVPNs               []map[string]any             `json:"lan_vpns,omitempty"`
	DualRouterHA          []map[string]any             `json:"dual_router_ha_features,omitempty"`
	IPv6Trackers          []map[string]any             `json:"ipv6_trackers,omitempty"`
	IPv6TrackerGroups     []map[string]any             `json:"ipv6_tracker_groups,omitempty"`
	CACertificate         map[string]any               `json:"ca_certificate,omitempty"`
	CiscoSSE              map[string]any               `json:"cisco,omitempty"`
	ZscalerSSE            map[string]any               `json:"zscaler,omitempty"`
	IPv4TrackerGroups     []map[string]any             `json:"ipv4_tracker_groups,omitempty"`
	ObjectTrackerGroups   []map[string]any             `json:"object_tracker_groups,omitempty"`
	CellularControllers   []map[string]any             `json:"cellular_controllers,omitempty"`
	CellularProfiles      []map[string]any             `json:"cellular_profiles,omitempty"`
	ObjectTrackers        []map[string]any             `json:"object_trackers,omitempty"`
	IPv4Trackers          []map[string]any             `json:"ipv4_trackers,omitempty"`
	DHCPServers           []map[string]any             `json:"dhcp_servers,omitempty"`
	AAA                   map[string]any               `json:"aaa,omitempty"`
	SNMP                  map[string]any               `json:"snmp,omitempty"`
	ThousandEyes          map[string]any               `json:"thousandeyes,omitempty"`
	GPS                   []map[string]any             `json:"gps_features,omitempty"`
	TrustSec              map[string]any               `json:"trustsec,omitempty"`
	UCSE                  map[string]any               `json:"ucse,omitempty"`
	Security              map[string]any               `json:"security,omitempty"`
	NTP                   map[string]any               `json:"ntp,omitempty"`
	Logging               map[string]any               `json:"logging,omitempty"`
	Basic                 map[string]any               `json:"basic,omitempty"`
	Global                map[string]any               `json:"global,omitempty"`
	OMP                   map[string]any               `json:"omp,omitempty"`
	BFD                   *BFD                         `json:"bfd,omitempty"`
	Config                *CLIConfig                   `json:"config,omitempty"`
	FlexiblePortSpeed     *FlexiblePortSpeed           `json:"flexible_port_speed,omitempty"`
	MRF                   *MRF                         `json:"mrf,omitempty"`
	PerformanceMonitoring *PerformanceMonitoring       `json:"performance_monitoring,omitempty"`
	Settings              *ApplicationPrioritySettings `json:"settings,omitempty"`
	Name                  string                       `json:"name"`
	Description           string                       `json:"description,omitempty"`
	Banner                *ProfileBanner               `json:"banner,omitempty"`
}
type ApplicationPrioritySettings struct {
	IPv4App  bool `json:"ipv4_application_visibility"`
	IPv6App  bool `json:"ipv6_application_visibility"`
	IPv4Flow bool `json:"ipv4_flow_visibility"`
	IPv6Flow bool `json:"ipv6_flow_visibility"`
}
type ProfileBanner struct {
	Name          *string `json:"name,omitempty"`
	Description   string  `json:"description,omitempty"`
	Login         *string `json:"login,omitempty"`
	LoginVariable *string `json:"login_variable,omitempty"`
	MOTD          *string `json:"motd,omitempty"`
	MOTDVariable  *string `json:"motd_variable,omitempty"`
}

var profileCollections = map[string]string{"system_profiles": "system", "transport_profiles": "transport", "service_profiles": "service", "cli_profiles": "cli", "other_profiles": "other", "application_priority_profiles": "application-priority", "ngfw_security_profiles": "embedded-security", "sse_profiles": "sse", "topology_profiles": "topology"}

func compileProfiles(raw map[string]json.RawMessage, managerVersion string) ([]Resource, error) {
	out := []Resource{}
	for section, data := range raw {
		if section == "policy_object_profile" {
			resources, err := compilePolicyObjectProfile(data)
			if err != nil {
				return nil, err
			}
			out = append(out, resources...)
			continue
		}

		collection, ok := profileCollections[section]
		if !ok {
			return nil, errors.New("unsupported feature profile collection")
		}
		var profiles []FeatureProfile
		if yaml.UnmarshalStrict(data, &profiles) != nil {
			return nil, errors.New("invalid or unsupported feature profile fields")
		}
		seen := map[string]bool{}
		for _, p := range profiles {
			if p.Name == "" || len(p.Name) > 128 || seen[p.Name] {
				return nil, errors.New("invalid or duplicate feature profile name")
			}
			seen[p.Name] = true
			key := "feature_profiles/" + section + "/" + p.Name
			out = append(out, Resource{Key: key, Name: p.Name, Section: "feature_profiles", Kind: "profile", Type: collection, Collection: "/dataservice/v1/feature-profile/sdwan/" + collection, Body: map[string]any{"name": p.Name, "description": p.Description}})
			parcels, e := simpleParcels(p, collection, key, managerVersion)
			if e != nil {
				return nil, e
			}
			out = append(out, parcels...)
			if p.Settings != nil && collection != "application-priority" {
				return nil, errors.New("settings require application priority profile")
			}
			if collection == "application-priority" {
				settings := p.Settings
				if settings == nil {
					settings = &ApplicationPrioritySettings{}
				}
				data := map[string]any{"cflowd": map[string]any{"optionType": "network-settings", "value": true}}
				for key, value := range map[string]bool{"appVisibility": settings.IPv4App, "appVisibilityIPv6": settings.IPv6App, "flowVisibility": settings.IPv4Flow, "flowVisibilityIPv6": settings.IPv6Flow} {
					data[key] = map[string]any{"optionType": "global", "value": value}
				}
				out = append(out, Resource{Key: key + "/policy-settings", Name: p.Name + "_settings", Section: "feature_profiles", Kind: "policy-parcel", Type: "application_priority_policy_settings", Collection: "/dataservice/v1/feature-profile/sdwan/application-priority", ParentName: p.Name, ParcelPath: "policy-settings", Body: map[string]any{"name": p.Name + "_settings", "description": "", "data": data}})
			}
			if b := p.Banner; b != nil {
				if collection != "system" {
					return nil, errors.New("banner is only valid in a system profile")
				}
				name := "banner"
				if b.Name != nil {
					name = *b.Name
				}
				if name == "" {
					return nil, errors.New("empty banner name")
				}
				out = append(out, Resource{Key: key + "/banner", Name: name, Section: "feature_profiles", Kind: "parcel", Type: "system_banner", Collection: "/dataservice/v1/feature-profile/sdwan/system", ParentName: p.Name, Body: map[string]any{"name": name, "description": b.Description, "data": map[string]any{"login": profileBannerValue(b.Login, b.LoginVariable), "motd": profileBannerValue(b.MOTD, b.MOTDVariable)}}})
			}
		}
	}
	return out, nil
}
func profileBannerValue(v, variable *string) map[string]any {
	if variable != nil {
		return map[string]any{"optionType": "variable", "value": "{{" + *variable + "}}"}
	}
	if v != nil {
		return map[string]any{"optionType": "global", "value": strings.ReplaceAll(*v, "\n", "\\n")}
	}
	return map[string]any{"optionType": "default", "value": ""}
}
func equalModern(r Resource, o map[string]any) (bool, error) {
	if r.Kind == "group-deployment" {
		return equalGroupDeployment(r, o)
	}
	if r.Kind == "device-attachment" {
		return equalDeviceAttachment(r, o)
	}
	if r.Kind == "activation" {
		return equalActivation(r, o)
	}
	if r.Kind == "device-template" {
		return equalDeviceTemplate(r, o)
	}
	if r.Kind == "tag" {
		return equalTag(r, o)
	}
	if r.Kind == "cloud-setting" {
		return equalCloudSettings(r, o)
	}
	if r.Kind == "hierarchy-setting" {
		return equalHierarchySetting(r, o)
	}
	if r.Kind == "localized-policy" || r.Kind == "centralized-policy" || r.Kind == "security-policy" {
		return equalLocalizedPolicy(r, o)
	}
	if r.Kind == "classic-definition" {
		return equalClassicDefinition(r, o)
	}
	if r.Kind == "custom-app" {
		return equalCustomApplication(r, o)
	}
	if r.Kind == "hierarchy" {
		return equalHierarchy(r, o)
	}
	if r.Kind == "policy-list" {
		return equalPolicyList(r, o)
	}
	if r.Kind == "group" {
		return equalGroup(r, o)
	}
	if r.Kind == "profile" {
		name, ok := o["profileName"].(string)
		if !ok {
			return false, ErrResponse
		}
		description, _ := o["description"].(string)
		return name == r.Name && description == r.Body["description"], nil
	}
	if r.Kind == "association" {
		parcelID, ok := o["parcelId"].(string)
		if !ok || !validID.MatchString(parcelID) {
			return false, ErrResponse
		}
		return parcelID == r.Body["parcelId"], nil
	}
	payload, ok := o["payload"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	want, e := json.Marshal(r.Body)
	if e != nil {
		return false, ErrResponse
	}
	var w map[string]any
	json.Unmarshal(want, &w)
	normalized := map[string]any{"name": payload["name"], "description": payload["description"], "data": payload["data"]}
	// Manager 20.18 adds this disabled feature flag on application lists even
	// though the pinned provider and NaC model do not expose it.
	if r.Type == "policy_object_application_list" {
		if data, ok := normalized["data"].(map[string]any); ok && data["saasApp"] == false {
			delete(data, "saasApp")
		}
	}
	if normalized["description"] == nil {
		normalized["description"] = ""
	}
	return reflect.DeepEqual(w, normalized), nil
}
