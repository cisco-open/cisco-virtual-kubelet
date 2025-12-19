package cisco

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// NetworkManager manages networking for Cisco devices
type NetworkManager struct {
	config          *CiscoConfig
	networkSpaces   map[string]*NetworkNamespace
	vlanAllocations map[int]bool
	vrfAllocations  map[string]string // VRF name -> namespace
	ipamManager     *IPAMManager
	mutex           sync.RWMutex
}

// IPAMManager manages IP address allocation
type IPAMManager struct {
	subnets map[string]*IPSubnet
	mutex   sync.RWMutex
}

// IPSubnet represents an IP subnet for allocation
type IPSubnet struct {
	CIDR        string
	Network     *net.IPNet
	Allocated   map[string]bool // IP -> allocated
	NextIP      net.IP
	Gateway     net.IP
}

// NewNetworkManager creates a new network manager
func NewNetworkManager(config *CiscoConfig) *NetworkManager {
	nm := &NetworkManager{
		config:          config,
		networkSpaces:   make(map[string]*NetworkNamespace),
		vlanAllocations: make(map[int]bool),
		vrfAllocations:  make(map[string]string),
		ipamManager:     NewIPAMManager(config),
	}

	// Pre-allocate reserved VLANs
	for i := 1; i < config.Networking.VLANRange.Start; i++ {
		nm.vlanAllocations[i] = true
	}
	for i := config.Networking.VLANRange.End + 1; i <= 4094; i++ {
		nm.vlanAllocations[i] = true
	}

	return nm
}

// NewIPAMManager creates a new IPAM manager
func NewIPAMManager(config *CiscoConfig) *IPAMManager {
	ipam := &IPAMManager{
		subnets: make(map[string]*IPSubnet),
	}

	// Initialize pod CIDR subnet
	if config.Networking.PodCIDR != "" {
		ipam.AddSubnet("pods", config.Networking.PodCIDR)
	}

	// Initialize service CIDR subnet  
	if config.Networking.ServiceCIDR != "" {
		ipam.AddSubnet("services", config.Networking.ServiceCIDR)
	}

	return ipam
}

// CreateNetworkNamespace creates a network namespace for a container
func (nm *NetworkManager) CreateNetworkNamespace(ctx context.Context, device *ManagedDevice, spec ContainerSpec) (string, error) {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	namespace := getNamespaceFromLabels(spec.Labels)
	podName := spec.Name

	// Generate network namespace ID
	networkID := fmt.Sprintf("%s-%s-%s", device.Config.Name, namespace, podName)

	// Check if network namespace already exists
	if _, exists := nm.networkSpaces[networkID]; exists {
		return networkID, nil
	}

	// Allocate VLAN
	vlanID := nm.allocateVLAN()
	if vlanID == -1 {
		return "", fmt.Errorf("no available VLANs")
	}

	// Allocate VRF (use namespace-based VRF)
	vrfName := nm.allocateVRF(namespace)

	// Allocate IP subnet for the pod
	subnet, gateway, err := nm.ipamManager.AllocateSubnet(networkID, 24)
	if err != nil {
		nm.deallocateVLAN(vlanID)
		return "", fmt.Errorf("failed to allocate subnet: %v", err)
	}

	// Create network namespace object
	ns := &NetworkNamespace{
		ID:         networkID,
		Name:       fmt.Sprintf("ns-%s", podName),
		VRF:        vrfName,
		VLAN:       vlanID,
		CIDR:       subnet,
		Gateway:    gateway,
		DNSServers: nm.config.Networking.DNSServers,
		Labels:     spec.Labels,
		DeviceID:   device.Config.Name,
	}

	// Configure networking on the device
	if err := nm.configureDeviceNetworking(ctx, device, ns); err != nil {
		nm.deallocateVLAN(vlanID)
		nm.ipamManager.ReleaseSubnet(networkID)
		return "", fmt.Errorf("failed to configure device networking: %v", err)
	}

	nm.networkSpaces[networkID] = ns
	log.G(ctx).Infof("Created network namespace %s (VLAN %d, VRF %s) on device %s", 
		networkID, vlanID, vrfName, device.Config.Name)

	return networkID, nil
}

