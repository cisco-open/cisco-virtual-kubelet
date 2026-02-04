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

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// networkConfig holds the network configuration for an app container
type networkConfig struct {
	interfaceType             config.InterfaceType
	virtualPortgroupInterface string
	virtualPortgroupIP        string
	virtualPortgroupNetmask   string
	guestInterface            uint8
	isPrimaryContainer        bool
	// AppGigabitEthernet specific
	appGigMode           config.AppGigabitEthernetMode
	appGigGuestInterface uint8
	vlanIf               config.VlanInterfaceConfig
	useDHCP              bool
	ipAllocationErr      error
	// Management specific
	mgmtGuestInterface uint8
	mgmtGuestIPv4      string
	mgmtGuestIPv4Mask  string
}

// resourceConfig holds the resource allocation for an app container
type resourceConfig struct {
	cpuUnits uint16
	memoryMB uint16
	diskMB   uint16
	vcpu     uint16
}

// AppHostingConfig represents a complete IOS-XE AppHosting configuration for a single container
type AppHostingConfig struct {
	AppName       string
	ContainerName string
	ImagePath     string
	Apps          *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps
}

// ConvertPodToAppConfigs converts a Kubernetes Pod spec into a slice of IOS-XE AppHosting configurations.
// Each container in the pod is converted to a separate AppHosting app configuration.
// Returns a slice of AppHostingConfig structs ready to be created on the device.
func (d *XEDriver) ConvertPodToAppConfigs(pod *v1.Pod) ([]AppHostingConfig, error) {
	containerAppIDs := common.GenerateContainerAppIDs(pod)
	configs := make([]AppHostingConfig, 0, len(pod.Spec.Containers))

	for _, container := range pod.Spec.Containers {
		appName := containerAppIDs[container.Name]

		// Create the Apps container structure
		apps := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{}

		// Create new app entry
		gapp, err := apps.NewApp(appName)
		if err != nil {
			return nil, fmt.Errorf("failed to create app struct for container %s: %w", container.Name, err)
		}

		// Configure network resources based on interface type
		netConfig := d.getNetworkConfig(pod, &container)

		switch netConfig.interfaceType {
		case config.InterfaceTypeVirtualPortGroup:
			if netConfig.useDHCP {
				// DHCP mode: only set interface name, omit static IP configuration
				gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
					VnicGateway_0:                        ygot.String("0"),
					VirtualportgroupGuestInterfaceName_1: ygot.String(netConfig.virtualPortgroupInterface),
				}
			} else {
				// Static IP mode: configure IP address and netmask (only for primary container)
				if netConfig.isPrimaryContainer && (netConfig.virtualPortgroupIP == "" || netConfig.virtualPortgroupNetmask == "") {
					return nil, fmt.Errorf("podPrefix is required to allocate IPs when dhcp=false")
				}
				netRes := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
					VnicGateway_0:                        ygot.String("0"),
					VirtualportgroupGuestInterfaceName_1: ygot.String(netConfig.virtualPortgroupInterface),
				}
				if netConfig.virtualPortgroupIP != "" && netConfig.virtualPortgroupNetmask != "" {
					netRes.VirtualportgroupGuestIpAddress_1 = ygot.String(netConfig.virtualPortgroupIP)
					netRes.VirtualportgroupGuestIpNetmask_1 = ygot.String(netConfig.virtualPortgroupNetmask)
				}
				gapp.ApplicationNetworkResource = netRes
			}

		case config.InterfaceTypeAppGigabitEthernet:
			guestIf := netConfig.vlanIf.GuestInterface
			if guestIf == 0 && netConfig.appGigGuestInterface != 0 {
				guestIf = netConfig.appGigGuestInterface
			}

			switch netConfig.appGigMode {
			case config.AppGigabitEthernetModeAccess:
				if gapp.ApplicationNetworkResource == nil {
					gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{}
				}
				gapp.ApplicationNetworkResource.AppintfVlanMode = Cisco_IOS_XEAppHostingCfg_ImAppAppintfMode_appintf_access
				gapp.ApplicationNetworkResource.AppintfAccessInterfaceNumber = ygot.Uint8(guestIf)

				if netConfig.vlanIf.Vlan > 0 {
					gapp.AppintfVlanRules = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_AppintfVlanRules{}
					vlanRule, err := gapp.AppintfVlanRules.NewAppintfVlanRule(netConfig.vlanIf.Vlan)
					if err != nil {
						return nil, fmt.Errorf("failed to create VLAN rule for container %s: %w", container.Name, err)
					}
					vlanRule.GuestInterface = ygot.Uint8(guestIf)

					if !netConfig.useDHCP {
						if netConfig.isPrimaryContainer && (netConfig.virtualPortgroupIP == "" || netConfig.virtualPortgroupNetmask == "") {
							return nil, fmt.Errorf("podPrefix is required to allocate IPs when dhcp=false")
						}
						if netConfig.virtualPortgroupIP != "" && netConfig.virtualPortgroupNetmask != "" {
							vlanRule.GuestIp = ygot.String(netConfig.virtualPortgroupIP)
							vlanRule.GuestIpnetmask = ygot.String(netConfig.virtualPortgroupNetmask)
						}
					}
				}

			case config.AppGigabitEthernetModeTrunk:
				if gapp.ApplicationNetworkResource == nil {
					gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{}
				}
				gapp.ApplicationNetworkResource.AppintfVlanMode = Cisco_IOS_XEAppHostingCfg_ImAppAppintfMode_appintf_trunk

				if netConfig.vlanIf.Vlan > 0 {
					gapp.AppintfVlanRules = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_AppintfVlanRules{}
					vlanRule, err := gapp.AppintfVlanRules.NewAppintfVlanRule(netConfig.vlanIf.Vlan)
					if err != nil {
						return nil, fmt.Errorf("failed to create VLAN rule for container %s: %w", container.Name, err)
					}
					vlanRule.GuestInterface = ygot.Uint8(guestIf)
					vlanRule.MacForwardEnable = ygot.Bool(netConfig.vlanIf.MacForwardingEnabled)
					vlanRule.McastEnable = ygot.Bool(netConfig.vlanIf.MulticastEnabled)
					vlanRule.MirrorEnable = ygot.Bool(netConfig.vlanIf.MirrorEnabled)

					if !netConfig.useDHCP {
						if netConfig.isPrimaryContainer && (netConfig.virtualPortgroupIP == "" || netConfig.virtualPortgroupNetmask == "") {
							return nil, fmt.Errorf("podPrefix is required to allocate IPs when dhcp=false")
						}
						if netConfig.virtualPortgroupIP != "" && netConfig.virtualPortgroupNetmask != "" {
							vlanRule.GuestIp = ygot.String(netConfig.virtualPortgroupIP)
							vlanRule.GuestIpnetmask = ygot.String(netConfig.virtualPortgroupNetmask)
						}
					}
				}
			default:
				return nil, fmt.Errorf("unsupported AppGigabitEthernet mode: %s", netConfig.appGigMode)
			}

		case config.InterfaceTypeManagement:
			gapp.AppintfMgmt = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_AppintfMgmt{
				AccessIfNum: ygot.Uint8(netConfig.mgmtGuestInterface),
			}
			gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{}
			gapp.ApplicationNetworkResource.ManagementInterfaceName = ygot.String(fmt.Sprintf("%d", netConfig.mgmtGuestInterface))

			if !netConfig.useDHCP {
				if netConfig.isPrimaryContainer && (netConfig.mgmtGuestIPv4 == "" || netConfig.mgmtGuestIPv4Mask == "") {
					return nil, fmt.Errorf("podPrefix is required to allocate IPs when dhcp=false")
				}
				if netConfig.mgmtGuestIPv4 != "" && netConfig.mgmtGuestIPv4Mask != "" {
					gapp.ApplicationNetworkResource.ManagementGuestIpAddress = ygot.String(netConfig.mgmtGuestIPv4)
					gapp.ApplicationNetworkResource.ManagementGuestIpNetmask = ygot.String(netConfig.mgmtGuestIPv4Mask)
				}
			}

		default:
			return nil, fmt.Errorf("unsupported interface type: %s", netConfig.interfaceType)
		}

		// Configure run options with pod/container labels
		gapp.RunOptss = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{
			RunOpts: map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
				1: {
					LineIndex: ygot.Uint16(1),
					LineRunOpts: ygot.String(fmt.Sprintf(
						"--label %s=%s "+
							"--label %s=%s "+
							"--label %s=%s "+
							"--label %s=%s",
						common.LabelPodName, pod.Name,
						common.LabelPodNamespace, pod.Namespace,
						common.LabelPodUID, pod.UID,
						common.LabelContainerName, container.Name,
					)),
				},
			},
		}

		// Configure resource profile
		resConfig := d.getResourceConfig(&container)
		gapp.ApplicationResourceProfile = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationResourceProfile{
			ProfileName:      ygot.String("custom"),
			CpuUnits:         ygot.Uint16(resConfig.cpuUnits),
			MemoryCapacityMb: ygot.Uint16(resConfig.memoryMB),
			DiskSizeMb:       ygot.Uint16(resConfig.diskMB),
			Vcpu:             ygot.Uint16(resConfig.vcpu),
		}

		// Set app to start automatically
		gapp.Start = ygot.Bool(true)

		// Add to configs slice
		configs = append(configs, AppHostingConfig{
			AppName:       appName,
			ContainerName: container.Name,
			ImagePath:     container.Image,
			Apps:          apps,
		})
	}

	return configs, nil
}

