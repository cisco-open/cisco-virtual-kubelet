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

package gnoiruntime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

type recordingPool struct {
	mu         sync.Mutex
	conn       *grpc.ClientConn
	leaseErr   error
	classes    []devicegrpc.WorkloadClass
	closeCalls int
}

func (p *recordingPool) Lease(_ context.Context, _ devicegrpc.DeviceKey, class devicegrpc.WorkloadClass) (*devicegrpc.Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.classes = append(p.classes, class)
	if p.leaseErr != nil {
		return nil, p.leaseErr
	}
	return &devicegrpc.Lease{Conn: p.conn}, nil
}

func (p *recordingPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeCalls++
	return nil
}

func (p *recordingPool) snapshot() ([]devicegrpc.WorkloadClass, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]devicegrpc.WorkloadClass(nil), p.classes...), p.closeCalls
}

func newTestProvider(t *testing.T) (*Provider, *recordingPool) {
	t.Helper()
	conn, err := grpc.NewClient(
		"passthrough:///gnoiruntime-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("new test connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	pool := &recordingPool{conn: conn}
	provider, err := NewProvider(pool, devicegrpc.DeviceKey{Address: "192.0.2.1", Port: 9339}, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(provider.Close)
	return provider, pool
}

func TestNewProviderValidatesDependencies(t *testing.T) {
	validKey := devicegrpc.DeviceKey{Address: "192.0.2.1", Port: 9339}
	tests := []struct {
		name string
		pool devicegrpc.Pool
		key  devicegrpc.DeviceKey
		want string
	}{
		{name: "nil pool", key: validKey, want: "pool is required"},
		{name: "empty address", pool: &recordingPool{}, key: devicegrpc.DeviceKey{Port: 9339}, want: "address is required"},
		{name: "zero port", pool: &recordingPool{}, key: devicegrpc.DeviceKey{Address: "192.0.2.1"}, want: "outside 1-65535"},
		{name: "oversized port", pool: &recordingPool{}, key: devicegrpc.DeviceKey{Address: "192.0.2.1", Port: 65536}, want: "outside 1-65535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewProvider(tt.pool, tt.key, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewProvider() provider=%v error=%v, want %q", provider, err, tt.want)
			}
		})
	}
}

func TestProviderLazilySharesControlClient(t *testing.T) {
	provider, pool := newTestProvider(t)

	const callers = 16
	clients := make(chan *gnoi.Client, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := provider.GNOIClient(context.Background())
			clients <- client
			errs <- err
		}()
	}
	wg.Wait()
	close(clients)
	close(errs)

	var first *gnoi.Client
	for err := range errs {
		if err != nil {
			t.Fatalf("GNOIClient: %v", err)
		}
	}
	for client := range clients {
		if first == nil {
			first = client
		}
		if client != first {
			t.Fatal("concurrent callers received different clients")
		}
	}
	classes, _ := pool.snapshot()
	if len(classes) != 1 || classes[0] != devicegrpc.ClassControl {
		t.Fatalf("lease classes=%v, want one ClassControl lease", classes)
	}
}

func TestProviderResetReleasesCurrentClientAndReacquires(t *testing.T) {
	provider, pool := newTestProvider(t)
	first, err := provider.GNOIClient(context.Background())
	if err != nil {
		t.Fatalf("first GNOIClient: %v", err)
	}
	provider.ResetGNOIClient(context.Background())
	second, err := provider.GNOIClient(context.Background())
	if err != nil {
		t.Fatalf("second GNOIClient: %v", err)
	}
	if first == second {
		t.Fatal("reset retained the old gNOI client")
	}
	classes, _ := pool.snapshot()
	if len(classes) != 2 || classes[0] != devicegrpc.ClassControl || classes[1] != devicegrpc.ClassControl {
		t.Fatalf("lease classes=%v, want two ClassControl leases", classes)
	}
}

func TestProviderUsesBulkTransferLease(t *testing.T) {
	provider, pool := newTestProvider(t)
	conn, release, err := provider.bulkConn(context.Background(), provider.generation)
	if err != nil {
		t.Fatalf("bulkConn: %v", err)
	}
	if conn == nil || release == nil {
		t.Fatalf("bulkConn returned conn=%v release=%v", conn, release != nil)
	}
	release()
	classes, _ := pool.snapshot()
	if len(classes) != 1 || classes[0] != devicegrpc.ClassBulkTransfer {
		t.Fatalf("lease classes=%v, want one ClassBulkTransfer lease", classes)
	}
}