// DeleteNetworkNamespace removes a network namespace
func (nm *NetworkManager) DeleteNetworkNamespace(ctx context.Context, device *ManagedDevice, networkID string) error {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	ns, exists := nm.networkSpaces[networkID]
	if !exists {
		return nil // Already deleted
	}

	// Remove networking configuration from device
	if err := nm.removeDeviceNetworking(ctx, device, ns); err != nil {
		log.G(ctx).Errorf("Failed to remove device networking for %s: %v", networkID, err)
		// Continue with cleanup despite error
	}

	// Deallocate resources
	nm.deallocateVLAN(ns.VLAN)
	nm.ipamManager.ReleaseSubnet(networkID)

	delete(nm.networkSpaces, networkID)
	log.G(ctx).Infof("Deleted network namespace %s from device %s", networkID, device.Config.Name)

	return nil
}

// configureDeviceNetworking configures networking on the Cisco device
func (nm *NetworkManager) configureDeviceNetworking(ctx context.Context, device *ManagedDevice, ns *NetworkNamespace) error {
	client, ok := device.Client.(*IOSXEClient)
	if !ok {
		return fmt.Errorf("unsupported client type for device %s", device.Config.Name)
	}

	// Create VRF if it doesn't exist
	if err := client.CreateVRF(ctx, ns.VRF); err != nil {
		log.G(ctx).Errorf("Failed to create VRF %s: %v", ns.VRF, err)
	}

	// Create VLAN
	vlanName := fmt.Sprintf("pod-%s", ns.Name)
	if err := client.CreateVLAN(ctx, ns.VLAN, vlanName); err != nil {
		return fmt.Errorf("failed to create VLAN %d: %v", ns.VLAN, err)
	}

	// Configure VLAN interface with VRF
	if err := nm.configureVLANInterface(ctx, client, ns); err != nil {
		return fmt.Errorf("failed to configure VLAN interface: %v", err)
	}

	return nil
}

// configureVLANInterface configures a VLAN interface with VRF assignment
func (nm *NetworkManager) configureVLANInterface(ctx context.Context, client *IOSXEClient, ns *NetworkNamespace) error {
	// Parse CIDR to get network and gateway
	_, network, err := net.ParseCIDR(ns.CIDR)
	if err != nil {
		return fmt.Errorf("invalid CIDR %s: %v", ns.CIDR, err)
	}

	// Calculate interface IP (typically gateway + 1)
	interfaceIP := ns.Gateway

	// Create interface configuration commands
	commands := []string{
		fmt.Sprintf("interface vlan %d", ns.VLAN),
		fmt.Sprintf("vrf forwarding %s", ns.VRF),
		fmt.Sprintf("ip address %s %s", interfaceIP, net.IP(network.Mask).String()),
		"no shutdown",
	}

	// Execute configuration commands
	for _, cmd := range commands {
		_, err := client.ExecuteCommand(cmd)
		if err != nil {
			log.G(ctx).Errorf("Failed to execute command '%s': %v", cmd, err)
		}
	}

	return nil
}

// removeDeviceNetworking removes networking configuration from device
func (nm *NetworkManager) removeDeviceNetworking(ctx context.Context, device *ManagedDevice, ns *NetworkNamespace) error {
	client, ok := device.Client.(*IOSXEClient)
	if !ok {
		return fmt.Errorf("unsupported client type for device %s", device.Config.Name)
	}

	// Remove VLAN interface
	commands := []string{
		fmt.Sprintf("no interface vlan %d", ns.VLAN),
		fmt.Sprintf("no vlan %d", ns.VLAN),
	}

	for _, cmd := range commands {
		_, err := client.ExecuteCommand(cmd)
		if err != nil {
			log.G(ctx).Errorf("Failed to execute cleanup command '%s': %v", cmd, err)
		}
	}

	return nil
}

// allocateVLAN allocates an available VLAN ID
func (nm *NetworkManager) allocateVLAN() int {
	vlanID := nm.config.GetAvailableVLAN(nm.vlanAllocations)
	if vlanID != -1 {
		nm.vlanAllocations[vlanID] = true
	}
	return vlanID
}

// deallocateVLAN releases a VLAN ID
func (nm *NetworkManager) deallocateVLAN(vlanID int) {
	delete(nm.vlanAllocations, vlanID)
}

