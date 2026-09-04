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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

// gNOIInsecureEnv forces legacy configurations to use the insecure gNXI
// listener (typically port 50052), bypassing spec.tls.enabled inference. It
// cannot override an explicit gnoi.transportSecurity=tls contract.
const gNOIInsecureEnv = "CISCO_VK_GNOI_INSECURE"

const (
	gNOIProvisioningMountPath     = "/var/run/secrets/cisco-vk/gnoi-provisioning"
	gNOIProvisioningCertFile      = "tls.crt"
	gNOIProvisioningCAKeyFile     = "ca.key"
	gNOIProvisioningCAFile        = "ca.crt"
	gNOIProvisioningBootstrapFile = "bootstrap.crt"
	gNOIProvisioningRPCTimeout    = 2 * time.Minute
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
	provisioningBundle, err := loadGNOIProvisioningBundle(
		opts.Spec,
		tlsEnabled,
		dialCfg.TLSConfig,
		gNOIProvisioningMountPath,
		opts.EnableWriteClassGNOI,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("gNOI: certificate provisioning: %w", err)
	}

	pool := devicegrpc.New(dialCfg, nil)
	key := devicegrpc.DeviceKey{Address: opts.Spec.Address, Port: port}

	pooled := &pooledGNOIProvider{
		pool:    pool,
		key:     key,
		auth:    dialCfg.AuthContext(),
		address: opts.Spec.Address,
		port:    port,
	}
	var provider gnoi.Provider = pooled
	if provisioningBundle != nil && opts.EnableWriteClassGNOI {
		provider = &provisioningGNOIProvider{
			pooledGNOIProvider: pooled,
			bundle:             provisioningBundle,
		}
	}

	log.G(ctx).Infof("gNOI: pillar enabled (%s:%d, tls=%v, lazy_bulk=true)", opts.Spec.Address, port, dialCfg.TLSConfig != nil)
	return provider, pooled.Close, nil
}

func loadGNOIProvisioningBundle(
	spec *ciskov1.DeviceSpec,
	tlsEnabled bool,
	tlsCfg *tls.Config,
	directory string,
	provisioningWritesEnabled bool,
) (*gnoi.ProvisioningBundle, error) {
	if spec == nil || spec.GNOI == nil || spec.GNOI.CertificateProvisioning == nil {
		return nil, nil
	}
	if !tlsEnabled || tlsCfg == nil {
		return nil, fmt.Errorf("TLS transport is required")
	}
	if tlsCfg.InsecureSkipVerify {
		return nil, fmt.Errorf("verified TLS is required; insecureSkipVerify cannot be used with certificate provisioning")
	}
	provisioning := spec.GNOI.CertificateProvisioning
	if !provisioning.ReplaceTargetCABundle {
		return nil, fmt.Errorf("replaceTargetCABundle must be true before replacing the shared gNXI/gNMI CA bundle")
	}
	leafPEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCertFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCertFile, err)
	}
	caBundlePEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCAFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCAFile, err)
	}
	var bootstrapPEM []byte
	var caKeyPEM []byte
	if provisioningWritesEnabled {
		bootstrapPEM, err = readOptionalFile(filepath.Join(directory, gNOIProvisioningBootstrapFile))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", gNOIProvisioningBootstrapFile, err)
		}
		caKeyPEM, err = readOptionalFile(filepath.Join(directory, gNOIProvisioningCAKeyFile))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCAKeyFile, err)
		}
		defer clear(caKeyPEM)
	}
	bundle, err := gnoi.NewProvisioningBundle(
		provisioning.CertificateID,
		spec.Address,
		leafPEM,
		caBundlePEM,
		caKeyPEM,
	)
	if err != nil {
		return nil, err
	}

	if err := bundle.ConfigureClientTLS(tlsCfg, bootstrapPEM); err != nil {
		return nil, err
	}
	return bundle, nil
}

func readOptionalFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func gnoiDialConfig(spec *ciskov1.DeviceSpec, password string, tlsEnabled bool) (devicegrpc.DialConfig, error) {
	dialCfg := devicegrpc.DialConfig{}
	if !explicitSecureGNOI(spec) {
		dialCfg.Username = spec.Username
		dialCfg.Password = password
	}
	if !tlsEnabled {
		return dialCfg, nil
	}

	// Shared device-client helper: honours spec.tls.caFile (RootCAs)
	// and the certFile/keyFile client pair in addition to
	// InsecureSkipVerify, matching the apphosting driver.
	tlsCfg, err := tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
	if err != nil {
		return devicegrpc.DialConfig{}, err
	}
	dialCfg.TLSConfig = tlsCfg
	if explicitSecureGNOI(spec) {
		if tlsCfg.InsecureSkipVerify {
			return devicegrpc.DialConfig{}, fmt.Errorf("verified TLS is required for explicit secure gNOI; insecureSkipVerify is not permitted")
		}
		if spec.Username != "" && password != "" {
			dialCfg.RPCCredentials = devicegrpc.NewIOSXEPasswordCredentials(spec.Username, password)
		}
	}
	return dialCfg, nil
}

func explicitSecureGNOI(spec *ciskov1.DeviceSpec) bool {
	return spec != nil && spec.GNOI != nil && spec.GNOI.TransportSecurity == ciskov1.GNOITransportSecurityTLS
}