func TestProviderRejectsBulkLeaseFromStaleClient(t *testing.T) {
	provider, pool := newTestProvider(t)
	generation := provider.generation
	provider.ResetGNOIClient(context.Background())

	if _, _, err := provider.bulkConn(context.Background(), generation); err == nil || !strings.Contains(err.Error(), "client is stale") {
		t.Fatalf("bulkConn after reset error=%v, want stale-client rejection", err)
	}
	classes, _ := pool.snapshot()
	if len(classes) != 0 {
		t.Fatalf("stale client acquired leases: %v", classes)
	}
}

func TestProviderRejectsBulkLeaseAfterClose(t *testing.T) {
	provider, pool := newTestProvider(t)
	generation := provider.generation
	provider.Close()

	if _, _, err := provider.bulkConn(context.Background(), generation); err == nil || !strings.Contains(err.Error(), "provider is closed") {
		t.Fatalf("bulkConn after close error=%v, want closed-provider rejection", err)
	}
	classes, _ := pool.snapshot()
	if len(classes) != 0 {
		t.Fatalf("closed provider acquired leases: %v", classes)
	}
}

func TestProviderPropagatesLeaseError(t *testing.T) {
	provider, pool := newTestProvider(t)
	pool.leaseErr = errors.New("dial refused")
	if _, err := provider.GNOIClient(context.Background()); err == nil || !strings.Contains(err.Error(), "ClassControl lease: dial refused") {
		t.Fatalf("GNOIClient error=%v, want lease cause", err)
	}
}

func TestProviderCloseIsIdempotentAndPreventsReuse(t *testing.T) {
	provider, pool := newTestProvider(t)
	if _, err := provider.GNOIClient(context.Background()); err != nil {
		t.Fatalf("GNOIClient: %v", err)
	}
	provider.Close()
	provider.Close()
	if _, err := provider.GNOIClient(context.Background()); err == nil || !strings.Contains(err.Error(), "provider is closed") {
		t.Fatalf("GNOIClient after Close error=%v", err)
	}
	_, closeCalls := pool.snapshot()
	if closeCalls != 1 {
		t.Fatalf("pool Close calls=%d, want 1", closeCalls)
	}
}

type testSigner struct{}

func (testSigner) SignCSR(context.Context, []byte) ([]byte, error) { return nil, nil }

type certificateProvisioner interface {
	ConfiguredIntent() (certificateID, publicMaterialSHA256 string)
	ProvisionGNOICertificate(context.Context, *gnoi.Client) (certificateID, version string, err error)
}

func TestProvisioningCapabilityIsSeparateFromProvider(t *testing.T) {
	provider, _ := newTestProvider(t)
	if _, ok := any(provider).(certificateProvisioner); ok {
		t.Fatal("base provider unexpectedly exposes certificate provisioning")
	}
	bundle := testProvisioningBundle(t)
	provisioner, err := NewProvisioner(provider, bundle, testSigner{})
	if err != nil {
		t.Fatalf("NewProvisioner: %v", err)
	}
	if _, ok := any(provisioner).(certificateProvisioner); !ok {
		t.Fatal("Provisioner does not expose certificate provisioning")
	}
	if id, digest := provisioner.ConfiguredIntent(); id != bundle.CertificateID() || digest != bundle.PublicMaterialSHA256() {
		t.Fatalf("ConfiguredIntent()=(%q,%q), want (%q,%q)", id, digest, bundle.CertificateID(), bundle.PublicMaterialSHA256())
	}
}

func TestNewProvisionerValidatesDependencies(t *testing.T) {
	provider, _ := newTestProvider(t)
	bundle := testProvisioningBundle(t)
	signer := testSigner{}
	tests := []struct {
		name     string
		provider *Provider
		bundle   *gnoi.ProvisioningBundle
		signer   gnoi.CertificateSigner
		want     string
	}{
		{name: "nil provider", bundle: bundle, signer: signer, want: "provider is required"},
		{name: "nil bundle", provider: provider, signer: signer, want: "bundle is required"},
		{name: "nil signer", provider: provider, bundle: bundle, want: "signer is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provisioner, err := NewProvisioner(tt.provider, tt.bundle, tt.signer)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewProvisioner() provisioner=%v error=%v, want %q", provisioner, err, tt.want)
			}
		})
	}
}