// allocateVRF allocates a VRF for a namespace
func (nm *NetworkManager) allocateVRF(namespace string) string {
	// Use namespace-based VRF naming
	vrfName := fmt.Sprintf("vrf-%s", namespace)
	
	// Check if this namespace already has a VRF
	if existingVRF, exists := nm.vrfAllocations[namespace]; exists {
		return existingVRF
	}

	// Default namespace uses the default VRF
	if namespace == "default" {
		vrfName = nm.config.Networking.DefaultVRF
	}

	nm.vrfAllocations[namespace] = vrfName
	return vrfName
}

// GetNetworkNamespace returns a network namespace by ID
func (nm *NetworkManager) GetNetworkNamespace(networkID string) (*NetworkNamespace, bool) {
	nm.mutex.RLock()
	defer nm.mutex.RUnlock()

	ns, exists := nm.networkSpaces[networkID]
	return ns, exists
}

// ListNetworkNamespaces returns all network namespaces
func (nm *NetworkManager) ListNetworkNamespaces() []*NetworkNamespace {
	nm.mutex.RLock()
	defer nm.mutex.RUnlock()

	namespaces := make([]*NetworkNamespace, 0, len(nm.networkSpaces))
	for _, ns := range nm.networkSpaces {
		namespaces = append(namespaces, ns)
	}

	return namespaces
}

// IPAM Manager methods

// AddSubnet adds a subnet to the IPAM manager
func (ipam *IPAMManager) AddSubnet(name, cidr string) error {
	ipam.mutex.Lock()
	defer ipam.mutex.Unlock()

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR %s: %v", cidr, err)
	}

	// Calculate gateway (first usable IP)
	gateway := make(net.IP, len(network.IP))
	copy(gateway, network.IP)
	gateway = incrementIP(gateway)

	subnet := &IPSubnet{
		CIDR:      cidr,
		Network:   network,
		Allocated: make(map[string]bool),
		NextIP:    incrementIP(gateway), // Start allocation after gateway
		Gateway:   gateway,
	}

	// Mark network and broadcast addresses as allocated
	subnet.Allocated[network.IP.String()] = true
	broadcast := calculateBroadcast(network)
	subnet.Allocated[broadcast.String()] = true
	subnet.Allocated[gateway.String()] = true

	ipam.subnets[name] = subnet
	return nil
}

// AllocateSubnet allocates a new subnet for a pod
func (ipam *IPAMManager) AllocateSubnet(podID string, prefixLength int) (string, string, error) {
	ipam.mutex.Lock()
	defer ipam.mutex.Unlock()

	podSubnet, exists := ipam.subnets["pods"]
	if !exists {
		return "", "", fmt.Errorf("pods subnet not configured")
	}

	// For simplicity, allocate /30 subnets from the pod CIDR
	// In production, you'd want more sophisticated subnet allocation
	ip := ipam.allocateNextIP(podSubnet)
	if ip == nil {
		return "", "", fmt.Errorf("no available IPs in subnet")
	}

	// Create /30 subnet for the pod
	mask := net.CIDRMask(30, 32)
	subnet := &net.IPNet{IP: ip, Mask: mask}
	
	gateway := incrementIP(ip)
	
	return subnet.String(), gateway.String(), nil
}

// ReleaseSubnet releases a subnet allocation
func (ipam *IPAMManager) ReleaseSubnet(podID string) {
	ipam.mutex.Lock()
	defer ipam.mutex.Unlock()

	// Implementation would track and release specific pod subnets
	// For now, we'll just mark it as available in the main pool
}

// allocateNextIP allocates the next available IP from a subnet
func (ipam *IPAMManager) allocateNextIP(subnet *IPSubnet) net.IP {
	for ip := subnet.NextIP; subnet.Network.Contains(ip); ip = incrementIP(ip) {
		ipStr := ip.String()
		if !subnet.Allocated[ipStr] {
			subnet.Allocated[ipStr] = true
			subnet.NextIP = incrementIP(ip)
			return ip
		}
	}
	return nil
}

// Helper functions

// getNamespaceFromLabels extracts namespace from container labels
func getNamespaceFromLabels(labels map[string]string) string {
	if labels == nil {
		return "default"
	}
	
	if ns, exists := labels["io.kubernetes.pod.namespace"]; exists {
		return ns
	}
	
	return "default"
}

