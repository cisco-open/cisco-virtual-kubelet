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

// Package gnoiruntime owns the per-device gNOI client and its connection-pool
// leases. Protocol encoding and certificate validation remain in package gnoi;
// process configuration and Kubernetes wiring remain at the command boundary.
package gnoiruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

const (
	defaultProvisioningRPCTimeout    = 2 * time.Minute
	defaultProvisioningRetryInterval = 2 * time.Second
)

var (
	_ gnoi.Provider      = (*Provider)(nil)
	_ gnoi.ResetProvider = (*Provider)(nil)
)

// Provider lazily constructs one gNOI client over a ClassControl lease and
// leases a separate ClassBulkTransfer connection for each bulk RPC. On a
// successful NewProvider call, ownership of pool transfers to Provider and
// Close closes it after releasing the control lease.
type Provider struct {
	// lifecycleMu prevents ResetGNOIClient or Close from releasing the
	// connection while the target-generated CSR/Load exchange is in flight.
	// Ordinary client access remains guarded by mu.
	lifecycleMu sync.RWMutex
	mu          sync.Mutex
	pool        devicegrpc.Pool
	key         devicegrpc.DeviceKey
	auth        gnoi.AuthContext

	controlLease *devicegrpc.Lease
	client       *gnoi.Client
	generation   uint64
	closed       bool
}

// NewProvider constructs a lazy per-device provider. It performs no network
// I/O; the first call to GNOIClient takes the control lease.
func NewProvider(pool devicegrpc.Pool, key devicegrpc.DeviceKey, auth gnoi.AuthContext) (*Provider, error) {
	if pool == nil {
		return nil, fmt.Errorf("gnoi runtime: pool is required")
	}
	if strings.TrimSpace(key.Address) == "" {
		return nil, fmt.Errorf("gnoi runtime: device address is required")
	}
	if key.Port <= 0 || key.Port > 65535 {
		return nil, fmt.Errorf("gnoi runtime: device port %d is outside 1-65535", key.Port)
	}
	return &Provider{pool: pool, key: key, auth: auth}, nil
}

// GNOIClient returns the current client, constructing it lazily on the first
// call. Concurrent callers share one client and one ClassControl lease.
func (p *Provider) GNOIClient(ctx context.Context) (*gnoi.Client, error) {
	if p == nil {
		return nil, fmt.Errorf("gnoi runtime: provider is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("gnoi runtime: provider is closed")
	}
	if p.client != nil {
		return p.client, nil
	}
	controlLease, err := p.pool.Lease(ctx, p.key, devicegrpc.ClassControl)
	if err != nil {
		return nil, fmt.Errorf("gnoi ClassControl lease: %w", err)
	}
	generation := p.generation
	client, err := gnoi.New(controlLease.Conn, gnoi.Options{
		Auth: p.auth,
		BulkConnProvider: func(ctx context.Context) (*grpc.ClientConn, func(), error) {
			return p.bulkConn(ctx, generation)
		},
	})
	if err != nil {
		controlLease.Release()
		return nil, fmt.Errorf("gnoi client construct: %w", err)
	}
	p.controlLease = controlLease
	p.client = client
	return client, nil
}

// ResetGNOIClient releases the current control lease. The next GNOIClient call
// creates a fresh connection and capability cache.
func (p *Provider) ResetGNOIClient(ctx context.Context) {
	if p == nil {
		return
	}
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.controlLease != nil {
		p.controlLease.Release()
		p.controlLease = nil
	}
	p.generation++
	p.client = nil
	log.G(ctx).Infof("gNOI: reset client leases for %s:%d", p.key.Address, p.key.Port)
}

// Close releases all provider-held leases and closes the owned pool. It is
// safe to call more than once.
func (p *Provider) Close() {
	if p == nil {
		return
	}
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.generation++
	if p.controlLease != nil {
		p.controlLease.Release()
		p.controlLease = nil
	}
	p.client = nil
	_ = p.pool.Close()
}

func (p *Provider) isCurrentClient(client *gnoi.Client) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.client != nil && p.client == client
}