type unavailableGNOIProvider struct {
	cause error
}

func (p unavailableGNOIProvider) GNOIClient(context.Context) (*gnoi.Client, error) {
	return nil, fmt.Errorf("gNOI unavailable: %w", p.cause)
}

// gnoiTransportForSpec resolves the effective gNOI port and transport security.
// A missing or zero-valued per-device block uses the historical resolver.
// Explicit fields affect only the gNOI connection.
func gnoiTransportForSpec(spec *ciskov1.DeviceSpec, forceInsecure bool) (int, bool, error) {
	if spec == nil {
		return 0, false, fmt.Errorf("nil DeviceSpec")
	}

	if err := spec.GNOI.Validate(); err != nil {
		return 0, false, err
	}
	if forceInsecure && explicitSecureGNOI(spec) {
		return 0, false, fmt.Errorf("CISCO_VK_GNOI_INSECURE cannot override explicit gnoi.transportSecurity=tls")
	}
	sharedTLS := spec.TLS != nil && spec.TLS.Enabled
	if port, ok := gnoiPortEnvOverride(); ok {
		return port, !forceInsecure && effectiveGNOITLS(spec.GNOI, sharedTLS), nil
	}
	if forceInsecure {
		// The legacy override selects the legacy listener as well as plaintext.
		// A custom plaintext port remains available through CISCO_VK_GNOI_PORT.
		return inferredGNOIPort(spec.Port, false), false, nil
	}
	if spec.GNOI == nil || (spec.GNOI.Port == 0 && (spec.GNOI.TransportSecurity == "" || spec.GNOI.TransportSecurity == ciskov1.GNOITransportSecurityAuto)) {
		return inferredGNOIPort(spec.Port, sharedTLS), sharedTLS, nil
	}

	tlsEnabled := effectiveGNOITLS(spec.GNOI, sharedTLS)
	if spec.GNOI.Port > 0 {
		return spec.GNOI.Port, tlsEnabled, nil
	}
	if tlsEnabled {
		return 9339, true, nil
	}
	return 50052, false, nil
}

func effectiveGNOITLS(config *ciskov1.GNOIConfig, sharedTLS bool) bool {
	return sharedTLS || (config != nil && config.TransportSecurity == ciskov1.GNOITransportSecurityTLS)
}

type pooledGNOIProvider struct {
	mu      sync.Mutex
	pool    devicegrpc.Pool
	key     devicegrpc.DeviceKey
	auth    gnoi.AuthContext
	address string
	port    int

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
	client, err := gnoi.New(controlLease.Conn, gnoi.Options{Auth: p.auth, BulkConnProvider: p.bulkConn})
	if err != nil {
		controlLease.Release()
		return nil, fmt.Errorf("gnoi client construct: %w", err)
	}
	p.controlLease = controlLease
	p.client = client
	return client, nil
}

// provisioningGNOIProvider is returned only for an explicit certificate
// provisioning block. Keeping this capability off the base provider limits
// certificate installation to the explicit write-class action.
type provisioningGNOIProvider struct {
	*pooledGNOIProvider

	provisionMu sync.Mutex
	bundle      *gnoi.ProvisioningBundle

	// Test seams default to the corresponding gnoi.Client methods.
	certificateInstalled func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error)
	installCertificate   func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) error
}

func (p *provisioningGNOIProvider) ProvisionGNOICertificate(ctx context.Context, client *gnoi.Client) (string, error) {
	p.provisionMu.Lock()
	defer p.provisionMu.Unlock()
	provisioningCtx, cancel := context.WithTimeout(ctx, gNOIProvisioningRPCTimeout)
	defer cancel()

	if p.bundle == nil {
		return "", fmt.Errorf("gnoi certificate provisioning is not configured")
	}
	if !p.isCurrentClient(client) {
		return "", fmt.Errorf("gnoi client changed before certificate provisioning could start")
	}

	installedFn := p.certificateInstalled
	if installedFn == nil {
		installedFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle) (bool, error) {
			return client.ProvisioningCertificateInstalled(ctx, bundle)
		}
	}
	installed, err := installedFn(provisioningCtx, client, p.bundle)
	if err != nil {
		return "", fmt.Errorf("inspect IOS XE provisioning certificate: %w", err)
	}

	if installed {
		return "", fmt.Errorf(
			"gnoi certificate %q is installed but IOS XE still reports that the device is not provisioned; verify the gNXI secure trustpoint binding",
			p.bundle.CertificateID(),
		)
	}
	installFn := p.installCertificate
	if installFn == nil {
		installFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle) error {
			return client.InstallProvisioningCertificate(ctx, bundle)
		}
	}
	if err := installFn(provisioningCtx, client, p.bundle); err != nil {
		if gnoi.IsCertificateInstallIndeterminate(err) {
			p.ResetGNOIClient(ctx)
		}
		return "", fmt.Errorf("install IOS XE provisioning certificate: %w", err)
	}
	p.ResetGNOIClient(ctx)
	return p.bundle.CertificateID(), nil
}

func (p *pooledGNOIProvider) isCurrentClient(client *gnoi.Client) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.client == client
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

func inferredGNOIPort(port int, tlsEnabled bool) int {
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
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}