// incrementIP increments an IP address by 1
func incrementIP(ip net.IP) net.IP {
	result := make(net.IP, len(ip))
	copy(result, ip)
	
	for i := len(result) - 1; i >= 0; i-- {
		result[i]++
		if result[i] != 0 {
			break
		}
	}
	
	return result
}

// calculateBroadcast calculates the broadcast address for a network
func calculateBroadcast(network *net.IPNet) net.IP {
	ip := make(net.IP, len(network.IP))
	copy(ip, network.IP)
	
	for i := 0; i < len(ip); i++ {
		ip[i] |= ^network.Mask[i]
	}
	
	return ip
}

// CreateServiceEndpoint creates a service endpoint for load balancing
func (nm *NetworkManager) CreateServiceEndpoint(ctx context.Context, service *v1.Service, endpoints []*v1.Endpoints) error {
	// Implementation would configure load balancing on devices
	// This could involve:
	// 1. Creating load balancer configuration
	// 2. Setting up health checks
	// 3. Configuring traffic distribution
	
	log.G(ctx).Infof("Creating service endpoint for service %s/%s", service.Namespace, service.Name)
	return nil
}

// DeleteServiceEndpoint removes a service endpoint
func (nm *NetworkManager) DeleteServiceEndpoint(ctx context.Context, serviceName, namespace string) error {
	log.G(ctx).Infof("Deleting service endpoint for service %s/%s", namespace, serviceName)
	return nil
}

// ApplyNetworkPolicy applies network security policies
func (nm *NetworkManager) ApplyNetworkPolicy(ctx context.Context, device *ManagedDevice, policy *SecurityPolicy) error {
	client, ok := device.Client.(*IOSXEClient)
	if !ok {
		return fmt.Errorf("unsupported client type for device %s", device.Config.Name)
	}

	// Convert network policy to ACL configuration
	aclName := fmt.Sprintf("acl-%s-%s", policy.Namespace, policy.Name)
	
	// Create ACL commands
	commands := []string{fmt.Sprintf("ip access-list extended %s", aclName)}
	
	for i, rule := range policy.Rules {
		aclRule := nm.convertPolicyRuleToACL(rule, i+10)
		commands = append(commands, aclRule)
	}
	
	// Apply ACL to interface (implementation specific)
	// commands = append(commands, fmt.Sprintf("interface vlan %d", vlanID))
	// commands = append(commands, fmt.Sprintf("ip access-group %s in", aclName))

	for _, cmd := range commands {
		_, err := client.ExecuteCommand(cmd)
		if err != nil {
			log.G(ctx).Errorf("Failed to execute ACL command '%s': %v", cmd, err)
		}
	}

	return nil
}

// convertPolicyRuleToACL converts a security policy rule to Cisco ACL syntax
func (nm *NetworkManager) convertPolicyRuleToACL(rule SecurityPolicyRule, sequence int) string {
	action := rule.Action
	if action == "allow" {
		action = "permit"
	} else if action == "deny" {
		action = "deny"
	}

	protocol := rule.Protocol
	if protocol == "" {
		protocol = "ip"
	}

	source := rule.SourceCIDR
	if source == "" {
		source = "any"
	}

	dest := rule.DestCIDR
	if dest == "" {
		dest = "any"
	}

	aclRule := fmt.Sprintf("%d %s %s %s %s", sequence, action, protocol, source, dest)

	// Add port information if specified
	if len(rule.Ports) > 0 {
		ports := make([]string, len(rule.Ports))
		for i, port := range rule.Ports {
			ports[i] = strconv.Itoa(int(port))
		}
		aclRule += fmt.Sprintf(" eq %s", strings.Join(ports, " "))
	}

	return aclRule
}

// GetNetworkingMetrics returns networking metrics
func (nm *NetworkManager) GetNetworkingMetrics() map[string]interface{} {
	nm.mutex.RLock()
	defer nm.mutex.RUnlock()

	usedVLANs := 0
	for _, allocated := range nm.vlanAllocations {
		if allocated {
			usedVLANs++
		}
	}

	return map[string]interface{}{
		"network_namespaces": len(nm.networkSpaces),
		"allocated_vlans":    usedVLANs,
		"total_vrfs":         len(nm.vrfAllocations),
		"vlan_range": map[string]int{
			"start": nm.config.Networking.VLANRange.Start,
			"end":   nm.config.Networking.VLANRange.End,
		},
	}
}