// getNetworkConfig converts pod/container specs to IOS-XE network configuration
func (d *XEDriver) getNetworkConfig(pod *v1.Pod, container *v1.Container) *networkConfig {
	// Check if new interface configuration is present
	if d.config.Networking.Interface != nil {
		cfg := d.getInterfaceConfig(pod, container, d.config.Networking.Interface)
		cfg.isPrimaryContainer = d.getContainerIndex(pod, container) == 0
		return cfg
	}

	// Default to VirtualPortGroup interface with DHCP implied
	return &networkConfig{
		interfaceType:             config.InterfaceTypeVirtualPortGroup,
		useDHCP:                   true,
		virtualPortgroupInterface: "0",
		guestInterface:            0,
		isPrimaryContainer:        d.getContainerIndex(pod, container) == 0,
	}
}

// getInterfaceConfig creates network configuration based on the new interface configuration model
func (d *XEDriver) getInterfaceConfig(pod *v1.Pod, container *v1.Container, ifConfig *config.InterfaceConfig) *networkConfig {
	netConfig := &networkConfig{
		interfaceType: ifConfig.Type,
		useDHCP:       true,
	}

	switch ifConfig.Type {
	case config.InterfaceTypeVirtualPortGroup:
		if ifConfig.VirtualPortGroup != nil {
			vpgInterface := ifConfig.VirtualPortGroup.Interface
			if vpgInterface == "" {
				vpgInterface = "0"
			}
			netConfig.virtualPortgroupInterface = vpgInterface
			netConfig.guestInterface = ifConfig.VirtualPortGroup.GuestInterface
			netConfig.useDHCP = ifConfig.VirtualPortGroup.Dhcp
			if !netConfig.useDHCP {
				ip, netmask, err := d.allocateIPForContainer(pod, container)
				netConfig.virtualPortgroupIP = ip
				netConfig.virtualPortgroupNetmask = netmask
				netConfig.ipAllocationErr = err
			}
		}

	case config.InterfaceTypeAppGigabitEthernet:
		if ifConfig.AppGigabitEthernet != nil {
			netConfig.appGigMode = ifConfig.AppGigabitEthernet.Mode
			netConfig.appGigGuestInterface = ifConfig.AppGigabitEthernet.GuestInterface
			netConfig.vlanIf = ifConfig.AppGigabitEthernet.VlanIf
			netConfig.useDHCP = ifConfig.AppGigabitEthernet.VlanIf.Dhcp
			if !netConfig.useDHCP {
				ip, netmask, err := d.allocateIPForContainer(pod, container)
				netConfig.virtualPortgroupIP = ip
				netConfig.virtualPortgroupNetmask = netmask
				netConfig.ipAllocationErr = err
			}
		}

	case config.InterfaceTypeManagement:
		if ifConfig.Management != nil {
			netConfig.mgmtGuestInterface = ifConfig.Management.GuestInterface
			netConfig.useDHCP = ifConfig.Management.Dhcp
			if !netConfig.useDHCP {
				ip, netmask, err := d.allocateIPForContainer(pod, container)
				netConfig.mgmtGuestIPv4 = ip
				netConfig.mgmtGuestIPv4Mask = netmask
				netConfig.ipAllocationErr = err
			}
		}
	}

	return netConfig
}

