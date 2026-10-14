// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
)

func compileSVIDHCPAssociations(p FeatureProfile, family, key string) ([]Resource, error) {
	out := []Resource{}
	for _, vpn := range p.LANVPNs {
		vpnName, _ := vpn["name"].(string)
		rows, err := vpnRows(vpn, "svi_interfaces")
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			input, ok := row.(map[string]any)
			if !ok {
				return nil, errors.New("SVI must be an object")
			}
			if input["dhcp_server"] == nil {
				continue
			}
			if family != "service" {
				return nil, errors.New("SVI DHCP association requires service profile")
			}
			sviName, _ := input["name"].(string)
			dhcpName, ok := input["dhcp_server"].(string)
			if !ok || dhcpName == "" {
				return nil, fmt.Errorf("SVI %s DHCP reference requires name", sviName)
			}
			out = append(out, Resource{
				Key:  key + "/lan_vpns/" + vpnName + "/svi_interfaces/" + sviName + "/dhcp_server",
				Name: dhcpName, Section: "feature_profiles", Kind: "association", Type: "service_lan_vpn_interface_svi_associate_dhcp_server",
				Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name,
				ParcelAncestors: []ParcelAncestor{{Path: "lan/vpn", Name: vpnName}, {Path: "interface/svi", Name: sviName}}, ParcelPath: "dhcp-server/",
				ParcelRefs: []ParcelReference{{Kind: "service_dhcp_server", Name: dhcpName, ParcelPath: "dhcp-server", BodyPath: []string{"parcelId"}}},
				Body:       map[string]any{"parcelId": parcelPlaceholder(dhcpName)},
			})
		}
	}
	return out, nil
}
