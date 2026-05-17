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

package softwareupgrade

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

// --- fixtures ---

type fakeOS struct {
	ospb.UnimplementedOSServer
	verifyVersion       string
	verifyVersions      []string
	verifyCalls         int
	verifyErr           error
	activateErr         error
	activateWantVersion string
	activateVersion     string
	validatedVersion    string
	installBytes        int
}

func (f *fakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	if len(f.verifyVersions) > 0 {
		idx := f.verifyCalls
		if idx >= len(f.verifyVersions) {
			idx = len(f.verifyVersions) - 1
		}
		f.verifyCalls++
		return &ospb.VerifyResponse{Version: f.verifyVersions[idx]}, nil
	}
	return &ospb.VerifyResponse{Version: f.verifyVersion}, nil
}

func (f *fakeOS) Activate(_ context.Context, req *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	f.activateVersion = req.Version
	if f.activateErr != nil {
		return nil, f.activateErr
	}
	if f.activateWantVersion != "" && req.Version != f.activateWantVersion {
		return &ospb.ActivateResponse{Response: &ospb.ActivateResponse_ActivateError{
			ActivateError: &ospb.ActivateError{Detail: "Version not present on device"},
		}}, nil
	}
	return &ospb.ActivateResponse{Response: &ospb.ActivateResponse_ActivateOk{ActivateOk: &ospb.ActivateOK{}}}, nil
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
	if err := stream.Send(&ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferReady{TransferReady: &ospb.TransferReady{}},
	}); err != nil {
		return err
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
		case *ospb.InstallRequest_TransferEnd:
			if _, err := stream.Recv(); err != io.EOF {
				if err == nil {
					return status.Error(codes.FailedPrecondition, "expected client half-close")
				}
				return err
			}
			version := f.validatedVersion
			if version == "" {
				version = req.Version
			}
			return stream.Send(&ospb.InstallResponse{
				Response: &ospb.InstallResponse_Validated{
					Validated: &ospb.Validated{Version: version, Description: "validated"},
				},
			})
		default:
			return status.Errorf(codes.InvalidArgument, "unexpected install request %T", r)
		}
	}
}

type unavailableGNOI struct{ err error }

func (u unavailableGNOI) GNOIClient(context.Context) (*gnoi.Client, error) { return nil, u.err }

type fakeSys struct {
	syspb.UnimplementedSystemServer
	timeErr error
}

func (f *fakeSys) Time(context.Context, *syspb.TimeRequest) (*syspb.TimeResponse, error) {
	if f.timeErr != nil {
		return nil, f.timeErr
	}
	return &syspb.TimeResponse{Time: uint64(time.Now().UnixNano())}, nil
}

