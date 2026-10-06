// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

// Copied into the pinned released module, never compiled against current flags.
package main

import (
	"strings"
	"testing"
)

func TestOctoberWorkerRejectsDeviceCAContract(t *testing.T) {
	// Cobra parses these flags before invoking RunE. No config, Kubernetes
	// credentials, transport or background worker is initialized by this test.
	err := runCmd.ParseFlags([]string{"--device-tls-ca-projection=v1"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --device-tls-ca-projection") {
		t.Fatalf("released worker did not reject unsupported device CA contract: %v", err)
	}
}
