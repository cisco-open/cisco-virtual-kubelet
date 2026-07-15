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

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"google.golang.org/grpc"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
)

// gNOIDisabledEnv lets operators force-disable the gNOI pillar even
// when the binary has it linked in. Useful for incremental rollout —
// matches the CISCO_VK_TELEMETRY_INSECURE escape-hatch pattern.
const gNOIDisabledEnv = "CISCO_VK_GNOI_DISABLED"

// gNOIPortEnv overrides the device-side gNOI listener port. Defaults
// follow the same secure/insecure heuristic the gNMI transport uses.
const gNOIPortEnv = "CISCO_VK_GNOI_PORT"

// gNOIInsecureEnv forces the gNOI dial to the device's insecure gnxi
// listener (typically port 50052), bypassing the spec.tls.enabled
// inference. Mirrors CISCO_VK_TELEMETRY_INSECURE — useful when the
// RESTCONF transport uses TLS (port 443) but gNOI is bound to the
// `gnxi server` (insecure) line rather than `gnxi secure-server`.
const gNOIInsecureEnv = "CISCO_VK_GNOI_INSECURE"

// setupGNOI builds the per-device gRPC pool and returns a lazy,
// resettable gNOI provider. The provider leases ClassControl on first
// use and ClassBulkTransfer only for the duration of File.Get/Put or
// OS.Install streams. The returned cleanup function releases leases
// and closes the pool when the surrounding ctx is done.
//
// Returns (nil, nil, nil) when:
//   - The device spec is missing the address (defensive — usually
//     caught earlier in startup).
//   - Operators have set CISCO_VK_GNOI_DISABLED=1 to opt out.
//
// A nil gnoi.Provider signals to the reconcilers that the gNOI
// dispatch path is unavailable; they fail fast with reason
// GNOIUnsupported on any CR they receive.
func setupGNOI(ctx context.Context, opts configReconcilerOptions) (gnoi.Provider, func(), error) {
	if v := os.Getenv(gNOIDisabledEnv); v == "1" || strings.EqualFold(v, "true") {
		log.G(ctx).Info("gNOI pillar disabled by CISCO_VK_GNOI_DISABLED")
		return nil, nil, nil
	}
	if opts.Spec == nil || opts.Spec.Address == "" {
		return nil, nil, nil
	}

	forceInsecure := false
	if v := os.Getenv(gNOIInsecureEnv); v == "1" || strings.EqualFold(v, "true") {
		forceInsecure = true
	}

	port := gnoiPortForSpec(opts.Spec, forceInsecure)

	dialCfg := devicegrpc.DialConfig{
		Username: opts.Spec.Username,
		Password: opts.Password,
	}
	if !forceInsecure && opts.Spec.TLS != nil && opts.Spec.TLS.Enabled {
		// Shared device-client helper: honours spec.tls.caFile (RootCAs)
		// and the certFile/keyFile client pair in addition to
		// InsecureSkipVerify, matching the apphosting driver.
		tlsCfg, err := tlsutil.ClientTLSFromDeviceTLS(opts.Spec.TLS)
		if err != nil {
			return nil, nil, fmt.Errorf("gNOI: TLS from spec: %w", err)
		}
		dialCfg.TLSConfig = tlsCfg
	}

	pool := devicegrpc.New(dialCfg, nil)
	key := devicegrpc.DeviceKey{Address: opts.Spec.Address, Port: port}

	provider := &pooledGNOIProvider{
		pool:    pool,
		key:     key,
		auth:    dialCfg.AuthContext(),
		address: opts.Spec.Address,
		port:    port,
		tls:     dialCfg.TLSConfig != nil,
	}

	log.G(ctx).Infof("gNOI: pillar enabled (%s:%d, tls=%v, lazy_bulk=true)", opts.Spec.Address, port, dialCfg.TLSConfig != nil)
	return provider, provider.Close, nil
}

type pooledGNOIProvider struct {
	mu      sync.Mutex
	pool    devicegrpc.Pool
	key     devicegrpc.DeviceKey
	auth    gnoi.AuthContext
	address string
	port    int
	tls     bool

	controlLease *devicegrpc.Lease
	client       *gnoi.Client
	closed       bool
}

func (p *pooledGNOIProvider) GNOIClient(ctx context.Context) (*gnoi.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("gnoi provider closed")
	}
	if p.client != nil {
		return p.client, nil
	}
	controlLease, err := p.pool.Lease(ctx, p.key, devicegrpc.ClassControl)
	if err != nil {
		return nil, fmt.Errorf("gnoi ClassControl lease: %w", err)
	}
	client, err := gnoi.New(controlLease.Conn, gnoi.Options{
		Auth:             p.auth,
		BulkConnProvider: p.bulkConn,
	})
	if err != nil {
		controlLease.Release()
		return nil, fmt.Errorf("gnoi client construct: %w", err)
	}
	p.controlLease = controlLease
	p.client = client
	return client, nil
}

func (p *pooledGNOIProvider) ResetGNOIClient(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.controlLease != nil {
		p.controlLease.Release()
		p.controlLease = nil
	}
	p.client = nil
	log.G(ctx).Infof("gNOI: reset client leases for %s:%d", p.address, p.port)
}

func (p *pooledGNOIProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.controlLease != nil {
		p.controlLease.Release()
		p.controlLease = nil
	}
	p.client = nil
	_ = p.pool.Close()
}

func (p *pooledGNOIProvider) bulkConn(ctx context.Context) (*grpc.ClientConn, func(), error) {
	lease, err := p.pool.Lease(ctx, p.key, devicegrpc.ClassBulkTransfer)
	if err != nil {
		return nil, nil, fmt.Errorf("gnoi ClassBulkTransfer lease: %w", err)
	}
	return lease.Conn, lease.Release, nil
}

// gnoiPortForSpec picks the device-side gNOI port. Same heuristic the
// telemetry factory uses: insecure path → 50052, secure → 9339. The
// CISCO_VK_GNOI_PORT env var pins an explicit port for operators on
// non-standard gnxi listeners; forceInsecure overrides spec.tls.enabled
// inference so operators can target the `gnxi server` (insecure) line
// even when RESTCONF on the same device uses TLS.
func gnoiPortForSpec(spec *ciskov1.DeviceSpec, forceInsecure bool) int {
	if v := os.Getenv(gNOIPortEnv); v != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 {
			return p
		}
	}
	tlsEnabled := !forceInsecure && spec.TLS != nil && spec.TLS.Enabled
	port := spec.Port
	if port == 0 || port == 80 || port == 443 {
		if tlsEnabled {
			return 9339
		}
		return 50052
	}
	return port
}
