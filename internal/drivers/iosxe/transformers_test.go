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
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNetworkConfig_VirtualPortGroup_DHCPEnabled(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "10.0.0.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeVirtualPortGroup,
					VirtualPortGroup: &config.VirtualPortGroupConfig{
						Dhcp:      true,
						Interface: "0",
					},
				},
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
		t.Error("Expected useDHCP to be true when dhcp is enabled")
	}

	if netConfig.virtualPortgroupInterface != "0" {
		t.Errorf("Expected virtualPortgroupInterface to be '0', got '%s'", netConfig.virtualPortgroupInterface)
	}

	// When DHCP is implied, IP fields should be empty
	if netConfig.virtualPortgroupIP != "" {
		t.Errorf("Expected virtualPortgroupIP to be empty in DHCP mode, got '%s'", netConfig.virtualPortgroupIP)
	}

	if netConfig.virtualPortgroupNetmask != "" {
		t.Errorf("Expected virtualPortgroupNetmask to be empty in DHCP mode, got '%s'", netConfig.virtualPortgroupNetmask)
	}

}

func TestGetNetworkConfig_VirtualPortGroup_StaticIP(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "10.0.0.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeVirtualPortGroup,
					VirtualPortGroup: &config.VirtualPortGroupConfig{
						Dhcp:      false,
						Interface: "0",
					},
				},
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

	netConfig0 := driver.getNetworkConfig(pod, &pod.Spec.Containers[0])

	if netConfig0.useDHCP {
		t.Error("Expected useDHCP to be false when dhcp is disabled")
	}

	if netConfig0.virtualPortgroupIP != "10.0.0.10" {
		t.Errorf("Expected first container IP to be '10.0.0.10', got '%s'", netConfig0.virtualPortgroupIP)
	}

	if netConfig0.virtualPortgroupNetmask != "255.255.255.0" {
		t.Errorf("Expected netmask to be '255.255.255.0', got '%s'", netConfig0.virtualPortgroupNetmask)
	}

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

func TestConvertPodToAppConfigs_SingleContainer_DHCP(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "10.0.0.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeVirtualPortGroup,
					VirtualPortGroup: &config.VirtualPortGroupConfig{
						Dhcp:      true,
						Interface: "0",
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "12345678-1234-1234-1234-123456789abc",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "nginx:latest",
				},
			},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	config := configs[0]

	// Verify basic fields
	if config.ContainerName != "nginx" {
		t.Errorf("Expected container name 'nginx', got %s", config.ContainerName)
	}

	if config.ImagePath != "nginx:latest" {
		t.Errorf("Expected image path 'nginx:latest', got %s", config.ImagePath)
	}

	// Verify app name format (should contain cleaned UID)
	expectedUIDPart := "123456781234123412"
	if !strings.Contains(config.AppName, expectedUIDPart) {
		t.Errorf("Expected app name to contain '%s', got %s", expectedUIDPart, config.AppName)
	}

	// Verify Apps struct is not nil
	if config.Apps == nil {
		t.Fatal("Expected Apps to be non-nil")
	}

	if config.Apps.App == nil || len(config.Apps.App) != 1 {
		t.Fatal("Expected exactly one App entry")
	}

	// Get the app entry
	var app *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App
	for _, a := range config.Apps.App {
		app = a
		break
	}

	// Verify network configuration for DHCP
	if app.ApplicationNetworkResource == nil {
		t.Fatal("Expected ApplicationNetworkResource to be non-nil")
	}

	netRes := app.ApplicationNetworkResource
	if netRes.VirtualportgroupGuestInterfaceName_1 == nil || *netRes.VirtualportgroupGuestInterfaceName_1 != "0" {
		t.Error("Expected VirtualportgroupGuestInterfaceName_1 to be '0'")
	}

	// In DHCP mode, IP fields should be nil
	if netRes.VirtualportgroupGuestIpAddress_1 != nil {
		t.Error("Expected VirtualportgroupGuestIpAddress_1 to be nil in DHCP mode")
	}

	// Verify Run Options contain labels
	if app.RunOptss == nil || len(app.RunOptss.RunOpts) != 1 {
		t.Fatal("Expected RunOptss with one entry")
	}

	runOpts := app.RunOptss.RunOpts[1]
	if runOpts.LineRunOpts == nil {
		t.Fatal("Expected LineRunOpts to be non-nil")
	}

	optsStr := *runOpts.LineRunOpts
	if !strings.Contains(optsStr, "pod.name=test-pod") {
		t.Error("Expected run opts to contain pod name label")
	}
	if !strings.Contains(optsStr, "pod.namespace=default") {
		t.Error("Expected run opts to contain namespace label")
	}
	if !strings.Contains(optsStr, "container.name=nginx") {
		t.Error("Expected run opts to contain container name label")
	}

	// Verify resource profile
	if app.ApplicationResourceProfile == nil {
		t.Fatal("Expected ApplicationResourceProfile to be non-nil")
	}

	resProf := app.ApplicationResourceProfile
	if resProf.ProfileName == nil || *resProf.ProfileName != "custom" {
		t.Error("Expected ProfileName to be 'custom'")
	}

	// Verify Start flag
	if app.Start == nil || !*app.Start {
		t.Error("Expected Start to be true")
	}
}