// allocateIPForContainer determines the IP address for a container based on pod prefix configuration
func (d *XEDriver) allocateIPForContainer(pod *v1.Pod, container *v1.Container) (ip, netmask string, err error) {
	if d.config.Networking.PodPrefix == "" {
		return "", "", fmt.Errorf("podPrefix is empty")
	}

	_, ipNet, parseErr := net.ParseCIDR(d.config.Networking.PodPrefix)
	if parseErr != nil {
		return "", "", fmt.Errorf("invalid podPrefix: %w", parseErr)
	}

	netmask = net.IP(ipNet.Mask).String()
	containerIndex := d.getContainerIndex(pod, container)
	if containerIndex != 0 {
		return "", "", nil
	}
	ip = d.getIPForContainer(ipNet, containerIndex)
	return ip, netmask, nil
}

// getContainerIndex returns the index of a container within a pod's container list
func (d *XEDriver) getContainerIndex(pod *v1.Pod, container *v1.Container) int {
	for i, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			return i
		}
	}
	return 0
}

// getIPForContainer calculates the IP address for a container based on its index
func (d *XEDriver) getIPForContainer(ipNet *net.IPNet, containerIndex int) string {
	ip := ipNet.IP.To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + uint8(10+containerIndex)
	return ip.String()
}

// allocateIPForContainer determines the IP address for a container based on pod CIDR configuration

