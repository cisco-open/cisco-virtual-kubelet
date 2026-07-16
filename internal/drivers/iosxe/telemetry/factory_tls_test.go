// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package telemetry

import (
	"crypto/tls"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// TestTLSConfigFromDeviceSpec mirrors the transport-package coverage for the
// MDT-over-gNMI path: skip-verify passthrough with the TLS 1.2 floor, and
// fail-fast on an unreadable caFile. Positive CA/RootCAs coverage lives with
// the shared helper in internal/tlsutil.
func TestTLSConfigFromDeviceSpec(t *testing.T) {
	cfg, err := tlsConfigFromDeviceSpec(&ciskov1.DeviceSpec{
		TLS: &ciskov1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureSkipVerify || cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unexpected config: skip=%v min=%d", cfg.InsecureSkipVerify, cfg.MinVersion)
	}

	if _, err := tlsConfigFromDeviceSpec(&ciskov1.DeviceSpec{
		TLS: &ciskov1.TLSConfig{Enabled: true, CAFile: "/nonexistent/cvk-ca.pem"},
	}); err == nil {
		t.Fatal("expected error for unreadable caFile, got nil")
	}
}
