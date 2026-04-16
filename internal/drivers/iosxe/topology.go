// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iosxe

import (
	"context"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// GetCDPNeighbors queries the device for CDP neighbor details via RESTCONF
// using the Cisco-IOS-XE-cdp-oper YANG model.
func (d *XEDriver) GetCDPNeighbors(ctx context.Context) ([]common.CDPNeighbor, error) {
	path := "/restconf/data/Cisco-IOS-XE-cdp-oper:cdp-neighbor-details"

	root := &Cisco_IOS_XECdpOper_CdpNeighborDetails{}
	err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch CDP neighbor data: %w", err)
	}

	var neighbors []common.CDPNeighbor
	for _, detail := range root.CdpNeighborDetail {
		if detail == nil {
			continue
		}
		n := common.CDPNeighbor{}
		if detail.DeviceName != nil {
			n.DeviceID = *detail.DeviceName
		}
		if detail.IpAddress != nil {
			n.IP = *detail.IpAddress
		}
		if detail.LocalIntfName != nil {
			n.LocalInterface = *detail.LocalIntfName
		}
		if detail.PortId != nil {
			n.RemoteInterface = *detail.PortId
		}
		if detail.PlatformName != nil {
			n.Platform = *detail.PlatformName
		}
		if detail.Capability != nil {
			n.Capabilities = *detail.Capability
		}
		neighbors = append(neighbors, n)
	}

	log.G(ctx).Debugf("Discovered %d CDP neighbors", len(neighbors))
	return neighbors, nil
}

// GetOSPFNeighbors queries the device for OSPF neighbor adjacencies via RESTCONF
// using the Cisco-IOS-XE-ospf-oper YANG model.
// It also populates the DeviceInfo.RouterID field if available.
func (d *XEDriver) GetOSPFNeighbors(ctx context.Context) ([]common.OSPFNeighbor, error) {
	path := "/restconf/data/Cisco-IOS-XE-ospf-oper:ospf-oper-data"

	root := &Cisco_IOS_XEOspfOper_OspfOperData{}
	err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OSPF oper data: %w", err)
	}

	var neighbors []common.OSPFNeighbor

	// Extract router ID and neighbors from ospf-state (OSPFv2 classic path)
	if root.OspfState != nil {
		for _, instance := range root.OspfState.OspfInstance {
			if instance == nil {
				continue
			}

			// Capture the router ID for device identity enrichment
			if instance.RouterId != nil && d.deviceInfo != nil && d.deviceInfo.RouterID == "" {
				d.deviceInfo.RouterID = uint32ToIPv4(*instance.RouterId)
			}

			for _, area := range instance.OspfArea {
				if area == nil {
					continue
				}
				areaStr := "0"
				if area.AreaId != nil {
					areaStr = fmt.Sprintf("%d", *area.AreaId)
				}

				for intfName, intf := range area.OspfInterface {
					if intf == nil {
						continue
					}
					for _, nbr := range intf.OspfNeighbor {
						if nbr == nil {
							continue
						}
						n := common.OSPFNeighbor{
							Interface: intfName,
							Area:      areaStr,
						}
						if nbr.NeighborId != nil {
							n.NeighborID = *nbr.NeighborId
						}
						if nbr.Address != nil {
							n.Address = *nbr.Address
						}
						n.State = nbrStateToString(nbr.State)
						neighbors = append(neighbors, n)
					}
				}
			}
		}
	}

	// Also check ospfv2-instance path (newer YANG structure)
	if root.Ospfv2Instance != nil {
		for _, instance := range root.Ospfv2Instance {
			if instance == nil {
				continue
			}
			if instance.RouterId != nil && d.deviceInfo != nil && d.deviceInfo.RouterID == "" {
				d.deviceInfo.RouterID = uint32ToIPv4(*instance.RouterId)
			}
		}
	}

	log.G(ctx).Debugf("Discovered %d OSPF neighbors", len(neighbors))
	return neighbors, nil
}

