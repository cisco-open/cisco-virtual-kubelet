// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestLANLinkNativeResultAddressEvidence(t *testing.T) {
	for _, mode := range []string{"point-to-point-31", "point-to-point-30", "duplicate", "outside-pool", "different-subnets", "network-address", "broadcast", "noncontiguous-mask", "wider-than-pool", "management-alias", "down-port", "down-link", "topology-disagrees"} {
		t.Run(mode, func(t *testing.T) {
			endpoints, topology := lanTopologyFixture()
			endpoints.primary.device.ManagementIP = "198.51.100.100"
			endpoints.secondary.device.ManagementIP = "198.51.100.101"
			endpoints.primary.port.IPv4 = "192.0.2.130"
			endpoints.secondary.port.IPv4 = "192.0.2.131"
			endpoints.primary.port.Mask = "255.255.255.254"
			endpoints.secondary.port.Mask = "255.255.255.254"
			result := &lanLinkCompletionSnapshot{dependencies: &lanLinkDependencies{adjacency: &lanLinkAdjacencySnapshot{endpoints: endpoints, link: &topology.Links[0]}, pool: &lanLinkPoolDependencies{pool: anycastPoolDependencies{primary: fixtureReservations()[0]}}}}
			switch mode {
			case "point-to-point-30":
				endpoints.primary.port.IPv4 = "192.0.2.133"
				endpoints.secondary.port.IPv4 = "192.0.2.134"
				endpoints.primary.port.Mask = "255.255.255.252"
				endpoints.secondary.port.Mask = "255.255.255.252"
			case "duplicate":
				endpoints.secondary.port.IPv4 = endpoints.primary.port.IPv4
			case "outside-pool":
				endpoints.primary.port.IPv4 = "192.0.2.10"
			case "different-subnets":
				endpoints.secondary.port.IPv4 = "192.0.2.133"
			case "network-address":
				endpoints.primary.port.IPv4 = "192.0.2.132"
				endpoints.secondary.port.IPv4 = "192.0.2.133"
				endpoints.primary.port.Mask = "255.255.255.252"
				endpoints.secondary.port.Mask = "255.255.255.252"
			case "broadcast":
				endpoints.primary.port.IPv4 = "192.0.2.133"
				endpoints.secondary.port.IPv4 = "192.0.2.135"
				endpoints.primary.port.Mask = "255.255.255.252"
				endpoints.secondary.port.Mask = "255.255.255.252"
			case "noncontiguous-mask":
				endpoints.primary.port.Mask = "255.0.255.0"
			case "wider-than-pool":
				endpoints.primary.port.Mask = "255.255.255.0"
				endpoints.secondary.port.Mask = "255.255.255.0"
			case "management-alias":
				endpoints.primary.device.ManagementIP = endpoints.primary.port.IPv4
			case "down-port":
				endpoints.secondary.port.Status = "Down"
			case "down-link":
				topology.Links[0].Status = "down"
			case "topology-disagrees":
				topology.Links[0].StartIP = "192.0.2.132"
			}
			err := lanLinkAddNativeResult(result)
			valid := mode == "point-to-point-31" || mode == "point-to-point-30"
			if (err == nil) != valid {
				t.Fatal("unexpected routed interface result", err)
			}
		})
	}
}
