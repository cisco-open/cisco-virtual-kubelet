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

package operationalaction

import (
	"context"
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
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

// --- fixtures ---

const (
	testProvisioningCertificateID = "cvk-gnoi"
	testPublicMaterialSHA256      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

type fakeSys struct {
	syspb.UnimplementedSystemServer
	rebootCalls       atomic.Int64
	cancelRebootCalls atomic.Int64
	killCalls         atomic.Int64
	rebootForce       bool
	rebootDelay       uint64
}

func (f *fakeSys) Reboot(_ context.Context, req *syspb.RebootRequest) (*syspb.RebootResponse, error) {
	f.rebootCalls.Add(1)
	f.rebootForce = req.Force
	f.rebootDelay = req.Delay
	return &syspb.RebootResponse{}, nil
}

func (f *fakeSys) CancelReboot(context.Context, *syspb.CancelRebootRequest) (*syspb.CancelRebootResponse, error) {
	f.cancelRebootCalls.Add(1)
	return &syspb.CancelRebootResponse{}, nil
}

func (f *fakeSys) KillProcess(context.Context, *syspb.KillProcessRequest) (*syspb.KillProcessResponse, error) {
	f.killCalls.Add(1)
	return &syspb.KillProcessResponse{}, nil
}

type fakeFile struct {
	filepb.UnimplementedFileServer
	removeCalls atomic.Int64
	putCalls    atomic.Int64
	putBytes    atomic.Int64
}

func (f *fakeFile) Remove(context.Context, *filepb.RemoveRequest) (*filepb.RemoveResponse, error) {
	f.removeCalls.Add(1)
	return &filepb.RemoveResponse{}, nil
}

func (f *fakeFile) Put(stream filepb.File_PutServer) error {
	f.putCalls.Add(1)
	for {
		req, err := stream.Recv()
		if err != nil {
			break
		}
		switch x := req.Request.(type) {
		case *filepb.PutRequest_Contents:
			f.putBytes.Add(int64(len(x.Contents)))
		case *filepb.PutRequest_Hash:
			// final
			return stream.SendAndClose(&filepb.PutResponse{})
		}
	}
	return stream.SendAndClose(&filepb.PutResponse{})
}

type fakeReset struct {
	resetpb.UnimplementedFactoryResetServer
	calls       atomic.Int64
	lastFactory bool
	resp        *resetpb.StartResponse
	err         error
}

type fakeOS struct {
	ospb.UnimplementedOSServer
	verifyCalls atomic.Int64
	verifyResp  *ospb.VerifyResponse
	verifyErr   error
}

func (f *fakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	f.verifyCalls.Add(1)
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	if f.verifyResp != nil {
		return f.verifyResp, nil
	}
	return &ospb.VerifyResponse{}, nil
}

func (f *fakeReset) Start(_ context.Context, req *resetpb.StartRequest) (*resetpb.StartResponse, error) {
	f.calls.Add(1)
	f.lastFactory = req.FactoryOs
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &resetpb.StartResponse{Response: &resetpb.StartResponse_ResetSuccess{ResetSuccess: &resetpb.ResetSuccess{}}}, nil
}

type rig struct {
	sys    *fakeSys
	file   *fakeFile
	reset  *fakeReset
	os     *fakeOS
	client *gnoi.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	r := &rig{sys: &fakeSys{}, file: &fakeFile{}, reset: &fakeReset{}, os: &fakeOS{}}
	syspb.RegisterSystemServer(srv, r.sys)
	filepb.RegisterFileServer(srv, r.file)
	resetpb.RegisterFactoryResetServer(srv, r.reset)
	certpb.RegisterCertificateManagementServer(srv, certpb.UnimplementedCertificateManagementServer{})
	ospb.RegisterOSServer(srv, r.os)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c, err := gnoi.New(conn, gnoi.Options{})
	if err != nil {
		t.Fatalf("gnoi.New: %v", err)
	}
	r.client = c
	return r
}

type staticGNOI struct {
	c           *gnoi.Client
	clientCalls atomic.Int64
}

func (s *staticGNOI) GNOIClient(context.Context) (*gnoi.Client, error) {
	s.clientCalls.Add(1)
	return s.c, nil
}

type provisioningGNOI struct {
	*staticGNOI
	provisionCalls           atomic.Int64
	certificateID            string
	provisionedCertificateID string
	publicMaterialSHA256     string
	version                  string
	provisionErr             error
}

func (p *provisioningGNOI) ConfiguredIntent() (string, string) {
	return p.certificateID, p.publicMaterialSHA256
}

func (p *provisioningGNOI) ProvisionGNOICertificate(context.Context, *gnoi.Client) (string, string, error) {
	p.provisionCalls.Add(1)
	certificateID := p.provisionedCertificateID
	if certificateID == "" {
		certificateID = p.certificateID
	}
	return certificateID, p.version, p.provisionErr
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go: %v", err)
	}
	if err := configv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops: %v", err)
	}
	return scheme
}