// GetInterfaceStats queries the device for interface operational data via RESTCONF
// using the Cisco-IOS-XE-interfaces-oper YANG model.
func (d *XEDriver) GetInterfaceStats(ctx context.Context) ([]common.InterfaceStats, error) {
	path := "/restconf/data/Cisco-IOS-XE-interfaces-oper:interfaces"

	root := &Cisco_IOS_XEInterfacesOper_Interfaces{}
	err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch interface oper data: %w", err)
	}

	var stats []common.InterfaceStats
	for _, intf := range root.Interface {
		if intf == nil {
			continue
		}
		s := common.InterfaceStats{}
		if intf.Name != nil {
			s.Name = *intf.Name
		}
		s.OperStatus = operStateToString(intf.OperStatus)
		if intf.Speed != nil {
			s.Speed = *intf.Speed
		}
		if intf.Ipv4 != nil {
			s.IPv4Address = *intf.Ipv4
		}

		if intf.Statistics != nil {
			if intf.Statistics.InOctets != nil {
				s.InOctets = *intf.Statistics.InOctets
			}
			if intf.Statistics.OutOctets_64 != nil {
				s.OutOctets = *intf.Statistics.OutOctets_64
			} else if intf.Statistics.OutOctets != nil {
				s.OutOctets = uint64(*intf.Statistics.OutOctets)
			}
			if intf.Statistics.RxKbps != nil {
				s.InBitsPerSec = *intf.Statistics.RxKbps * 1000
			}
			if intf.Statistics.TxKbps != nil {
				s.OutBitsPerSec = *intf.Statistics.TxKbps * 1000
			}
		}
		stats = append(stats, s)
	}

	log.G(ctx).Debugf("Collected stats for %d interfaces", len(stats))
	return stats, nil
}

// GetInterfaceIPs queries the device for interface IP addresses via RESTCONF
// using the Cisco-IOS-XE-interfaces-oper YANG model.
func (d *XEDriver) GetInterfaceIPs(ctx context.Context) ([]common.InterfaceIP, error) {
	path := "/restconf/data/Cisco-IOS-XE-interfaces-oper:interfaces"

	root := &Cisco_IOS_XEInterfacesOper_Interfaces{}
	err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch interface data for IPs: %w", err)
	}

	var ips []common.InterfaceIP
	for _, intf := range root.Interface {
		if intf == nil || intf.Ipv4 == nil || *intf.Ipv4 == "" {
			continue
		}
		ip := common.InterfaceIP{}
		if intf.Name != nil {
			ip.Interface = *intf.Name
		}
		ip.IPv4 = *intf.Ipv4
		ip.Status = operStateToString(intf.OperStatus)
		ips = append(ips, ip)
	}

	log.G(ctx).Debugf("Discovered %d interface IPs", len(ips))
	return ips, nil
}

// nbrStateToString converts an OSPF neighbor state enum to a human-readable string.
func nbrStateToString(state E_Cisco_IOS_XEOspfOper_NbrStateType) string {
	switch state {
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_down:
		return "down"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_attempt:
		return "attempt"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_init:
		return "init"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_two_way:
		return "2way"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_exchange_start:
		return "exstart"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_exchange:
		return "exchange"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_loading:
		return "loading"
	case Cisco_IOS_XEOspfOper_NbrStateType_ospf_nbr_full:
		return "full"
	default:
		return "unknown"
	}
}

// operStateToString converts an interface oper-state enum to "up" or "down".
func operStateToString(state E_Cisco_IOS_XEInterfacesOper_OperState) string {
	if state == Cisco_IOS_XEInterfacesOper_OperState_if_oper_state_ready {
		return "up"
	}
	return "down"
}

// uint32ToIPv4 converts a uint32 OSPF router ID to dotted-decimal notation.
func uint32ToIPv4(id uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		(id>>24)&0xFF,
		(id>>16)&0xFF,
		(id>>8)&0xFF,
		id&0xFF,
	)
}
