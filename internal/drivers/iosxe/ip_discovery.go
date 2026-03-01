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
	"net"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// discoverPodIP extracts the IPv4 address from the first container's network interface.
// If app-hosting oper data doesn't have an IP, falls back to ARP table lookup using MAC address.
func (d *XEDriver) discoverPodIP(ctx context.Context,
	discoveredContainers map[string]string,
	appOperData map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) string {

	// Default fallback IP
	defaultIP := "0.0.0.0"

	// Collect MAC addresses for ARP fallback
	var macAddresses []string

	// Try to get IP from the first container's operational data
	for containerName, appID := range discoveredContainers {
		operData := appOperData[appID]
		if operData == nil || operData.NetworkInterfaces == nil {
			continue
		}

		// Iterate through network interfaces to find an IPv4 address or MAC
		for macAddr, netIf := range operData.NetworkInterfaces.NetworkInterface {
			// First, try to get IP directly from app-hosting oper data
			if netIf.Ipv4Address != nil && *netIf.Ipv4Address != "" && isValidPodIP(*netIf.Ipv4Address) {
				ipAddress := *netIf.Ipv4Address
				log.G(ctx).Infof("Discovered Pod IP from app-hosting oper data - container %s (app: %s, MAC: %s): %s",
					containerName, appID, macAddr, ipAddress)
				return ipAddress
			}

			// Collect MAC address for ARP fallback
			if macAddr != "" {
				macAddresses = append(macAddresses, macAddr)
				log.G(ctx).Debugf("Collected MAC address %s from container %s (app: %s) for ARP lookup",
					macAddr, containerName, appID)
			}
		}
	}

	// Fallback: Look up MAC addresses in ARP table
	if len(macAddresses) > 0 {
		log.G(ctx).Debug("No IP in app-hosting oper data, attempting ARP table lookup")
		ipAddress, err := d.lookupIPInArpTable(ctx, macAddresses)
		if err != nil {
			log.G(ctx).Warnf("ARP lookup failed: %v", err)
		} else if ipAddress != "" {
			log.G(ctx).Infof("Discovered Pod IP from ARP table: %s", ipAddress)
			return ipAddress
		}
	}

	log.G(ctx).Debug("No IPv4 address found in app-hosting or ARP table, using default")
	return defaultIP
}

// lookupIPInArpTable queries the device ARP table to find an IP for the given MAC addresses
func (d *XEDriver) lookupIPInArpTable(ctx context.Context, macAddresses []string) (string, error) {
	if d.client == nil {
		return "", fmt.Errorf("network client not initialized")
	}

	arpPath := "/restconf/data/Cisco-IOS-XE-arp-oper:arp-data"

	arpData := &Cisco_IOS_XEArpOper_ArpData{}
	err := d.client.Get(ctx, arpPath, arpData, d.getRestconfUnmarshaller())
	if err != nil {
		return "", fmt.Errorf("failed to fetch ARP data: %w", err)
	}

	// Normalize MAC addresses for comparison (lowercase, consistent format)
	normalizedMacs := make(map[string]bool)
	for _, mac := range macAddresses {
		normalizedMacs[normalizeMacAddress(mac)] = true
	}

	// Search through all VRFs for matching MAC address
	for vrfName, vrf := range arpData.ArpVrf {
		// Use ArpOper entries (keyed by address)
		for _, entry := range vrf.ArpOper {
			if entry.Hardware == nil {
				continue
			}
			normalizedArpMac := normalizeMacAddress(*entry.Hardware)
			if normalizedMacs[normalizedArpMac] {
				if entry.Address != nil {
					intfName := ""
					if entry.Interface != nil {
						intfName = *entry.Interface
					}
					log.G(ctx).Debugf("Found ARP entry: IP=%s, MAC=%s, VRF=%s, Interface=%s",
						*entry.Address, *entry.Hardware, vrfName, intfName)
					return *entry.Address, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no ARP entry found for MAC addresses: %v", macAddresses)
}

// isValidPodIP checks if an IP address is valid for use as a Pod IP
// Returns false for unassigned/invalid IPs like 0.0.0.0
func isValidPodIP(ip string) bool {
	if ip == "" || ip == "0.0.0.0" {
		return false
	}
	// Parse and validate the IP
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	// Check for unspecified address (0.0.0.0 or ::)
	if parsedIP.IsUnspecified() {
		return false
	}
	return true
}

// normalizeMacAddress converts a MAC address to lowercase with colons for consistent comparison
func normalizeMacAddress(mac string) string {
	// Remove common separators and convert to lowercase
	mac = strings.ToLower(mac)
	mac = strings.ReplaceAll(mac, "-", "")
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, ".", "")

	// Reformat to colon-separated if we have 12 hex chars
	if len(mac) == 12 {
		return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
			mac[0:2], mac[2:4], mac[4:6], mac[6:8], mac[8:10], mac[10:12])
	}
	return mac
}
