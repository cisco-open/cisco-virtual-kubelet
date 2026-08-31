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
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
)

// FactoryOptions carries construction-time parameters that are not part
// of the CiscoDevice CR — typically process-lifetime state like the
// shared HTTP client.
type FactoryOptions struct {
	// HTTPClient, when non-nil, is used by the RESTCONF transport. When
	// nil, the factory builds one from DeviceSpec.TLS. Reuse the
	// apphosting driver's HTTP client in production so both drivers
	// share a single TLS session and credential.
	HTTPClient *http.Client

	// SessionLock, when non-nil, is attached to the RESTCONF transport
	// so every RESTCONF request serialises against callers holding the
	// same lock. Use this to keep apphosting writes from interleaving
	// with config writes at the device.
	SessionLock *sync.Mutex

	// DefaultTimeout is used for HTTPClient construction when
	// FactoryOptions.HTTPClient is nil. Zero means 30s.
	DefaultTimeout time.Duration

	// GRPCConn, when non-nil, is injected into the gNMI transport in
	// place of a fresh dial. Production callers obtain it from a
	// devicegrpc.Pool lease (WorkloadClass ClassControl) and retain
	// the lease — the transport does not own the conn and never
	// closes it. Tests pass a bufconn-backed conn here directly.
	//
	// Ignored by non-gNMI transports.
	GRPCConn *grpc.ClientConn
}

// For builds a transport for a CiscoDevice per its spec.Transport field.
// Unknown transports fail fast with a specific error so operators see
// the misconfiguration at reconciler startup rather than on first write.
func For(spec *ciskov1.DeviceSpec, pass string, opts FactoryOptions) (Interface, error) {
	if spec == nil {
		return nil, fmt.Errorf("transport.For: nil DeviceSpec")
	}

	kind := Kind(strings.ToLower(spec.Transport))
	if kind == "" {
		kind = KindRESTCONF
	}

	switch kind {
	case KindRESTCONF:
		return buildRESTCONF(spec, pass, opts)
	case KindNETCONF:
		return buildNETCONF(spec, pass, opts)
	case KindGNMI:
		return buildGNMI(spec, pass, opts)
	default:
		return nil, fmt.Errorf("transport.For: unknown transport %q", spec.Transport)
	}
}

// buildGNMI constructs a gNMI (gRPC) transport. Port defaults to
// 6030 (OpenConfig gNMI default; IOS-XE listens here too). TLS
// honours DeviceSpec.TLS just like RESTCONF — gNMI runs over TLS
// in production. The factory does not load client certs; operators
// who need them inject a pre-built grpc.ClientConn via
// GNMIConfig.Conn (mirrors the FactoryOptions.HTTPClient pattern
// for RESTCONF).
func buildGNMI(spec *ciskov1.DeviceSpec, pass string, opts FactoryOptions) (Interface, error) {
	if spec.Address == "" {
		return nil, fmt.Errorf("transport.For: CiscoDevice.spec.address empty")
	}
	// Same caveat as buildNETCONF: spec.Port is the apphosting RESTCONF
	// port and is NOT a sensible gNMI default. Treat the well-known
	// RESTCONF defaults as "not a gNMI intent" and fall through to the
	// IOS-XE 17.18 gnxi insecure default (50052). Operators on older
	// `gnmi-yang` builds with 6030/57400 set spec.Port explicitly.
	port := spec.Port
	if port == 0 || port == 80 || port == 443 {
		port = 50052
	}
	cfg := GNMIConfig{
		Address:  spec.Address,
		Port:     port,
		Username: spec.Username,
		Password: pass,
		Conn:     opts.GRPCConn,
	}
	// spec.TLS.Enabled is the apphosting RESTCONF intent (443 RESTCONF
	// requires TLS). The IOS-XE gnxi insecure listener on port 50052
	// does NOT speak TLS — wrapping it in a TLS handshake produces
	// `tls: first record does not look like a TLS handshake`. Mirror
	// the port-defaulting pattern above: when gNMI is on its well-
	// known insecure default, it picks insecure regardless of the
	// apphosting-shaped spec.TLS.Enabled. Operators who run secure
	// gnxi (port 9339) leave spec.Port at the apphosting default
	// AND set their secure gnxi up on the device; we'd then need to
	// extend this guard. For now the rule is simply "gnxi insecure
	// on 50052 → no TLS"; explicit operator-override ports (everything
	// outside 50052/80/443) still honour spec.TLS.Enabled.
	//
	// Wave 10.4 — caught against the live C9K-4 retest of test 04
	// (2026-04-28) where the gNMI client got
	// `transport: authentication handshake failed` against gnxi:50052.
	if spec.TLS != nil && spec.TLS.Enabled && port != 50052 {
		tlsCfg, err := buildTLSFromSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("transport.For: gNMI TLS from spec: %w", err)
		}
		cfg.TLSConfig = tlsCfg
	}
	return NewGNMI(cfg)
}

