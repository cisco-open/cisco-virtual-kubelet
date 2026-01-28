package iosxe

import (
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
