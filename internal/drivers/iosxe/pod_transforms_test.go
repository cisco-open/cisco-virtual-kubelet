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
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNetworkConfig_VirtualPortGroup_DHCPEnabled(t *testing.T) {
	driver := &XEDriver{
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "10.0.0.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeVirtualPortGroup,
						VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{
							Dhcp:      true,
							Interface: "0",
						},
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "10.0.0.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeVirtualPortGroup,
						VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{
							Dhcp:      false,
							Interface: "0",
						},
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
		config: &v1alpha1.DeviceSpec{
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
		config: &v1alpha1.DeviceSpec{
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
		config: &v1alpha1.DeviceSpec{
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
		config: &v1alpha1.DeviceSpec{
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "10.0.0.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeVirtualPortGroup,
						VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{
							Dhcp:      true,
							Interface: "0",
						},
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
	if config.ContainerName() != "nginx" {
		t.Errorf("Expected container name 'nginx', got %s", config.ContainerName())
	}

	if config.ImagePath() != "nginx:latest" {
		t.Errorf("Expected image path 'nginx:latest', got %s", config.ImagePath())
	}

	// Verify app name format (should contain cleaned UID)
	expectedUIDPart := "123456781234123412"
	if !strings.Contains(config.AppName(), expectedUIDPart) {
		t.Errorf("Expected app name to contain '%s', got %s", expectedUIDPart, config.AppName())
	}

	// Verify DeviceConfig struct is not nil
	if config.Spec.DeviceConfig == nil {
		t.Fatal("Expected Spec.DeviceConfig to be non-nil")
	}

	if config.Spec.DeviceConfig.App == nil || len(config.Spec.DeviceConfig.App) != 1 {
		t.Fatal("Expected exactly one App entry")
	}

	// Get the app entry
	var app *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App
	for _, a := range config.Spec.DeviceConfig.App {
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "10.0.0.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeVirtualPortGroup,
						VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{
							Dhcp:      false,
							Interface: "1",
						},
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
	if config0.ContainerName() != "web" {
		t.Errorf("Expected first container name 'web', got %s", config0.ContainerName())
	}
	if config0.ImagePath() != "nginx:1.19" {
		t.Errorf("Expected first image 'nginx:1.19', got %s", config0.ImagePath())
	}

	// Verify second container config
	config1 := configs[1]
	if config1.ContainerName() != "sidecar" {
		t.Errorf("Expected second container name 'sidecar', got %s", config1.ContainerName())
	}
	if config1.ImagePath() != "busybox:latest" {
		t.Errorf("Expected second image 'busybox:latest', got %s", config1.ImagePath())
	}

	// Verify app names are unique
	if config0.AppName() == config1.AppName() {
		t.Error("Expected unique app names for different containers")
	}

	// Verify static IP configuration for first container
	var app0 *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App
	for _, a := range config0.Spec.DeviceConfig.App {
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
	for _, a := range config1.Spec.DeviceConfig.App {
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "10.0.0.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeVirtualPortGroup,
						VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{
							Dhcp:      true,
							Interface: "0",
						},
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "198.51.100.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeManagement,
						Management: &v1alpha1.XEManagementConfig{
							Dhcp:           false,
							GuestInterface: 0,
						},
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
	for _, a := range configs[0].Spec.DeviceConfig.App {
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "198.51.100.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeManagement,
						Management: &v1alpha1.XEManagementConfig{
							Dhcp:           false,
							GuestInterface: 0,
						},
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
	payload, err := marshaller(configs[0].Spec.DeviceConfig)
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
		config: &v1alpha1.DeviceSpec{
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeAppGigabitEthernet,
						AppGigabitEthernet: &v1alpha1.XEAppGigabitEthernetConfig{
							Mode: v1alpha1.XEAppGigabitEthernetModeAccess,
							Dhcp: true,
							VlanIf: v1alpha1.XEVlanInterfaceConfig{
								Vlan:           0,
								GuestInterface: 0,
							},
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
	payload, err := marshaller(configs[0].Spec.DeviceConfig)
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
		config: &v1alpha1.DeviceSpec{
			PodCIDR: "10.0.0.0/24",
			XE: &v1alpha1.XEConfig{
				Networking: v1alpha1.XENetworkConfig{
					Interface: &v1alpha1.XEInterfaceConfig{
						Type: v1alpha1.XEInterfaceTypeAppGigabitEthernet,
						AppGigabitEthernet: &v1alpha1.XEAppGigabitEthernetConfig{
							Mode: v1alpha1.XEAppGigabitEthernetModeTrunk,
							VlanIf: v1alpha1.XEVlanInterfaceConfig{
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
	payload, err := marshaller(configs[0].Spec.DeviceConfig)
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

// ─────────────────────────────────────────────────────────────────────────────
// getContainerIndex
// ─────────────────────────────────────────────────────────────────────────────

func TestGetContainerIndex(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
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
		expected      int
	}{
		{"first", 0},
		{"second", 1},
		{"third", 2},
		{"missing", 0}, // fallback to 0
	}

	for _, tt := range tests {
		t.Run(tt.containerName, func(t *testing.T) {
			c := &v1.Container{Name: tt.containerName}
			got := d.getContainerIndex(pod, c)
			if got != tt.expected {
				t.Errorf("getContainerIndex(%q) = %d, want %d", tt.containerName, got, tt.expected)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// getIPForContainer
// ─────────────────────────────────────────────────────────────────────────────

func TestGetIPForContainer(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}

	tests := []struct {
		name           string
		cidr           string
		containerIndex int
		expected       string
	}{
		{
			name:           "first container in /24",
			cidr:           "10.0.0.0/24",
			containerIndex: 0,
			expected:       "10.0.0.10",
		},
		{
			name:           "second container in /24",
			cidr:           "10.0.0.0/24",
			containerIndex: 1,
			expected:       "10.0.0.11",
		},
		{
			name:           "first container in different subnet",
			cidr:           "192.168.1.0/24",
			containerIndex: 0,
			expected:       "192.168.1.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ipNet, _ := net.ParseCIDR(tt.cidr)
			got := d.getIPForContainer(ipNet, tt.containerIndex)
			if got != tt.expected {
				t.Errorf("getIPForContainer(%s, %d) = %q, want %q", tt.cidr, tt.containerIndex, got, tt.expected)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// getResourceConfig
// ─────────────────────────────────────────────────────────────────────────────

func TestGetResourceConfig_Defaults(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	container := &v1.Container{Name: "app"}
	cfg := d.getResourceConfig(container)

	if cfg.cpuUnits != 1000 {
		t.Errorf("cpuUnits = %d, want 1000", cfg.cpuUnits)
	}
	if cfg.memoryMB != 512 {
		t.Errorf("memoryMB = %d, want 512", cfg.memoryMB)
	}
	if cfg.diskMB != 1024 {
		t.Errorf("diskMB = %d, want 1024", cfg.diskMB)
	}
	if cfg.vcpu != 1 {
		t.Errorf("vcpu = %d, want 1", cfg.vcpu)
	}
}

func TestGetResourceConfig_FromRequests(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	container := &v1.Container{
		Name: "app",
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{
				v1.ResourceCPU:              resource.MustParse("500m"),
				v1.ResourceMemory:           resource.MustParse("256Mi"),
				v1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
			},
		},
	}
	cfg := d.getResourceConfig(container)

	if cfg.cpuUnits != 500 {
		t.Errorf("cpuUnits = %d, want 500", cfg.cpuUnits)
	}
	if cfg.memoryMB != 256 {
		t.Errorf("memoryMB = %d, want 256", cfg.memoryMB)
	}
	if cfg.diskMB != 2048 {
		t.Errorf("diskMB = %d, want 2048", cfg.diskMB)
	}
}

func TestGetResourceConfig_VcpuFromLimits(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	container := &v1.Container{
		Name: "app",
		Resources: v1.ResourceRequirements{
			Limits: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("3500m"),
			},
		},
	}
	cfg := d.getResourceConfig(container)

	// 3500m → ceil(3500/1000) = 4 vCPUs
	if cfg.vcpu != 4 {
		t.Errorf("vcpu = %d, want 4 (ceil of 3.5)", cfg.vcpu)
	}
}

func TestGetResourceConfig_VcpuRoundsUp(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	container := &v1.Container{
		Name: "app",
		Resources: v1.ResourceRequirements{
			Limits: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("100m"),
			},
		},
	}
	cfg := d.getResourceConfig(container)

	// 100m → ceil(100/1000) = 1 vCPU
	if cfg.vcpu != 1 {
		t.Errorf("vcpu = %d, want 1 (minimum)", cfg.vcpu)
	}
}

func TestGetResourceConfig_DefaultOverrides(t *testing.T) {
	d := &XEDriver{
		config: &v1alpha1.DeviceSpec{
			ResourceLimits: v1alpha1.ResourceConfig{
				DefaultCPU:     "2000m",
				DefaultMemory:  "1Gi",
				DefaultStorage: "4Gi",
			},
		},
	}
	container := &v1.Container{Name: "app"}
	cfg := d.getResourceConfig(container)

	if cfg.cpuUnits != 2000 {
		t.Errorf("cpuUnits = %d, want 2000 (from DefaultCPU override)", cfg.cpuUnits)
	}
	if cfg.memoryMB != 1024 {
		t.Errorf("memoryMB = %d, want 1024 (from DefaultMemory override)", cfg.memoryMB)
	}
	if cfg.diskMB != 4096 {
		t.Errorf("diskMB = %d, want 4096 (from DefaultStorage override)", cfg.diskMB)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// getNetworkConfig — default (no XE config)
// ─────────────────────────────────────────────────────────────────────────────

func TestGetNetworkConfig_NoXEConfig(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "app"}},
		},
	}

	netCfg := d.getNetworkConfig(pod, &pod.Spec.Containers[0])

	if netCfg.interfaceType != v1alpha1.XEInterfaceTypeVirtualPortGroup {
		t.Errorf("interfaceType = %v, want VirtualPortGroup", netCfg.interfaceType)
	}
	if !netCfg.useDHCP {
		t.Error("expected useDHCP=true when no XE config")
	}
	if netCfg.virtualPortgroupInterface != "0" {
		t.Errorf("virtualPortgroupInterface = %q, want 0", netCfg.virtualPortgroupInterface)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// allocateIPForContainer
// ─────────────────────────────────────────────────────────────────────────────

func TestAllocateIPForContainer_EmptyPodCIDR(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{PodCIDR: ""}}
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "app"}},
		},
	}
	ip, mask, err := d.allocateIPForContainer(pod, &pod.Spec.Containers[0])
	if err == nil {
		t.Fatal("expected error for empty podCIDR")
	}
	if ip != "" || mask != "" {
		t.Errorf("expected empty ip/mask, got %q/%q", ip, mask)
	}
}

func TestAllocateIPForContainer_InvalidCIDR(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{PodCIDR: "not-a-cidr"}}
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "app"}},
		},
	}
	_, _, err := d.allocateIPForContainer(pod, &pod.Spec.Containers[0])
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestAllocateIPForContainer_NonPrimary_NoIP(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{PodCIDR: "10.0.0.0/24"}}
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "first"}, {Name: "second"}},
		},
	}
	ip, _, err := d.allocateIPForContainer(pod, &pod.Spec.Containers[1])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "" {
		t.Errorf("non-primary container should get empty IP, got %q", ip)
	}
}