func (p *Provider) bulkConn(ctx context.Context, generation uint64) (*grpc.ClientConn, func(), error) {
	if p == nil {
		return nil, nil, fmt.Errorf("gnoi runtime: provider is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, fmt.Errorf("gnoi runtime: provider is closed")
	}
	if generation != p.generation {
		return nil, nil, fmt.Errorf("gnoi runtime: client is stale")
	}
	lease, err := p.pool.Lease(ctx, p.key, devicegrpc.ClassBulkTransfer)
	if err != nil {
		return nil, nil, fmt.Errorf("gnoi ClassBulkTransfer lease: %w", err)
	}
	return lease.Conn, lease.Release, nil
}

// Provisioner serializes the create-only certificate installation workflow.
// It is deliberately separate from Provider so merely enabling gNOI does not
// expose certificate-install authority to write-class reconcilers.
type Provisioner struct {
	provider *Provider
	bundle   *gnoi.ProvisioningBundle
	signer   gnoi.CertificateSigner
	// timeout bounds the complete inspect, Install, reconnect, and Verify flow.
	timeout       time.Duration
	retryInterval time.Duration

	provisionMu sync.Mutex

	// Test seams default to the corresponding gnoi.Client methods.
	certificateInstalled  func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error)
	installCertificate    func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error
	clientForVerification func(context.Context) (*gnoi.Client, error)
	provisioningReady     func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (version string, ready bool, err error)
}

// NewProvisioner binds validated certificate policy and an authorized signer
// to a Provider. Callers should construct it only when write-class gNOI is
// explicitly enabled.
func NewProvisioner(provider *Provider, bundle *gnoi.ProvisioningBundle, signer gnoi.CertificateSigner) (*Provisioner, error) {
	if provider == nil {
		return nil, fmt.Errorf("gnoi runtime: provider is required for certificate provisioning")
	}
	if bundle == nil {
		return nil, fmt.Errorf("gnoi runtime: provisioning bundle is required")
	}
	if signer == nil {
		return nil, fmt.Errorf("gnoi runtime: certificate signer is required")
	}
	return &Provisioner{
		provider:      provider,
		bundle:        bundle,
		signer:        signer,
		timeout:       defaultProvisioningRPCTimeout,
		retryInterval: defaultProvisioningRetryInterval,
	}, nil
}

// ConfiguredIntent exposes the non-secret certificate identity bound to this
// provisioner so the action reconciler can reject stale requests before any
// device RPC is attempted.
func (p *Provisioner) ConfiguredIntent() (certificateID, publicMaterialSHA256 string) {
	if p == nil || p.bundle == nil {
		return "", ""
	}
	return p.bundle.CertificateID(), p.bundle.PublicMaterialSHA256()
}