func newAction(name string, mutate func(*opsv1alpha1.IOSXEOperationalAction)) *opsv1alpha1.IOSXEOperationalAction {
	a := &opsv1alpha1.IOSXEOperationalAction{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       name,
			Generation: 1,
		},
		Spec: opsv1alpha1.IOSXEOperationalActionSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
			Confirm:   "dev1",
			Action: opsv1alpha1.ActionRequest{
				Kind:   opsv1alpha1.ActionKindReboot,
				Reboot: &opsv1alpha1.RebootActionArgs{Method: "COLD", DelaySeconds: 0},
			},
		},
	}
	if mutate != nil {
		mutate(a)
	}
	return a
}

func provisionCertificateAction() opsv1alpha1.ActionRequest {
	return opsv1alpha1.ActionRequest{
		Kind: opsv1alpha1.ActionKindProvisionCertificate,
		ProvisionCertificate: &opsv1alpha1.ProvisionCertificateActionArgs{
			CertificateID:        testProvisioningCertificateID,
			PublicMaterialSHA256: testPublicMaterialSHA256,
		},
	}
}

func runReconcile(t *testing.T, r *Reconciler, a *opsv1alpha1.IOSXEOperationalAction) *opsv1alpha1.IOSXEOperationalAction {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: a.Namespace, Name: a.Name}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: a.Namespace, Name: a.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	return &got
}

func newReconciler(t *testing.T, rig *rig, a *opsv1alpha1.IOSXEOperationalAction, extra ...client.Object) *Reconciler { //nolint:unparam // future tests
	t.Helper()
	scheme := newScheme(t)
	objs := []client.Object{a}
	objs = append(objs, extra...)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&opsv1alpha1.IOSXEOperationalAction{}).
		Build()
	return &Reconciler{
		Client:     c,
		DeviceName: "dev1",
		Scheme:     scheme,
		GNOI:       &staticGNOI{c: rig.client},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

// --- tests ---

func TestRebootHappyPath(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-1", nil)
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 1 {
		t.Fatalf("Reboot call count=%d", rig.sys.rebootCalls.Load())
	}
	if len(got.Finalizers) != 0 {
		t.Fatalf("finalizers retained after terminal phase: %v", got.Finalizers)
	}
}

func TestConfirmMismatchRejected(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-typo", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Confirm = "wrongname"
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatal("Reboot should not have been called on confirm mismatch")
	}
}

func TestCancelReboot(t *testing.T) {
	rig := newRig(t)
	a := newAction("cancel-1", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:         opsv1alpha1.ActionKindCancelReboot,
			CancelReboot: &opsv1alpha1.CancelRebootArgs{Message: "abort"},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.cancelRebootCalls.Load() != 1 {
		t.Fatalf("CancelReboot call count=%d", rig.sys.cancelRebootCalls.Load())
	}
}

func TestKillProcessRequiresPIDOrName(t *testing.T) {
	rig := newRig(t)
	a := newAction("kill-empty", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:        opsv1alpha1.ActionKindKillProcess,
			KillProcess: &opsv1alpha1.KillProcessArgs{Signal: "TERM"},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "PID or Name") {
		t.Fatalf("expected PID-or-Name required error, got %q", got.Status.Message)
	}
}

func TestRebootRequiresMatchingArgsBlock(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-no-args", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{Kind: opsv1alpha1.ActionKindReboot}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("Reboot dispatched despite missing args block; calls=%d", rig.sys.rebootCalls.Load())
	}
}

