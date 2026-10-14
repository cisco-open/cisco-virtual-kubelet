// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
)

func compileServiceEthernetTrackerAssociations(p FeatureProfile, family, key string) ([]Resource, error) {
	out := []Resource{}
	for _, vpn := range p.LANVPNs {
		vpnName, _ := vpn["name"].(string)
		rows, err := vpnRows(vpn, "ethernet_interfaces")
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			input, ok := row.(map[string]any)
			if !ok {
				return nil, errors.New("Ethernet interface must be object")
			}
			interfaceName, _ := input["name"].(string)
			for _, spec := range []struct{ field, parcelPath, targetType, typeName string }{
				{"ipv4_tracker", "tracker/", "service_tracker", "service_lan_vpn_interface_ethernet_associate_tracker"},
				{"ipv4_tracker_group", "trackergroup/", "service_tracker_group", "service_lan_vpn_interface_ethernet_associate_tracker_group"},
			} {
				if input[spec.field] == nil {
					continue
				}
				if family != "service" {
					return nil, errors.New("Ethernet tracker association requires service profile")
				}
				name, ok := input[spec.field].(string)
				if !ok || name == "" {
					return nil, fmt.Errorf("interface %s %s reference requires name", interfaceName, spec.field)
				}
				out = append(out, Resource{
					Key:  key + "/lan_vpns/" + vpnName + "/ethernet_interfaces/" + interfaceName + "/" + spec.field,
					Name: name, Section: "feature_profiles", Kind: "association", Type: spec.typeName,
					Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name,
					ParcelAncestors: []ParcelAncestor{{Path: "lan/vpn", Name: vpnName}, {Path: "interface/ethernet", Name: interfaceName}}, ParcelPath: spec.parcelPath,
					ParcelRefs: []ParcelReference{{Kind: spec.targetType, Name: name, ParcelPath: spec.parcelPath[:len(spec.parcelPath)-1], BodyPath: []string{"parcelId"}}},
					Body:       map[string]any{"parcelId": parcelPlaceholder(name)},
				})
			}
		}
	}
	return out, nil
}