// ProvisionGNOICertificate checks for a conflicting existing identity, submits
// at most one create-only certificate installation, and reconnects until both
// Cert.GetCertificates and OS.Verify prove that IOS XE is provisioned. Install
// is never retried because a transport error may follow a committed Load.
func (p *Provisioner) ProvisionGNOICertificate(ctx context.Context, client *gnoi.Client) (certificateID, version string, err error) {
	if p == nil || p.provider == nil || p.bundle == nil || p.signer == nil {
		return "", "", fmt.Errorf("gnoi certificate provisioning is not configured")
	}
	p.provisionMu.Lock()
	defer p.provisionMu.Unlock()

	timeout := p.timeout
	if timeout <= 0 {
		timeout = defaultProvisioningRPCTimeout
	}
	installedFn := p.certificateInstalled
	if installedFn == nil {
		installedFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle) (bool, error) {
			return client.ProvisioningCertificateInstalled(ctx, bundle)
		}
	}
	installFn := p.installCertificate
	if installFn == nil {
		installFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle, signer gnoi.CertificateSigner) error {
			return client.InstallProvisioningCertificate(ctx, bundle, signer)
		}
	}

	// Hold a lifecycle read lock for the complete CSR/Load transaction. A
	// software-operation reset may wait, but it cannot invalidate the stream
	// after the one-shot signer has consumed its key.
	provisioningCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p.provider.lifecycleMu.RLock()
	if !p.provider.isCurrentClient(client) {
		p.provider.lifecycleMu.RUnlock()
		return "", "", fmt.Errorf("gnoi client changed before certificate provisioning could start")
	}
	installed, inspectErr := installedFn(provisioningCtx, client, p.bundle)
	var installErr error
	if inspectErr == nil && !installed {
		installErr = installFn(provisioningCtx, client, p.bundle, p.signer)
	}
	p.provider.lifecycleMu.RUnlock()

	if inspectErr != nil {
		return "", "", fmt.Errorf("inspect IOS XE provisioning certificate: %w", inspectErr)
	}
	if installErr != nil && !gnoi.IsCertificateInstallIndeterminate(installErr) {
		return "", "", fmt.Errorf("install IOS XE provisioning certificate: %w", installErr)
	}

	version, verifyErr := p.waitUntilProvisioned(provisioningCtx, installedFn)
	if verifyErr == nil {
		return p.bundle.CertificateID(), version, nil
	}
	if installed {
		return "", "", fmt.Errorf(
			"gnoi certificate %q is installed but IOS XE did not become provisioned; verify the gNXI secure trustpoint binding: %w",
			p.bundle.CertificateID(),
			verifyErr,
		)
	}

	cause := fmt.Errorf("post-install certificate and OS.Verify checks did not converge: %w", verifyErr)
	if installErr != nil {
		cause = errors.Join(installErr, cause)
	}
	return "", "", &gnoi.ErrCertificateInstallIndeterminate{
		CertificateID: p.bundle.CertificateID(),
		Cause:         cause,
	}
}

func (p *Provisioner) waitUntilProvisioned(
	ctx context.Context,
	installedFn func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error),
) (string, error) {
	clientFn := p.clientForVerification
	if clientFn == nil {
		clientFn = p.provider.GNOIClient
	}
	readyFn := p.provisioningReady
	if readyFn == nil {
		readyFn = func(ctx context.Context, client *gnoi.Client, bundle *gnoi.ProvisioningBundle) (string, bool, error) {
			installed, err := installedFn(ctx, client, bundle)
			if err != nil || !installed {
				return "", false, err
			}
			verified, err := client.Verify(ctx)
			if err != nil {
				return "", false, err
			}
			return verified.Version, true, nil
		}
	}
	interval := p.retryInterval
	if interval <= 0 {
		interval = defaultProvisioningRetryInterval
	}

	p.provider.ResetGNOIClient(ctx)
	var lastErr error
	for {
		freshClient, err := clientFn(ctx)
		if err == nil {
			var ready bool
			var version string
			version, ready, err = readyFn(ctx, freshClient, p.bundle)
			if err == nil && ready {
				return version, nil
			}
			if err == nil {
				lastErr = fmt.Errorf("provisioning certificate %q is not visible on the target", p.bundle.CertificateID())
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}

		if err != nil && !provisioningCheckRetryable(err) {
			return "", err
		}
		// The fresh grpc.ClientConn reconnects itself across the expected gNXI
		// restart. Replace the whole gNOI client only when Unimplemented may
		// have populated its per-service unsupported cache.
		if status.Code(err) == codes.Unimplemented {
			p.provider.ResetGNOIClient(ctx)
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr == nil {
				return "", ctx.Err()
			}
			return "", errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
}

func provisioningCheckRetryable(err error) bool {
	if err == nil || gnoi.IsDeviceNotProvisioned(err) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(err) {
	case codes.Aborted, codes.Canceled, codes.DeadlineExceeded, codes.Internal, codes.Unavailable, codes.Unimplemented:
		return true
	default:
		return false
	}
}