func TestKindArgsMismatchRejected(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-wrong-args", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:       opsv1alpha1.ActionKindReboot,
			FileRemove: &opsv1alpha1.FileRemoveArgs{Path: "flash:old.bin"},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 0 || rig.file.removeCalls.Load() != 0 {
		t.Fatalf("mismatched action dispatched: reboot=%d remove=%d", rig.sys.rebootCalls.Load(), rig.file.removeCalls.Load())
	}
}

func TestProvisionCertificateRequiresIntentArgs(t *testing.T) {
	if err := validateActionRequest(provisionCertificateAction()); err != nil {
		t.Fatalf("valid ProvisionCertificate rejected: %v", err)
	}
	err := validateActionRequest(opsv1alpha1.ActionRequest{
		Kind: opsv1alpha1.ActionKindProvisionCertificate,
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one matching args block") {
		t.Fatalf("ProvisionCertificate without args error=%v", err)
	}

	badDigest := provisionCertificateAction()
	badDigest.ProvisionCertificate.PublicMaterialSHA256 = strings.ToUpper(testPublicMaterialSHA256)
	if err := validateActionRequest(badDigest); err == nil || !strings.Contains(err.Error(), "64 lowercase hexadecimal") {
		t.Fatalf("ProvisionCertificate uppercase digest error=%v", err)
	}
}

func TestProvisionCertificateIntentMismatchRejectedBeforeDeviceRPC(t *testing.T) {
	tests := []struct {
		name             string
		configuredID     string
		configuredDigest string
	}{
		{
			name:             "certificate ID",
			configuredID:     "different-id",
			configuredDigest: testPublicMaterialSHA256,
		},
		{
			name:             "public material digest",
			configuredID:     testProvisioningCertificateID,
			configuredDigest: strings.Repeat("a", 64),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			a := newAction("provision-mismatch", func(a *opsv1alpha1.IOSXEOperationalAction) {
				a.Spec.Action = provisionCertificateAction()
			})
			r := newReconciler(t, rig, a)
			provider := &provisioningGNOI{
				staticGNOI:           &staticGNOI{c: rig.client},
				certificateID:        tt.configuredID,
				publicMaterialSHA256: tt.configuredDigest,
			}
			r.GNOI = provider
			r.CertificateProvisioner = provider

			got := runReconcile(t, r, a)
			if got.Status.Phase != opsv1alpha1.ActionPhaseRejected || got.Status.FailureReason != "ProvisioningIntentMismatch" {
				t.Fatalf("status=%+v, want rejected ProvisioningIntentMismatch", got.Status)
			}
			if got.Status.InvocationID != "" {
				t.Fatalf("invocationID=%q, want empty", got.Status.InvocationID)
			}
			if got := rig.os.verifyCalls.Load(); got != 0 {
				t.Fatalf("OS.Verify calls=%d, want 0", got)
			}
			if got := provider.clientCalls.Load(); got != 0 {
				t.Fatalf("GNOIClient calls=%d, want 0", got)
			}
			if got := provider.provisionCalls.Load(); got != 0 {
				t.Fatalf("ProvisionGNOICertificate calls=%d, want 0", got)
			}
		})
	}
}

func TestProvisionCertificateUnavailableRejectedBeforeDeviceRPC(t *testing.T) {
	rig := newRig(t)
	a := newAction("provision-unavailable", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = provisionCertificateAction()
	})
	r := newReconciler(t, rig, a)

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected || got.Status.FailureReason != "ProvisioningUnavailable" {
		t.Fatalf("status=%+v, want rejected ProvisioningUnavailable", got.Status)
	}
	baseProvider := r.GNOI.(*staticGNOI)
	if got.Status.InvocationID != "" || baseProvider.clientCalls.Load() != 0 || rig.os.verifyCalls.Load() != 0 {
		t.Fatalf("action touched device: invocationID=%q GNOIClient calls=%d OS.Verify calls=%d",
			got.Status.InvocationID, baseProvider.clientCalls.Load(), rig.os.verifyCalls.Load())
	}
}

