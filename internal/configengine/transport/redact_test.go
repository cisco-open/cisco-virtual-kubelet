// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import "testing"

// B5: the shared redactor is the single credential filter for both IOS-XE
// and NX-OS. NX-OS introduces the DME login "pwd" field and namespace/module
// -prefixed credential shapes; all must be redacted before reaching logs,
// events, or status.
func TestRedactCredentialsCoversNXOSShapes(t *testing.T) {
	const token = "S3cr3t!"
	cases := []struct {
		name string
		in   string
	}{
		{"dme login pwd", `{"aaaUser":{"attributes":{"name":"admin","pwd":"` + token + `"}}}`},
		{"namespaced xml password", `<nxos:password>` + token + `</nxos:password>`},
		{"namespaced xml secret", `<aaa:secret>` + token + `</aaa:secret>`},
		{"module-prefixed json key", `{"Cisco-IOS-XE-native:password":"` + token + `"}`},
		{"module-prefixed json secret", `{"Cisco-IOS-XE-aaa:secret":"` + token + `"}`},
		{"bare xml password (regression)", `<password>` + token + `</password>`},
		{"bare json password (regression)", `{"password":"` + token + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactCredentials(tc.in)
			if contains(got, token) {
				t.Fatalf("credential leaked: %q -> %q", tc.in, got)
			}
			if !contains(got, "***REDACTED***") {
				t.Fatalf("expected redaction marker in %q", got)
			}
		})
	}
}

func contains(s, sub string) bool { return substringIndex(s, sub) >= 0 }