// buildNETCONF constructs a NETCONF-over-SSH transport. Port
// defaults to 830 (IANA NETCONF-over-SSH). TLS configuration on
// DeviceSpec is ignored because NETCONF is transported over SSH,
// not TLS; operators who want to pin the SSH host key set
// NETCONFConfig.HostKeyCallback directly when wiring the factory
// in cisco-vk-run.
//
// `spec.Port` is the **apphosting RESTCONF** port (defaulted to 443
// by config.SetDeviceDefaults for HTTPS hosts). It is NOT a sensible
// default for NETCONF — picking 443 lands the SSH dial on the
// device's HTTPS listener and produces
// `ssh: overflow reading version string` while reading TLS bytes as
// an SSH version banner. Caught against a live Cat9300-24P / IOS-XE
// 17.18.2 live-device validation on 2026-04-27.
//
// To preserve the operator-override pathway without coupling the
// configdriver port to the apphosting port, treat the well-known
// RESTCONF default ports (80 / 443) as "not a NETCONF intent" and
// fall through to 830. Any other value is taken as an explicit
// NETCONF port override (e.g. 8830 on hardened lab images).
func buildNETCONF(spec *ciskov1.DeviceSpec, pass string, opts FactoryOptions) (Interface, error) {
	if spec.Address == "" {
		return nil, fmt.Errorf("transport.For: CiscoDevice.spec.address empty")
	}
	port := spec.Port
	if port == 0 || port == 80 || port == 443 {
		port = 830
	}
	timeout := opts.DefaultTimeout
	return NewNETCONF(NETCONFConfig{
		Address:  spec.Address,
		Port:     port,
		Username: spec.Username,
		Password: pass,
		Timeout:  timeout,
	})
}

func buildRESTCONF(spec *ciskov1.DeviceSpec, pass string, opts FactoryOptions) (Interface, error) {
	scheme := "https"
	port := spec.Port
	if spec.TLS == nil || !spec.TLS.Enabled {
		scheme = "http"
		if port == 0 {
			port = 80
		}
	} else if port == 0 {
		port = 443
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("transport.For: CiscoDevice.spec.address empty")
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		timeout := opts.DefaultTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		tlsCfg, err := buildTLSFromSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("transport.For: RESTCONF TLS from spec: %w", err)
		}
		httpClient = &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		}
	}

	baseURL := fmt.Sprintf("%s://%s:%d/restconf/data", scheme, spec.Address, port)
	return NewRESTCONF(RESTCONFConfig{
		BaseURL:     baseURL,
		HTTPClient:  httpClient,
		Username:    spec.Username,
		Password:    pass,
		SessionLock: opts.SessionLock,
		// Diagnostics-RFC Phase A v2: SSH-CLI side-channel for
		// show-command capture (IOS-XE 17.18 has no YANG RPC
		// returning textual show output). spec.address is the
		// CLI host; port 22 is the IOS-XE CLI default.
		CLIHost: spec.Address,
		CLIPort: 22,
	})
}

// buildTLSFromSpec matches the apphosting driver's behaviour: the shared
// helper honours InsecureSkipVerify, loads spec.tls.caFile into RootCAs
// (so private-CA devices get verified TLS on this path too, instead of
// being forced to skip-verify), and loads the certFile/keyFile client
// pair when both are set.
func buildTLSFromSpec(spec *ciskov1.DeviceSpec) (*tls.Config, error) {
	return tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
}
