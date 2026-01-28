package iosxe

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ArpData represents the Cisco-IOS-XE-arp-oper:arp-data structure
type ArpData struct {
	ArpVrf []ArpVrf `json:"Cisco-IOS-XE-arp-oper:arp-vrf"`
}

// ArpVrf represents a VRF's ARP entries
type ArpVrf struct {
	Vrf      string     `json:"vrf"`
	ArpEntry []ArpEntry `json:"arp-entry"`
}

// ArpEntry represents a single ARP table entry
type ArpEntry struct {
	Address   string `json:"address"`
	Hardware  string `json:"hardware"`
	Mode      string `json:"mode"`
	Interface string `json:"interface"`
}

// networkConfig holds the network configuration for an app container
type networkConfig struct {
	useDHCP                   bool
	virtualPortgroupInterface string
	virtualPortgroupIP        string
	virtualPortgroupNetmask   string
	defaultGateway            string
	gatewayInterface          uint8
}

// resourceConfig holds the resource allocation for an app container
type resourceConfig struct {
	cpuUnits uint16
	memoryMB uint16
	diskMB   uint16
	vcpu     uint16
}

// getNetworkConfig converts pod/container specs to IOS-XE network configuration
func (d *XEDriver) getNetworkConfig(pod *v1.Pod, container *v1.Container) *networkConfig {
	// Determine virtualportgroup interface (default to "0" for VirtualPortGroup0)
	vpgInterface := d.config.Networking.VirtualPortGroup
	if vpgInterface == "" {
		vpgInterface = "0"
	}

	// If DHCP is enabled, return minimal config without static IP settings
	if d.config.Networking.DHCPEnabled {
		return &networkConfig{
			useDHCP:                   true,
			virtualPortgroupInterface: vpgInterface,
		}
	}

	// Static IP mode: allocate IP from PodCIDR or use defaults
	ip, netmask, gateway := d.allocateIPForContainer(pod, container)

	return &networkConfig{
		useDHCP:                   false,
		virtualPortgroupInterface: vpgInterface,
		virtualPortgroupIP:        ip,
		virtualPortgroupNetmask:   netmask,
		defaultGateway:            gateway,
		gatewayInterface:          0,
	}
}

// allocateIPForContainer determines the IP address for a container based on pod CIDR configuration
func (d *XEDriver) allocateIPForContainer(pod *v1.Pod, container *v1.Container) (ip, netmask, gateway string) {
	if d.config.Networking.PodCIDR != "" {
		_, ipNet, err := net.ParseCIDR(d.config.Networking.PodCIDR)
		if err == nil {
			netmask = net.IP(ipNet.Mask).String()
			gateway = d.getGatewayFromCIDR(ipNet)
			containerIndex := d.getContainerIndex(pod, container)
			ip = d.getIPForContainer(ipNet, containerIndex)
			return
		}
	}

	return "1.1.1.10", "255.255.255.0", "1.1.1.1"
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

// getGatewayFromCIDR calculates the gateway IP (first usable IP) from a CIDR
func (d *XEDriver) getGatewayFromCIDR(ipNet *net.IPNet) string {
	ip := ipNet.IP.To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + 1
	return ip.String()
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
			if netIf.Ipv4Address != nil && *netIf.Ipv4Address != "" {
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

	arpPath := "/restconf/data/Cisco-IOS-XE-arp-oper:arp-data/arp-vrf"

	// Use a simple JSON unmarshaller for ARP data (not ygot)
	jsonUnmarshaller := func(data []byte, v any) error {
		return json.Unmarshal(data, v)
	}

	arpData := &ArpData{}
	err := d.client.Get(ctx, arpPath, arpData, jsonUnmarshaller)
	if err != nil {
		return "", fmt.Errorf("failed to fetch ARP data: %w", err)
	}

	// Normalize MAC addresses for comparison (lowercase, consistent format)
	normalizedMacs := make(map[string]bool)
	for _, mac := range macAddresses {
		normalizedMacs[normalizeMacAddress(mac)] = true
	}

	// Search through all VRFs for matching MAC address
	for _, vrf := range arpData.ArpVrf {
		for _, entry := range vrf.ArpEntry {
			normalizedArpMac := normalizeMacAddress(entry.Hardware)
			if normalizedMacs[normalizedArpMac] {
				log.G(ctx).Debugf("Found ARP entry: IP=%s, MAC=%s, VRF=%s, Interface=%s",
					entry.Address, entry.Hardware, vrf.Vrf, entry.Interface)
				return entry.Address, nil
			}
		}
	}

	return "", fmt.Errorf("no ARP entry found for MAC addresses: %v", macAddresses)
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
			case "DEPLOYED", "Activated":
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