type rig struct {
	os     *fakeOS
	sys    *fakeSys
	client *gnoi.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	r := &rig{os: &fakeOS{verifyVersion: "17.15.01a"}, sys: &fakeSys{}}
	ospb.RegisterOSServer(srv, r.os)
	syspb.RegisterSystemServer(srv, r.sys)
	filepb.RegisterFileServer(srv, filepb.UnimplementedFileServer{})
	certpb.RegisterCertificateManagementServer(srv, certpb.UnimplementedCertificateManagementServer{})
	resetpb.RegisterFactoryResetServer(srv, resetpb.UnimplementedFactoryResetServer{})
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

type staticGNOI struct{ c *gnoi.Client }

func (s *staticGNOI) GNOIClient(context.Context) (*gnoi.Client, error) { return s.c, nil }

type staticTP struct{ tr transport.Interface }

func (s *staticTP) GetTransport() transport.Interface { return s.tr }

type fakeTransport struct {
	caps transport.Capabilities
	raw  []byte
}

func (f *fakeTransport) Capabilities() transport.Capabilities { return f.caps }
func (f *fakeTransport) Fetch(context.Context, string) ([]byte, error) {
	return f.raw, nil
}
func (f *fakeTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}
func (f *fakeTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return transport.ErrUnsupported
}
func (f *fakeTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (f *fakeTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (f *fakeTransport) SaveStartup(context.Context) error                 { return nil }
func (f *fakeTransport) Close() error                                      { return nil }

type localImageResolver struct{}

func (localImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	return &ResolvedImage{Local: true, Cleanup: func() error { return nil }}, nil
}

type erroringImageResolver struct{ err error }

func (e erroringImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	return nil, e.err
}

type countingImageResolver struct {
	calls int
}

func (r *countingImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	r.calls++
	return &ResolvedImage{
		Reader:  strings.NewReader("image"),
		Size:    int64(len("image")),
		Cleanup: func() error { return nil },
	}, nil
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

func newUpgrade(name string, mutate func(*opsv1alpha1.IOSXESoftwareUpgrade)) *opsv1alpha1.IOSXESoftwareUpgrade {
	up := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       name,
			Generation: 1,
		},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{
			DeviceRef:     configv1alpha1.DeviceRef{Name: "dev1"},
			TargetVersion: "17.15.01a",
			Strategy:      opsv1alpha1.UpgradeStrategyReload,
			ImageSource:   opsv1alpha1.UpgradeImageSource{LocalPath: "flash:cat9k.bin"},
		},
	}
	if mutate != nil {
		mutate(up)
	}
	return up
}

func runReconcile(t *testing.T, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade, max int) *opsv1alpha1.IOSXESoftwareUpgrade {
	t.Helper()
	for i := 0; i < max; i++ {
		res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
		if err != nil {
			t.Fatalf("Reconcile (iter %d): %v", i, err)
		}
		var got opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
			t.Fatalf("Get (iter %d): %v", i, err)
		}
		if isTerminal(got.Status.Phase) {
			return &got
		}
		if res.RequeueAfter == 0 {
			// Continue to drive the loop manually to exercise the state machine.
		}
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	_ = r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got)
	return &got
}

func isTerminal(p opsv1alpha1.UpgradePhase) bool {
	switch p {
	case opsv1alpha1.UpgradePhaseSucceeded,
		opsv1alpha1.UpgradePhaseFailed,
		opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseValidationFailed,
		opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseRebootTimeout,
		opsv1alpha1.UpgradePhaseCancelled:
		return true
	}
	return false
}

func conditionReason(conditions []metav1.Condition, typ string) string {
	for _, cond := range conditions {
		if cond.Type == typ {
			return cond.Reason
		}
	}
	return ""
}

func newReconciler(t *testing.T, rig *rig, up *opsv1alpha1.IOSXESoftwareUpgrade) *Reconciler {
	t.Helper()
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(up).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		Build()
	return &Reconciler{
		Client:        c,
		DeviceName:    "dev1",
		Scheme:        scheme,
		GNOI:          &staticGNOI{c: rig.client},
		ImageResolver: localImageResolver{},
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

// --- tests ---

func TestHappyPathLocalPathReloadStrategy(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersions = []string{"17.14.01a", "17.15.01a"}
	up := newUpgrade("upgrade-1", nil)
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 12)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if got.Status.RunningVersion != "17.15.01a" {
		t.Fatalf("RunningVersion=%q", got.Status.RunningVersion)
	}
	if got.Status.CompletionTime == nil {
		t.Fatal("CompletionTime not set on Succeeded")
	}
}

func TestNoRebootStrategyStopsAtActivate(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersions = []string{"17.14.01a"}
	up := newUpgrade("upgrade-noreboot", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 10)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "not rebooted") {
		t.Fatalf("expected NoReboot message, got %q", got.Status.Message)
	}
}

func TestUnsupportedSystemServiceStillVerifiesAfterActivation(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersions = []string{"17.14.01a", "17.15.01a"}
	rig.sys.timeErr = status.Error(codes.Unimplemented, "")
	up := newUpgrade("upgrade-system-unsupported", nil)
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 12)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.RunningVersion != "17.15.01a" {
		t.Fatalf("RunningVersion=%q", got.Status.RunningVersion)
	}
}

func TestResolvingSucceedsWhenTargetAlreadyRunning(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-already-running", nil)
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 4)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want 0", resolver.calls)
	}
	if got.Status.RunningVersion != "17.15.01a" {
		t.Fatalf("RunningVersion=%q", got.Status.RunningVersion)
	}
	if reason := conditionReason(got.Status.Conditions, "Ready"); reason != "AlreadyRunning" {
		t.Fatalf("Ready reason=%q", reason)
	}
}

func TestResolvingWaitsForGNOIReachabilityBeforeImageResolution(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-resolve-waits-gnoi", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
	})
	r := newReconciler(t, rig, up)
	r.GNOI = unavailableGNOI{err: errors.New("connection refused")}
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != awaitingReachabilityPoll {
		t.Fatalf("RequeueAfter=%v, want %v", res.RequeueAfter, awaitingReachabilityPoll)
	}
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want 0", resolver.calls)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseResolving {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if reason := conditionReason(got.Status.Conditions, "Ready"); reason != "DeviceUnreachable" {
		t.Fatalf("Ready reason=%q", reason)
	}
}

