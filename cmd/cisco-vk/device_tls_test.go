// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestDeviceTLSCAProjectionStartupFence(t *testing.T) {
	if err := validateDeviceTLSCAProjection(nil, "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"exact", "old-bytes", "missing-file", "missing-revision", "missing-secret", "missing-contract", "unknown-contract", "bad-digest", "insecure", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			directory, _ := writeGNOIProvisioningFiles(t, "192.0.2.1")
			material, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCAFile))
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "ca.crt")
			if err := os.WriteFile(file, material, 0600); err != nil {
				t.Fatal(err)
			}
			spec := &ciskov1.DeviceSpec{TLS: &ciskov1.TLSConfig{Enabled: true, CAFile: file}}
			contract, revision, digest := "v1", "101", fmt.Sprintf("%x", sha256.Sum256(material))
			switch scenario {
			case "old-bytes":
				if err := os.WriteFile(file, []byte("stale CA projection"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-file":
				spec.TLS.CAFile += ".absent"
			case "missing-revision":
				revision = ""
			case "missing-secret":
				revision = "missing"
			case "missing-contract":
				contract = ""
			case "unknown-contract":
				contract = "v2"
			case "bad-digest":
				digest = strings.Repeat("z", 64)
			case "insecure":
				spec.TLS.InsecureSkipVerify = true
			case "disabled":
				spec.TLS.Enabled = false
			}
			if err := validateDeviceTLSCAProjection(spec, contract, revision, digest); (err == nil) != (scenario == "exact") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}