func TestConvertPodToAppConfigs_MultipleContainers_StaticIP(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "10.0.0.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeVirtualPortGroup,
					VirtualPortGroup: &config.VirtualPortGroupConfig{
						Dhcp:      false,
						Interface: "1",
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-pod",
			Namespace: "default",
			UID:       "abcdef12-3456-7890-abcd-ef1234567890",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "web",
					Image: "nginx:1.19",
				},
				{
					Name:  "sidecar",
					Image: "busybox:latest",
				},
			},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("Expected 2 configs, got %d", len(configs))
	}

	// Verify first container config
	config0 := configs[0]
	if config0.ContainerName != "web" {
		t.Errorf("Expected first container name 'web', got %s", config0.ContainerName)
	}
	if config0.ImagePath != "nginx:1.19" {
		t.Errorf("Expected first image 'nginx:1.19', got %s", config0.ImagePath)
	}

	// Verify second container config
	config1 := configs[1]
	if config1.ContainerName != "sidecar" {
		t.Errorf("Expected second container name 'sidecar', got %s", config1.ContainerName)
	}
	if config1.ImagePath != "busybox:latest" {
		t.Errorf("Expected second image 'busybox:latest', got %s", config1.ImagePath)
	}

	// Verify app names are unique
	if config0.AppName == config1.AppName {
		t.Error("Expected unique app names for different containers")
	}

	// Verify static IP configuration for first container
	var app0 *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App
	for _, a := range config0.Apps.App {
		app0 = a
		break
	}

	if app0.ApplicationNetworkResource == nil {
		t.Fatal("Expected ApplicationNetworkResource to be non-nil")
	}

	netRes := app0.ApplicationNetworkResource
	if netRes.VirtualportgroupGuestIpAddress_1 == nil {
		t.Error("Expected VirtualportgroupGuestIpAddress_1 to be set in static IP mode")
	}

	if netRes.VirtualportgroupGuestIpNetmask_1 == nil {
		t.Error("Expected VirtualportgroupGuestIpNetmask_1 to be set in static IP mode")
	}

	if netRes.VirtualportgroupApplicationDefaultGateway_1 != nil {
		t.Error("Expected VirtualportgroupApplicationDefaultGateway_1 to be nil in static IP mode")
	}

	if netRes.VirtualportgroupGuestInterfaceName_1 == nil || *netRes.VirtualportgroupGuestInterfaceName_1 != "1" {
		t.Error("Expected VirtualportgroupGuestInterfaceName_1 to be '1'")
	}

	// Verify second container does not get a static IP
	var app1 *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App
	for _, a := range config1.Apps.App {
		app1 = a
		break
	}
	if app1 == nil || app1.ApplicationNetworkResource == nil {
		t.Fatal("Expected ApplicationNetworkResource to be non-nil for second container")
	}
	if app1.ApplicationNetworkResource.VirtualportgroupGuestIpAddress_1 != nil {
		t.Error("Expected VirtualportgroupGuestIpAddress_1 to be nil for non-primary container")
	}
}

