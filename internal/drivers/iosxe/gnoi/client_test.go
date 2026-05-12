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

package gnoi

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"
)

// --- fake server hooks ---

type fakeSystem struct {
	syspb.UnimplementedSystemServer
	timeResp     *syspb.TimeResponse
	timeErr      error
	pingReplies  []*syspb.PingResponse
	pingErr      error
	rebootStatus *syspb.RebootStatusResponse
}

func (f *fakeSystem) Time(context.Context, *syspb.TimeRequest) (*syspb.TimeResponse, error) {
	return f.timeResp, f.timeErr
}

func (f *fakeSystem) Ping(_ *syspb.PingRequest, stream grpc.ServerStreamingServer[syspb.PingResponse]) error {
	if f.pingErr != nil {
		return f.pingErr
	}
	for _, r := range f.pingReplies {
		if err := stream.Send(r); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSystem) RebootStatus(context.Context, *syspb.RebootStatusRequest) (*syspb.RebootStatusResponse, error) {
	if f.rebootStatus == nil {
		return &syspb.RebootStatusResponse{}, nil
	}
	return f.rebootStatus, nil
}

type fakeFile struct {
	filepb.UnimplementedFileServer
	statResp *filepb.StatResponse
	statErr  error
}

func (f *fakeFile) Stat(context.Context, *filepb.StatRequest) (*filepb.StatResponse, error) {
	return f.statResp, f.statErr
}

type fakeCert struct {
	certpb.UnimplementedCertificateManagementServer
	getResp        *certpb.GetCertificatesResponse
	getErr         error
	canGenResponse *certpb.CanGenerateCSRResponse
}

func (f *fakeCert) GetCertificates(context.Context, *certpb.GetCertificatesRequest) (*certpb.GetCertificatesResponse, error) {
	return f.getResp, f.getErr
}

func (f *fakeCert) CanGenerateCSR(context.Context, *certpb.CanGenerateCSRRequest) (*certpb.CanGenerateCSRResponse, error) {
	if f.canGenResponse == nil {
		return &certpb.CanGenerateCSRResponse{CanGenerate: true}, nil
	}
	return f.canGenResponse, nil
}

type fakeOS struct {
	ospb.UnimplementedOSServer
	verifyResp *ospb.VerifyResponse
	verifyErr  error
}

func (f *fakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	return f.verifyResp, f.verifyErr
}

type fakeReset struct {
	resetpb.UnimplementedFactoryResetServer
}

// --- test rig ---

type testServer struct {
	System *fakeSystem
	File   *fakeFile
	Cert   *fakeCert
	OS     *fakeOS
	Reset  *fakeReset

	srv *grpc.Server
	lis *bufconn.Listener
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	ts := &testServer{
		System: &fakeSystem{},
		File:   &fakeFile{},
		Cert:   &fakeCert{},
		OS:     &fakeOS{},
		Reset:  &fakeReset{},
		srv:    srv,
		lis:    lis,
	}
	syspb.RegisterSystemServer(srv, ts.System)
	filepb.RegisterFileServer(srv, ts.File)
	certpb.RegisterCertificateManagementServer(srv, ts.Cert)
	ospb.RegisterOSServer(srv, ts.OS)
	resetpb.RegisterFactoryResetServer(srv, ts.Reset)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return ts
}

func (ts *testServer) dial(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return ts.lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (ts *testServer) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(ts.dial(t), Options{})
	if err != nil {
		t.Fatalf("New gNOI client: %v", err)
	}
	return c
}

// --- system ---

func TestTime(t *testing.T) {
	ts := newTestServer(t)
	ts.System.timeResp = &syspb.TimeResponse{Time: uint64(time.Unix(1234, 0).UnixNano())}
	got, err := ts.client(t).Time(context.Background())
	if err != nil {
		t.Fatalf("Time: %v", err)
	}
	if got.Unix() != 1234 {
		t.Fatalf("Time: got=%v, want unix=1234", got)
	}
}

func TestPingStreamingResults(t *testing.T) {
	ts := newTestServer(t)
	ts.System.pingReplies = []*syspb.PingResponse{
		{Source: "10.0.0.2", Time: int64(2 * time.Millisecond), Ttl: 64, Sequence: 1, Bytes: 64},
		{Source: "10.0.0.2", Time: int64(3 * time.Millisecond), Ttl: 64, Sequence: 2, Bytes: 64},
		{Sent: 2, Received: 2, MinTime: int64(2 * time.Millisecond), AvgTime: int64(2500 * time.Microsecond), MaxTime: int64(3 * time.Millisecond)},
	}
	res, err := ts.client(t).Ping(context.Background(), "10.0.0.2", PingOpts{Count: 2})
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if len(res.Replies) != 2 {
		t.Fatalf("Replies: got %d, want 2", len(res.Replies))
	}
	if res.Summary.Sent != 2 || res.Summary.Received != 2 {
		t.Fatalf("Summary: %+v", res.Summary)
	}
	if res.Summary.LossPct != 0 {
		t.Fatalf("LossPct: got %v, want 0", res.Summary.LossPct)
	}
}

// --- file ---

func TestStatRejectsBareRelativePath(t *testing.T) {
	ts := newTestServer(t)
	_, err := ts.client(t).Stat(context.Background(), "foo.bin")
	if err == nil || !strings.Contains(err.Error(), "filesystem prefix") {
		t.Fatalf("expected path-prefix error, got %v", err)
	}
}

func TestStatAcceptsFlashPath(t *testing.T) {
	ts := newTestServer(t)
	ts.File.statResp = &filepb.StatResponse{
		Stats: []*filepb.StatInfo{
			{Path: "flash:cat9k.bin", Size: 12345, Permissions: 0o644, LastModified: uint64(time.Unix(100, 0).UnixNano())},
		},
	}
	stats, err := ts.client(t).Stat(context.Background(), "flash:cat9k.bin")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if len(stats) != 1 || stats[0].Size != 12345 {
		t.Fatalf("Stat result: %+v", stats)
	}
}

// --- cert ---

func TestGetCertificates(t *testing.T) {
	ts := newTestServer(t)
	ts.Cert.getResp = &certpb.GetCertificatesResponse{
		CertificateInfo: []*certpb.CertificateInfo{
			{
				CertificateId: "cvk-trustpoint",
				Certificate: &certpb.Certificate{
					Type:        certpb.CertificateType_CT_X509,
					Certificate: []byte("--cert--"),
				},
				ModificationTime: time.Unix(42, 0).UnixNano(),
			},
		},
	}
	got, err := ts.client(t).GetCertificates(context.Background())
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(got) != 1 || got[0].CertificateID != "cvk-trustpoint" {
		t.Fatalf("GetCertificates result: %+v", got)
	}
	if got[0].Type != "CT_X509" {
		t.Fatalf("Type: %q", got[0].Type)
	}
}

func TestCanGenerateCSRDefaults(t *testing.T) {
	ts := newTestServer(t)
	ok, err := ts.client(t).CanGenerateCSR(context.Background(), CanGenerateCSROpts{})
	if err != nil {
		t.Fatalf("CanGenerateCSR: %v", err)
	}
	if !ok {
		t.Fatalf("expected default fake to report capability")
	}
}

// --- os ---

func TestVerify(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.verifyResp = &ospb.VerifyResponse{Version: "17.15.01a"}
	res, err := ts.client(t).Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Version != "17.15.01a" {
		t.Fatalf("Verify version: %q", res.Version)
	}
}

// --- capability cache ---

func TestUnimplementedFlipsCacheAndShortCircuits(t *testing.T) {
	ts := newTestServer(t)
	ts.System.timeErr = status.Error(codes.Unimplemented, "system service not enabled on this image")

	c := ts.client(t)
	_, err := c.Time(context.Background())
	var notSupported *ErrServiceUnsupported
	if err == nil {
		t.Fatalf("expected error from Time, got nil")
	}
	// First call returns the wrapped grpc error
	if errors.As(err, &notSupported) {
		t.Fatalf("first Time call should surface raw grpc error, got %v", err)
	}

	// Second call should short-circuit at the cache and return
	// ErrServiceUnsupported without ever hitting the server.
	_, err = c.Time(context.Background())
	if !errors.As(err, &notSupported) {
		t.Fatalf("second Time call should return *ErrServiceUnsupported, got %v", err)
	}
	if notSupported.Service != ServiceSystem {
		t.Fatalf("unsupported svc: %q", notSupported.Service)
	}
}

func TestPinSupportedSurvivesObserve(t *testing.T) {
	cache := NewCapabilityCache(func() time.Time { return time.Unix(0, 0) })
	cache.Pin(ServiceOS, true)
	cache.Observe(ServiceOS, status.Error(codes.Unimplemented, "force-unsupported"))
	supported, known, _ := cache.Supported(ServiceOS)
	if !known || !supported {
		t.Fatalf("pinned verdict was overwritten; supported=%v known=%v", supported, known)
	}
}

func TestTransientErrorDoesNotCache(t *testing.T) {
	cache := NewCapabilityCache(func() time.Time { return time.Unix(0, 0) })
	cache.Observe(ServiceFile, status.Error(codes.Unavailable, "transient"))
	_, known, _ := cache.Supported(ServiceFile)
	if known {
		t.Fatalf("transient Unavailable should not pin a cache verdict")
	}
}

func TestCacheTTL(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	now := func() time.Time { return clock }
	cache := NewCapabilityCache(now)
	cache.Observe(ServiceOS, nil) // supported=true
	supported, known, _ := cache.Supported(ServiceOS)
	if !known || !supported {
		t.Fatalf("expected fresh supported verdict")
	}
	clock = clock.Add(25 * time.Hour)
	_, known, _ = cache.Supported(ServiceOS)
	if known {
		t.Fatalf("cache verdict should expire after %v", capabilityTTL)
	}
}

// FactoryReset is now implemented; exercised by the IOSXEOperationalAction
// reconciler tests against a fake gNOI server that returns a Start
// response. The fake here defaults to Unimplemented so a bare call
// surfaces the gRPC error, which the IOSXEOperationalAction reconciler
// classifies for the operator.

// Install/Activate are now implemented and exercised by the
// IOSXESoftwareUpgrade reconciler tests in Phase C. The gnoi package
// keeps the byte-stream protocol details unit-tested by the reconciler
// fixture since both halves (sender goroutine + recv loop) need to
// be driven against a fake gNOI server with a non-trivial scripted
// response sequence.

var _ io.Reader // keep the import slot
