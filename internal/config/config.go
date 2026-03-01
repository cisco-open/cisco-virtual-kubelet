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

package config

import (
	"fmt"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
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

	// Validate driver-specific config
	if err := validateDeviceSpec(&cfg.Device); err != nil {
		return nil, err
	}

	SetDeviceDefaults(&cfg.Device)

	return &cfg, nil
}

// validateDeviceSpec validates the driver-specific sections of a DeviceSpec.
func validateDeviceSpec(spec *v1alpha1.DeviceSpec) error {
	switch spec.Driver {
	case v1alpha1.DeviceDriverXE:
		if spec.XE == nil {
			return fmt.Errorf("driver XE requires xe config section")
		}
		if spec.XE.Networking.Interface != nil {
			if err := spec.XE.Networking.Interface.Validate(); err != nil {
				return fmt.Errorf("invalid XE interface config: %w", err)
			}
		}
	case v1alpha1.DeviceDriverFAKE:
		// No extra validation needed
	default:
		// Future drivers: XR, NXOS
	}
	return nil
}

func SetDeviceDefaults(spec *v1alpha1.DeviceSpec) error {
	// Apply default if Port is not explicitly set (is 0)
	if spec.Port == 0 {
		if spec.TLS == nil || !spec.TLS.Enabled {
			spec.TLS = &v1alpha1.TLSConfig{
				Enabled: false,
			}
			spec.Port = 80
		} else {
			spec.TLS.Enabled = true
			spec.Port = 443
		}
	}

	if spec.TLS == nil {
		spec.TLS = &v1alpha1.TLSConfig{
			Enabled: false,
		}
	}

	return nil
}
