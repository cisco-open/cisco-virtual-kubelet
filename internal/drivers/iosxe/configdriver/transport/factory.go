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

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
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
		return nil, fmt.Errorf("transport.For: NETCONF transport is reserved; Phase-2 deliverable")
	case KindGNMI:
		return nil, fmt.Errorf("transport.For: gNMI transport is reserved; not yet scheduled")
	default:
		return nil, fmt.Errorf("transport.For: unknown transport %q", spec.Transport)
	}
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
		httpClient = &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: buildTLSFromSpec(spec)},
		}
	}

	baseURL := fmt.Sprintf("%s://%s:%d/restconf/data", scheme, spec.Address, port)
	return NewRESTCONF(RESTCONFConfig{
		BaseURL:     baseURL,
		HTTPClient:  httpClient,
		Username:    spec.Username,
		Password:    pass,
		SessionLock: opts.SessionLock,
	})
}

// buildTLSFromSpec mirrors the existing apphosting driver's behaviour:
// honours InsecureSkipVerify but does not load client certs here (the
// apphosting driver does, and the intent is for its HTTP client to be
// passed in via FactoryOptions). This fallback keeps tests and
// local-dev configurations simple.
func buildTLSFromSpec(spec *ciskov1.DeviceSpec) *tls.Config {
	if spec.TLS == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: spec.TLS.InsecureSkipVerify,
	}
}
