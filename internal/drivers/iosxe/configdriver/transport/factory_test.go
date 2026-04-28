// Copyright © 2026 Cisco Systems Inc.
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

package transport

import (
	"fmt"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestForDefaultsToRESTCONF(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "10.0.0.1",
		Port:    443,
		TLS:     &ciskov1.TLSConfig{Enabled: true},
	}
	cli, err := For(spec, "pw", FactoryOptions{})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if cli.Capabilities().Kind != KindRESTCONF {
		t.Errorf("got kind %v, want restconf", cli.Capabilities().Kind)
	}
}

// TestForNETCONFDialFailsGracefully pins the factory's NETCONF
// path now that it dials SSH. We can't stand up a real SSH
// server here; the address points at a local port with no
// listener so ssh.Dial returns an error. The test shape is
// "factory returned the NETCONF build path's error, not the
// 'reserved' placeholder".
func TestForNETCONFDialFailsGracefully(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address:   "127.0.0.1",
		Port:      1, // guaranteed-no-listener
		Username:  "x",
		Transport: "netconf",
	}
	_, err := For(spec, "pw", FactoryOptions{})
	if err == nil {
		t.Fatal("expected dial error, got nil")
	}
	// A 'reserved' placeholder would have literally contained
	// the word 'reserved'; a real dial error won't.
	if strings.Contains(err.Error(), "reserved") {
		t.Fatalf("factory still returns reserved placeholder: %v", err)
	}
}

// TestBuildNETCONFRejectsRESTCONFPortDefault is a regression test
// for finding #6(a) root cause. config.SetDeviceDefaults applies
// the apphosting/RESTCONF port (80 / 443) to spec.Port — which is
// NOT a sensible NETCONF default. buildNETCONF must override these
// well-known RESTCONF defaults to 830 (IANA NETCONF-over-SSH).
// Operator-supplied non-RESTCONF ports are left untouched.
func TestBuildNETCONFRejectsRESTCONFPortDefault(t *testing.T) {
	cases := []struct {
		name     string
		specPort int
		wantPort int
	}{
		{"unset → 830", 0, 830},
		{"RESTCONF default 80 → 830", 80, 830},
		{"RESTCONF default 443 → 830", 443, 830},
		{"explicit 8830 (hardened lab) preserved", 8830, 8830},
		{"explicit 8443 (NETCONF on alt port) preserved", 8443, 8443},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &ciskov1.DeviceSpec{
				Address:   "127.0.0.1",
				Port:      tc.specPort,
				Username:  "x",
				Transport: "netconf",
			}
			// The factory dials immediately. Use a dial error to
			// inspect the port it actually tried to dial — surfaced
			// in the wrapped error message as `127.0.0.1:<port>`.
			_, err := For(spec, "pw", FactoryOptions{})
			if err == nil {
				t.Fatal("expected dial error, got nil")
			}
			want := fmt.Sprintf("127.0.0.1:%d", tc.wantPort)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("dial port mismatch: err=%v, expected to contain %q", err, want)
			}
		})
	}
}

