// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package tlsutil

import (
	"os"
	"testing"
)

func TestPublicCABundleContainsOnlyCertificates(t *testing.T) {
	ca, err := os.ReadFile(writeTestCA(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	key := "-----BEGIN PRIVATE KEY-----\nprivate material\n-----END PRIVATE KEY-----\n"
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"CA", string(ca), true},
		{"chain", string(ca) + string(ca), true},
		{"whitespace", " \n" + string(ca) + "\n ", true},
		{"empty", "", false},
		{"key-before", key + string(ca), false},
		{"key-after", string(ca) + key, false},
		{"preamble", "untrusted bytes\n" + string(ca), false},
		{"suffix", string(ca) + "untrusted bytes", false},
		{"malformed", "-----BEGIN CERTIFICATE-----\nZm9v\n-----END CERTIFICATE-----\n", false},
		{"skipped-block", "-----BEGIN CERTIFICATE-----\ninvalid\n" + string(ca), false},
		{"skipped-private", "-----BEGIN CERTIFICATE-----\ninvalid\n" + key + string(ca), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePublicCABundle([]byte(tc.data)); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
