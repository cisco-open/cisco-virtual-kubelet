// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/spf13/viper"
)

func TestLoadAppArtifactMode(t *testing.T) {
	for _, tc := range []struct{ name, extra, wantErr string }{
		{"omitted", "", ""},
		{"empty", "  appArtifacts: {}\n", ""},
		{"standard", "  appArtifacts:\n    mode: Standard\n", ""},
		{"unknown", "  appArtifacts:\n    mode: Governed\n", "mode must be"},
		{"incomplete verified", "  appArtifacts:\n    mode: Verified\n", "storeURL"},
		{"standard trust", "  appArtifacts:\n    mode: Standard\n    storeURL: https://artifacts.example.test\n", "require mode Verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("device:\n  driver: FAKE\n"+tc.extra), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Device.AppArtifacts.EffectiveMode() != v1alpha1.AppArtifactModeStandard {
				t.Fatal("Standard default changed")
			}
			if tc.name == "omitted" && cfg.Device.AppArtifacts != nil {
				t.Fatal("Standard allocated artifact configuration")
			}
		})
	}
}