func TestProvisionCertificateAlreadyProvisioned(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyResp = &ospb.VerifyResponse{Version: "17.18.04"}
	a := newAction("provision-existing", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = provisionCertificateAction()
	})
	r := newReconciler(t, rig, a)
	r.CertificateProvisioner = &provisioningGNOI{
		staticGNOI:           &staticGNOI{c: rig.client},
		certificateID:        testProvisioningCertificateID,
		publicMaterialSHA256: testPublicMaterialSHA256,
	}
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.os.verifyCalls.Load() != 1 {
		t.Fatalf("OS.Verify calls=%d, want 1", rig.os.verifyCalls.Load())
	}
	if !strings.Contains(got.Status.Result, `"status":"alreadyProvisioned"`) ||
		!strings.Contains(got.Status.Result, `"requestedCertificateID":"`+testProvisioningCertificateID+`"`) ||
		!strings.Contains(got.Status.Result, `"requestedPublicMaterialSHA256":"`+testPublicMaterialSHA256+`"`) ||
		!strings.Contains(got.Status.Result, `"version":"17.18.04"`) {
		t.Fatalf("result=%q", got.Status.Result)
	}
}

func TestProvisionCertificateAcceptedDoesNotRedispatch(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.FailedPrecondition, "Device has not been provisioned")
	a := newAction("provision-new", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = provisionCertificateAction()
	})
	r := newReconciler(t, rig, a)
	provider := &provisioningGNOI{
		staticGNOI:           &staticGNOI{c: rig.client},
		certificateID:        testProvisioningCertificateID,
		publicMaterialSHA256: testPublicMaterialSHA256,
		version:              "17.18.04",
	}
	r.CertificateProvisioner = provider

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Result, `"status":"provisioned"`) ||
		!strings.Contains(got.Status.Result, `"certificateID":"`+testProvisioningCertificateID+`"`) ||
		!strings.Contains(got.Status.Result, `"publicMaterialSHA256":"`+testPublicMaterialSHA256+`"`) ||
		!strings.Contains(got.Status.Result, `"version":"17.18.04"`) {
		t.Fatalf("result=%q", got.Status.Result)
	}
	_ = runReconcile(t, r, a)
	if got := provider.provisionCalls.Load(); got != 1 {
		t.Fatalf("ProvisionGNOICertificate calls=%d, want 1", got)
	}
	if got := rig.os.verifyCalls.Load(); got != 1 {
		t.Fatalf("OS.Verify calls=%d, want 1", got)
	}
}

func TestProvisionCertificateFailureIsTerminal(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.FailedPrecondition, "Device has not been provisioned")
	a := newAction("provision-failed", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = provisionCertificateAction()
	})
	r := newReconciler(t, rig, a)
	provider := &provisioningGNOI{
		staticGNOI:           &staticGNOI{c: rig.client},
		certificateID:        testProvisioningCertificateID,
		publicMaterialSHA256: testPublicMaterialSHA256,
		provisionErr:         status.Error(codes.PermissionDenied, "certificate rejected"),
	}
	r.CertificateProvisioner = provider

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "certificate rejected") {
		t.Fatalf("message=%q", got.Status.Message)
	}
	if got := provider.provisionCalls.Load(); got != 1 {
		t.Fatalf("ProvisionGNOICertificate calls=%d, want 1", got)
	}
}

func TestProvisionCertificateIndeterminateFailureWarnsAgainstRetry(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.FailedPrecondition, "Device has not been provisioned")
	a := newAction("provision-indeterminate", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = provisionCertificateAction()
	})
	r := newReconciler(t, rig, a)
	r.CertificateProvisioner = &provisioningGNOI{
		staticGNOI:           &staticGNOI{c: rig.client},
		certificateID:        testProvisioningCertificateID,
		publicMaterialSHA256: testPublicMaterialSHA256,
		provisionErr: &gnoi.ErrCertificateInstallIndeterminate{
			CertificateID: "cvk-gnoi-cert",
			Cause:         status.Error(codes.Unavailable, "gNXI restarted"),
		},
	}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed || got.Status.FailureReason != "CertificateInstallIndeterminate" {
		t.Fatalf("status=%+v, want failed CertificateInstallIndeterminate", got.Status)
	}
	if !strings.Contains(got.Status.Message, "do not retry") || !strings.Contains(got.Status.Message, "GNOICertGet") {
		t.Fatalf("message=%q, want reconciliation guidance", got.Status.Message)
	}
}

