package cisco

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
)

// LoadConfig loads the Cisco provider configuration from file
func LoadConfig(configPath string) (*CiscoConfig, error) {
	// Follow Other provider pattern - only read file if path is not empty
	if configPath == "" {
		return getDefaultConfig(), nil
	}

	// Check if file exists before trying to read
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file does not exist: %s", configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %v", configPath, err)
	}

	var config CiscoConfig

	// Determine file format based on extension
	ext := filepath.Ext(configPath)
	switch ext {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &config)
	case ".json":
		err = json.Unmarshal(data, &config)
	default:
		// Try JSON first, then YAML
		if err = json.Unmarshal(data, &config); err != nil {
			err = yaml.Unmarshal(data, &config)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %v", configPath, err)
	}

	// Validate and set defaults
	if err := validateAndSetDefaults(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %v", err)
	}

	return &config, nil
}

// getDefaultConfig returns a default configuration
func getDefaultConfig() *CiscoConfig {
	return &CiscoConfig{
		Devices: []DeviceConfig{},
		Authentication: AuthConfig{
			Method: "password",
		},
		ResourceLimits: ResourceConfig{
			DefaultCPU:     "100m",
			DefaultMemory:  "128Mi",
			DefaultStorage: "1Gi",
			MaxCPU:         "2",
			MaxMemory:      "4Gi",
			MaxStorage:     "10Gi",
		},
		Networking: NetworkConfig{
			DefaultVRF:  "default",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
			DNSServers:  []string{"8.8.8.8", "8.8.4.4"},
			VLANRange: VLANRange{
				Start: 100,
				End:   199,
			},
			LoadBalancer: LoadBalancerConfig{
				Enabled:   false,
				Type:      "native",
				Algorithm: "round-robin",
			},
		},
		Monitoring: MonitoringConfig{
			Enabled:         true,
			MetricsPort:     9090,
			HealthCheckPort: 8080,
			LogLevel:        "info",
		},
	}
}

// validateAndSetDefaults validates the configuration and sets defaults
func validateAndSetDefaults(config *CiscoConfig) error {
	// Validate devices
	if len(config.Devices) == 0 {
		return fmt.Errorf("at least one device must be configured")
	}

	deviceNames := make(map[string]bool)
	for i, device := range config.Devices {
		// Check for duplicate names
		if deviceNames[device.Name] {
			return fmt.Errorf("duplicate device name: %s", device.Name)
		}
		deviceNames[device.Name] = true

		// Validate device configuration
		if err := validateDeviceConfig(&config.Devices[i]); err != nil {
			return fmt.Errorf("invalid device config for %s: %v", device.Name, err)
		}
	}

	// Validate resource limits
	if err := validateResourceConfig(&config.ResourceLimits); err != nil {
		return fmt.Errorf("invalid resource limits: %v", err)
	}

	// Validate networking configuration
	if err := validateNetworkConfig(&config.Networking); err != nil {
		return fmt.Errorf("invalid network config: %v", err)
	}

	// Set monitoring defaults
	if config.Monitoring.MetricsPort == 0 {
		config.Monitoring.MetricsPort = 9090
	}
	if config.Monitoring.HealthCheckPort == 0 {
		config.Monitoring.HealthCheckPort = 8080
	}
	if config.Monitoring.LogLevel == "" {
		config.Monitoring.LogLevel = "info"
	}

	return nil
}

// validateDeviceConfig validates a single device configuration
func validateDeviceConfig(device *DeviceConfig) error {
	if device.Name == "" {
		return fmt.Errorf("device name is required")
	}

	if device.Address == "" {
		return fmt.Errorf("device address is required")
	}

	if device.Port == 0 {
		switch device.Type {
		case DeviceTypeC9K, DeviceTypeC8K, DeviceTypeC8Kv:
			device.Port = 443 // Default HTTPS port for RESTCONF
		default:
			return fmt.Errorf("unsupported device type: %s", device.Type)
		}
	}

	if device.Username == "" {
		return fmt.Errorf("device username is required")
	}

	if device.Password == "" {
		return fmt.Errorf("device password is required")
	}

	// Set default capabilities based on device type
	if err := setDefaultCapabilities(device); err != nil {
		return fmt.Errorf("failed to set device capabilities: %v", err)
	}

	// Validate max pods
	if device.MaxPods == 0 {
		device.MaxPods = 20 // Default max pods per device
	}

	return nil
}

