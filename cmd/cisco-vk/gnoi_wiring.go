// Copyright © 2026 Cisco Systems, Inc.
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
	"crypto/tls"
	"os"
	"strconv"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/log"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
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

// setupGNOI builds the per-device gRPC pool, leases the ClassControl
// and ClassBulkTransfer connections, and assembles a *gnoi.Client.
// The returned cleanup function releases both leases and closes the
// pool; it is invoked by the caller when the surrounding ctx is done.
//
// Returns (nil, nil) when:
//   - The device spec is missing the address (defensive — usually
//     caught earlier in startup).
//   - Operators have set CISCO_VK_GNOI_DISABLED=1 to opt out.
//
// A nil GNOIProvider signals to the reconcilers that the gNOI
// dispatch path is unavailable; they fail fast with reason
// GNOIUnsupported on any CR they receive.
func setupGNOI(ctx context.Context, opts configReconcilerOptions) (*staticGNOIProvider, func()) {
	if v := os.Getenv(gNOIDisabledEnv); v == "1" || strings.EqualFold(v, "true") {
		log.G(ctx).Info("gNOI pillar disabled by CISCO_VK_GNOI_DISABLED")
		return nil, nil
	}
	if opts.Spec == nil || opts.Spec.Address == "" {
		return nil, nil
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
		dialCfg.TLSConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: opts.Spec.TLS.InsecureSkipVerify,
		}
	}

	pool := devicegrpc.New(dialCfg, nil)
	key := devicegrpc.DeviceKey{Address: opts.Spec.Address, Port: port}

	// Lease the two conns we hold for the lifetime of the VK pod.
	// Bulk transfer is leased eagerly so OS.Install / File.Put can
	// stream into it without re-dialing; the gRPC layer holds the
	// underlying TCP connection idle until first use, so the cost
	// of a held-but-unused lease is just a map entry.
	controlLease, err := pool.Lease(ctx, key, devicegrpc.ClassControl)
	if err != nil {
		log.G(ctx).WithError(err).Warn("gNOI: ClassControl lease failed; gNOI pillar disabled for this device")
		_ = pool.Close()
		return nil, nil
	}
	bulkLease, err := pool.Lease(ctx, key, devicegrpc.ClassBulkTransfer)
	if err != nil {
		controlLease.Release()
		_ = pool.Close()
		log.G(ctx).WithError(err).Warn("gNOI: ClassBulkTransfer lease failed; gNOI pillar disabled for this device")
		return nil, nil
	}

	client, err := gnoi.New(controlLease.Conn, gnoi.Options{
		Auth:     dialCfg.AuthContext(),
		BulkConn: bulkLease.Conn,
	})
	if err != nil {
		controlLease.Release()
		bulkLease.Release()
		_ = pool.Close()
		log.G(ctx).WithError(err).Warn("gNOI: client construct failed; gNOI pillar disabled for this device")
		return nil, nil
	}

	log.G(ctx).Infof("gNOI: pillar enabled (%s:%d, tls=%v)", opts.Spec.Address, port, dialCfg.TLSConfig != nil)

	cleanup := func() {
		controlLease.Release()
		bulkLease.Release()
		_ = pool.Close()
	}
	return &staticGNOIProvider{c: client}, cleanup
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
