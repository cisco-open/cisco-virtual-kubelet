// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
)

func compileVPNRoutingAssociations(p FeatureProfile, family, key string) ([]Resource, error) {
	if family != "service" && family != "transport" {
		if len(p.LANVPNs) != 0 || p.WANVPN != nil {
			return nil, errors.New("VPN associations require service or transport profile")
		}
		return nil, nil
	}
	type associationSpec struct{ field, targetPath, targetType, childPath string }
	serviceSpecs := []associationSpec{
		{"ospf", "routing/ospf", "service_routing_ospf", "routing/ospf/"},
		{"ospfv3_ipv4", "routing/ospfv3/ipv4", "service_routing_ospfv3_ipv4", "routing/ospfv3/ipv4/"},
		{"ospfv3_ipv6", "routing/ospfv3/ipv6", "service_routing_ospfv3_ipv6", "routing/ospfv3/ipv6/"},
		{"eigrp", "routing/eigrp", "service_routing_eigrp", "routing/eigrp/"},
		{"multicast", "routing/multicast", "service_multicast", "routing/multicast/"},
	}
	out := []Resource{}
	add := func(vpnName, vpnPath, label string, input map[string]any, specs []associationSpec) error {
		for _, spec := range specs {
			value := input[spec.field]
			if value == nil {
				continue
			}
			name, ok := value.(string)
			if !ok || name == "" {
				return fmt.Errorf("%s association requires a named %s parcel", label, spec.field)
			}
			out = append(out, Resource{
				Key:  key + "/" + label + "/" + vpnName + "/" + spec.field,
				Name: name, Section: "feature_profiles", Kind: "association", Type: family + "_vpn_associate_" + spec.field,
				Collection: "/dataservice/v1/feature-profile/sdwan/" + family, ParentName: p.Name,
				ParentParcelPath: vpnPath, ParentParcelName: vpnName, ParcelPath: spec.childPath,
				ParcelRefs: []ParcelReference{{Kind: spec.targetType, Name: name, ParcelPath: spec.targetPath, BodyPath: []string{"parcelId"}}},
				Body:       map[string]any{"parcelId": parcelPlaceholder(name)},
			})
		}
		return nil
	}
	if family == "service" {
		for _, vpn := range p.LANVPNs {
			name, _ := vpn["name"].(string)
			if err := add(name, "lan/vpn", "lan_vpns", vpn, serviceSpecs); err != nil {
				return nil, err
			}
		}
	} else if p.WANVPN != nil {
		name := "wan_vpn"
		if p.WANVPN["name"] != nil {
			name, _ = p.WANVPN["name"].(string)
		}
		if err := add(name, "wan/vpn", "wan_vpn", p.WANVPN, []associationSpec{{"ospf", "routing/ospf", "transport_routing_ospf", "routing/ospf/"}}); err != nil {
			return nil, err
		}
	}
	return out, nil
}
