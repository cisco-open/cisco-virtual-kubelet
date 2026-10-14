// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
)

func compileTransportGRETrackerAssociations(p FeatureProfile, family, key string) ([]Resource, error) {
	if p.WANVPN == nil {
		return nil, nil
	}
	rows, err := vpnRows(p.WANVPN, "gre_interfaces")
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
			return nil, errors.New("WAN GRE interface must be object")
		}
		if input["ipv4_tracker"] == nil {
			continue
		}
		if family != "transport" {
			return nil, errors.New("WAN GRE tracker requires transport profile")
		}
		interfaceName, _ := input["name"].(string)
		name, ok := input["ipv4_tracker"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("WAN GRE %s tracker requires name", interfaceName)
		}
		out = append(out, Resource{Key: key + "/wan_vpn/gre_interfaces/" + interfaceName + "/ipv4_tracker", Name: name, Section: "feature_profiles", Kind: "association", Type: "transport_wan_vpn_interface_gre_associate_tracker", Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParcelAncestors: []ParcelAncestor{{Path: "wan/vpn", Name: vpnName}, {Path: "interface/gre", Name: interfaceName}}, ParcelPath: "tracker/", ParcelRefs: []ParcelReference{{Kind: "transport_tracker", Name: name, ParcelPath: "tracker", BodyPath: []string{"parcelId"}}}, Body: map[string]any{"parcelId": parcelPlaceholder(name)}})
	}
	return out, nil
}
