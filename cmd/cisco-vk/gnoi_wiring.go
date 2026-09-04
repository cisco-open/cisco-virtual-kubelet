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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

const (
	gNOIProvisioningMountPath  = "/var/run/secrets/cisco-vk/gnoi-provisioning"
	gNOIProvisioningCertFile   = "tls.crt"
	gNOIProvisioningKeyFile    = "tls.key"
	gNOIProvisioningCAKeyFile  = "ca.key"
	gNOIProvisioningCAFile     = "ca.crt"
	gNOIProvisioningRPCTimeout = 2 * time.Minute
)

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

	port, tlsEnabled, err := gnoiTransportForSpec(opts.Spec, forceInsecure)
	if err != nil {
		return nil, nil, fmt.Errorf("gNOI: invalid transport config: %w", err)
	}

	dialCfg, err := gnoiDialConfig(opts.Spec, opts.Password, tlsEnabled)
	if err != nil {
		return nil, nil, fmt.Errorf("gNOI: TLS from spec: %w", err)
	}
	provisioningBundle, err := loadGNOIProvisioningBundle(opts.Spec, tlsEnabled, dialCfg.TLSConfig, gNOIProvisioningMountPath)
	if err != nil {
		return nil, nil, fmt.Errorf("gNOI: certificate provisioning: %w", err)
	}

	pool := devicegrpc.New(dialCfg, nil)
	key := devicegrpc.DeviceKey{Address: opts.Spec.Address, Port: port}

	provider := &pooledGNOIProvider{
		pool:               pool,
		key:                key,
		address:            opts.Spec.Address,
		port:               port,
		tls:                dialCfg.TLSConfig != nil,
		provisioningBundle: provisioningBundle,
	}

	log.G(ctx).Infof("gNOI: pillar enabled (%s:%d, tls=%v, lazy_bulk=true)", opts.Spec.Address, port, dialCfg.TLSConfig != nil)
	return provider, provider.Close, nil
}

func loadGNOIProvisioningBundle(
	spec *ciskov1.DeviceSpec,
	tlsEnabled bool,
	tlsCfg *tls.Config,
	directory string,
) (*gnoi.ProvisioningBundle, error) {
	if spec == nil || spec.GNOI == nil || spec.GNOI.CertificateProvisioning == nil {
		return nil, nil
	}
	if !tlsEnabled || tlsCfg == nil {
		return nil, fmt.Errorf("TLS transport is required")
	}
	provisioning := spec.GNOI.CertificateProvisioning
	if !provisioning.ReplaceTargetCABundle {
		return nil, fmt.Errorf("replaceTargetCABundle must be true before replacing the shared gNXI/gNMI CA bundle")
	}
	leafPEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCertFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCertFile, err)
	}
	privateKeyPEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningKeyFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningKeyFile, err)
	}
	caBundlePEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCAFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCAFile, err)
	}
	caKeyPEM, caKeyErr := os.ReadFile(filepath.Join(directory, gNOIProvisioningCAKeyFile))
	if caKeyErr != nil && !errors.Is(caKeyErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCAKeyFile, caKeyErr)
	}
	var bundle *gnoi.ProvisioningBundle
	if len(caKeyPEM) > 0 {
		bundle, err = gnoi.NewProvisioningBundleWithSigningKey(
			provisioning.CertificateID,
			spec.Address,
			leafPEM,
			privateKeyPEM,
			caBundlePEM,
			caKeyPEM,
		)
	} else {
		bundle, err = gnoi.NewProvisioningBundle(
			provisioning.CertificateID,
			spec.Address,
			leafPEM,
			privateKeyPEM,
			caBundlePEM,
		)
	}
	if err != nil {
		return nil, err
	}

	// The CA installed by Certificate.Install becomes the trust anchor for the
	// restarted gNXI listener. Add it only to this gNOI client's TLS config;
	// shared RESTCONF, NETCONF and gNMI TLS settings remain untouched.
	roots := tlsCfg.RootCAs
	if roots == nil {
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	if !roots.AppendCertsFromPEM(bundle.RootCAPEM()) {
		return nil, fmt.Errorf("parse %s: no CA certificates found", gNOIProvisioningCAFile)
	}
	tlsCfg.RootCAs = roots
	return bundle, nil
}

func gnoiDialConfig(spec *ciskov1.DeviceSpec, password string, tlsEnabled bool) (devicegrpc.DialConfig, error) {
	if !tlsEnabled {
		return devicegrpc.DialConfig{}, nil
	}

	// Shared device-client helper: honours spec.tls.caFile (RootCAs)
	// and the certFile/keyFile client pair in addition to
	// InsecureSkipVerify, matching the apphosting driver.
	tlsCfg, err := tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
	if err != nil {
		return devicegrpc.DialConfig{}, err
	}
	dialCfg := devicegrpc.DialConfig{TLSConfig: tlsCfg}
	if spec.Username != "" {
		dialCfg.RPCCredentials = devicegrpc.NewIOSXEPasswordCredentials(spec.Username, password)
	}
	return dialCfg, nil
}

