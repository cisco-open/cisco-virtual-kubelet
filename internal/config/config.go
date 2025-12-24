package config

import (
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var registerFlagsOnce sync.Once

func Load(filePath ...string) (*CiscoConfig, error) {

	if len(filePath) > 0 && filePath[0] != "" {
		viper.SetConfigFile(filePath[0])
	} else {
		// Production defaults
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
	}

	// Setup Environment Variables
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_")) // allow SERVER_PORT for server.port
	viper.AutomaticEnv()

	registerFlagsOnce.Do(func() {
		// This doesn't actually work for the current schema
		pflag.String("devices.name", "", "Device name")
		// Add any other pflag definitions here
	})

	// Parse flags only if not already parsed (to avoid errors in tests)
	if !pflag.Parsed() {
		pflag.Parse()
	}

	if err := viper.BindPFlags(pflag.CommandLine); err != nil {
		return nil, err
	}

	// 4. Read the file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// It's okay if file is missing; we can rely on ENV or Flags
	}

	// 5. Unmarshal into struct
	var cfg CiscoConfig
	if err := viper.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	SetConfigDefault(&cfg)

	return &cfg, nil
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

func SetConfigDefault(cfg *CiscoConfig) error {
	for i := range cfg.Devices {
		device := &cfg.Devices[i]

		// Apply default if Port is not explicitly set (is 0)
		if device.Port == 0 {
			if device.TLSConfig == nil || !device.TLSConfig.Enabled {
				device.Port = 80
			} else {
				device.TLSConfig.Enabled = true
				device.Port = 443
			}
		}
	}

	return nil
}
