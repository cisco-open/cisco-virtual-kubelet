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
	"net"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNetworkConfig_DHCPEnabled(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				DHCPEnabled: true,
				PodCIDR:     "10.0.0.0/24", // Should be ignored when DHCP enabled
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "container-1"},
			},
		},
	}

	netConfig := driver.getNetworkConfig(pod, &pod.Spec.Containers[0])

	if !netConfig.useDHCP {
		t.Error("Expected useDHCP to be true when DHCPEnabled is set")
	}

	if netConfig.virtualPortgroupInterface != "0" {
		t.Errorf("Expected virtualPortgroupInterface to be '0', got '%s'", netConfig.virtualPortgroupInterface)
	}

	// When DHCP is enabled, IP fields should be empty
	if netConfig.virtualPortgroupIP != "" {
		t.Errorf("Expected virtualPortgroupIP to be empty in DHCP mode, got '%s'", netConfig.virtualPortgroupIP)
	}

	if netConfig.virtualPortgroupNetmask != "" {
		t.Errorf("Expected virtualPortgroupNetmask to be empty in DHCP mode, got '%s'", netConfig.virtualPortgroupNetmask)
	}

	if netConfig.defaultGateway != "" {
		t.Errorf("Expected defaultGateway to be empty in DHCP mode, got '%s'", netConfig.defaultGateway)
	}
}

func TestGetNetworkConfig_StaticIP_WithPodCIDR(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				DHCPEnabled: false,
				PodCIDR:     "10.0.0.0/24",
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "container-0"},
				{Name: "container-1"},
			},
		},
	}

	// Test first container
	// Note: Gateway calculation mutates ipNet.IP (+1), then container IP adds (10+index)
	// So first container gets: base(0) + gateway(1) + 10 + index(0) = 11
	netConfig0 := driver.getNetworkConfig(pod, &pod.Spec.Containers[0])

	if netConfig0.useDHCP {
		t.Error("Expected useDHCP to be false when DHCPEnabled is not set")
	}

	if netConfig0.virtualPortgroupIP != "10.0.0.11" {
		t.Errorf("Expected first container IP to be '10.0.0.11', got '%s'", netConfig0.virtualPortgroupIP)
	}

	if netConfig0.virtualPortgroupNetmask != "255.255.255.0" {
		t.Errorf("Expected netmask to be '255.255.255.0', got '%s'", netConfig0.virtualPortgroupNetmask)
	}

	if netConfig0.defaultGateway != "10.0.0.1" {
		t.Errorf("Expected gateway to be '10.0.0.1', got '%s'", netConfig0.defaultGateway)
	}

	// Test second container
	// Same formula: base(0) + gateway(1) + 10 + index(1) = 12
	netConfig1 := driver.getNetworkConfig(pod, &pod.Spec.Containers[1])

	if netConfig1.virtualPortgroupIP != "10.0.0.12" {
		t.Errorf("Expected second container IP to be '10.0.0.12', got '%s'", netConfig1.virtualPortgroupIP)
	}
}

func TestGetNetworkConfig_StaticIP_Fallback(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				DHCPEnabled: false,
				PodCIDR:     "", // No CIDR configured, should use fallback
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "container-1"},
			},
		},
	}

	netConfig := driver.getNetworkConfig(pod, &pod.Spec.Containers[0])

	if netConfig.useDHCP {
		t.Error("Expected useDHCP to be false")
	}

	// Should use fallback values
	if netConfig.virtualPortgroupIP != "1.1.1.10" {
		t.Errorf("Expected fallback IP '1.1.1.10', got '%s'", netConfig.virtualPortgroupIP)
	}

	if netConfig.virtualPortgroupNetmask != "255.255.255.0" {
		t.Errorf("Expected fallback netmask '255.255.255.0', got '%s'", netConfig.virtualPortgroupNetmask)
	}

	if netConfig.defaultGateway != "1.1.1.1" {
		t.Errorf("Expected fallback gateway '1.1.1.1', got '%s'", netConfig.defaultGateway)
	}
}