// gnoiTransportForSpec resolves the effective gNOI port and transport security.
// A missing per-device block intentionally uses the historical resolver without
// alteration. Once a block is present, gNOI no longer inherits DeviceSpec.Port:
// an omitted port selects the protocol default for the effective security mode.
func gnoiTransportForSpec(spec *ciskov1.DeviceSpec, forceInsecure bool) (int, bool, error) {
	if spec == nil {
		return 0, false, fmt.Errorf("nil DeviceSpec")
	}

	tlsEnabled := !forceInsecure && spec.TLS != nil && spec.TLS.Enabled
	if spec.GNOI == nil {
		return gnoiPortForSpec(spec, forceInsecure), tlsEnabled, nil
	}
	if err := spec.GNOI.Validate(); err != nil {
		return 0, false, err
	}

	switch spec.GNOI.TransportSecurity {
	case "", ciskov1.GNOITransportSecurityAuto:
		tlsEnabled = spec.TLS != nil && spec.TLS.Enabled
	case ciskov1.GNOITransportSecurityTLS:
		tlsEnabled = true
	case ciskov1.GNOITransportSecurityPlaintext:
		tlsEnabled = false
	}
	if forceInsecure {
		tlsEnabled = false
	}

	if port, ok := gnoiPortEnvOverride(); ok {
		return port, tlsEnabled, nil
	}
	if spec.GNOI.Port > 0 {
		return spec.GNOI.Port, tlsEnabled, nil
	}
	if tlsEnabled {
		return 9339, true, nil
	}
	return 50052, false, nil
}

type pooledGNOIProvider struct {
	mu      sync.Mutex
	pool    devicegrpc.Pool
	key     devicegrpc.DeviceKey
	address string
	port    int
	tls     bool

	controlLease *devicegrpc.Lease
	client       *gnoi.Client
	closed       bool

	provisionMu        sync.Mutex
	provisioningBundle *gnoi.ProvisioningBundle
	matchingObserved   bool
	provisioningActive atomic.Bool

	// Test seams default to the corresponding gnoi.Client methods.
	provisioningState              func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error)
	installProvisioningCertificate func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) error
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
	clientOpts := gnoi.Options{BulkConnProvider: p.bulkConn}
	if p.provisioningBundle != nil {
		clientOpts.OnDeviceNotProvisioned = p.provisionGNOICertificate
		clientOpts.OnOSVerifySuccess = func() { p.provisioningActive.Store(false) }
	}
	client, err := gnoi.New(controlLease.Conn, clientOpts)
	if err != nil {
		controlLease.Release()
		return nil, fmt.Errorf("gnoi client construct: %w", err)
	}
	p.controlLease = controlLease
	p.client = client
	return client, nil
}

func (p *pooledGNOIProvider) GNOICertificateProvisioningInProgress() bool {
	return p.provisioningActive.Load()
}

func (p *pooledGNOIProvider) provisionGNOICertificate(ctx context.Context, client *gnoi.Client) error {
	p.provisionMu.Lock()
	defer p.provisionMu.Unlock()
	provisioningCtx, cancel := context.WithTimeout(ctx, gNOIProvisioningRPCTimeout)
	defer cancel()

	if p.provisioningBundle == nil {
		return fmt.Errorf("gnoi certificate provisioning is not configured")
	}
	if !p.isCurrentClient(client) {
		// Another caller completed the state transition and reset this client's
		// connection while this RPC was in flight. Preserve the winner's active
		// state: a newer client may already have verified successfully while this
		// callback waited for provisionMu. The next reconcile must use the new
		// client rather than touching this stale one.
		return p.provisioningInProgress(nil)
	}

	stateFn := p.provisioningState
	if stateFn == nil {
		stateFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error) {
			return client.ProvisioningCertificateState(ctx, bundle)
		}
	}
	state, err := stateFn(provisioningCtx, client, p.provisioningBundle)
	if err != nil {
		if isTransientGNOIProvisioningError(err) {
			p.provisioningActive.Store(true)
			p.ResetGNOIClient(ctx)
			return p.provisioningInProgress(err)
		}
		p.provisioningActive.Store(false)
		return fmt.Errorf("inspect IOS XE provisioning certificate: %w", err)
	}

	switch state {
	case gnoi.ProvisioningCertificateMissing:
		installFn := p.installProvisioningCertificate
		if installFn == nil {
			installFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle) error {
				return client.InstallProvisioningCertificate(ctx, bundle)
			}
		}
		installErr := installFn(provisioningCtx, client, p.provisioningBundle)
		if installErr != nil && !gnoi.IsCertificateInstallIndeterminate(installErr) {
			if isTransientGNOIProvisioningError(installErr) {
				p.provisioningActive.Store(true)
				p.ResetGNOIClient(ctx)
				return p.provisioningInProgress(installErr)
			}
			p.provisioningActive.Store(false)
			return fmt.Errorf("install IOS XE provisioning certificate: %w", installErr)
		}
		p.matchingObserved = false
		p.provisioningActive.Store(true)
		p.ResetGNOIClient(ctx)
		return p.provisioningInProgress(installErr)

	case gnoi.ProvisioningCertificateMatching:
		if !p.matchingObserved {
			// The certificate can become visible before the restarted service has
			// bound it. Give IOS XE one fresh connection before declaring the
			// persistent not-provisioned response a binding/configuration error.
			p.matchingObserved = true
			p.provisioningActive.Store(true)
			p.ResetGNOIClient(ctx)
			return p.provisioningInProgress(nil)
		}
		p.provisioningActive.Store(false)
		return fmt.Errorf(
			"gnoi certificate %q is installed but IOS XE still reports that the device is not provisioned; verify the gNXI secure trustpoint binding",
			p.provisioningBundle.CertificateID(),
		)

	default:
		p.provisioningActive.Store(false)
		return fmt.Errorf("unexpected gnoi provisioning certificate state %q", state)
	}
}

func isTransientGNOIProvisioningError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

func (p *pooledGNOIProvider) isCurrentClient(client *gnoi.Client) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.client == client
}

func (p *pooledGNOIProvider) provisioningInProgress(cause error) error {
	return &gnoi.ErrProvisioningInProgress{
		CertificateID: p.provisioningBundle.CertificateID(),
		Cause:         cause,
	}
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
	if port, ok := gnoiPortEnvOverride(); ok {
		return port
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

func gnoiPortEnvOverride() (int, bool) {
	v := os.Getenv(gNOIPortEnv)
	if v == "" {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}
