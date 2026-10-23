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
		for _, spec := range []struct{ field, parcelPath, typeName string }{{"svi_interfaces", "interface/svi", "service_lan_vpn_interface_svi_associate_dhcp_server"}, {"ethernet_interfaces", "interface/ethernet", "service_lan_vpn_interface_ethernet_associate_dhcp_server"}} {
			rows, err := vpnRows(vpn, spec.field)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				input, ok := row.(map[string]any)
				if !ok {
					return nil, errors.New("VPN interface must be an object")
				}
				if input["dhcp_server"] == nil {
					continue
				}
				if family != "service" {
					return nil, errors.New("VPN DHCP association requires service profile")
				}
				interfaceName, _ := input["name"].(string)
				dhcpName, ok := input["dhcp_server"].(string)
				if !ok || dhcpName == "" {
					return nil, fmt.Errorf("interface %s DHCP reference requires name", interfaceName)
				}
				out = append(out, Resource{
					Key:  key + "/lan_vpns/" + vpnName + "/" + spec.field + "/" + interfaceName + "/dhcp_server",
					Name: dhcpName, Section: "feature_profiles", Kind: "association", Type: spec.typeName,
					Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name,
					ParcelAncestors: []ParcelAncestor{{Path: "lan/vpn", Name: vpnName}, {Path: spec.parcelPath, Name: interfaceName}}, ParcelPath: "dhcp-server/",
					ParcelRefs: []ParcelReference{{Kind: "service_dhcp_server", Name: dhcpName, ParcelPath: "dhcp-server", BodyPath: []string{"parcelId"}}},
					Body:       map[string]any{"parcelId": parcelPlaceholder(dhcpName)},
				})
			}
		}
	}
	return out, nil
}