// setDefaultCapabilities sets default capabilities based on device type
func setDefaultCapabilities(device *DeviceConfig) error {
	var cpu, memory, storage, pods string
	var runtime string
	var arch []string

	switch device.Type {
	case DeviceTypeC9K:
		cpu = "2"      // C9K switches have more capable x86_64 processors
		memory = "4Gi" // Increased memory for x86_64 architecture
		storage = "16Gi"
		pods = "15" // Slightly higher pod capacity for more capable hardware
		runtime = "docker"
		arch = []string{"amd64"} // Correct architecture: x86_64/amd64
	case DeviceTypeC8K:
		cpu = "4" // C8K routers are powerful edge devices
		memory = "8Gi"
		storage = "32Gi"
		pods = "30" // Higher capacity for edge routing scenarios
		runtime = "docker"
		arch = []string{"amd64"} // C8K uses x86_64/amd64 architecture
	case DeviceTypeC8Kv:
		cpu = "8" // Virtual routers can be allocated more resources
		memory = "16Gi"
		storage = "64Gi"
		pods = "50"
		runtime = "docker"
		arch = []string{"amd64"} // Virtual routers run on x86_64/amd64
	default:
		return fmt.Errorf("unsupported device type: %s", device.Type)
	}

	// Parse resource quantities
	cpuQty, err := resource.ParseQuantity(cpu)
	if err != nil {
		return fmt.Errorf("invalid CPU quantity %s: %v", cpu, err)
	}

	memQty, err := resource.ParseQuantity(memory)
	if err != nil {
		return fmt.Errorf("invalid memory quantity %s: %v", memory, err)
	}

	storageQty, err := resource.ParseQuantity(storage)
	if err != nil {
		return fmt.Errorf("invalid storage quantity %s: %v", storage, err)
	}

	podsQty, err := resource.ParseQuantity(pods)
	if err != nil {
		return fmt.Errorf("invalid pods quantity %s: %v", pods, err)
	}

	// Set capabilities if not already configured
	if device.Capabilities.CPU.IsZero() {
		device.Capabilities.CPU = cpuQty
	}
	if device.Capabilities.Memory.IsZero() {
		device.Capabilities.Memory = memQty
	}
	if device.Capabilities.Storage.IsZero() {
		device.Capabilities.Storage = storageQty
	}
	if device.Capabilities.Pods.IsZero() {
		device.Capabilities.Pods = podsQty
	}
	if device.Capabilities.ContainerRuntime == "" {
		device.Capabilities.ContainerRuntime = runtime
	}
	if len(device.Capabilities.SupportedArch) == 0 {
		device.Capabilities.SupportedArch = arch
	}

	// Set default network features
	device.Capabilities.NetworkFeatures = NetworkFeatures{
		VRFSupport:       true,
		VLANSupport:      true,
		ACLSupport:       true,
		QoSSupport:       true,
		LoadBalancing:    device.Type != DeviceTypeC9K, // C9K has limited LB support
		SupportedMTU:     []int{1500, 9000},
		RoutingProtocols: []string{"static", "ospf", "bgp"},
	}

	return nil
}

// validateResourceConfig validates resource configuration
func validateResourceConfig(config *ResourceConfig) error {
	resources := map[string]string{
		"defaultCPU":     config.DefaultCPU,
		"defaultMemory":  config.DefaultMemory,
		"defaultStorage": config.DefaultStorage,
		"maxCPU":         config.MaxCPU,
		"maxMemory":      config.MaxMemory,
		"maxStorage":     config.MaxStorage,
	}

	for name, value := range resources {
		if value == "" {
			continue
		}
		if _, err := resource.ParseQuantity(value); err != nil {
			return fmt.Errorf("invalid %s quantity %s: %v", name, value, err)
		}
	}

	// Validate other resources
	for name, value := range config.Others {
		if _, err := resource.ParseQuantity(value); err != nil {
			return fmt.Errorf("invalid other resource %s quantity %s: %v", name, value, err)
		}
	}

	return nil
}

// validateNetworkConfig validates network configuration
func validateNetworkConfig(config *NetworkConfig) error {
	if config.DefaultVRF == "" {
		config.DefaultVRF = "default"
	}

	if config.PodCIDR == "" {
		config.PodCIDR = "10.244.0.0/16"
	}

	if config.ServiceCIDR == "" {
		config.ServiceCIDR = "10.96.0.0/12"
	}

	// Validate VLAN range
	if config.VLANRange.Start < 1 || config.VLANRange.Start > 4094 {
		return fmt.Errorf("invalid VLAN start range: %d", config.VLANRange.Start)
	}
	if config.VLANRange.End < 1 || config.VLANRange.End > 4094 {
		return fmt.Errorf("invalid VLAN end range: %d", config.VLANRange.End)
	}
	if config.VLANRange.Start > config.VLANRange.End {
		return fmt.Errorf("VLAN start range (%d) cannot be greater than end range (%d)",
			config.VLANRange.Start, config.VLANRange.End)
	}

	// Set default DNS servers if none specified
	if len(config.DNSServers) == 0 {
		config.DNSServers = []string{"8.8.8.8", "8.8.4.4"}
	}

	// Validate load balancer config
	if config.LoadBalancer.Algorithm == "" {
		config.LoadBalancer.Algorithm = "round-robin"
	}

	validAlgorithms := map[string]bool{
		"round-robin": true,
		"least-conn":  true,
		"weighted":    true,
		"ip-hash":     true,
	}
	if !validAlgorithms[config.LoadBalancer.Algorithm] {
		return fmt.Errorf("invalid load balancer algorithm: %s", config.LoadBalancer.Algorithm)
	}

	return nil
}