// getResourceConfig converts Kubernetes resource requests/limits to IOS-XE resource configuration
func (d *XEDriver) getResourceConfig(container *v1.Container) *resourceConfig {
	config := &resourceConfig{
		cpuUnits: 1000,
		memoryMB: 512,
		diskMB:   1024,
		vcpu:     1,
	}

	if container.Resources.Requests != nil {
		if cpu := container.Resources.Requests.Cpu(); cpu != nil && !cpu.IsZero() {
			config.cpuUnits = uint16(cpu.MilliValue())
		}
		if mem := container.Resources.Requests.Memory(); mem != nil && !mem.IsZero() {
			config.memoryMB = uint16(mem.Value() / (1024 * 1024))
		}
		if storage := container.Resources.Requests.Storage(); storage != nil && !storage.IsZero() {
			config.diskMB = uint16(storage.Value() / (1024 * 1024))
		}
	}

	if container.Resources.Limits != nil {
		if cpu := container.Resources.Limits.Cpu(); cpu != nil && !cpu.IsZero() {
			milliCores := cpu.MilliValue()
			config.vcpu = uint16((milliCores + 999) / 1000)
			if config.vcpu < 1 {
				config.vcpu = 1
			}
		}
	}

	if d.config.ResourceLimits.DefaultCPU != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultCPU); err == nil {
			config.cpuUnits = uint16(q.MilliValue())
		}
	}
	if d.config.ResourceLimits.DefaultMemory != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultMemory); err == nil {
			config.memoryMB = uint16(q.Value() / (1024 * 1024))
		}
	}
	if d.config.ResourceLimits.DefaultStorage != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultStorage); err == nil {
			config.diskMB = uint16(q.Value() / (1024 * 1024))
		}
	}

	return config
}

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

// GetContainerStatus maps IOS-XE app operational data to Kubernetes container statuses
func (d *XEDriver) GetContainerStatus(ctx context.Context, pod *v1.Pod,
	discoveredContainers map[string]string,
	appOperData map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) error {

	now := metav1.Now()

	// Try to discover Pod IP from the first container's network interface
	podIP := d.discoverPodIP(ctx, discoveredContainers, appOperData)

	pod.Status = v1.PodStatus{
		Phase:     v1.PodPending,
		HostIP:    d.config.Address,
		PodIP:     podIP,
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	allReady := true
	anyRunning := false

	for containerName, appID := range discoveredContainers {
		var containerSpec *v1.Container
		for i := range pod.Spec.Containers {
			if pod.Spec.Containers[i].Name == containerName {
				containerSpec = &pod.Spec.Containers[i]
				break
			}
		}

		if containerSpec == nil {
			log.G(ctx).Warnf("Container spec not found for %s (appID: %s)", containerName, appID)
			continue
		}

		operData := appOperData[appID]

		containerStatus := v1.ContainerStatus{
			Name:        containerName,
			Image:       containerSpec.Image,
			ImageID:     containerSpec.Image,
			ContainerID: fmt.Sprintf("cisco://%s", appID),
			Ready:       false,
		}

		if operData != nil && operData.Details != nil && operData.Details.State != nil {
			state := *operData.Details.State

			switch state {
			case "RUNNING":
				containerStatus.State = v1.ContainerState{
					Running: &v1.ContainerStateRunning{
						StartedAt: now,
					},
				}
				containerStatus.Ready = true
				anyRunning = true
			case "DEPLOYED", "ACTIVATED":
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "ContainerCreating",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			case "STOPPED", "Uninstalled":
				containerStatus.State = v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						ExitCode:   0,
						Reason:     "Completed",
						FinishedAt: now,
					},
				}
				allReady = false
			default:
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "Unknown",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			}

			log.G(ctx).Infof("Container %s (app: %s) state: %s, ready: %v",
				containerName, appID, state, containerStatus.Ready)
		} else {
			containerStatus.State = v1.ContainerState{
				Waiting: &v1.ContainerStateWaiting{
					Reason:  "ContainerCreating",
					Message: "No operational data available",
				},
			}
			allReady = false
			log.G(ctx).Warnf("No operational data for container %s (app: %s)", containerName, appID)
		}

		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}

	if anyRunning && allReady {
		pod.Status.Phase = v1.PodRunning
		for i := range pod.Status.Conditions {
			if pod.Status.Conditions[i].Type == v1.PodReady ||
				pod.Status.Conditions[i].Type == v1.PodInitialized {
				pod.Status.Conditions[i].Status = v1.ConditionTrue
			}
		}
	} else if anyRunning {
		pod.Status.Phase = v1.PodRunning
	}

	log.G(ctx).Infof("Pod %s/%s status: Phase=%s, Containers=%d/%d ready",
		pod.Namespace, pod.Name, pod.Status.Phase,
		len(pod.Status.ContainerStatuses), len(pod.Spec.Containers))

	return nil
}