func TestMarkTransferCompleteSetsTerminalProgress(t *testing.T) {
	up := newUpgrade("upgrade-progress", nil)
	up.Status.TransferProgress = &opsv1alpha1.UpgradeTransferProgress{
		BytesTransferred: 999,
		TotalBytes:       1000,
		Percent:          99,
	}
	markTransferComplete(up)
	if up.Status.TransferProgress.BytesTransferred != 1000 {
		t.Fatalf("BytesTransferred=%d", up.Status.TransferProgress.BytesTransferred)
	}
	if up.Status.TransferProgress.Percent != 100 {
		t.Fatalf("Percent=%d", up.Status.TransferProgress.Percent)
	}
}

func TestVerifyMismatchTriggersRollback(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a" // doesn't match target 17.15.01a
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-mismatch", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
		up.Status.StartTime = &start
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 12)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseRolledBack {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "RolledBack" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
}

func TestVerifyMismatchWithoutRollbackFails(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rollbackOff := false
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-mismatch-norollback", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RollbackOnFailure = &rollbackOff
		up.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
		up.Status.StartTime = &start
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 12)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "VerifyMismatch" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
}

func TestAwaitingReachabilityWaitsWhenDeviceStillRunsOldVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.18.03.0.5496.1776157760"
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-activation-settling", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		up.Status.StartTime = &start
	})
	r := newReconciler(t, rig, up)

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != awaitingReachabilityPoll {
		t.Fatalf("RequeueAfter=%v, want %v", res.RequeueAfter, awaitingReachabilityPoll)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.RunningVersion != "17.18.03.0.5496.1776157760" {
		t.Fatalf("RunningVersion=%q", got.Status.RunningVersion)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason != "VersionPending" {
		t.Fatalf("Verified reason=%q", reason)
	}
}

func TestAwaitingReachabilityFailsOldVersionAfterTimeout(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.18.03.0.5496.1776157760"
	start := metav1.Time{Time: time.Unix(1_699_990_000, 0).UTC()}
	up := newUpgrade("upgrade-activation-timeout", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		up.Status.StartTime = &start
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "ActivationDidNotConverge" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
}

func TestAwaitingReachabilityTimeoutUsesActivationTime(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.18.03.0.5496.1776157760"
	start := metav1.Time{Time: time.Unix(1_699_990_000, 0).UTC()}
	activated := metav1.Time{Time: time.Unix(1_699_999_980, 0).UTC()}
	up := newUpgrade("upgrade-activation-time-reference", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		up.Status.StartTime = &start
		up.Status.Conditions = []metav1.Condition{
			{
				Type:               "Activated",
				Status:             metav1.ConditionTrue,
				Reason:             "Activated",
				Message:            "activate accepted",
				LastTransitionTime: activated,
			},
		}
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
}

func TestVerifyMismatchWaitsWhenTargetStillStaged(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.18.02.0.4112.1766116039"
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-verify-staged", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.03"
		up.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
		up.Status.StartTime = &start
	})
	r := newReconciler(t, rig, up)
	r.TP = &staticTP{tr: &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindRESTCONF},
		raw: []byte(`{
		  "Cisco-IOS-XE-install-oper:install-location-information": [
		    {
		      "install-version-info": [
		        {"version": "17.18.03.0.5496", "version-extension": "1776157760", "current": "install-version-state-provisioned-committed"}
		      ]
		    }
		  ]
		}`),
	}}

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != 30*time.Second {
		t.Fatalf("RequeueAfter=%v, want 30s", res.RequeueAfter)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseVerifying {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if got.Status.ValidatedVersion != "17.18.03.0.5496.1776157760" {
		t.Fatalf("ValidatedVersion=%q", got.Status.ValidatedVersion)
	}
}

func TestInvalidImageSourceFailsPreflight(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-bad-src", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{} // empty — invalid
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 5)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
}

func TestMaintenanceWindowDefersToNotBefore(t *testing.T) {
	rig := newRig(t)
	future := metav1.Time{Time: time.Unix(2_000_000_000, 0)}
	up := newUpgrade("upgrade-window", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotBefore: &future}
	})
	r := newReconciler(t, rig, up)
	// First call adds finalizer; second runs Pending.
	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePending {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "maintenance window") {
		t.Fatalf("expected maintenance-window message, got %q", got.Status.Message)
	}
}

