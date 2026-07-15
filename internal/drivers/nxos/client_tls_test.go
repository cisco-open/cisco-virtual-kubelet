// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// TestNXAPIClientTLSFromSpec asserts the NX-API HTTP client is built through
// the shared device-client TLS helper: the TLS 1.2 floor and skip-verify
// passthrough apply, and an unreadable caFile fails construction rather than
// silently producing an unverified client.
func TestNXAPIClientTLSFromSpec(t *testing.T) {
	c, err := newNXAPIClient(&v1alpha1.DeviceSpec{
		Address: "192.0.2.9",
		TLS:     &v1alpha1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := c.client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		t.Fatalf("expected *http.Transport with TLS config, got %#v", c.client.Transport)
	}
	if !tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unexpected TLS config: skip=%v min=%d",
			tr.TLSClientConfig.InsecureSkipVerify, tr.TLSClientConfig.MinVersion)
	}

	if _, err := newNXAPIClient(&v1alpha1.DeviceSpec{
		Address: "192.0.2.9",
		TLS:     &v1alpha1.TLSConfig{Enabled: true, CAFile: "/nonexistent/cvk-ca.pem"},
	}); err == nil {
		t.Fatal("expected error for unreadable caFile, got nil")
	}

	if c, err := newNXAPIClient(&v1alpha1.DeviceSpec{
		Address: "192.0.2.9",
		TLS:     &v1alpha1.TLSConfig{Enabled: false, CAFile: "/nonexistent/cvk-ca.pem"},
	}); err != nil {
		t.Fatalf("disabled TLS loaded caFile: %v", err)
	} else if c.rootURL != "http://192.0.2.9:80" {
		t.Fatalf("disabled TLS root URL = %q, want HTTP", c.rootURL)
	}
}