// TestBuildGNMITLSAnchoredToInsecurePort pins the rule that
// spec.TLS.Enabled is the apphosting RESTCONF intent and the gNMI
// factory takes the same liberty with TLS as it does with port:
// gnxi insecure (50052) is treated as "no TLS" regardless of
// spec.TLS.Enabled. Caught against the live C9K-4 retest of test
// 04 (2026-04-28).
func TestBuildGNMITLSAnchoredToInsecurePort(t *testing.T) {
	cases := []struct {
		name       string
		specPort   int
		tlsEnabled bool
		wantPort   int
		wantTLS    bool
	}{
		{"insecure-50052-tls-true → no TLS", 50052, true, 50052, false},
		{"insecure-default-port-tls-true → no TLS", 0, true, 50052, false},
		{"insecure-default-port-tls-false → no TLS", 0, false, 50052, false},
		{"explicit-9339-tls-true → TLS", 9339, true, 9339, true},
		{"explicit-9339-tls-false → no TLS", 9339, false, 9339, false},
		{"explicit-6030-tls-true → TLS", 6030, true, 6030, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &ciskov1.DeviceSpec{
				Address:   "127.0.0.1",
				Port:      tc.specPort,
				Username:  "x",
				Transport: "gnmi",
			}
			if tc.tlsEnabled {
				spec.TLS = &ciskov1.TLSConfig{Enabled: true, InsecureSkipVerify: true}
			}
			tr, err := For(spec, "pw", FactoryOptions{})
			if err != nil {
				t.Fatalf("gNMI factory: %v", err)
			}
			defer tr.Close()
			gt, ok := tr.(*gnmiTransport)
			if !ok {
				t.Fatalf("expected *gnmiTransport, got %T", tr)
			}
			if gt.cfg.Port != tc.wantPort {
				t.Errorf("port: got %d, want %d", gt.cfg.Port, tc.wantPort)
			}
			gotTLS := gt.cfg.TLSConfig != nil
			if gotTLS != tc.wantTLS {
				t.Errorf("TLS: got %v, want %v", gotTLS, tc.wantTLS)
			}
		})
	}
}

// TestBuildGNMIRejectsRESTCONFPortDefault is the gNMI counterpart
// of TestBuildNETCONFRejectsRESTCONFPortDefault. Same rationale:
// 80 / 443 are RESTCONF intent, not gNMI intent.
func TestBuildGNMIRejectsRESTCONFPortDefault(t *testing.T) {
	cases := []struct {
		name     string
		specPort int
		wantPort int
	}{
		{"unset → gnxi insecure default 50052", 0, 50052},
		{"RESTCONF default 80 → 50052", 80, 50052},
		{"RESTCONF default 443 → 50052", 443, 50052},
		{"explicit 6030 (legacy gnmi-yang) preserved", 6030, 6030},
		{"explicit 57400 preserved", 57400, 57400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &ciskov1.DeviceSpec{
				Address:   "127.0.0.1",
				Port:      tc.specPort,
				Username:  "x",
				Transport: "gnmi",
			}
			tr, err := For(spec, "pw", FactoryOptions{})
			if err != nil {
				t.Fatalf("gNMI factory should not block on dial: %v", err)
			}
			defer tr.Close()
			gt, ok := tr.(*gnmiTransport)
			if !ok {
				t.Fatalf("expected *gnmiTransport, got %T", tr)
			}
			if gt.cfg.Port != tc.wantPort {
				t.Fatalf("gNMI port mismatch: got %d, want %d", gt.cfg.Port, tc.wantPort)
			}
		})
	}
}

func TestForGNMIBuildsTransport(t *testing.T) {
	// gNMI is no longer reserved — the factory now builds a real
	// transport. Construction must succeed without dialling, since
	// dialling is lazy. A bad address surfaces on first RPC, not
	// at NewGNMI time, matching every other transport's contract.
	spec := &ciskov1.DeviceSpec{Address: "10.0.0.1", Transport: "gnmi"}
	got, err := For(spec, "pw", FactoryOptions{})
	if err != nil {
		t.Fatalf("For(gnmi) errored: %v", err)
	}
	if got.Capabilities().Kind != KindGNMI {
		t.Errorf("Kind=%v, want gnmi", got.Capabilities().Kind)
	}
}

func TestForUnknownTransport(t *testing.T) {
	spec := &ciskov1.DeviceSpec{Address: "10.0.0.1", Transport: "telnet"}
	_, err := For(spec, "pw", FactoryOptions{})
	if err == nil || !strings.Contains(err.Error(), "unknown transport") {
		t.Fatalf("got %v, want unknown-transport error", err)
	}
}

func TestForEmptyAddressRejected(t *testing.T) {
	spec := &ciskov1.DeviceSpec{}
	_, err := For(spec, "", FactoryOptions{})
	if err == nil {
		t.Error("expected error for empty address")
	}
}