// GetDeviceByName returns a device configuration by name
func (c *CiscoConfig) GetDeviceByName(name string) *DeviceConfig {
	for _, device := range c.Devices {
		if device.Name == name {
			return &device
		}
	}
	return nil
}

// GetDevicesByType returns all devices of a specific type
func (c *CiscoConfig) GetDevicesByType(deviceType DeviceType) []DeviceConfig {
	var devices []DeviceConfig
	for _, device := range c.Devices {
		if device.Type == deviceType {
			devices = append(devices, device)
		}
	}
	return devices
}

// GetAvailableVLAN returns an available VLAN ID from the configured range
func (c *CiscoConfig) GetAvailableVLAN(usedVLANs map[int]bool) int {
	for vlan := c.Networking.VLANRange.Start; vlan <= c.Networking.VLANRange.End; vlan++ {
		if !usedVLANs[vlan] {
			return vlan
		}
	}
	return -1 // No available VLAN
}

// ValidateConfig performs comprehensive configuration validation
func ValidateConfig(config *CiscoConfig) error {
	return validateAndSetDefaults(config)
}

// SaveConfig saves the configuration to a file
func SaveConfig(config *CiscoConfig, configPath string) error {
	var data []byte
	var err error

	ext := filepath.Ext(configPath)
	switch ext {
	case ".yaml", ".yml":
		data, err = yaml.Marshal(config)
	case ".json":
		data, err = json.MarshalIndent(config, "", "  ")
	default:
		data, err = yaml.Marshal(config)
	}

	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// loadConfigFromEnvOrFile attempts to load configuration from environment variables first,
// then falls back to file-based configuration
func loadConfigFromEnvOrFile(configPath string) (*CiscoConfig, error) {
	// Check if environment variables are set for device configuration
	deviceAddr := os.Getenv("CISCO_DEVICE_ADDRESS")
	deviceUser := os.Getenv("CISCO_DEVICE_USERNAME")
	devicePass := os.Getenv("CISCO_DEVICE_PASSWORD")

	if deviceAddr != "" && deviceUser != "" && devicePass != "" {
		// Create configuration from environment variables
		config := &CiscoConfig{
			Devices: []DeviceConfig{
				{
					Name:     "c9k-env-device",
					Type:     DeviceTypeC9K,
					Address:  deviceAddr,
					Port:     443, // Default HTTPS port
					Username: deviceUser,
					Password: devicePass,
					TLSConfig: &TLSConfig{
						Enabled:            true,
						InsecureSkipVerify: true,
					},
					Capabilities: DeviceCapability{
						CPU:              resource.MustParse("2"),
						Memory:           resource.MustParse("2Gi"),
						Storage:          resource.MustParse("95Gi"),
						Pods:             resource.MustParse("10"),
						ContainerRuntime: "docker",
						SupportedArch:    []string{"amd64"},
					},
					MaxPods: 10,
					Labels: map[string]string{
						"device-type": "switch",
						"location":    "env-configured",
						"environment": "production",
					},
					Region: "default",
					Zone:   "default",
				},
			},
			Authentication: AuthConfig{
				Method: "password",
			},
			ResourceLimits: ResourceConfig{
				DefaultCPU:     "100m",
				DefaultMemory:  "128Mi",
				DefaultStorage: "1Gi",
				MaxCPU:         "1500m",
				MaxMemory:      "3Gi",
				MaxStorage:     "10Gi",
			},
			Networking: NetworkConfig{
				DefaultVRF:  "global",
				PodCIDR:     "10.244.0.0/16",
				ServiceCIDR: "10.96.0.0/12",
				DNSServers:  []string{"8.8.8.8", "8.8.4.4"},
				VLANRange: VLANRange{
					Start: 100,
					End:   120,
				},
				LoadBalancer: LoadBalancerConfig{
					Enabled:   false,
					Type:      "native",
					Algorithm: "round-robin",
				},
			},
			Monitoring: MonitoringConfig{
				Enabled:         true,
				MetricsPort:     9090,
				HealthCheckPort: 8080,
				LogLevel:        "debug",
				ScrapeInterval:  30 * time.Second,
				RetentionPeriod: time.Hour,
			},
		}

		// Validate and set defaults
		if err := validateAndSetDefaults(config); err != nil {
			return nil, fmt.Errorf("invalid environment-based configuration: %v", err)
		}

		return config, nil
	}

	// Fall back to file-based configuration - follow common provider pattern
	if configPath != "" {
		// Try the specified config path first
		config, err := LoadConfig(configPath)
		if err == nil {
			return config, nil
		}
		// Config file not found or invalid, continue to default paths
	}

	// Try default locations
	defaultPaths := []string{
		"/etc/cisco-vk/config.yaml",
		"/etc/cisco-vk/config.yml",
		"/etc/cisco-vk/config.json",
		"/etc/cisco/config.yaml",
		"./cisco-config.yaml",
	}

	for _, path := range defaultPaths {
		config, err := LoadConfig(path)
		if err == nil {
			// Successfully loaded config from default path
			return config, nil
		}
	}

	// If no config file found, return default config (like common  provider)
	return getDefaultConfig(), nil
}
