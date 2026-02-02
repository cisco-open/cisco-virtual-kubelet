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
	v1 "k8s.io/api/core/v1"
)

type DeviceDriver string

type Config struct {
	// Kubelet tier: Standard Virtual Kubelet settings
	Kubelet KubeletConfig `mapstructure:"kubelet"`

	// Device tier: Abstracted Cisco-specific settings
	Device DeviceConfig `mapstructure:"device"`
}

type KubeletConfig struct {
	NodeName       string `mapstructure:"node_name"`
	NodeInternalIP string `mapstructure:"node_internal_ip"`
}

type DeviceConfig struct {
	Name           string            `mapstructure:"name"`
	Driver         DeviceDriver      `mapstructure:"driver"`
	Address        string            `mapstructure:"address"`
	Port           int               `mapstructure:"port"`
	Username       string            `mapstructure:"username"`
	Password       string            `mapstructure:"password"`
	TLSConfig      *TLSConfig        `mapstructure:"tls,omitempty"`
	Labels         map[string]string `mapstructure:"labels,omitempty"`
	Taints         []v1.Taint        `mapstructure:"taints,omitempty"`
	MaxPods        int32             `mapstructure:"maxPods"`
	Region         string            `mapstructure:"region,omitempty"`
	Zone           string            `mapstructure:"zone,omitempty"`
	ResourceLimits ResourceConfig    `mapstructure:"resourceLimits"`
	Networking     NetworkConfig     `mapstructure:"networking"`
}

// TLSConfig represents TLS configuration for device communication
type TLSConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	InsecureSkipVerify bool   `mapstructure:"insecureSkipVerify"`
	CertFile           string `mapstructure:"certFile,omitempty"`
	KeyFile            string `mapstructure:"keyFile,omitempty"`
	CAFile             string `mapstructure:"caFile,omitempty"`
}

// ResourceConfig represents resource limits and defaults
type ResourceConfig struct {
	DefaultCPU     string            `mapstructure:"defaultCPU"`
	DefaultMemory  string            `mapstructure:"defaultMemory"`
	DefaultStorage string            `mapstructure:"defaultStorage"`
	MaxCPU         string            `mapstructure:"maxCPU"`
	MaxMemory      string            `mapstructure:"maxMemory"`
	MaxStorage     string            `mapstructure:"maxStorage"`
	Others         map[string]string `mapstructure:"others,omitempty"`
}

// NetworkConfig represents networking configuration
type NetworkConfig struct {
	PodCIDR          string `mapstructure:"podCIDR"`
	DHCPEnabled      bool   `mapstructure:"dhcpEnabled"`
	VirtualPortGroup string `mapstructure:"virtualPortGroup"`
}