func TestProvisionCertificateUnexpectedResultIsIndeterminate(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.FailedPrecondition, "Device has not been provisioned")
	a := newAction("provision-unexpected-id", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = provisionCertificateAction()
	})
	r := newReconciler(t, rig, a)
	r.CertificateProvisioner = &provisioningGNOI{
		staticGNOI:               &staticGNOI{c: rig.client},
		certificateID:            testProvisioningCertificateID,
		provisionedCertificateID: "unexpected-id",
		publicMaterialSHA256:     testPublicMaterialSHA256,
	}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed || got.Status.FailureReason != "CertificateInstallIndeterminate" {
		t.Fatalf("status=%+v, want failed CertificateInstallIndeterminate", got.Status)
	}
	if !strings.Contains(got.Status.Message, "do not retry") || !strings.Contains(got.Status.Message, "unexpected-id") {
		t.Fatalf("message=%q, want mismatch and reconciliation guidance", got.Status.Message)
	}
}

func TestUnknownActionKindRejected(t *testing.T) {
	rig := newRig(t)
	a := newAction("unknown-kind", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:   opsv1alpha1.ActionKind("PowerCycle"),
			Reboot: &opsv1alpha1.RebootActionArgs{Method: "COLD"},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "unsupported action kind") {
		t.Fatalf("expected unsupported-kind message, got %q", got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("unknown action dispatched reboot; calls=%d", rig.sys.rebootCalls.Load())
	}
}

func TestRunningActionDoesNotRedispatch(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-running", nil)
	a.Status = opsv1alpha1.IOSXEOperationalActionStatus{
		Phase:        opsv1alpha1.ActionPhaseRunning,
		InvocationID: "already-invoked",
	}
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRunning {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("running action re-dispatched reboot; calls=%d", rig.sys.rebootCalls.Load())
	}
}

func TestMarkRunningConcurrentClaimSingleWinner(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-concurrent-claim", nil)
	r := newReconciler(t, rig, a)

	const contenders = 8
	start := make(chan struct{})
	results := make(chan bool, contenders)
	errs := make(chan error, contenders)
	var ready sync.WaitGroup
	ready.Add(contenders)
	for range contenders {
		go func() {
			ready.Done()
			<-start
			claimed, err := r.markRunning(context.Background(), a, time.Unix(1_700_000_000, 0).UTC())
			results <- claimed
			errs <- err
		}()
	}
	ready.Wait()
	close(start)

	claims := 0
	for range contenders {
		if err := <-errs; err != nil {
			t.Fatalf("markRunning: %v", err)
		}
		if <-results {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("successful claims=%d, want 1", claims)
	}

	var got opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.ActionPhaseRunning || got.Status.InvocationID == "" {
		t.Fatalf("status=%+v, want Running with invocation ID", got.Status)
	}
}

func TestMarkRunningRefusesDeletingAction(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-delete-race", nil)
	deletedAt := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	a.DeletionTimestamp = &deletedAt
	a.Finalizers = []string{finalizerName}
	r := newReconciler(t, rig, a)

	claimed, err := r.markRunning(context.Background(), a, deletedAt.Time)
	if err != nil {
		t.Fatalf("markRunning: %v", err)
	}
	if claimed {
		t.Fatal("deleting action was claimed for dispatch")
	}

	var got opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != "" || got.Status.InvocationID != "" {
		t.Fatalf("status=%+v, want unclaimed deleting action", got.Status)
	}
}

func TestOperationalActionEvents(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-events", nil)
	r := newReconciler(t, rig, a)
	recorder := record.NewFakeRecorder(4)
	r.Recorder = recorder
	_ = runReconcile(t, r, a)
	for _, want := range []string{"Normal Running", "Normal Succeeded"} {
		select {
		case got := <-recorder.Events:
			if !strings.Contains(got, want) {
				t.Fatalf("event=%q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %q event", want)
		}
	}
}

func TestFilePutHappyPath(t *testing.T) {
	rig := newRig(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "payload-cm"},
		BinaryData: map[string][]byte{"content": []byte("hello flash")},
	}
	a := newAction("put-1", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:    opsv1alpha1.ActionKindFilePut,
			FilePut: &opsv1alpha1.FilePutArgs{Path: "flash:dropoff.bin", ConfigMapName: "payload-cm", Permissions: 0o644},
		}
	})
	r := newReconciler(t, rig, a, cm)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.file.putCalls.Load() != 1 {
		t.Fatalf("File.Put call count=%d", rig.file.putCalls.Load())
	}
	if rig.file.putBytes.Load() != int64(len("hello flash")) {
		t.Fatalf("expected %d bytes streamed, got %d", len("hello flash"), rig.file.putBytes.Load())
	}
}

