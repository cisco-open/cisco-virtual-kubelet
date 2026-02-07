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

// Package config handles loading and validating the VK configuration.
// The canonical type definitions live in api/v1alpha1; this package provides
// the YAML/viper loader that returns a DeviceSpec.
//
// Runtime settings (node name, listen address, etc.) are supplied via CLI
// flags and environment variables — they are NOT part of the device config.
package config

import (
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Config is the top-level configuration loaded from YAML or environment.
// It contains only device-level settings; kubelet runtime settings are
// supplied via CLI flags / env vars.
type Config struct {
	// Device tier: Shared device spec (same schema as the CRD).
	Device v1alpha1.DeviceSpec `mapstructure:"device"`
}
