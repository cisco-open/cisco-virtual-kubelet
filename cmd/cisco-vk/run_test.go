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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestValidateConfig(t *testing.T) {
	tmpDir := t.TempDir()
	validConfigPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(validConfigPath, []byte("test: config"), 0644); err != nil {
		t.Fatalf("failed to create test config: %v", err)
	}

	tests := []struct {
		name        string
		configPath  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid config",
			configPath: validConfigPath,
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

func TestGetKubeConfig(t *testing.T) {
	originalKubeconfig := os.Getenv("KUBECONFIG")
	defer os.Setenv("KUBECONFIG", originalKubeconfig)

	tests := []struct {
		name        string
		flagValue   string
		envValue    string
		wantErr     bool
		errContains string
		setupEnv    bool
	}{
		{
			name:        "flag provided with invalid path",
			flagValue:   "/nonexistent/kubeconfig",
			wantErr:     true,
			errContains: "kubeconfig file not found",
		},
		{
			name:        "no flag, env var with invalid path",
			flagValue:   "",
			envValue:    "/nonexistent/kubeconfig",
			setupEnv:    true,
			wantErr:     true,
			errContains: "kubeconfig file from KUBECONFIG env not found",
		},
		{
			name:        "no flag, no env - in-cluster expected",
			flagValue:   "",
			envValue:    "",
			setupEnv:    true,
			wantErr:     true,
			errContains: "failed to load in-cluster config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupEnv {
				os.Setenv("KUBECONFIG", tt.envValue)
				defer os.Unsetenv("KUBECONFIG")
			}

			_, err := GetKubeConfig(tt.flagValue)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