func TestProvisionerInstallsOnceAndReturnsVerifiedVersion(t *testing.T) {
	provisioner, provider, current, bundle := newProvisionerHarness(t)
	fresh := &gnoi.Client{}
	var inspectCalls atomic.Int32
	var installCalls atomic.Int32
	var clientCalls atomic.Int32
	var readyCalls atomic.Int32
	provisioner.certificateInstalled = func(_ context.Context, gotClient *gnoi.Client, gotBundle *gnoi.ProvisioningBundle) (bool, error) {
		inspectCalls.Add(1)
		if gotClient != current || gotBundle != bundle {
			t.Error("initial inspection did not receive the current client and configured bundle")
		}
		return false, nil
	}
	provisioner.installCertificate = func(_ context.Context, gotClient *gnoi.Client, gotBundle *gnoi.ProvisioningBundle, signer gnoi.CertificateSigner) error {
		installCalls.Add(1)
		if gotClient != current || gotBundle != bundle || signer == nil {
			t.Error("install did not receive the current client, bundle, and signer")
		}
		return nil
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		clientCalls.Add(1)
		return fresh, nil
	}
	provisioner.provisioningReady = func(_ context.Context, gotClient *gnoi.Client, gotBundle *gnoi.ProvisioningBundle) (string, bool, error) {
		readyCalls.Add(1)
		if gotClient != fresh || gotBundle != bundle {
			t.Error("convergence check did not use the fresh client and configured bundle")
		}
		return "17.18.04", true, nil
	}

	certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
	if err != nil {
		t.Fatalf("ProvisionGNOICertificate: %v", err)
	}
	if certificateID != bundle.CertificateID() || version != "17.18.04" {
		t.Fatalf("result certificateID=%q version=%q, want %q and 17.18.04", certificateID, version, bundle.CertificateID())
	}
	if inspectCalls.Load() != 1 || installCalls.Load() != 1 || clientCalls.Load() != 1 || readyCalls.Load() != 1 {
		t.Fatalf(
			"calls inspect=%d install=%d client=%d ready=%d, want one each",
			inspectCalls.Load(), installCalls.Load(), clientCalls.Load(), readyCalls.Load(),
		)
	}
	if provider.client != nil {
		t.Fatal("provider retained the pre-install client after convergence")
	}
}

func TestProvisionerRejectsStaleClient(t *testing.T) {
	provisioner, provider, current, _ := newProvisionerHarness(t)
	provisioner.certificateInstalled = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error) {
		t.Fatal("stale client must not inspect certificate state")
		return false, nil
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		t.Fatal("stale client must not start convergence")
		return nil, nil
	}

	certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), &gnoi.Client{})
	if err == nil || !strings.Contains(err.Error(), "client changed") {
		t.Fatalf("ProvisionGNOICertificate result=(%q,%q) error=%v, want stale-client rejection", certificateID, version, err)
	}
	if certificateID != "" || version != "" {
		t.Fatalf("stale-client result certificateID=%q version=%q, want empty", certificateID, version)
	}
	if provider.client != current {
		t.Fatal("stale call changed the current client")
	}
}

func TestProvisionerReportsInstalledButUnboundCertificate(t *testing.T) {
	provisioner, provider, current, _ := newProvisionerHarness(t)
	var installCalls atomic.Int32
	provisioner.certificateInstalled = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error) {
		return true, nil
	}
	provisioner.installCertificate = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error {
		installCalls.Add(1)
		return nil
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		return &gnoi.Client{}, nil
	}
	provisioner.provisioningReady = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (string, bool, error) {
		return "", false, status.Error(codes.PermissionDenied, "trustpoint is not bound")
	}

	certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
	if err == nil || !strings.Contains(err.Error(), "secure trustpoint binding") || !strings.Contains(err.Error(), "trustpoint is not bound") {
		t.Fatalf("ProvisionGNOICertificate result=(%q,%q) error=%v, want binding error", certificateID, version, err)
	}
	if gnoi.IsCertificateInstallIndeterminate(err) {
		t.Fatalf("installed-but-unbound error unexpectedly marked indeterminate: %v", err)
	}
	if installCalls.Load() != 0 {
		t.Fatalf("matching certificate was reinstalled %d times", installCalls.Load())
	}
	if provider.client != nil {
		t.Fatal("convergence check did not discard the original client")
	}
}

