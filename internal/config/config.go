// Copyright © 2026 Cisco Systems, Inc.
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

package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

func Load(filePath ...string) (*Config, error) {
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

	// Read the config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// It's okay if file is missing; we can rely on ENV
	}

	// Unmarshal into struct
	var cfg Config
	if err := viper.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	// var cfg Config

	// // Use a manual decoder configuration instead of UnmarshalExact
	// // This allows us to provide "Hooks" that fix the map[string]interface{} issue
	// err := viper.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
	// 	dc.TagName = "mapstructure"
	// 	// This is the magic line for 2025:
	// 	// It tells mapstructure how to convert weak types (interfaces) to strong types (strings)
	// 	dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
	// 		mapstructure.StringToTimeDurationHookFunc(),
	// 		mapstructure.WeaklyTypedHook, // This specifically fixes the map[string]string error
	// 	)
	// 	// If you still want the "Exact" behavior to fail on unknown keys:
	// 	dc.ErrorUnused = true
	// })

	// if err != nil {
	// 	return nil, fmt.Errorf("unable to decode into struct: %w", err)
	// }

	SetDeviceDefaults(&cfg.Device)

	return &cfg, nil
}

// GetAvailableVLAN returns an available VLAN ID from the configured range
func (c *DeviceConfig) GetAvailableVLAN(usedVLANs map[int]bool) int {
	for vlan := c.Networking.VLANRange.Start; vlan <= c.Networking.VLANRange.End; vlan++ {
		if !usedVLANs[vlan] {
			return vlan
		}
	}
	return -1 // No available VLAN
}

func SetDeviceDefaults(cfg *DeviceConfig) error {
	// Apply default if Port is not explicitly set (is 0)
	if cfg.Port == 0 {
		if cfg.TLSConfig == nil || !cfg.TLSConfig.Enabled {
			cfg.TLSConfig = &TLSConfig{
				Enabled: false,
			}
			cfg.Port = 80
		} else {
			cfg.TLSConfig.Enabled = true
			cfg.Port = 443
		}
	}

	if cfg.TLSConfig == nil {
		cfg.TLSConfig = &TLSConfig{
			Enabled: false,
		}
	}

	return nil
}