func TestFilePutMissingConfigMapFails(t *testing.T) {
	rig := newRig(t)
	a := newAction("put-missing-cm", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:    opsv1alpha1.ActionKindFilePut,
			FilePut: &opsv1alpha1.FilePutArgs{Path: "flash:dropoff.bin", ConfigMapName: "missing-cm", Permissions: 0o644},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "get ConfigMap") {
		t.Fatalf("expected ConfigMap lookup error, got %q", got.Status.Message)
	}
	if rig.file.putCalls.Load() != 0 {
		t.Fatalf("File.Put called despite missing ConfigMap; calls=%d", rig.file.putCalls.Load())
	}
}

func TestFilePutMissingContentKeyFails(t *testing.T) {
	rig := newRig(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "payload-cm"},
		BinaryData: map[string][]byte{
			"other": []byte("wrong key"),
		},
	}
	a := newAction("put-missing-key", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:    opsv1alpha1.ActionKindFilePut,
			FilePut: &opsv1alpha1.FilePutArgs{Path: "flash:dropoff.bin", ConfigMapName: "payload-cm", Permissions: 0o644},
		}
	})
	r := newReconciler(t, rig, a, cm)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "binaryData[\"content\"]") {
		t.Fatalf("expected missing binaryData content error, got %q", got.Status.Message)
	}
	if rig.file.putCalls.Load() != 0 {
		t.Fatalf("File.Put called despite missing content key; calls=%d", rig.file.putCalls.Load())
	}
}

func TestFileRemoveRejectsBarePath(t *testing.T) {
	rig := newRig(t)
	a := newAction("rm-bad", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:       opsv1alpha1.ActionKindFileRemove,
			FileRemove: &opsv1alpha1.FileRemoveArgs{Path: "tmp/foo.bin"},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
}

func TestFactoryResetDefaultsRetainCertsTrue(t *testing.T) {
	rig := newRig(t)
	a := newAction("fr-1", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:         opsv1alpha1.ActionKindFactoryReset,
			FactoryReset: &opsv1alpha1.FactoryResetArgs{},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.reset.calls.Load() != 1 {
		t.Fatalf("FactoryReset call count=%d", rig.reset.calls.Load())
	}
}

func TestFactoryResetDeviceErrorFailsWithClassifier(t *testing.T) {
	rig := newRig(t)
	rig.reset.resp = &resetpb.StartResponse{
		Response: &resetpb.StartResponse_ResetError{
			ResetError: &resetpb.ResetError{
				Detail:               "factory OS unsupported on this platform",
				FactoryOsUnsupported: true,
			},
		},
	}
	a := newAction("fr-device-error", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:         opsv1alpha1.ActionKindFactoryReset,
			FactoryReset: &opsv1alpha1.FactoryResetArgs{FactoryOS: true},
		}
	})
	r := newReconciler(t, rig, a)
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "factory_os_unsupported") {
		t.Fatalf("expected classifier in message, got %q", got.Status.Message)
	}
}

func TestTerminalPhasesNotRerun(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-twice", nil)
	a.Status = opsv1alpha1.IOSXEOperationalActionStatus{Phase: opsv1alpha1.ActionPhaseSucceeded}
	r := newReconciler(t, rig, a)
	_ = runReconcile(t, r, a)
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("terminal-phase action re-ran reboot; calls=%d", rig.sys.rebootCalls.Load())
	}
}