func TestGetNetworkConfig_StaticIP_InvalidCIDR(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				DHCPEnabled: false,
				PodCIDR:     "invalid-cidr",
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "container-1"},
			},
		},
	}

	netConfig := driver.getNetworkConfig(pod, &pod.Spec.Containers[0])

	// Should fall back to defaults when CIDR is invalid
	if netConfig.virtualPortgroupIP != "1.1.1.10" {
		t.Errorf("Expected fallback IP '1.1.1.10' for invalid CIDR, got '%s'", netConfig.virtualPortgroupIP)
	}

	if netConfig.defaultGateway != "1.1.1.1" {
		t.Errorf("Expected fallback gateway '1.1.1.1' for invalid CIDR, got '%s'", netConfig.defaultGateway)
	}
}

func TestGetContainerIndex(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{},
	}

	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "first"},
				{Name: "second"},
				{Name: "third"},
			},
		},
	}

	tests := []struct {
		containerName string
		expectedIndex int
	}{
		{"first", 0},
		{"second", 1},
		{"third", 2},
		{"nonexistent", 0}, // Returns 0 for non-existent container
	}

	for _, tt := range tests {
		t.Run(tt.containerName, func(t *testing.T) {
			container := &v1.Container{Name: tt.containerName}
			index := driver.getContainerIndex(pod, container)
			if index != tt.expectedIndex {
				t.Errorf("Expected index %d for container '%s', got %d", tt.expectedIndex, tt.containerName, index)
			}
		})
	}
}

func TestGetGatewayFromCIDR(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{},
	}

	tests := []struct {
		cidr            string
		expectedGateway string
	}{
		{"10.0.0.0/24", "10.0.0.1"},
		{"192.168.1.0/24", "192.168.1.1"},
		{"172.16.0.0/16", "172.16.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			_, ipNet, _ := parseTestCIDR(tt.cidr)
			gateway := driver.getGatewayFromCIDR(ipNet)
			if gateway != tt.expectedGateway {
				t.Errorf("Expected gateway '%s' for CIDR '%s', got '%s'", tt.expectedGateway, tt.cidr, gateway)
			}
		})
	}
}

func TestGetIPForContainer(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{},
	}

	// Note: getIPForContainer mutates the ipNet.IP, so each test needs a fresh CIDR
	tests := []struct {
		containerIndex int
		expectedIP     string
	}{
		{0, "10.0.0.10"},
		{1, "10.0.0.11"},
		{2, "10.0.0.12"},
		{5, "10.0.0.15"},
	}

	for _, tt := range tests {
		t.Run(tt.expectedIP, func(t *testing.T) {
			// Create fresh ipNet for each test since getIPForContainer mutates it
			_, ipNet, _ := parseTestCIDR("10.0.0.0/24")
			ip := driver.getIPForContainer(ipNet, tt.containerIndex)
			if ip != tt.expectedIP {
				t.Errorf("Expected IP '%s' for container index %d, got '%s'", tt.expectedIP, tt.containerIndex, ip)
			}
		})
	}
}

// Helper function for tests
func parseTestCIDR(cidr string) (string, *net.IPNet, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	return ip.String(), ipNet, err
}

func TestIsValidPodIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{
			name:     "valid IPv4",
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name:     "valid IPv4 from DHCP",
			ip:       "1.1.1.14",
			expected: true,
		},
		{
			name:     "unspecified 0.0.0.0",
			ip:       "0.0.0.0",
			expected: false,
		},
		{
			name:     "empty string",
			ip:       "",
			expected: false,
		},
		{
			name:     "invalid IP",
			ip:       "not-an-ip",
			expected: false,
		},
		{
			name:     "loopback",
			ip:       "127.0.0.1",
			expected: true, // loopback is technically valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidPodIP(tt.ip)
			if result != tt.expected {
				t.Errorf("isValidPodIP(%q) = %v, expected %v", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestNormalizeMacAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "colon separated lowercase",
			input:    "00:11:22:33:44:55",
			expected: "00:11:22:33:44:55",
		},
		{
			name:     "colon separated uppercase",
			input:    "00:11:22:AA:BB:CC",
			expected: "00:11:22:aa:bb:cc",
		},
		{
			name:     "dash separated",
			input:    "00-11-22-33-44-55",
			expected: "00:11:22:33:44:55",
		},
		{
			name:     "Cisco dot notation",
			input:    "0011.2233.4455",
			expected: "00:11:22:33:44:55",
		},
		{
			name:     "no separator",
			input:    "001122334455",
			expected: "00:11:22:33:44:55",
		},
		{
			name:     "mixed case Cisco notation",
			input:    "00AA.BBCC.DDEE",
			expected: "00:aa:bb:cc:dd:ee",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMacAddress(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeMacAddress(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDiscoverPodIP_FromAppHostingOperData(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Address: "192.168.1.1",
		},
	}

	// Create mock operational data with IPv4 address present
	ipv4Addr := "10.0.0.100"
	macAddr := "00:11:22:33:44:55"

	appOperData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"test-app": {
			NetworkInterfaces: &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_NetworkInterfaces{
				NetworkInterface: map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_NetworkInterfaces_NetworkInterface{
					macAddr: {
						Ipv4Address: &ipv4Addr,
						MacAddress:  &macAddr,
					},
				},
			},
		},
	}

	discoveredContainers := map[string]string{
		"container-1": "test-app",
	}

	// Test that IP is discovered from app-hosting oper data
	ctx := context.Background()
	podIP := driver.discoverPodIP(ctx, discoveredContainers, appOperData)

	if podIP != ipv4Addr {
		t.Errorf("Expected Pod IP %q from app-hosting oper data, got %q", ipv4Addr, podIP)
	}
}

func TestDiscoverPodIP_NoIPInOperData(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Address: "192.168.1.1",
		},
	}

	// Create mock operational data WITHOUT IPv4 address (simulating unreliable app-hosting IP)
	macAddr := "00:11:22:33:44:55"
	emptyIP := ""

	appOperData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"test-app": {
			NetworkInterfaces: &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_NetworkInterfaces{
				NetworkInterface: map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_NetworkInterfaces_NetworkInterface{
					macAddr: {
						Ipv4Address: &emptyIP, // Empty IP - should trigger ARP fallback
						MacAddress:  &macAddr,
					},
				},
			},
		},
	}

	discoveredContainers := map[string]string{
		"container-1": "test-app",
	}

	// Test that MAC addresses are collected for ARP fallback
	// Note: Without mocking the network client, ARP lookup will fail and return default IP
	ctx := context.Background()
	podIP := driver.discoverPodIP(ctx, discoveredContainers, appOperData)

	// Should return default IP since ARP lookup will fail without mocked client
	if podIP != "0.0.0.0" {
		t.Errorf("Expected default IP '0.0.0.0' when ARP lookup fails, got %q", podIP)
	}
}

func TestDiscoverPodIP_NoNetworkInterfaces(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Address: "192.168.1.1",
		},
	}

	// Create mock operational data with no network interfaces
	appOperData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"test-app": {
			NetworkInterfaces: nil, // No network interfaces
		},
	}

	discoveredContainers := map[string]string{
		"container-1": "test-app",
	}

	ctx := context.Background()
	podIP := driver.discoverPodIP(ctx, discoveredContainers, appOperData)

	// Should return default IP when no network interfaces
	if podIP != "0.0.0.0" {
		t.Errorf("Expected default IP '0.0.0.0' when no network interfaces, got %q", podIP)
	}
}

func TestDiscoverPodIP_EmptyContainers(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Address: "192.168.1.1",
		},
	}

	appOperData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{}
	discoveredContainers := map[string]string{}

	ctx := context.Background()
	podIP := driver.discoverPodIP(ctx, discoveredContainers, appOperData)

	// Should return default IP when no containers
	if podIP != "0.0.0.0" {
		t.Errorf("Expected default IP '0.0.0.0' when no containers, got %q", podIP)
	}
}