func TestConvertPodToAppConfigs_EmptyPod(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "10.0.0.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeVirtualPortGroup,
					VirtualPortGroup: &config.VirtualPortGroupConfig{
						Dhcp:      true,
						Interface: "0",
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "empty-pod",
			Namespace: "default",
			UID:       "00000000-0000-0000-0000-000000000000",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(configs) != 0 {
		t.Fatalf("Expected 0 configs for empty pod, got %d", len(configs))
	}
}

func TestConvertPodToAppConfigs_Management_StaticIP(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "198.51.100.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeManagement,
					Management: &config.ManagementConfig{
						Dhcp:           false,
						GuestInterface: 0,
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mgmt-pod",
			Namespace: "default",
			UID:       "11111111-1111-1111-1111-111111111111",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "mgmt-app",
					Image: "busybox:latest",
				},
			},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	var app *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App
	for _, a := range configs[0].Apps.App {
		app = a
		break
	}
	if app == nil || app.ApplicationNetworkResource == nil {
		t.Fatal("Expected ApplicationNetworkResource to be non-nil")
	}

	if app.ApplicationNetworkResource.ManagementGuestIpAddress == nil {
		t.Fatal("Expected ManagementGuestIpAddress to be set")
	}
	if *app.ApplicationNetworkResource.ManagementGuestIpAddress != "198.51.100.10" {
		t.Fatalf("Expected ManagementGuestIpAddress to be '198.51.100.10', got %q", *app.ApplicationNetworkResource.ManagementGuestIpAddress)
	}
	if app.ApplicationNetworkResource.ManagementGuestIpNetmask == nil {
		t.Fatal("Expected ManagementGuestIpNetmask to be set")
	}
}

func TestConvertPodToAppConfigs_Golden_ManagementStaticIP(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "198.51.100.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeManagement,
					Management: &config.ManagementConfig{
						Dhcp:           false,
						GuestInterface: 0,
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mgmt-pod",
			Namespace: "default",
			UID:       "11111111-1111-1111-1111-111111111111",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "mgmt-app",
					Image: "busybox:latest",
				},
			},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	marshaller := driver.getRestconfMarshaller()
	payload, err := marshaller(configs[0].Apps)
	if err != nil {
		t.Fatalf("Failed to marshal payload: %v", err)
	}

	actual := mustUnmarshalJSON(t, payload)
	expected := loadGoldenJSON(t, "apphosting_management_static.json")

	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Golden JSON mismatch for management static IP payload")
	}
}

func TestConvertPodToAppConfigs_Golden_AppGigAccess(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeAppGigabitEthernet,
					AppGigabitEthernet: &config.AppGigabitEthernetConfig{
						Mode: config.AppGigabitEthernetModeAccess,
						Dhcp: true,
						VlanIf: config.VlanInterfaceConfig{
							Vlan:           0,
							GuestInterface: 0,
						},
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "appgig-access-pod",
			Namespace: "default",
			UID:       "22222222-2222-2222-2222-222222222222",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "appgig-access-app",
					Image: "busybox:latest",
				},
			},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	marshaller := driver.getRestconfMarshaller()
	payload, err := marshaller(configs[0].Apps)
	if err != nil {
		t.Fatalf("Failed to marshal payload: %v", err)
	}

	actual := mustUnmarshalJSON(t, payload)
	expected := loadGoldenJSON(t, "apphosting_appgig_access.json")

	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Golden JSON mismatch for AppGigabitEthernet access payload")
	}
}

func TestConvertPodToAppConfigs_Golden_AppGigTrunk(t *testing.T) {
	driver := &XEDriver{
		config: &config.DeviceConfig{
			Networking: config.NetworkConfig{
				PodCIDR: "10.0.0.0/24",
				Interface: &config.InterfaceConfig{
					Type: config.InterfaceTypeAppGigabitEthernet,
					AppGigabitEthernet: &config.AppGigabitEthernetConfig{
						Mode: config.AppGigabitEthernetModeTrunk,
						VlanIf: config.VlanInterfaceConfig{
							Dhcp:                 false,
							Vlan:                 100,
							GuestInterface:       0,
							MacForwardingEnabled: true,
							MulticastEnabled:     true,
							MirrorEnabled:        true,
						},
					},
				},
			},
		},
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "appgig-trunk-pod",
			Namespace: "default",
			UID:       "33333333-3333-3333-3333-333333333333",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "appgig-trunk-app",
					Image: "busybox:latest",
				},
			},
		},
	}

	configs, err := driver.ConvertPodToAppConfigs(pod)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	marshaller := driver.getRestconfMarshaller()
	payload, err := marshaller(configs[0].Apps)
	if err != nil {
		t.Fatalf("Failed to marshal payload: %v", err)
	}

	actual := mustUnmarshalJSON(t, payload)
	expected := loadGoldenJSON(t, "apphosting_appgig_trunk.json")

	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Golden JSON mismatch for AppGigabitEthernet trunk payload")
	}
}

func loadGoldenJSON(t *testing.T, name string) any {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read golden file %s: %v", path, err)
	}
	return mustUnmarshalJSON(t, data)
}

func mustUnmarshalJSON(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}
	return v
}
