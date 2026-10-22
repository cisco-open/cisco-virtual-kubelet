// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import "testing"

func TestVPNRoutingAssociationDependencies(t *testing.T) {
	for _, fixture := range []string{"service-lan-vpn-ospf-association", "transport-wan-vpn-ospf-association", "service-lan-vpn-eigrp-association", "service-lan-vpn-multicast-association", "service-lan-vpn-ospfv3-associations"} {
		t.Run(fixture, func(t *testing.T) {
			resources := parcelFixtures(t, fixture)
			for i, association := range resources {
				if association.Kind != "association" {
					continue
				}
				if len(association.ParcelRefs) != 1 || association.ParentParcelName == "" || association.ParentParcelPath == "" || association.Body["parcelId"] == nil {
					t.Fatal("association is missing VPN or routing dependency")
				}
				parents, targets := false, false
				for j, resource := range resources {
					if resource.Kind != "parcel" {
						continue
					}
					if resource.Name == association.ParentParcelName && resource.ParcelPath == association.ParentParcelPath {
						parents = j < i
					}
					if resource.Name == association.ParcelRefs[0].Name && resource.ParcelPath == association.ParcelRefs[0].ParcelPath {
						targets = j < i
					}
				}
				if !parents || !targets {
					t.Fatal("association must follow its VPN parent and routing parcel")
				}
				withoutTarget := append([]Resource{}, resources[:i]...)
				for _, resource := range resources[i+1:] {
					withoutTarget = append(withoutTarget, resource)
				}
				for j, resource := range withoutTarget {
					if resource.Name == association.ParcelRefs[0].Name && resource.ParcelPath == association.ParcelRefs[0].ParcelPath {
						withoutTarget = append(withoutTarget[:j], withoutTarget[j+1:]...)
						break
					}
				}
				withoutTarget = append(withoutTarget, association)
				if _, err := orderParcelDependencies(withoutTarget); err == nil {
					t.Fatal("undeclared routing target was accepted")
				}
			}
		})
	}
}
