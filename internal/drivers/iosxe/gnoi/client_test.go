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
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
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
	commonpb "github.com/openconfig/gnoi/types"
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
	statResp  *filepb.StatResponse
	statErr   error
	getChunks [][]byte
	getHash   *commonpb.HashType
	getErr    error
}

func (f *fakeFile) Stat(context.Context, *filepb.StatRequest) (*filepb.StatResponse, error) {
	return f.statResp, f.statErr
}

func (f *fakeFile) Get(_ *filepb.GetRequest, stream grpc.ServerStreamingServer[filepb.GetResponse]) error {
	if f.getErr != nil {
		return f.getErr
	}
	for _, chunk := range f.getChunks {
		if err := stream.Send(&filepb.GetResponse{
			Response: &filepb.GetResponse_Contents{Contents: chunk},
		}); err != nil {
			return err
		}
	}
	if f.getHash != nil {
		return stream.Send(&filepb.GetResponse{
			Response: &filepb.GetResponse_Hash{Hash: f.getHash},
		})
	}
	return nil
}

type fakeCert struct {
	certpb.UnimplementedCertificateManagementServer
	getResp             *certpb.GetCertificatesResponse
	getErr              error
	canGenResponse      *certpb.CanGenerateCSRResponse
	canGenRequest       *certpb.CanGenerateCSRRequest
	installRequest      *certpb.InstallCertificateRequest
	installResponse     *certpb.InstallCertificateResponse
	installErr          error
	installEOFSeen      bool
	installResponseSent chan struct{}
	installRelease      chan struct{}
	installCSR          []byte
	installCSRType      *certpb.CertificateType
	installRequests     []*certpb.InstallCertificateRequest
}

type malformedGetCertificatesClient struct {
	certpb.CertificateManagementClient
	response *certpb.GetCertificatesResponse
}

func (c malformedGetCertificatesClient) GetCertificates(
	context.Context,
	*certpb.GetCertificatesRequest,
	...grpc.CallOption,
) (*certpb.GetCertificatesResponse, error) {
	return c.response, nil
}

func (f *fakeCert) GetCertificates(context.Context, *certpb.GetCertificatesRequest) (*certpb.GetCertificatesResponse, error) {
	return f.getResp, f.getErr
}

func (f *fakeCert) CanGenerateCSR(_ context.Context, request *certpb.CanGenerateCSRRequest) (*certpb.CanGenerateCSRResponse, error) {
	f.canGenRequest = request
	if f.canGenResponse == nil {
		return &certpb.CanGenerateCSRResponse{CanGenerate: true}, nil
	}
	return f.canGenResponse, nil
}

func (f *fakeCert) Install(stream grpc.BidiStreamingServer[certpb.InstallCertificateRequest, certpb.InstallCertificateResponse]) error {
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	f.installRequest = request
	f.installRequests = append(f.installRequests, request)
	if f.installErr != nil {
		return f.installErr
	}
	if request.GetGenerateCsr() != nil {
		csrType := certpb.CertificateType_CT_X509
		if f.installCSRType != nil {
			csrType = *f.installCSRType
		}
		if err := stream.Send(&certpb.InstallCertificateResponse{
			InstallResponse: &certpb.InstallCertificateResponse_GeneratedCsr{
				GeneratedCsr: &certpb.GenerateCSRResponse{
					Csr: &certpb.CSR{Type: csrType, Csr: f.installCSR},
				},
			},
		}); err != nil {
			return err
		}
		request, err = stream.Recv()
		if err != nil {
			return err
		}
		f.installRequest = request
		f.installRequests = append(f.installRequests, request)
	}
	response := f.installResponse
	if response == nil {
		response = &certpb.InstallCertificateResponse{
			InstallResponse: &certpb.InstallCertificateResponse_LoadCertificate{
				LoadCertificate: &certpb.LoadCertificateResponse{},
			},
		}
	}
	if err := stream.Send(response); err != nil {
		return err
	}
	if f.installResponseSent != nil {
		close(f.installResponseSent)
	}
	if f.installRelease != nil {
		<-f.installRelease
		return nil
	}
	if _, err := stream.Recv(); err != io.EOF {
		if err == nil {
			return status.Error(codes.InvalidArgument, "expected client half-close")
		}
		return err
	}
	f.installEOFSeen = true
	return nil
}