func TestProvisionerDeterminateInstallFailureDoesNotReset(t *testing.T) {
	provisioner, provider, current, _ := newProvisionerHarness(t)
	determinate := status.Error(codes.PermissionDenied, "certificate rejected")
	var installCalls atomic.Int32
	provisioner.certificateInstalled = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error) {
		return false, nil
	}
	provisioner.installCertificate = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error {
		installCalls.Add(1)
		return determinate
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		t.Fatal("determinate install failure must not start convergence")
		return nil, nil
	}

	certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
	if err == nil || !errors.Is(err, determinate) {
		t.Fatalf("ProvisionGNOICertificate result=(%q,%q) error=%v, want determinate cause", certificateID, version, err)
	}
	if gnoi.IsCertificateInstallIndeterminate(err) {
		t.Fatalf("determinate error unexpectedly marked indeterminate: %v", err)
	}
	if installCalls.Load() != 1 {
		t.Fatalf("install calls=%d, want 1", installCalls.Load())
	}
	if provider.client != current || provider.generation != 0 {
		t.Fatalf("determinate failure reset provider: client=%p generation=%d", provider.client, provider.generation)
	}
}

func TestProvisionerIndeterminateInstallConvergesWithoutSecondInstall(t *testing.T) {
	provisioner, _, current, bundle := newProvisionerHarness(t)
	indeterminate := &gnoi.ErrCertificateInstallIndeterminate{
		CertificateID: bundle.CertificateID(),
		Cause:         status.Error(codes.Unavailable, "gNXI restarted"),
	}
	var installCalls atomic.Int32
	var readyCalls atomic.Int32
	provisioner.certificateInstalled = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error) {
		return false, nil
	}
	provisioner.installCertificate = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error {
		installCalls.Add(1)
		return indeterminate
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		return &gnoi.Client{}, nil
	}
	provisioner.provisioningReady = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (string, bool, error) {
		readyCalls.Add(1)
		return "17.18.04", true, nil
	}

	certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
	if err != nil {
		t.Fatalf("ProvisionGNOICertificate: %v", err)
	}
	if certificateID != bundle.CertificateID() || version != "17.18.04" {
		t.Fatalf("result certificateID=%q version=%q, want %q and 17.18.04", certificateID, version, bundle.CertificateID())
	}
	if installCalls.Load() != 1 || readyCalls.Load() != 1 {
		t.Fatalf("calls install=%d ready=%d, want one each", installCalls.Load(), readyCalls.Load())
	}
}

func TestProvisionerPostInstallTimeoutIsTypedIndeterminate(t *testing.T) {
	provisioner, provider, current, bundle := newProvisionerHarness(t)
	provisioner.timeout = 25 * time.Millisecond
	provisioner.retryInterval = time.Hour
	var installCalls atomic.Int32
	var readyCalls atomic.Int32
	provisioner.certificateInstalled = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error) {
		return false, nil
	}
	provisioner.installCertificate = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error {
		installCalls.Add(1)
		return nil
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		return &gnoi.Client{}, nil
	}
	provisioner.provisioningReady = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (string, bool, error) {
		readyCalls.Add(1)
		return "", false, nil
	}

	certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
	if certificateID != "" || version != "" {
		t.Fatalf("timeout result certificateID=%q version=%q, want empty", certificateID, version)
	}
	if !gnoi.IsCertificateInstallIndeterminate(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProvisionGNOICertificate error=%v, want typed indeterminate wrapping DeadlineExceeded", err)
	}
	var typed *gnoi.ErrCertificateInstallIndeterminate
	if !errors.As(err, &typed) || typed.CertificateID != bundle.CertificateID() {
		t.Fatalf("indeterminate error=%#v, want certificate ID %q", typed, bundle.CertificateID())
	}
	if installCalls.Load() != 1 || readyCalls.Load() != 1 {
		t.Fatalf("calls install=%d ready=%d, want one each", installCalls.Load(), readyCalls.Load())
	}
	if provider.generation != 1 {
		t.Fatalf("provider generation=%d, want one post-install reset", provider.generation)
	}
}

