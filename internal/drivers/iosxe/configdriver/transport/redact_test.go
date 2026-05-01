// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import (
	"strings"
	"testing"
)

func TestRedactCredentials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     string
		leaks  []string // substrings that MUST NOT appear in output
		hasTok bool     // output should contain ***REDACTED***
	}{
		{
			name:   "netconf-password-element",
			in:     `<error-info><bad-element><user><name>cisco</name><password>p4ssw0rd!</password></user></bad-element></error-info>`,
			leaks:  []string{"p4ssw0rd!"},
			hasTok: true,
		},
		{
			name:   "netconf-encrypted-password",
			in:     `<encrypted-password>$1$abcd$xyz</encrypted-password>`,
			leaks:  []string{"$1$abcd$xyz"},
			hasTok: true,
		},
		{
			name:   "netconf-shared-secret",
			in:     `<radius><server><shared-secret>topsecretvalue</shared-secret></server></radius>`,
			leaks:  []string{"topsecretvalue"},
			hasTok: true,
		},
		{
			name:   "netconf-key-string",
			in:     `<key-chain><key><key-string>SECRETKEY123</key-string></key></key-chain>`,
			leaks:  []string{"SECRETKEY123"},
			hasTok: true,
		},
		{
			name:   "restconf-json-password",
			in:     `{"user":{"name":"cisco","password":"p4ssw0rd!"}}`,
			leaks:  []string{"p4ssw0rd!"},
			hasTok: true,
		},
		{
			name:   "restconf-json-shared-secret",
			in:     `{"radius-server":{"shared-secret":"radkey42"}}`,
			leaks:  []string{"radkey42"},
			hasTok: true,
		},
		{
			name:   "cli-password-type-7",
			in:     `error: invalid password 7 094F471A1A0A — must be at least 8 characters`,
			leaks:  []string{"094F471A1A0A"},
			hasTok: true,
		},
		{
			name:   "cli-secret-type-5",
			in:     `enable secret 5 $1$abc$DEFGHIjkl`,
			leaks:  []string{"$1$abc$DEFGHIjkl"},
			hasTok: true,
		},
		{
			name:   "no-credential-passes-through",
			in:     `op[0] MERGE /interface/Loopback=9999: rpc-error: unknown-element name`,
			leaks:  nil,
			hasTok: false,
		},
		{
			name:   "narrative-mention-of-password-keyword-not-redacted",
			in:     `error: device rejected the password change due to policy`,
			leaks:  nil,
			hasTok: false,
		},
		{
			name:   "empty-string",
			in:     ``,
			leaks:  nil,
			hasTok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactCredentials(tc.in)
			for _, leak := range tc.leaks {
				if strings.Contains(got, leak) {
					t.Errorf("leak: %q present in redacted output\n  in:  %q\n  out: %q", leak, tc.in, got)
				}
			}
			has := strings.Contains(got, "***REDACTED***")
			if has != tc.hasTok {
				t.Errorf("redaction-token mismatch: got=%v want=%v\n  in:  %q\n  out: %q", has, tc.hasTok, tc.in, got)
			}
		})
	}
}

func TestRedactCredentialsIdempotent(t *testing.T) {
	t.Parallel()
	in := `<password>hunter2</password> and <secret>$1$abc$xyz</secret>`
	once := RedactCredentials(in)
	twice := RedactCredentials(once)
	if once != twice {
		t.Errorf("RedactCredentials not idempotent:\n  once:  %q\n  twice: %q", once, twice)
	}
	if strings.Contains(once, "hunter2") || strings.Contains(once, "$1$abc$xyz") {
		t.Errorf("first pass left credential: %q", once)
	}
}
