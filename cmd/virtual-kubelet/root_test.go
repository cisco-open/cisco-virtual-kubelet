// Copyright © 2025 Cisco Systems, Inc.
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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFileValidation(t *testing.T) {
	tests := []struct {
		name        string
		configPath  string
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid config file",
			configPath:  "../../dev/config.yaml",
			wantErr:     false,
			errContains: "",
		},
		{
			name:        "missing config file",
			configPath:  "/nonexistent/config.yaml",
			wantErr:     true,
			errContains: "config file not found",
		},
		{
			name:        "empty path uses default",
			configPath:  "",
			wantErr:     true, // default path won't exist in test environment
			errContains: "config file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := tt.configPath
			if configPath == "" {
				configPath = "/etc/virtual-kubelet/config.yaml"
			}

			_, err := os.Stat(configPath)
			gotErr := os.IsNotExist(err)

			if tt.wantErr && !gotErr {
				t.Errorf("expected error for path %q, but file exists", configPath)
			}
			if !tt.wantErr && gotErr {
				t.Errorf("expected file to exist at %q, but got error", configPath)
			}
		})
	}
}

func TestLogLevelValidation(t *testing.T) {
	tests := []struct {
		name        string
		logLevel    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "valid level - debug",
			logLevel: "debug",
			wantErr:  false,
		},
		{
			name:     "valid level - info",
			logLevel: "info",
			wantErr:  false,
		},
		{
			name:     "valid level - warn",
			logLevel: "warn",
			wantErr:  false,
		},
		{
			name:     "valid level - warning",
			logLevel: "warning",
			wantErr:  false,
		},
		{
			name:     "valid level - error",
			logLevel: "error",
			wantErr:  false,
		},
		{
			name:     "valid level - empty (defaults to info)",
			logLevel: "",
			wantErr:  false,
		},
		{
			name:        "invalid level - verbose",
			logLevel:    "verbose",
			wantErr:     true,
			errContains: "invalid log level",
		},
		{
			name:        "invalid level - trace",
			logLevel:    "trace",
			wantErr:     true,
			errContains: "invalid log level",
		},
		{
			name:        "invalid level - DEBUG (case sensitive)",
			logLevel:    "DEBUG",
			wantErr:     true,
			errContains: "invalid log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLogLevel(tt.logLevel)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for log level %q, got nil", tt.logLevel)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for log level %q: %v", tt.logLevel, err)
				}
			}
		})
	}
}

func TestKubeconfigValidation(t *testing.T) {
	// Create a temporary kubeconfig file for testing
	tmpDir := t.TempDir()
	validKubeconfig := filepath.Join(tmpDir, "kubeconfig")
	if err := os.WriteFile(validKubeconfig, []byte("apiVersion: v1\nkind: Config"), 0644); err != nil {
		t.Fatalf("failed to create test kubeconfig: %v", err)
	}

	tests := []struct {
		name        string
		kubeconfig  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid kubeconfig file",
			kubeconfig: validKubeconfig,
			wantErr:    false,
		},
		{
			name:        "missing kubeconfig file",
			kubeconfig:  "/nonexistent/kubeconfig",
			wantErr:     true,
			errContains: "kubeconfig file not found",
		},
		{
			name:       "empty path (will try in-cluster or env)",
			kubeconfig: "",
			wantErr:    false, // empty is valid - will fall back to env or in-cluster
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKubeconfig(tt.kubeconfig)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for kubeconfig %q, got nil", tt.kubeconfig)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for kubeconfig %q: %v", tt.kubeconfig, err)
				}
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name        string
		configPath  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid config",
			configPath: "../../dev/config.yaml",
			wantErr:    false,
		},
		{
			name:        "missing config",
			configPath:  "/nonexistent/path/config.yaml",
			wantErr:     true,
			errContains: "config file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.configPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for config %q, got nil", tt.configPath)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for config %q: %v", tt.configPath, err)
				}
			}
		})
	}
}