func TestProvisionerResetIsBlockedDuringInstall(t *testing.T) {
	provisioner, provider, current, _ := newProvisionerHarness(t)
	provisioner.timeout = time.Second
	installEntered := make(chan struct{})
	releaseInstall := make(chan struct{})
	provisioner.certificateInstalled = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (bool, error) {
		return false, nil
	}
	provisioner.installCertificate = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error {
		close(installEntered)
		<-releaseInstall
		return nil
	}
	provisioner.clientForVerification = func(context.Context) (*gnoi.Client, error) {
		return &gnoi.Client{}, nil
	}
	provisioner.provisioningReady = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (string, bool, error) {
		return "17.18.04", true, nil
	}

	type provisionResult struct {
		certificateID string
		version       string
		err           error
	}
	resultCh := make(chan provisionResult, 1)
	go func() {
		certificateID, version, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
		resultCh <- provisionResult{certificateID: certificateID, version: version, err: err}
	}()
	select {
	case <-installEntered:
	case <-time.After(time.Second):
		t.Fatal("install callback was not entered")
	}
	if provider.lifecycleMu.TryLock() {
		provider.lifecycleMu.Unlock()
		t.Fatal("provisioner did not hold the lifecycle read lock during Install")
	}

	resetStarted := make(chan struct{})
	resetDone := make(chan struct{})
	go func() {
		close(resetStarted)
		provider.ResetGNOIClient(context.Background())
		close(resetDone)
	}()
	<-resetStarted
	select {
	case <-resetDone:
		t.Fatal("ResetGNOIClient completed while Install was in flight")
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseInstall)
	select {
	case result := <-resultCh:
		if result.err != nil || result.certificateID == "" || result.version != "17.18.04" {
			t.Fatalf("ProvisionGNOICertificate result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("ProvisionGNOICertificate did not finish after Install was released")
	}
	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("ResetGNOIClient remained blocked after Install completed")
	}
}

func TestProvisionerInspectionTimeoutDoesNotBecomeIndeterminate(t *testing.T) {
	provisioner, provider, current, _ := newProvisionerHarness(t)
	provisioner.timeout = 10 * time.Millisecond
	provisioner.certificateInstalled = func(ctx context.Context, _ *gnoi.Client, _ *gnoi.ProvisioningBundle) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}
	provisioner.installCertificate = func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle, gnoi.CertificateSigner) error {
		t.Fatal("timed-out inspection must not install")
		return nil
	}

	_, _, err := provisioner.ProvisionGNOICertificate(context.Background(), current)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProvisionGNOICertificate error=%v, want DeadlineExceeded", err)
	}
	if gnoi.IsCertificateInstallIndeterminate(err) {
		t.Fatalf("pre-install timeout unexpectedly marked indeterminate: %v", err)
	}
	if provider.client != current || provider.generation != 0 {
		t.Fatal("pre-install timeout reset the provider")
	}
}

func newProvisionerHarness(t *testing.T) (*Provisioner, *Provider, *gnoi.Client, *gnoi.ProvisioningBundle) {
	t.Helper()
	bundle := testProvisioningBundle(t)
	current := &gnoi.Client{}
	provider := &Provider{client: current}
	provisioner, err := NewProvisioner(provider, bundle, testSigner{})
	if err != nil {
		t.Fatalf("NewProvisioner: %v", err)
	}
	return provisioner, provider, current, bundle
}

func testProvisioningBundle(t *testing.T) *gnoi.ProvisioningBundle {
	t.Helper()
	now := time.Now()
	rootKey := generateRSAKey(t)
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CVK runtime test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	root := createCertificate(t, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)

	issuerKey := generateRSAKey(t)
	issuerTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "CVK runtime test issuer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	issuer := createCertificate(t, issuerTemplate, root, &issuerKey.PublicKey, rootKey)

	address := net.ParseIP("192.0.2.1")
	leafKey := generateRSAKey(t)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			CommonName:         "cvk-device",
			Country:            []string{"US"},
			Province:           []string{"CA"},
			Organization:       []string{"Cisco"},
			OrganizationalUnit: []string{"CVK"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{address},
	}
	leaf := createCertificate(t, leafTemplate, issuer, &leafKey.PublicKey, issuerKey)
	bundle, err := gnoi.NewProvisioningBundle(
		"cvk-gnoi",
		address.String(),
		certificatePEM(leaf),
		append(certificatePEM(root), certificatePEM(issuer)...),
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	return bundle
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func createCertificate(t *testing.T, template, parent *x509.Certificate, publicKey any, parentKey any) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, publicKey, parentKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certificate
}

func certificatePEM(certificate *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
}
