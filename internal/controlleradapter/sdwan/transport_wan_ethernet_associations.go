// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
)

func compileWANEthernetTrackerAssociations(p FeatureProfile, family, key string) ([]Resource, error) {
	if p.WANVPN == nil {
		return nil, nil
	}
	rows, err := vpnRows(p.WANVPN, "ethernet_interfaces")
	if err != nil {
		return nil, err
	}
	vpnName := "wan_vpn"
	if p.WANVPN["name"] != nil {
		vpnName, _ = p.WANVPN["name"].(string)
	}
	out := []Resource{}
	for _, row := range rows {
		input, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("WAN Ethernet interface must be object")
		}
		interfaceName, _ := input["name"].(string)
		for _, spec := range []struct{ field, path, targetType, typeName string }{
			{"ipv4_tracker", "tracker/", "transport_tracker", "transport_wan_vpn_interface_ethernet_associate_tracker"},
			{"ipv4_tracker_group", "trackergroup/", "transport_tracker_group", "transport_wan_vpn_interface_ethernet_associate_tracker_group"},
			{"ipv6_tracker", "ipv6-tracker/", "transport_ipv6_tracker", "transport_wan_vpn_interface_ethernet_associate_ipv6_tracker"},
			{"ipv6_tracker_group", "ipv6-trackergroup/", "transport_ipv6_tracker_group", "transport_wan_vpn_interface_ethernet_associate_ipv6_tracker_group"},
		} {
			if input[spec.field] == nil {
				continue
			}
			if family != "transport" {
				return nil, errors.New("WAN Ethernet tracker association requires transport profile")
			}
			name, ok := input[spec.field].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("WAN Ethernet %s %s requires name", interfaceName, spec.field)
			}
			out = append(out, Resource{Key: key + "/wan_vpn/ethernet_interfaces/" + interfaceName + "/" + spec.field, Name: name, Section: "feature_profiles", Kind: "association", Type: spec.typeName, Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParcelAncestors: []ParcelAncestor{{Path: "wan/vpn", Name: vpnName}, {Path: "interface/ethernet", Name: interfaceName}}, ParcelPath: spec.path, ParcelRefs: []ParcelReference{{Kind: spec.targetType, Name: name, ParcelPath: spec.path[:len(spec.path)-1], BodyPath: []string{"parcelId"}}}, Body: map[string]any{"parcelId": parcelPlaceholder(name)}})
		}
	}
	return out, nil
}