type fakeOS struct {
	ospb.UnimplementedOSServer
	verifyResp              *ospb.VerifyResponse
	verifyErr               error
	verifyCalls             atomic.Int32
	installRequireClose     bool
	installFirstResp        *ospb.InstallResponse
	installFollowupResponse []*ospb.InstallResponse
	installFinalResp        *ospb.InstallResponse
	installAfterReady       *ospb.InstallResponse
	installAfterReadyGate   <-chan struct{}
	installSendSync         bool
	installEOFSeen          bool
	installBytes            int
	activateReq             *ospb.ActivateRequest
	activateResp            *ospb.ActivateResponse
	activateErr             error
}

func (f *fakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	f.verifyCalls.Add(1)
	return f.verifyResp, f.verifyErr
}

func (f *fakeOS) Install(stream grpc.BidiStreamingServer[ospb.InstallRequest, ospb.InstallResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	req := first.GetTransferRequest()
	if req == nil {
		return status.Error(codes.InvalidArgument, "expected transfer request")
	}
	firstResp := f.installFirstResp
	if firstResp == nil {
		firstResp = &ospb.InstallResponse{
			Response: &ospb.InstallResponse_TransferReady{TransferReady: &ospb.TransferReady{}},
		}
	}
	if err := stream.Send(firstResp); err != nil {
		return err
	}
	if _, ok := firstResp.Response.(*ospb.InstallResponse_TransferReady); !ok {
		for _, response := range f.installFollowupResponse {
			if err := stream.Send(response); err != nil {
				return err
			}
		}
		return nil
	}
	if f.installAfterReady != nil {
		if f.installAfterReadyGate != nil {
			select {
			case <-f.installAfterReadyGate:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
		return stream.Send(f.installAfterReady)
	}
	for {
		next, err := stream.Recv()
		if err != nil {
			return err
		}
		switch r := next.Request.(type) {
		case *ospb.InstallRequest_TransferContent:
			f.installBytes += len(r.TransferContent)
			if err := stream.Send(&ospb.InstallResponse{
				Response: &ospb.InstallResponse_TransferProgress{
					TransferProgress: &ospb.TransferProgress{BytesReceived: uint64(f.installBytes)},
				},
			}); err != nil {
				return err
			}
			if f.installSendSync {
				if err := stream.Send(&ospb.InstallResponse{
					Response: &ospb.InstallResponse_SyncProgress{
						SyncProgress: &ospb.SyncProgress{PercentageTransferred: 42},
					},
				}); err != nil {
					return err
				}
			}
		case *ospb.InstallRequest_TransferEnd:
			if f.installRequireClose {
				if _, err := stream.Recv(); err != io.EOF {
					if err == nil {
						return status.Error(codes.FailedPrecondition, "expected client half-close")
					}
					return err
				}
				f.installEOFSeen = true
			}
			response := f.installFinalResp
			if response == nil {
				response = &ospb.InstallResponse{
					Response: &ospb.InstallResponse_Validated{
						Validated: &ospb.Validated{Version: req.Version, Description: "validated"},
					},
				}
			}
			return stream.Send(response)
		default:
			return status.Errorf(codes.InvalidArgument, "unexpected install request %T", r)
		}
	}
}

type blockingInstallReader struct {
	entered chan struct{}
	unblock chan struct{}
	exited  chan struct{}
}

func newBlockingInstallReader() *blockingInstallReader {
	return &blockingInstallReader{
		entered: make(chan struct{}),
		unblock: make(chan struct{}),
		exited:  make(chan struct{}),
	}
}

func (r *blockingInstallReader) Read([]byte) (int, error) {
	close(r.entered)
	<-r.unblock
	close(r.exited)
	return 0, errors.New("reader released")
}

func (f *fakeOS) Activate(_ context.Context, req *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	f.activateReq = req
	if f.activateErr != nil {
		return nil, f.activateErr
	}
	if f.activateResp != nil {
		return f.activateResp, nil
	}
	return &ospb.ActivateResponse{
		Response: &ospb.ActivateResponse_ActivateOk{ActivateOk: &ospb.ActivateOK{}},
	}, nil
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

func TestGetStreamsContentsAndHash(t *testing.T) {
	ts := newTestServer(t)
	sum := sha256.Sum256([]byte("hello flash"))
	ts.File.getChunks = [][]byte{[]byte("hello "), []byte("flash")}
	ts.File.getHash = &commonpb.HashType{Method: commonpb.HashType_SHA256, Hash: sum[:]}

	var buf bytes.Buffer
	hash, err := ts.client(t).Get(context.Background(), "flash:hello-app.iosxe.tar", &buf)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if buf.String() != "hello flash" {
		t.Fatalf("streamed contents=%q", buf.String())
	}
	if hash.GetMethod() != commonpb.HashType_SHA256 || !bytes.Equal(hash.GetHash(), sum[:]) {
		t.Fatalf("hash=%+v, want SHA256 %x", hash, sum)
	}
}

func TestGetMissingHashFails(t *testing.T) {
	ts := newTestServer(t)
	ts.File.getChunks = [][]byte{[]byte("payload")}

	var buf bytes.Buffer
	_, err := ts.client(t).Get(context.Background(), "flash:payload.bin", &buf)
	if err == nil || !strings.Contains(err.Error(), "no terminal hash") {
		t.Fatalf("expected missing-hash error, got %v", err)
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

func TestGetCertificatesRejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		resp *certpb.GetCertificatesResponse
		want string
	}{
		{name: "nil response", want: "empty response"},
		{
			name: "nil certificate entry",
			resp: &certpb.GetCertificatesResponse{CertificateInfo: []*certpb.CertificateInfo{nil}},
			want: "certificate entry 0 is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				cert: malformedGetCertificatesClient{response: tt.resp},
				cap:  NewCapabilityCache(nil),
			}
			if _, err := client.GetCertificates(context.Background()); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("GetCertificates error=%v, want substring %q", err, tt.want)
			}
		})
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
	if res.Standby.State != StandbyStateNotReported {
		t.Fatalf("Verify standby state: got %q, want %q", res.Standby.State, StandbyStateNotReported)
	}
}

func TestStandbyVerifyResultFromProto(t *testing.T) {
	tests := []struct {
		name string
		in   *ospb.VerifyStandby
		want StandbyVerifyResult
	}{
		{
			name: "not reported",
			want: StandbyVerifyResult{State: StandbyStateNotReported},
		},
		{
			name: "empty choice",
			in:   &ospb.VerifyStandby{},
			want: StandbyVerifyResult{State: StandbyStateUnspecified},
		},
		{
			name: "nil state payload",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
				StandbyState: nil,
			}},
			want: StandbyVerifyResult{State: StandbyStateUnspecified},
		},
		{
			name: "unspecified",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
				StandbyState: &ospb.StandbyState{State: ospb.StandbyState_UNSPECIFIED},
			}},
			want: StandbyVerifyResult{State: StandbyStateUnspecified},
		},
		{
			name: "unsupported",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
				StandbyState: &ospb.StandbyState{State: ospb.StandbyState_UNSUPPORTED},
			}},
			want: StandbyVerifyResult{State: StandbyStateUnsupported},
		},
		{
			name: "non existent",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
				StandbyState: &ospb.StandbyState{State: ospb.StandbyState_NON_EXISTENT},
			}},
			want: StandbyVerifyResult{State: StandbyStateNonExistent},
		},
		{
			name: "unavailable",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
				StandbyState: &ospb.StandbyState{State: ospb.StandbyState_UNAVAILABLE},
			}},
			want: StandbyVerifyResult{State: StandbyStateUnavailable},
		},
		{
			name: "unknown future state",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
				StandbyState: &ospb.StandbyState{State: ospb.StandbyState_State(99)},
			}},
			want: StandbyVerifyResult{State: StandbyStateUnknown},
		},
		{
			name: "nil ready payload",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_VerifyResponse{
				VerifyResponse: nil,
			}},
			want: StandbyVerifyResult{State: StandbyStateUnspecified},
		},
		{
			name: "ready",
			in: &ospb.VerifyStandby{State: &ospb.VerifyStandby_VerifyResponse{
				VerifyResponse: &ospb.StandbyResponse{
					Id:                    "R1",
					Version:               "17.18.04",
					ActivationFailMessage: "standby boot failed",
				},
			}},
			want: StandbyVerifyResult{
				State:                 StandbyStateReady,
				ID:                    "R1",
				Version:               "17.18.04",
				ActivationFailMessage: "standby boot failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := standbyVerifyResultFromProto(tt.in)
			if got != tt.want {
				t.Fatalf("standbyVerifyResultFromProto()=%+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestVerifyReturnsReadyStandby(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.verifyResp = &ospb.VerifyResponse{
		Version: "17.18.04",
		VerifyStandby: &ospb.VerifyStandby{State: &ospb.VerifyStandby_VerifyResponse{
			VerifyResponse: &ospb.StandbyResponse{
				Id:                    "R1",
				Version:               "17.18.04",
				ActivationFailMessage: "previous standby activation failed",
			},
		}},
		IndividualSupervisorInstall: true,
	}

	res, err := ts.client(t).Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := StandbyVerifyResult{
		State:                 StandbyStateReady,
		ID:                    "R1",
		Version:               "17.18.04",
		ActivationFailMessage: "previous standby activation failed",
	}
	if res.Standby != want {
		t.Fatalf("Verify standby=%+v, want %+v", res.Standby, want)
	}
	if !res.IndividualSupervisorInstall {
		t.Fatal("Verify did not preserve individual_supervisor_install")
	}
}

func TestVerifyDeviceNotProvisionedIsReadOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.verifyErr = status.Error(codes.FailedPrecondition, "Device has not been provisioned")

	_, err := ts.client(t).Verify(context.Background())
	if !IsDeviceNotProvisioned(err) {
		t.Fatalf("Verify error=%T %v, want exact device-not-provisioned classification", err, err)
	}
	if ts.Cert.canGenRequest != nil || ts.Cert.installRequest != nil {
		t.Fatal("Verify attempted certificate provisioning")
	}
}

func TestDeviceNotProvisionedClassificationTraversesJoinedErrors(t *testing.T) {
	exact := status.Error(codes.FailedPrecondition, iosXEDeviceNotProvisionedMessage)
	if err := errors.Join(errors.New("parallel cleanup failed"), exact); !IsDeviceNotProvisioned(err) {
		t.Fatalf("IsDeviceNotProvisioned(%v)=false, want true for joined exact IOS XE status", err)
	}

	for _, err := range []error{
		errors.Join(errors.New("parallel cleanup failed"), status.Error(codes.Unavailable, iosXEDeviceNotProvisionedMessage)),
		errors.Join(errors.New("parallel cleanup failed"), status.Error(codes.FailedPrecondition, iosXEDeviceNotProvisionedMessage+" unexpectedly")),
	} {
		if IsDeviceNotProvisioned(err) {
			t.Fatalf("IsDeviceNotProvisioned(%v)=true, want exact code/message classification", err)
		}
	}
}

func TestInstallClosesSendAfterTransferEnd(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.installRequireClose = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	progress, err := ts.client(t).Install(ctx, bytes.NewReader([]byte("image")), InstallOpts{
		Version:     "17.18.03",
		PackageSize: 5,
		ChunkSize:   2,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	var validated *InstallValidated
	for ev := range progress {
		if ev.Err != nil {
			t.Fatalf("Install progress error: %v", ev.Err)
		}
		if ev.Validated != nil {
			validated = ev.Validated
		}
	}
	if validated == nil || validated.Version != "17.18.03" {
		t.Fatalf("validated=%+v, want version 17.18.03", validated)
	}
	if !ts.OS.installEOFSeen {
		t.Fatal("server did not observe client half-close")
	}
	if ts.OS.installBytes != 5 {
		t.Fatalf("installBytes=%d, want 5", ts.OS.installBytes)
	}
}

func TestInstallSurfacesDeviceInstallError(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.installFirstResp = &ospb.InstallResponse{
		Response: &ospb.InstallResponse_InstallError{
			InstallError: &ospb.InstallError{Type: ospb.InstallError_INTEGRITY_FAIL, Detail: "bad sha"},
		},
	}

	progress, err := ts.client(t).Install(context.Background(), bytes.NewReader([]byte("image")), InstallOpts{
		Version:     "17.18.03",
		PackageSize: 5,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	var gotErr error
	for ev := range progress {
		if ev.Err != nil {
			gotErr = ev.Err
		}
	}
	var installErr *InstallError
	if !errors.As(gotErr, &installErr) {
		t.Fatalf("expected *InstallError, got %T %v", gotErr, gotErr)
	}
	if installErr.Type != InstallErrorIntegrityFail {
		t.Fatalf("InstallError.Type=%q", installErr.Type)
	}
}

func TestInstallJoinsSenderBeforeBulkReleaseOnDeviceError(t *testing.T) {
	ts := newTestServer(t)
	reader := newBlockingInstallReader()
	readerUnblocked := false
	defer func() {
		if !readerUnblocked {
			close(reader.unblock)
		}
	}()
	allowResponse := make(chan struct{})
	ts.OS.installAfterReadyGate = allowResponse
	ts.OS.installAfterReady = &ospb.InstallResponse{
		Response: &ospb.InstallResponse_InstallError{
			InstallError: &ospb.InstallError{Type: ospb.InstallError_INTEGRITY_FAIL, Detail: "device rejected image"},
		},
	}

	released := make(chan struct{})
	releasedBeforeSenderExit := make(chan struct{}, 1)
	conn := ts.dial(t)
	client, err := New(conn, Options{BulkConnProvider: func(context.Context) (*grpc.ClientConn, func(), error) {
		return conn, func() {
			select {
			case <-reader.exited:
			default:
				releasedBeforeSenderExit <- struct{}{}
			}
			close(released)
		}, nil
	}})
	if err != nil {
		t.Fatalf("New gNOI client: %v", err)
	}

	progress, err := client.Install(context.Background(), reader, InstallOpts{Version: "17.18.04"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	select {
	case event := <-progress:
		if !event.TransferReady {
			t.Fatalf("first Install event=%+v, want TransferReady", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TransferReady")
	}
	select {
	case <-reader.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("content sender did not enter reader")
	}
	close(allowResponse)

	select {
	case event := <-progress:
		var installErr *InstallError
		if !errors.As(event.Err, &installErr) || installErr.Type != InstallErrorIntegrityFail {
			t.Fatalf("Install event=%+v, want integrity InstallError", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for device error")
	}
	select {
	case <-released:
		t.Fatal("bulk lease released while content sender was blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(reader.unblock)
	readerUnblocked = true
	for range progress {
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("bulk lease was not released after content sender exited")
	}
	select {
	case <-releasedBeforeSenderExit:
		t.Fatal("bulk lease release ran before content sender exited")
	default:
	}
}

func TestInstallJoinsSenderBeforeBulkReleaseOnCancellation(t *testing.T) {
	ts := newTestServer(t)
	reader := newBlockingInstallReader()
	readerUnblocked := false
	defer func() {
		if !readerUnblocked {
			close(reader.unblock)
		}
	}()

	released := make(chan struct{})
	releasedBeforeSenderExit := make(chan struct{}, 1)
	conn := ts.dial(t)
	client, err := New(conn, Options{BulkConnProvider: func(context.Context) (*grpc.ClientConn, func(), error) {
		return conn, func() {
			select {
			case <-reader.exited:
			default:
				releasedBeforeSenderExit <- struct{}{}
			}
			close(released)
		}, nil
	}})
	if err != nil {
		t.Fatalf("New gNOI client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	progress, err := client.Install(ctx, reader, InstallOpts{Version: "17.18.04"})
	if err != nil {
		cancel()
		t.Fatalf("Install: %v", err)
	}
	select {
	case event := <-progress:
		if !event.TransferReady {
			cancel()
			t.Fatalf("first Install event=%+v, want TransferReady", event)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for TransferReady")
	}
	select {
	case <-reader.entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("content sender did not enter reader")
	}
	cancel()
	select {
	case <-released:
		t.Fatal("bulk lease released while canceled content sender was blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(reader.unblock)
	readerUnblocked = true
	for range progress {
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("bulk lease was not released after canceled content sender exited")
	}
	select {
	case <-releasedBeforeSenderExit:
		t.Fatal("bulk lease release ran before canceled content sender exited")
	default:
	}
}

func TestInstallEmitsSyncProgress(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.installSendSync = true

	progress, err := ts.client(t).Install(context.Background(), bytes.NewReader([]byte("image")), InstallOpts{
		Version:     "17.18.03",
		PackageSize: 5,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	var syncPct uint32
	for ev := range progress {
		if ev.Err != nil {
			t.Fatalf("Install progress error: %v", ev.Err)
		}
		if ev.SyncProgress != nil {
			syncPct = ev.SyncProgress.PercentageTransferred
		}
	}
	if syncPct != 42 {
		t.Fatalf("SyncProgress=%d, want 42", syncPct)
	}
}

func TestInstallAcceptsInitialSyncProgressWithoutSendingImage(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.installFirstResp = &ospb.InstallResponse{
		Response: &ospb.InstallResponse_SyncProgress{
			SyncProgress: &ospb.SyncProgress{PercentageTransferred: 25},
		},
	}
	ts.OS.installFollowupResponse = []*ospb.InstallResponse{
		{
			Response: &ospb.InstallResponse_SyncProgress{
				SyncProgress: &ospb.SyncProgress{PercentageTransferred: 100},
			},
		},
		{
			Response: &ospb.InstallResponse_Validated{
				Validated: &ospb.Validated{Version: "17.18.04", Description: "synced from active supervisor"},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	progress, err := ts.client(t).Install(ctx, bytes.NewReader([]byte("must not be sent")), InstallOpts{
		Version:     "17.18.04",
		PackageSize: 16,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	var syncProgress []uint32
	var validated *InstallValidated
	for event := range progress {
		if event.Err != nil {
			t.Fatalf("Install progress error: %v", event.Err)
		}
		if event.TransferReady {
			t.Fatal("Install emitted TransferReady while target was syncing from its peer")
		}
		if event.SyncProgress != nil {
			syncProgress = append(syncProgress, event.SyncProgress.PercentageTransferred)
		}
		if event.Validated != nil {
			validated = event.Validated
		}
	}
	if len(syncProgress) != 2 || syncProgress[0] != 25 || syncProgress[1] != 100 {
		t.Fatalf("SyncProgress=%v, want [25 100]", syncProgress)
	}
	if validated == nil || validated.Version != "17.18.04" {
		t.Fatalf("Validated=%+v, want version 17.18.04", validated)
	}
	if ts.OS.installBytes != 0 {
		t.Fatalf("Install sent %d bytes while target was syncing from its peer, want 0", ts.OS.installBytes)
	}
}

func TestInstallRejectsTransferProgressDuringSupervisorSync(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.installFirstResp = &ospb.InstallResponse{
		Response: &ospb.InstallResponse_SyncProgress{
			SyncProgress: &ospb.SyncProgress{PercentageTransferred: 25},
		},
	}
	ts.OS.installFollowupResponse = []*ospb.InstallResponse{
		{
			Response: &ospb.InstallResponse_TransferProgress{
				TransferProgress: &ospb.TransferProgress{BytesReceived: 1},
			},
		},
	}

	progress, err := ts.client(t).Install(context.Background(), bytes.NewReader([]byte("must not be sent")), InstallOpts{
		Version: "17.18.04",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	var gotErr error
	for event := range progress {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "unexpected TransferProgress during supervisor sync") {
		t.Fatalf("Install error=%v, want invalid sync sequence error", gotErr)
	}
	if ts.OS.installBytes != 0 {
		t.Fatalf("Install sent %d bytes after initial SyncProgress, want 0", ts.OS.installBytes)
	}
}

func TestInstallRejectsEmptyValidatedVersion(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeOS)
	}{
		{
			name: "already staged",
			configure: func(server *fakeOS) {
				server.installFirstResp = &ospb.InstallResponse{
					Response: &ospb.InstallResponse_Validated{
						Validated: &ospb.Validated{Description: "missing version"},
					},
				}
			},
		},
		{
			name: "after transfer",
			configure: func(server *fakeOS) {
				server.installFinalResp = &ospb.InstallResponse{
					Response: &ospb.InstallResponse_Validated{
						Validated: &ospb.Validated{Version: "  ", Description: "blank version"},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			tt.configure(ts.OS)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			progress, err := ts.client(t).Install(ctx, bytes.NewReader([]byte("image")), InstallOpts{
				Version:     "17.18.04",
				PackageSize: 5,
			})
			if err != nil {
				t.Fatalf("Install: %v", err)
			}
			var gotErr error
			for event := range progress {
				if event.Err != nil {
					gotErr = event.Err
				}
				if event.Validated != nil {
					t.Fatalf("Install emitted invalid Validated event: %+v", event.Validated)
				}
			}
			if gotErr == nil || !strings.Contains(gotErr.Error(), "Validated response has empty version") {
				t.Fatalf("Install error=%v, want empty Validated version error", gotErr)
			}
		})
	}
}

func TestInstallRejectsInvalidLocalInputs(t *testing.T) {
	ts := newTestServer(t)
	client := ts.client(t)
	if _, err := client.Install(context.Background(), nil, InstallOpts{}); err == nil || !strings.Contains(err.Error(), "image reader is required") {
		t.Fatalf("Install nil reader error=%v, want validation error", err)
	}
	if _, err := client.Install(context.Background(), bytes.NewReader(nil), InstallOpts{ChunkSize: -1}); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("Install negative ChunkSize error=%v, want validation error", err)
	}
}

func TestInstallRejectsUnexpectedFirstResponse(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.installFirstResp = &ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferProgress{
			TransferProgress: &ospb.TransferProgress{BytesReceived: 1},
		},
	}

	progress, err := ts.client(t).Install(context.Background(), bytes.NewReader([]byte("image")), InstallOpts{
		Version:     "17.18.03",
		PackageSize: 5,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	var gotErr error
	for ev := range progress {
		if ev.Err != nil {
			gotErr = ev.Err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "unexpected first response") {
		t.Fatalf("expected unexpected-first-response error, got %v", gotErr)
	}
}

func TestActivateSendsCompleteRequest(t *testing.T) {
	ts := newTestServer(t)
	err := ts.client(t).Activate(context.Background(), ActivateOpts{
		Version:           "17.18.04",
		StandbySupervisor: true,
		NoReboot:          true,
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if ts.OS.activateReq == nil {
		t.Fatal("Activate request was not received")
	}
	if ts.OS.activateReq.Version != "17.18.04" || !ts.OS.activateReq.StandbySupervisor || !ts.OS.activateReq.NoReboot {
		t.Fatalf("Activate request=%+v, want all options forwarded", ts.OS.activateReq)
	}
}

func TestActivateRejectsEmptyVersionBeforeRPC(t *testing.T) {
	ts := newTestServer(t)
	err := ts.client(t).Activate(context.Background(), ActivateOpts{})
	if err == nil || !strings.Contains(err.Error(), "Version is required") {
		t.Fatalf("Activate error=%v, want required-version error", err)
	}
	if ts.OS.activateReq != nil {
		t.Fatalf("Activate sent request despite empty version: %+v", ts.OS.activateReq)
	}
}

func TestActivateTranslatesDeviceErrors(t *testing.T) {
	tests := []struct {
		name string
		in   ospb.ActivateError_Type
		want ActivateErrorType
	}{
		{name: "unspecified", in: ospb.ActivateError_UNSPECIFIED, want: ActivateErrorUnspecified},
		{name: "non-existent version", in: ospb.ActivateError_NON_EXISTENT_VERSION, want: ActivateErrorNonExistentVersion},
		{name: "standby unsupported", in: ospb.ActivateError_NOT_SUPPORTED_ON_BACKUP, want: ActivateErrorNotSupportedOnBackup},
		{name: "unknown value", in: ospb.ActivateError_Type(99), want: ActivateErrorUnspecified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			ts.OS.activateResp = &ospb.ActivateResponse{
				Response: &ospb.ActivateResponse_ActivateError{
					ActivateError: &ospb.ActivateError{Type: tt.in, Detail: "device rejected activation"},
				},
			}
			err := ts.client(t).Activate(context.Background(), ActivateOpts{Version: "17.18.04"})
			var activateErr *ActivateError
			if !errors.As(err, &activateErr) {
				t.Fatalf("Activate error=%T %v, want *ActivateError", err, err)
			}
			if activateErr.Type != tt.want || activateErr.Detail != "device rejected activation" {
				t.Fatalf("ActivateError=%+v, want type %q with device detail", activateErr, tt.want)
			}
		})
	}
}

func TestActivatePreservesRPCStatus(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.activateErr = status.Error(codes.Unavailable, "device rebooting")
	err := ts.client(t).Activate(context.Background(), ActivateOpts{Version: "17.18.04"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("Activate error=%T %v, want Unavailable status", err, err)
	}
}

func TestActivateRejectsUnexpectedResponse(t *testing.T) {
	ts := newTestServer(t)
	ts.OS.activateResp = &ospb.ActivateResponse{}
	err := ts.client(t).Activate(context.Background(), ActivateOpts{Version: "17.18.04"})
	if err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("Activate error=%v, want unexpected-response error", err)
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