func TestMaintenanceWindowExpiredTerminal(t *testing.T) {
	rig := newRig(t)
	past := metav1.Time{Time: time.Unix(1_500_000_000, 0)}
	up := newUpgrade("upgrade-window-past", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotAfter: &past}
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "MaintenanceWindowExpired" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
}

func TestImageResolveErrorTerminalFails(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolve-err", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{URL: "https://example.invalid/img.bin", SHA256: "deadbeef" + strings.Repeat("0", 56)}
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = erroringImageResolver{err: errors.New("network down")}
	got := runReconcile(t, r, up, 5)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "ImageResolveFailed" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
}

func TestTransferPreflightsGNOIBeforeResolvingImage(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.Unavailable, "connect: connection refused")
	up := newUpgrade("upgrade-gnoi-preflight", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.MaxRetries = 1
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.RetryCount = 1
	})
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 3)
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want 0", resolver.calls)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "TransferMaxRetries" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if !strings.Contains(got.Status.Message, "gnoi OS.Verify preflight") {
		t.Fatalf("expected preflight error in message, got %q", got.Status.Message)
	}
}

func TestActivatesDeviceValidatedVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersions = []string{
		"17.18.02.0.4112.1766116039",
		"17.18.03.0.5000.1234567890",
	}
	rig.os.validatedVersion = "17.18.03"
	rig.os.activateWantVersion = "17.18.03.0.5000.1234567890"
	up := newUpgrade("upgrade-validated-version", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.TargetVersion = "17.18.03"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:    "https://example.invalid/cat9k.bin",
			SHA256: "deadbeef" + strings.Repeat("0", 56),
		}
	})
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver
	r.TP = &staticTP{tr: &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindRESTCONF},
		raw: []byte(`{
		  "Cisco-IOS-XE-install-oper:install-location-information": [
		    {
		      "install-version-info": [
		        {"version": "17.18.02.0.4112", "version-extension": "1766116039", "current": "install-version-state-provisioned-committed"},
		        {"version": "17.18.03.0.5000", "version-extension": "1234567890", "current": "install-version-state-in-progress"}
		      ]
		    }
		  ]
		}`),
	}}

	got := runReconcile(t, r, up, 12)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if rig.os.activateVersion != "17.18.03.0.5000.1234567890" {
		t.Fatalf("Activate version=%q, want validated version", rig.os.activateVersion)
	}
	if got.Status.ValidatedVersion != "17.18.03.0.5000.1234567890" {
		t.Fatalf("ValidatedVersion=%q", got.Status.ValidatedVersion)
	}
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want staged-version shortcut", resolver.calls)
	}
}

func TestFinalizerAddedThenClearedOnDelete(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-delete", nil)
	r := newReconciler(t, rig, up)

	// First reconcile adds finalizer.
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		if f == Finalizer {
			found = true
		}
	}
	if !found {
		t.Fatalf("finalizer not added; got %v", got.Finalizers)
	}

	// Simulate deletion through the fake client's Delete (which sets
	// DeletionTimestamp internally because the object has finalizers).
	if err := r.Client.Delete(context.Background(), &got); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		// fake client GC removes the object once the finalizer clears
		return
	}
	for _, f := range got.Finalizers {
		if f == Finalizer {
			t.Fatalf("finalizer not cleared after deletion reconcile")
		}
	}
}

func TestVersionMatches(t *testing.T) {
	cases := []struct {
		device, target string
		want           bool
	}{
		{"26.01.01.0.340", "26.01.01.0.340", true},       // exact
		{"26.01.01.0.340", "26.01.01", true},             // short-form prefix
		{"17.18.02.0.4112.1766116039", "17.18.02", true}, // long oper-data form
		{"26.01.01.0.340", "26.01", true},                // even shorter prefix
		{"26.01.011", "26.01.01", false},                 // suffix-extension, not dot boundary
		{"26.01.01a", "26.01.01", false},                 // letter-suffix not on dot boundary
		{"17.15.01a", "17.15.01a", true},                 // exact release-format
		{"17.15.01", "17.15.01a", false},                 // missing trailing letter
		{"", "26.01.01", false},
	}
	for _, c := range cases {
		got := versionMatches(c.device, c.target)
		if got != c.want {
			t.Errorf("versionMatches(%q, %q) = %v, want %v", c.device, c.target, got, c.want)
		}
	}
}
