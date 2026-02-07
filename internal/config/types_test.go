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
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestInterfaceConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *v1alpha1.XEInterfaceConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "valid VirtualPortGroup",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type:             v1alpha1.XEInterfaceTypeVirtualPortGroup,
				VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{},
			},
			wantErr: false,
		},
		{
			name: "valid AppGigabitEthernet",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type:               v1alpha1.XEInterfaceTypeAppGigabitEthernet,
				AppGigabitEthernet: &v1alpha1.XEAppGigabitEthernetConfig{},
			},
			wantErr: false,
		},
		{
			name: "valid Management",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type:       v1alpha1.XEInterfaceTypeManagement,
				Management: &v1alpha1.XEManagementConfig{},
			},
			wantErr: false,
		},
		{
			name:        "nil config",
			cfg:         nil,
			wantErr:     true,
			errContains: "cannot be nil",
		},
		{
			name: "no interface config set",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type: v1alpha1.XEInterfaceTypeVirtualPortGroup,
			},
			wantErr:     true,
			errContains: "one interface config must be set",
		},
		{
			name: "multiple interface configs set",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type:             v1alpha1.XEInterfaceTypeVirtualPortGroup,
				VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{},
				Management:       &v1alpha1.XEManagementConfig{},
			},
			wantErr:     true,
			errContains: "only one interface config may be set",
		},
		{
			name: "type mismatch",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type:             v1alpha1.XEInterfaceTypeAppGigabitEthernet,
				VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{},
			},
			wantErr:     true,
			errContains: "requires appGigabitEthernet config",
		},
		{
			name: "unsupported type",
			cfg: &v1alpha1.XEInterfaceConfig{
				Type:             v1alpha1.XEInterfaceType("Other"),
				VirtualPortGroup: &v1alpha1.XEVirtualPortGroupConfig{},
			},
			wantErr:     true,
			errContains: "unsupported interface type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
