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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

// --- fixtures ---

type fakeOS struct {
	ospb.UnimplementedOSServer
	verifyVersion string
	activateErr   error
}

func (f *fakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	return &ospb.VerifyResponse{Version: f.verifyVersion}, nil
}

func (f *fakeOS) Activate(context.Context, *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	if f.activateErr != nil {
		return nil, f.activateErr
	}
	return &ospb.ActivateResponse{Response: &ospb.ActivateResponse_ActivateOk{ActivateOk: &ospb.ActivateOK{}}}, nil
}

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

type localImageResolver struct{}

func (localImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	return &ResolvedImage{Local: true, Cleanup: func() error { return nil }}, nil
}

type erroringImageResolver struct{ err error }

func (e erroringImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	return nil, e.err
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
	up := newUpgrade("upgrade-mismatch", nil)
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
	up := newUpgrade("upgrade-mismatch-norollback", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.RollbackOnFailure = &rollbackOff
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
