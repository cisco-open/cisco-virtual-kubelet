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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"
	commonpb "github.com/openconfig/gnoi/types"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

const Finalizer = upgradeFinalizer

// --- fixtures ---

type fakeOS struct {
	ospb.UnimplementedOSServer
	mu                   sync.Mutex
	verifyVersion        string
	verifyVersions       []string
	verifyCalls          int
	verifyErr            error
	verifyErrs           []error
	individualInstall    bool
	verifyStandby        *ospb.VerifyStandby
	verifyStandbys       []*ospb.VerifyStandby
	activateErr          error
	activateCalls        int
	activateStandby      []bool
	activateRemaining    time.Duration
	activateDeadlineSet  bool
	activateWantVersion  string
	activateVersion      string
	activateEntered      chan struct{}
	activateRelease      chan struct{}
	validatedVersion     string
	installBytes         int
	installCalls         int
	installStandby       []bool
	installErrAfterReady error
	installEntered       chan struct{}
	installRelease       chan struct{}
}

func (f *fakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	idx := f.verifyCalls
	f.verifyCalls++
	if len(f.verifyErrs) > 0 {
		if err := f.verifyErrs[min(idx, len(f.verifyErrs)-1)]; err != nil {
			return nil, err
		}
	}
	version := f.verifyVersion
	if len(f.verifyVersions) > 0 {
		version = f.verifyVersions[min(idx, len(f.verifyVersions)-1)]
	}
	standby := f.verifyStandby
	if len(f.verifyStandbys) > 0 {
		standby = f.verifyStandbys[min(idx, len(f.verifyStandbys)-1)]
	}
	return &ospb.VerifyResponse{
		Version:                     version,
		IndividualSupervisorInstall: f.individualInstall,
		VerifyStandby:               standby,
	}, nil
}

func (f *fakeOS) Activate(ctx context.Context, req *ospb.ActivateRequest) (*ospb.ActivateResponse, error) {
	f.mu.Lock()
	f.activateCalls++
	f.activateVersion = req.Version
	f.activateStandby = append(f.activateStandby, req.StandbySupervisor)
	if deadline, ok := ctx.Deadline(); ok {
		f.activateDeadlineSet = true
		f.activateRemaining = time.Until(deadline)
	}
	activateErr := f.activateErr
	activateWantVersion := f.activateWantVersion
	activateEntered := f.activateEntered
	activateRelease := f.activateRelease
	f.mu.Unlock()
	if activateEntered != nil {
		select {
		case activateEntered <- struct{}{}:
		default:
		}
	}
	if activateRelease != nil {
		select {
		case <-activateRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if activateErr != nil {
		return nil, activateErr
	}
	if activateWantVersion != "" && req.Version != activateWantVersion {
		return &ospb.ActivateResponse{Response: &ospb.ActivateResponse_ActivateError{
			ActivateError: &ospb.ActivateError{
				Type:   ospb.ActivateError_NON_EXISTENT_VERSION,
				Detail: "Version not present on device",
			},
		}}, nil
	}
	return &ospb.ActivateResponse{Response: &ospb.ActivateResponse_ActivateOk{ActivateOk: &ospb.ActivateOK{}}}, nil
}

func readyStandby(id, version string) *ospb.VerifyStandby {
	return &ospb.VerifyStandby{State: &ospb.VerifyStandby_VerifyResponse{
		VerifyResponse: &ospb.StandbyResponse{Id: id, Version: version},
	}}
}

func unavailableStandby() *ospb.VerifyStandby {
	return &ospb.VerifyStandby{State: &ospb.VerifyStandby_StandbyState{
		StandbyState: &ospb.StandbyState{State: ospb.StandbyState_UNAVAILABLE},
	}}
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
	f.mu.Lock()
	f.installCalls++
	f.installStandby = append(f.installStandby, req.GetStandbySupervisor())
	installErrAfterReady := f.installErrAfterReady
	installEntered := f.installEntered
	installRelease := f.installRelease
	f.mu.Unlock()
	if installEntered != nil {
		select {
		case installEntered <- struct{}{}:
		default:
		}
	}
	if installRelease != nil {
		select {
		case <-installRelease:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	if err := stream.Send(&ospb.InstallResponse{
		Response: &ospb.InstallResponse_TransferReady{TransferReady: &ospb.TransferReady{}},
	}); err != nil {
		return err
	}
	if installErrAfterReady != nil {
		return installErrAfterReady
	}
	for {
		next, err := stream.Recv()
		if err != nil {
			return err
		}
		switch r := next.Request.(type) {
		case *ospb.InstallRequest_TransferContent:
			f.mu.Lock()
			f.installBytes += len(r.TransferContent)
			installBytes := f.installBytes
			f.mu.Unlock()
			if err := stream.Send(&ospb.InstallResponse{
				Response: &ospb.InstallResponse_TransferProgress{
					TransferProgress: &ospb.TransferProgress{BytesReceived: uint64(installBytes)},
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

type fakeFile struct {
	filepb.UnimplementedFileServer
	mu       sync.Mutex
	body     []byte
	getCalls int
	onGet    func()
	block    bool
}

func (f *fakeFile) Get(_ *filepb.GetRequest, stream grpc.ServerStreamingServer[filepb.GetResponse]) error {
	f.mu.Lock()
	f.getCalls++
	body := append([]byte(nil), f.body...)
	onGet := f.onGet
	block := f.block
	f.mu.Unlock()
	if onGet != nil {
		onGet()
	}
	if block {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if len(body) == 0 {
		body = []byte("device-image")
	}
	if err := stream.Send(&filepb.GetResponse{Response: &filepb.GetResponse_Contents{Contents: body}}); err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	return stream.Send(&filepb.GetResponse{Response: &filepb.GetResponse_Hash{Hash: &commonpb.HashType{
		Method: commonpb.HashType_SHA256,
		Hash:   sum[:],
	}}})
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
	file   *fakeFile
	client *gnoi.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	r := &rig{
		os:   &fakeOS{verifyVersion: "17.15.01a", validatedVersion: "17.15.01a"},
		sys:  &fakeSys{},
		file: &fakeFile{},
	}
	ospb.RegisterOSServer(srv, r.os)
	syspb.RegisterSystemServer(srv, r.sys)
	filepb.RegisterFileServer(srv, r.file)
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

type staticGNOI struct {
	c     *gnoi.Client
	calls atomic.Int64
}

func (s *staticGNOI) GNOIClient(context.Context) (*gnoi.Client, error) {
	s.calls.Add(1)
	return s.c, nil
}

type fakeLifecycle struct {
	mu sync.Mutex

	inspectImage softwarelifecycle.InventoryImage
	inspectErr   error
	inspectFn    func(string) (softwarelifecycle.InventoryImage, error)
	inspectCalls int

	registerResult  softwarelifecycle.DeviceFileRegistration
	registerErr     error
	registerCalls   []softwarelifecycle.DeviceFileRequest
	registerRemain  time.Duration
	registerDLSet   bool
	registerEntered chan struct{}
	registerRelease chan struct{}

	observeResult softwarelifecycle.DeviceFileObservation
	observeErr    error
	observeCalls  int
}

func (f *fakeLifecycle) Inspect(_ context.Context, target string) (softwarelifecycle.InventoryImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	if f.inspectFn != nil {
		return f.inspectFn(target)
	}
	if f.inspectErr != nil {
		return softwarelifecycle.InventoryImage{}, f.inspectErr
	}
	image := f.inspectImage
	if image.Version == "" {
		image.Version = target
	}
	if image.State == "" {
		image.State = softwarelifecycle.InventoryStateInstalled
	}
	return image, nil
}

func (f *fakeLifecycle) RegisterDeviceFile(ctx context.Context, req softwarelifecycle.DeviceFileRequest) (softwarelifecycle.DeviceFileRegistration, error) {
	f.mu.Lock()
	f.registerCalls = append(f.registerCalls, req)
	if deadline, ok := ctx.Deadline(); ok {
		f.registerDLSet = true
		f.registerRemain = time.Until(deadline)
	}
	registerErr := f.registerErr
	result := f.registerResult
	registerEntered := f.registerEntered
	registerRelease := f.registerRelease
	f.mu.Unlock()
	if registerEntered != nil {
		select {
		case registerEntered <- struct{}{}:
		default:
		}
	}
	if registerRelease != nil {
		select {
		case <-registerRelease:
		case <-ctx.Done():
			return softwarelifecycle.DeviceFileRegistration{}, ctx.Err()
		}
	}
	if registerErr != nil {
		return softwarelifecycle.DeviceFileRegistration{}, registerErr
	}
	if result.OperationID == "" {
		result.OperationID = req.OperationID
	}
	return result, nil
}

func (f *fakeLifecycle) ValidateDeviceFilePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return softwarelifecycle.ErrInvalidDevicePath
	}
	return nil
}

func (f *fakeLifecycle) ObserveDeviceFile(_ context.Context, _, _ string) (softwarelifecycle.DeviceFileObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observeCalls++
	return f.observeResult, f.observeErr
}

type erroringImageResolver struct{ err error }

func (e erroringImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	return nil, e.err
}

type countingErrorImageResolver struct {
	calls int
	err   error
}

func (r *countingErrorImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	r.calls++
	return nil, r.err
}

type deleteUpgradeOnNthGetReader struct {
	client.Reader
	writer  client.Client
	key     types.NamespacedName
	trigger int
	vanish  bool

	mu   sync.Mutex
	gets int
}

func (r *deleteUpgradeOnNthGetReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	r.mu.Lock()
	if key == r.key {
		r.gets++
	}
	shouldDelete := key == r.key && r.gets == r.trigger
	r.mu.Unlock()
	if shouldDelete {
		var current opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.writer.Get(ctx, key, &current); err != nil {
			return err
		}
		if r.vanish {
			current.Finalizers = nil
			if err := r.writer.Update(ctx, &current); err != nil {
				return err
			}
			if err := r.writer.Get(ctx, key, &current); err != nil {
				return err
			}
		}
		if err := r.writer.Delete(ctx, &current); err != nil {
			return err
		}
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

type deadlineImageResolver struct {
	remaining       time.Duration
	waitForDeadline bool
}

func (r *deadlineImageResolver) Resolve(ctx context.Context, _ string, _ opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("resolver context has no deadline")
	}
	r.remaining = time.Until(deadline)
	if r.waitForDeadline {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, errors.New("stop after observing resolver deadline")
}

type countingImageResolver struct {
	calls     int
	body      string
	digest    string
	size      int64
	onResolve func()
}

func (r *countingImageResolver) Resolve(context.Context, string, opsv1alpha1.UpgradeImageSource) (*ResolvedImage, error) {
	r.calls++
	if r.onResolve != nil {
		r.onResolve()
	}
	body := r.body
	if body == "" {
		body = "image"
	}
	digest := r.digest
	if digest == "" {
		digest = "sha256:" + strings.Repeat("a", 64)
	}
	size := r.size
	if size == 0 {
		size = int64(len(body))
	}
	return &ResolvedImage{
		Reader:  strings.NewReader(body),
		Size:    size,
		Digest:  digest,
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
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
			ExecutionModel: opsv1alpha1.UpgradeExecutionModelAtMostOnceV1,
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
		opsv1alpha1.UpgradePhaseStagedForNextBoot,
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
		Client:     c,
		DeviceName: "dev1",
		GNOI:       &staticGNOI{c: rig.client},
		Lifecycle:  &fakeLifecycle{},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

// --- tests ---

func TestUpgradeExecutionModelSafeAdoption(t *testing.T) {
	tests := []struct {
		name      string
		phase     opsv1alpha1.UpgradePhase
		wantPhase opsv1alpha1.UpgradePhase
	}{
		{name: "new", wantPhase: opsv1alpha1.UpgradePhasePending},
		{name: "pending", phase: opsv1alpha1.UpgradePhasePending, wantPhase: opsv1alpha1.UpgradePhasePending},
		{name: "resolving", phase: opsv1alpha1.UpgradePhaseResolving, wantPhase: opsv1alpha1.UpgradePhaseResolving},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-adopt-"+tt.name, func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{upgradeFinalizer}
				up.Status.ExecutionModel = ""
				up.Status.Phase = tt.phase
			})
			r := newReconciler(t, rig, up)

			got := runReconcile(t, r, up, 1)
			if got.Status.ExecutionModel != opsv1alpha1.UpgradeExecutionModelAtMostOnceV1 {
				t.Fatalf("executionModel=%q, want %q", got.Status.ExecutionModel, opsv1alpha1.UpgradeExecutionModelAtMostOnceV1)
			}
			if got.Status.Phase != tt.wantPhase {
				t.Fatalf("phase=%q, want %q", got.Status.Phase, tt.wantPhase)
			}
			if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("safe adoption performed device RPCs: verify=%d install=%d activate=%d",
					rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
			}
		})
	}
}

func TestUnknownUpgradeExecutionModelFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		phase opsv1alpha1.UpgradePhase
	}{
		{name: "pending", phase: opsv1alpha1.UpgradePhasePending},
		{name: "resolving", phase: opsv1alpha1.UpgradePhaseResolving},
		{name: "activating", phase: opsv1alpha1.UpgradePhaseActivating},
		{name: "known-terminal", phase: opsv1alpha1.UpgradePhaseSucceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-unknown-model-"+tt.name, func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("unknown-model-uid-" + tt.name)
				up.Finalizers = []string{upgradeFinalizer}
				up.Status.ExecutionModel = opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2")
				up.Status.Phase = tt.phase
			})
			r := newReconciler(t, rig, up)
			r.DeviceNamespace = up.Namespace
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace, TTL: 26 * time.Hour}

			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(up)})
			if err != nil {
				t.Fatalf("reconcile unknown execution model: %v", err)
			}
			if result.RequeueAfter <= 0 {
				t.Fatalf("RequeueAfter=%s, want retry for a compatible controller", result.RequeueAfter)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
				t.Fatalf("get upgrade: %v", err)
			}
			if got.Status.Phase != tt.phase || got.Status.FailureReason != "" {
				t.Fatalf("future status was rewritten: phase=%q reason=%q", got.Status.Phase, got.Status.FailureReason)
			}
			if got.Status.ExecutionModel != opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2") {
				t.Fatalf("unknown execution model was overwritten: %q", got.Status.ExecutionModel)
			}
			if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("unknown execution model performed device RPCs: verify=%d install=%d activate=%d",
					rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
			}

			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: leaseName}, &lease); err != nil {
				t.Fatalf("unknown execution model did not conservatively retain Lease: %v", err)
			}
		})
	}
}

func TestUnknownExecutionModelAddsFinalizerBeforeHoldingLease(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-unknown-model-finalizer", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("unknown-model-finalizer-uid")
		up.Status.ExecutionModel = opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2")
		up.Status.Phase = opsv1alpha1.UpgradePhasePending
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = up.Namespace
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace, TTL: 26 * time.Hour}
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(up)}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
		t.Fatalf("get upgrade: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&got, upgradeFinalizer) {
		t.Fatalf("finalizers=%v, want %q before quarantine acquisition", got.Finalizers, upgradeFinalizer)
	}
	if got.Status.ExecutionModel != opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2") || got.Status.Phase != opsv1alpha1.UpgradePhasePending {
		t.Fatalf("first reconcile rewrote future status: %+v", got.Status)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	leaseKey := types.NamespacedName{Namespace: up.Namespace, Name: leaseName}
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), leaseKey, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("quarantine Lease exists before finalizer is durable: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if err := r.Client.Get(context.Background(), leaseKey, &lease); err != nil {
		t.Fatalf("second reconcile did not hold quarantine Lease: %v", err)
	}
	if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
		t.Fatalf("unknown execution model performed device RPCs: verify=%d install=%d activate=%d",
			rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
	}
}

func TestLegacyInFlightUpgradeIsQuarantinedWithoutReplay(t *testing.T) {
	for _, phase := range []opsv1alpha1.UpgradePhase{
		opsv1alpha1.UpgradePhaseStaging,
		opsv1alpha1.UpgradePhaseTransferring,
		opsv1alpha1.UpgradePhaseTransferInterrupted,
		opsv1alpha1.UpgradePhaseValidating,
		opsv1alpha1.UpgradePhaseActivating,
		opsv1alpha1.UpgradePhaseAwaitingReachability,
		opsv1alpha1.UpgradePhaseVerifying,
		opsv1alpha1.UpgradePhaseRollingBack,
		opsv1alpha1.UpgradePhase("FutureMutating"),
	} {
		t.Run(string(phase), func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-legacy-"+strings.ToLower(string(phase)), func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("legacy-upgrade-uid-" + strings.ToLower(string(phase)))
				up.Finalizers = []string{upgradeFinalizer}
				up.Status.ExecutionModel = ""
				up.Status.Phase = phase
				up.Status.ValidatedVersion = up.Spec.TargetVersion
				up.Status.PreviousVersion = "17.14.01a"
			})
			r := newReconciler(t, rig, up)
			r.DeviceNamespace = up.Namespace
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace, TTL: 26 * time.Hour}

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed ||
				got.Status.FailureReason != "LegacyStateOutcomeUnknown" {
				t.Fatalf("status phase=%q reason=%q, want fail-closed legacy quarantine", got.Status.Phase, got.Status.FailureReason)
			}
			if got.Status.ExecutionModel != "" {
				t.Fatalf("legacy terminal executionModel=%q, want legacy marker to remain absent", got.Status.ExecutionModel)
			}
			if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("legacy state replayed a device RPC: verify=%d install=%d activate=%d",
					rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
			}

			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			leaseKey := types.NamespacedName{Namespace: up.Namespace, Name: leaseName}
			if err := r.Client.Get(context.Background(), leaseKey, &lease); err != nil {
				t.Fatalf("legacy quarantine Lease was not retained: %v", err)
			}
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(up)}); err != nil {
				t.Fatalf("reconcile quarantined upgrade: %v", err)
			}
			if err := r.Client.Get(context.Background(), leaseKey, &lease); err != nil {
				t.Fatalf("terminal reconcile released legacy quarantine Lease: %v", err)
			}
		})
	}
}

func TestLegacyTerminalUpgradeHoldsBoundedQuarantineWithoutReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, phase := range []opsv1alpha1.UpgradePhase{
		opsv1alpha1.UpgradePhaseFailed,
		opsv1alpha1.UpgradePhaseValidationFailed,
		opsv1alpha1.UpgradePhaseRebootTimeout,
	} {
		t.Run(string(phase), func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-legacy-terminal-"+strings.ToLower(string(phase)), func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("legacy-terminal-" + strings.ToLower(string(phase)))
				up.CreationTimestamp = metav1.NewTime(now.Add(-time.Hour))
				up.Finalizers = []string{upgradeFinalizer}
				up.Status.ExecutionModel = ""
				up.Status.Phase = phase
				completed := metav1.NewTime(now)
				up.Status.CompletionTime = &completed
			})
			r := newReconciler(t, rig, up)
			r.DeviceNamespace = up.Namespace
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace, TTL: 26 * time.Hour}

			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(up)})
			if err != nil {
				t.Fatalf("reconcile terminal legacy upgrade: %v", err)
			}
			if result.RequeueAfter <= 0 {
				t.Fatalf("result=%+v, want bounded quarantine renewal", result)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
				t.Fatalf("get terminal legacy upgrade: %v", err)
			}
			if got.Status.Phase != phase || got.Status.ExecutionModel != "" {
				t.Fatalf("terminal legacy status changed: %+v", got.Status)
			}
			if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("terminal legacy state replayed device RPCs: verify=%d install=%d activate=%d",
					rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
			}
			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: leaseName}, &lease); err != nil {
				t.Fatalf("terminal legacy quarantine Lease missing: %v", err)
			}
			if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds != int32((26*time.Hour)/time.Second) {
				t.Fatalf("LeaseDurationSeconds=%v, want 26h", lease.Spec.LeaseDurationSeconds)
			}
		})
	}
}

func TestSoftwareUpgradeQuarantinesReleasedRunningActionBeforeDeviceAccess(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base)
	rig := newRig(t)
	up := newUpgrade("upgrade-blocked-by-released-action", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-uid")
		up.Finalizers = []string{upgradeFinalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhasePending
	})
	r := newReconciler(t, rig, up)
	action := &opsv1alpha1.IOSXEOperationalAction{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "released-running-action", UID: types.UID("released-action-uid")},
		Spec: opsv1alpha1.IOSXEOperationalActionSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
			Action: opsv1alpha1.ActionRequest{
				Kind:   opsv1alpha1.ActionKindReboot,
				Reboot: &opsv1alpha1.RebootActionArgs{DelaySeconds: 7 * 24 * 60 * 60},
			},
		},
		Status: opsv1alpha1.IOSXEOperationalActionStatus{
			Phase:        opsv1alpha1.ActionPhaseRunning,
			InvocationID: "released-invocation",
			StartTime:    &started,
		},
	}
	if err := r.Client.Create(context.Background(), action); err != nil {
		t.Fatalf("create released action: %v", err)
	}
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePending || got.Status.FailureReason != "" {
		t.Fatalf("status=%+v, want blocked Pending upgrade", got.Status)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "LegacyMutationQuarantine" {
		t.Fatalf("Ready reason=%q, want LegacyMutationQuarantine", reason)
	}
	if calls := r.GNOI.(*staticGNOI).calls.Load(); calls != 0 || rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
		t.Fatalf("compatibility guard touched device: client=%d verify=%d install=%d activate=%d",
			calls, rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("get compatibility Lease: %v", err)
	}
	wantHolder := devicecoordination.HolderIdentity("operational-action", action.Namespace, action.Name, string(action.UID))
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != wantHolder {
		t.Fatalf("holder=%v, want %q", lease.Spec.HolderIdentity, wantHolder)
	}
	wantSeconds := int32((7*24*time.Hour + 26*time.Hour) / time.Second)
	if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds != wantSeconds {
		t.Fatalf("LeaseDurationSeconds=%v, want %d", lease.Spec.LeaseDurationSeconds, wantSeconds)
	}
}

func TestReleasedCancelledUpgradeRemainsTerminal(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-cancelled", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.ExecutionModel = ""
		up.Status.Phase = opsv1alpha1.UpgradePhaseCancelled
		up.Status.RetryCount = 2
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseCancelled || got.Status.RetryCount != 2 {
		t.Fatalf("released terminal status changed: %+v", got.Status)
	}
	if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
		t.Fatalf("cancelled upgrade performed device RPCs: verify=%d install=%d activate=%d",
			rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
	}
}

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
	if got.Status.Phase != opsv1alpha1.UpgradePhaseStagedForNextBoot {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "staged for the next boot") {
		t.Fatalf("expected NoReboot message, got %q", got.Status.Message)
	}
	for _, condition := range got.Status.Conditions {
		if condition.Type == conditionTypeVerified && condition.Status != metav1.ConditionFalse {
			t.Fatalf("Verified=%s, want False until reboot", condition.Status)
		}
	}
}

func TestConcurrentObserverWaitsForLiveNoRebootActivation(t *testing.T) {
	rig := newRig(t)
	rig.os.activateEntered = make(chan struct{}, 1)
	rig.os.activateRelease = make(chan struct{})
	up := newUpgrade("upgrade-noreboot-live-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.PreviousVersion = "17.14.01a"
	})
	r := newReconciler(t, rig, up)
	owner := *r
	observer := *r
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := owner.Reconcile(ownerCtx, req)
		ownerDone <- err
	}()

	select {
	case <-rig.os.activateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the claimed Activate RPC")
	}
	result, err := observer.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("observer Reconcile: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > time.Second {
		t.Fatalf("observer RequeueAfter=%s, want a bounded wait of at most one second", result.RequeueAfter)
	}
	var during opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &during); err != nil {
		t.Fatalf("Get during Activate: %v", err)
	}
	if during.Status.Phase != opsv1alpha1.UpgradePhaseActivating ||
		!during.Status.PrimarySupervisorActivationRequested || during.Status.NoRebootActivationAccepted ||
		during.Status.FailureReason != "" {
		t.Fatalf("observer disturbed live activation: phase=%q requested=%t accepted=%t reason=%q message=%q",
			during.Status.Phase, during.Status.PrimarySupervisorActivationRequested,
			during.Status.NoRebootActivationAccepted, during.Status.FailureReason, during.Status.Message)
	}

	close(rig.os.activateRelease)
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner Reconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the Activate owner to finish")
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after Activate: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating || !got.Status.NoRebootActivationAccepted ||
		got.Status.FailureReason != "" {
		t.Fatalf("owner completion was rejected after peer observation: phase=%q accepted=%t reason=%q message=%q",
			got.Status.Phase, got.Status.NoRebootActivationAccepted, got.Status.FailureReason, got.Status.Message)
	}
}

func TestConcurrentObserverPreservesDefinitiveActivationRejection(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	sequenceStarted := metav1.NewTime(base.Add(-5 * time.Minute))
	for _, tt := range []struct {
		name         string
		standby      bool
		afterStandby bool
		wantReason   string
	}{
		{name: "active supervisor after standby", afterStandby: true, wantReason: "ActivateFailed"},
		{name: "standby supervisor", standby: true, wantReason: "StandbyActivateFailed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			rig.os.activateWantVersion = "17.16.01"
			rig.os.activateEntered = make(chan struct{}, 1)
			rig.os.activateRelease = make(chan struct{})
			up := newUpgrade("upgrade-activation-rejected-live-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
				up.Status.ValidatedVersion = "17.15.01a"
				if tt.standby || tt.afterStandby {
					up.Status.IndividualSupervisorInstall = true
					up.Status.PrimarySupervisorInstalled = true
					up.Status.StandbySupervisorInstalled = true
				}
				if tt.afterStandby {
					up.Status.StandbySupervisorActivationRequested = true
					up.Status.StandbySupervisorActivated = true
					up.Status.ActivationStartTime = &sequenceStarted
				}
			})
			r := newReconciler(t, rig, up)
			r.Now = func() time.Time { return base }
			owner := *r
			observer := *r
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}
			ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelOwner()
			ownerDone := make(chan error, 1)
			go func() {
				_, err := owner.Reconcile(ownerCtx, req)
				ownerDone <- err
			}()

			select {
			case <-rig.os.activateEntered:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for the claimed Activate RPC")
			}
			result, err := observer.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("observer Reconcile: %v", err)
			}
			if result.RequeueAfter <= 0 || result.RequeueAfter > time.Second {
				t.Fatalf("observer RequeueAfter=%s, want a bounded wait of at most one second", result.RequeueAfter)
			}
			var during opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), req.NamespacedName, &during); err != nil {
				t.Fatalf("Get during Activate: %v", err)
			}
			requested := during.Status.PrimarySupervisorActivationRequested
			if tt.standby {
				requested = during.Status.StandbySupervisorActivationRequested
			}
			if during.Status.Phase != opsv1alpha1.UpgradePhaseActivating || !requested || during.Status.FailureReason != "" {
				t.Fatalf("observer disturbed live activation: phase=%q requested=%t reason=%q message=%q",
					during.Status.Phase, requested, during.Status.FailureReason, during.Status.Message)
			}

			close(rig.os.activateRelease)
			select {
			case err := <-ownerDone:
				if err != nil {
					t.Fatalf("owner Reconcile: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for the Activate owner to finish")
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
				t.Fatalf("Get after Activate: %v", err)
			}
			if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != tt.wantReason {
				t.Fatalf("owner rejection was suppressed after peer observation: phase=%q reason=%q message=%q",
					got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if rig.os.activateCalls != 1 || len(rig.os.activateStandby) != 1 || rig.os.activateStandby[0] != tt.standby {
				t.Fatalf("Activate calls=%d standby flags=%v, want one call with standby=%t",
					rig.os.activateCalls, rig.os.activateStandby, tt.standby)
			}
		})
	}
}

func TestRecordedActivationAdvancesToObservationAfterRPCGrace(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-(activationRPCTimeout + mutationResultGrace)))
	for _, tt := range []struct {
		name    string
		standby bool
	}{
		{name: "active supervisor"},
		{name: "standby supervisor", standby: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-activation-abandoned-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
				up.Status.ValidatedVersion = "17.15.01a"
				up.Status.ActivationStartTime = &started
				if tt.standby {
					up.Status.IndividualSupervisorInstall = true
					up.Status.StandbySupervisorActivationRequested = true
				} else {
					up.Status.PrimarySupervisorActivationRequested = true
				}
			})
			r := newReconciler(t, rig, up)
			r.Now = func() time.Time { return base }

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability || got.Status.FailureReason != "" {
				t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if rig.os.activateCalls != 0 {
				t.Fatalf("Activate calls=%d, want no replay after grace expiry", rig.os.activateCalls)
			}
		})
	}
}

func TestNoRebootRecordedIntentFailsClosedAfterRPCGrace(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-(activationRPCTimeout + mutationResultGrace)))
	rig := newRig(t)
	up := newUpgrade("upgrade-noreboot-abandoned-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.PrimarySupervisorActivationRequested = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "ActivationOutcomeUnknown" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay after grace expiry", rig.os.activateCalls)
	}
}

func TestAcceptedNoRebootRetriesReadOnlyVerification(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.verifyErrs = []error{status.Error(codes.Unavailable, "temporarily unavailable"), nil}
	started := metav1.Time{Time: time.Unix(1_699_999_900, 0).UTC()}
	up := newUpgrade("upgrade-noreboot-verify-retry", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.PreviousVersion = "17.14.01a"
		up.Status.PrimarySupervisorActivationRequested = true
		up.Status.NoRebootActivationAccepted = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)

	first := runReconcile(t, r, up, 1)
	if first.Status.Phase != opsv1alpha1.UpgradePhaseActivating {
		t.Fatalf("first phase=%q msg=%q", first.Status.Phase, first.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay", rig.os.activateCalls)
	}
	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseStagedForNextBoot {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
}

func TestAcceptedNoRebootFailsClosedWhenIndividualSupervisorRequirementAppears(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.individualInstall = true
	started := metav1.NewTime(time.Unix(1_699_999_990, 0).UTC())
	up := newUpgrade("upgrade-noreboot-dual-discovered", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.PreviousVersion = "17.14.01a"
		up.Status.PrimarySupervisorActivationRequested = true
		up.Status.NoRebootActivationAccepted = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "IndividualSupervisorNoRebootUnsupported" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay", rig.os.activateCalls)
	}
	if !retainMutationLeaseUntilExpiry(got) {
		t.Fatal("accepted NoReboot operation did not require mutation-lease quarantine")
	}
}

func TestAcceptedNoRebootSucceedsOnTargetVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.15.01a"
	started := metav1.Time{Time: time.Unix(1_699_999_900, 0).UTC()}
	up := newUpgrade("upgrade-noreboot-target", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.PreviousVersion = "17.14.01a"
		up.Status.PrimarySupervisorActivationRequested = true
		up.Status.NoRebootActivationAccepted = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay", rig.os.activateCalls)
	}
}

func TestAcceptedNoRebootRejectsUnexpectedRunningVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.13.01a"
	started := metav1.Time{Time: time.Unix(1_699_999_900, 0).UTC()}
	up := newUpgrade("upgrade-noreboot-unexpected", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.PreviousVersion = "17.14.01a"
		up.Status.PrimarySupervisorActivationRequested = true
		up.Status.NoRebootActivationAccepted = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "UnexpectedRunningVersion" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay", rig.os.activateCalls)
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

func TestActivateTransportLossMovesToAwaitingReachability(t *testing.T) {
	rig := newRig(t)
	rig.os.activateErr = status.Error(codes.Unavailable, "transport is closing")
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-activate-transport-loss", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.StartTime = &start
		up.Status.ValidatedVersion = "17.15.01a"
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
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if reason := conditionReason(got.Status.Conditions, "Activated"); reason != "ActivationResponseLost" {
		t.Fatalf("Activated reason=%q", reason)
	}
}

func TestActivateInternalErrorIsObservedWithoutReplay(t *testing.T) {
	rig := newRig(t)
	rig.os.activateErr = status.Error(codes.Internal, "response lost after dispatch")
	up := newUpgrade("upgrade-activate-internal", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if !got.Status.PrimarySupervisorActivationRequested || rig.os.activateCalls != 1 {
		t.Fatalf("activation marker=%t calls=%d, want one durably claimed request",
			got.Status.PrimarySupervisorActivationRequested, rig.os.activateCalls)
	}
}

func TestActivationDeadlinePreventsSecondSupervisorDispatch(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-time.Minute))
	rig := newRig(t)
	up := newUpgrade("upgrade-active-supervisor-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.IndividualSupervisorInstall = true
		up.Status.StandbySupervisorActivationRequested = true
		up.Status.StandbySupervisorActivated = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "ActivationControlTimeout" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 || got.Status.PrimarySupervisorActivationRequested {
		t.Fatalf("active activation calls=%d marker=%t after deadline, want none",
			rig.os.activateCalls, got.Status.PrimarySupervisorActivationRequested)
	}
}

func TestOldUpgradeStartDoesNotConsumeFirstActivationDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	oldStart := metav1.NewTime(base.Add(-24 * time.Hour))
	rig := newRig(t)
	up := newUpgrade("upgrade-fresh-activation-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.StartTime = &oldStart
		up.Status.ValidatedVersion = "17.15.01a"
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if rig.os.activateCalls != 1 || !got.Status.PrimarySupervisorActivationRequested || got.Status.ActivationStartTime == nil {
		t.Fatalf("Activate calls=%d marker=%t start=%v, want a fresh first-activation deadline",
			rig.os.activateCalls, got.Status.PrimarySupervisorActivationRequested, got.Status.ActivationStartTime)
	}
}

func TestActivationRPCUsesRemainingSequenceDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-30 * time.Second))
	rig := newRig(t)
	up := newUpgrade("upgrade-activation-rpc-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	_ = runReconcile(t, r, up, 1)
	if !rig.os.activateDeadlineSet || rig.os.activateRemaining < 29*time.Second || rig.os.activateRemaining > 30*time.Second {
		t.Fatalf("Activate context remaining=%s set=%t, want approximately 30s",
			rig.os.activateRemaining, rig.os.activateDeadlineSet)
	}
}

func TestSecondSupervisorActivationHonorsMaintenanceWindow(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-10 * time.Second))
	closed := metav1.NewTime(base.Add(-time.Second))
	rig := newRig(t)
	up := newUpgrade("upgrade-active-supervisor-window", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotAfter: &closed}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.IndividualSupervisorInstall = true
		up.Status.StandbySupervisorActivationRequested = true
		up.Status.StandbySupervisorActivated = true
		up.Status.ActivationStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "MaintenanceWindowExpired" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d after maintenance window, want 0", rig.os.activateCalls)
	}
}

func TestActivatingReconnectFailureAfterSubmittedWaitsForReachability(t *testing.T) {
	rig := newRig(t)
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-activate-reconnect-failure", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.StartTime = &start
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.Conditions = []metav1.Condition{
			{
				Type:               "Activated",
				Status:             metav1.ConditionFalse,
				Reason:             "ActivationRequested",
				Message:            "submitting gNOI OS.Activate",
				LastTransitionTime: start,
			},
		}
	})
	r := newReconciler(t, rig, up)
	r.GNOI = unavailableGNOI{err: errors.New("connection refused")}

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
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want 0 after durable request marker", rig.os.activateCalls)
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

func TestResolvingDoesNotSucceedWithMismatchedStandby(t *testing.T) {
	rig := newRig(t)
	rig.os.individualInstall = true
	rig.os.verifyStandby = readyStandby("R1", "17.14.01a")
	up := newUpgrade("upgrade-already-running-standby-old", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed || got.Status.FailureReason != "SupervisorTargetMismatch" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
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
	if reason := conditionReason(got.Status.Conditions, "Ready"); reason != "VerifyPending" {
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

func TestVerifyMismatchWithRollbackReactivatesPreviousVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersions = []string{"17.14.01a", "17.13.01a"}
	rig.os.activateWantVersion = "17.13.01a"
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-mismatch", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
		up.Status.StartTime = &start
		up.Status.PreviousVersion = "17.13.01a"
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 12)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseRolledBack {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "RolledBack" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if rig.os.activateVersion != "17.13.01a" {
		t.Fatalf("activated version=%q, want previous version", rig.os.activateVersion)
	}
	if got.Status.RollbackStartTime == nil {
		t.Fatal("rollback sequence did not receive an independent start time")
	}
}

func TestRecordedRollbackActivationIsNeverReplayedWhileWaiting(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	started := metav1.Time{Time: time.Unix(1_699_999_900, 0).UTC()}
	up := newUpgrade("upgrade-rollback-recorded", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.PreviousVersion = "17.13.01a"
		up.Status.ValidatedVersion = "17.13.01a"
		up.Status.RollbackActivationRequested = true
		up.Status.RollbackStartTime = &started
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseRollingBack {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no rollback replay", rig.os.activateCalls)
	}
	if !got.Status.RollbackActivationRequested || got.Status.RollbackStartTime == nil {
		t.Fatalf("rollback marker lost: %+v", got.Status)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeRollback); reason != "RollbackDispatched" {
		t.Fatalf("Rollback reason=%q, want durable dispatched state", reason)
	}
}

func TestRollbackInternalErrorIsObservedWithoutReplay(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.activateErr = status.Error(codes.Internal, "rollback response lost")
	started := metav1.NewTime(time.Unix(1_699_999_990, 0).UTC())
	up := newUpgrade("upgrade-rollback-internal", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.PreviousVersion = "17.13.01a"
		up.Status.ValidatedVersion = "17.13.01a"
		up.Status.RollbackStartTime = &started
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseRollingBack || !got.Status.RollbackActivationRequested {
		t.Fatalf("phase=%q marker=%t reason=%q msg=%q", got.Status.Phase,
			got.Status.RollbackActivationRequested, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 1 {
		t.Fatalf("rollback Activate calls=%d, want exactly one without replay", rig.os.activateCalls)
	}
}

func TestOldUpgradeStartDoesNotConsumeIndependentRollbackDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	oldStart := metav1.NewTime(base.Add(-24 * time.Hour))
	rig := newRig(t)
	up := newUpgrade("upgrade-fresh-rollback-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.StartTime = &oldStart
		up.Status.PreviousVersion = "17.13.01a"
	})
	r := newReconciler(t, rig, up)
	r.GNOI = unavailableGNOI{err: status.Error(codes.Unavailable, "device reloading")}
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseRollingBack || got.Status.FailureReason != "" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
}

func TestRollbackDeadlinePreventsActivationDispatch(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-time.Minute))
	rig := newRig(t)
	up := newUpgrade("upgrade-rollback-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.PreviousVersion = "17.13.01a"
		up.Status.RollbackStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "RollbackDidNotConverge" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("rollback Activate calls=%d after deadline, want 0", rig.os.activateCalls)
	}
}

func TestRecordedRollbackTimesOutAfterResultPersistenceGrace(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-(time.Minute + mutationResultGrace)))
	rig := newRig(t)
	up := newUpgrade("upgrade-recorded-rollback-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.PreviousVersion = "17.13.01a"
		up.Status.RollbackActivationRequested = true
		up.Status.RollbackStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "RollbackDidNotConverge" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("rollback Activate calls=%d after persistence grace, want 0", rig.os.activateCalls)
	}
}

func TestRollbackRPCUsesRemainingIndependentDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-30 * time.Second))
	rig := newRig(t)
	up := newUpgrade("upgrade-rollback-rpc-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.PreviousVersion = "17.13.01a"
		up.Status.ValidatedVersion = "17.13.01a"
		up.Status.RollbackStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	_ = runReconcile(t, r, up, 1)
	if !rig.os.activateDeadlineSet || rig.os.activateRemaining < 29*time.Second || rig.os.activateRemaining > 30*time.Second {
		t.Fatalf("rollback Activate context remaining=%s set=%t, want approximately 30s",
			rig.os.activateRemaining, rig.os.activateDeadlineSet)
	}
}

func TestRollbackActivationHonorsMaintenanceWindow(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-10 * time.Second))
	closed := metav1.NewTime(base.Add(-time.Second))
	rig := newRig(t)
	up := newUpgrade("upgrade-rollback-window", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotAfter: &closed}
		up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		up.Status.PreviousVersion = "17.13.01a"
		up.Status.RollbackStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "MaintenanceWindowExpired" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("rollback Activate calls=%d after maintenance window, want 0", rig.os.activateCalls)
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

func TestAwaitingReachabilityRecognizesPreviousVersionRecoveryAfterTimeout(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.18.03.0.5496.1776157760"
	start := metav1.Time{Time: time.Unix(1_699_990_000, 0).UTC()}
	up := newUpgrade("upgrade-activation-timeout", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		up.Status.StartTime = &start
		up.Status.ActivationStartTime = &start
		up.Status.PreviousVersion = "17.18.03"
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseRolledBack {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "RolledBack" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay after the device recovered itself", rig.os.activateCalls)
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

func TestValidatingWaitsWhenTargetInstallIsStillInProgress(t *testing.T) {
	rig := newRig(t)
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	up := newUpgrade("upgrade-validating-in-progress", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.03"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: strings.Repeat("a", 64),
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseValidating
		up.Status.StartTime = &start
		up.Status.StagingOperationID = operationID
	})
	r := newReconciler(t, rig, up)
	r.Lifecycle = &fakeLifecycle{observeResult: softwarelifecycle.DeviceFileObservation{
		OperationID: operationID,
		State:       softwarelifecycle.OperationStateInProgress,
		Image: &softwarelifecycle.InventoryImage{
			Version: "17.18.03.0.5496.1776157760",
			State:   softwarelifecycle.InventoryStateInProgress,
		},
	}}

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != installInventoryPoll {
		t.Fatalf("RequeueAfter=%v, want %v", res.RequeueAfter, installInventoryPoll)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidating {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeValidated); reason != "StagingInProgress" {
		t.Fatalf("Validated reason=%q", reason)
	}
}

func TestValidatingRejectsMismatchedStagingOperation(t *testing.T) {
	rig := newRig(t)
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	up := newUpgrade("upgrade-validating-operation-mismatch", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: strings.Repeat("a", 64),
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseValidating
		up.Status.StartTime = &start
		up.Status.StagingOperationID = operationID
		up.Status.StagingRequested = true
	})
	lifecycle := &fakeLifecycle{observeResult: softwarelifecycle.DeviceFileObservation{
		OperationID: "ca567949-9caf-46d4-a827-5d13a8aa4c93",
		State:       softwarelifecycle.OperationStateSucceeded,
		Image: &softwarelifecycle.InventoryImage{
			Version: "17.15.01a", State: softwarelifecycle.InventoryStateInstalled, SourcePath: "flash:cat9k.bin",
		},
	}}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "StagingOperationMismatch" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want 0", rig.os.activateCalls)
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

func TestURLSecretIsLimitedToCredentialBearingSchemes(t *testing.T) {
	err := validateImageSource(opsv1alpha1.UpgradeImageSource{
		URL:          "https://images.example.test/cat9k.bin",
		SHA256:       strings.Repeat("a", 64),
		URLSecretRef: &corev1.LocalObjectReference{Name: "image-credentials"},
	})
	if err == nil || !strings.Contains(err.Error(), "ftp, scp, or sftp") {
		t.Fatalf("validateImageSource error=%v, want credential-scheme restriction", err)
	}
}

func TestImageSourceURLUserInfoIsRejectedBeforeResolution(t *testing.T) {
	err := validateImageSource(opsv1alpha1.UpgradeImageSource{
		URL:    "sftp://image-user:secret@images.example.test/cat9k.bin",
		SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain user information") {
		t.Fatalf("validateImageSource error=%v, want URL user-information rejection", err)
	}
}

func TestActivationOutcomeClassificationIsConservativeAfterClaim(t *testing.T) {
	for _, err := range []error{
		errors.New("malformed successful response"),
		io.EOF,
		status.Error(codes.Internal, "internal"),
		status.Error(codes.Unknown, "unknown"),
		status.Error(codes.DataLoss, "data loss"),
		status.Error(codes.Aborted, "aborted"),
		status.Error(codes.ResourceExhausted, "overloaded"),
	} {
		if !activationMayHaveStarted(err) {
			t.Errorf("activationMayHaveStarted(%v)=false, want indeterminate", err)
		}
	}
	for _, err := range []error{
		&gnoi.ActivateError{Type: gnoi.ActivateErrorNonExistentVersion},
		status.Error(codes.Unauthenticated, "unauthenticated"),
		status.Error(codes.PermissionDenied, "denied"),
		status.Error(codes.Unimplemented, "unsupported"),
		status.Error(codes.InvalidArgument, "invalid"),
		status.Error(codes.FailedPrecondition, "precondition"),
		status.Error(codes.NotFound, "not found"),
	} {
		if activationMayHaveStarted(err) {
			t.Errorf("activationMayHaveStarted(%v)=true, want definitive rejection", err)
		}
	}
}

func TestPreinstalledTargetMustExistInActivatableInventory(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-preinstalled-absent", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{Preinstalled: &opsv1alpha1.PreinstalledImageSource{}}
	})
	r := newReconciler(t, rig, up)
	r.Lifecycle = &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}

	got := runReconcile(t, r, up, 5)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "TargetNotInstalled" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want 0", rig.os.activateCalls)
	}
}

func TestPreinstalledAmbiguousTargetFailsClosed(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-preinstalled-ambiguous", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{Preinstalled: &opsv1alpha1.PreinstalledImageSource{}}
	})
	r := newReconciler(t, rig, up)
	r.Lifecycle = &fakeLifecycle{inspectErr: softwarelifecycle.ErrAmbiguousTarget}

	got := runReconcile(t, r, up, 5)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "AmbiguousTargetVersion" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want 0", rig.os.activateCalls)
	}
}

func TestNativeSourcesPreserveIndividualSupervisorRequirement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source opsv1alpha1.UpgradeImageSource
	}{
		{name: "preinstalled", source: opsv1alpha1.UpgradeImageSource{Preinstalled: &opsv1alpha1.PreinstalledImageSource{}}},
		{name: "legacy local path", source: opsv1alpha1.UpgradeImageSource{LocalPath: "flash:cat9k.bin"}},
	} {
		for _, durableRequirement := range []bool{false, true} {
			name := tc.name + "/verify-requires-individual"
			if durableRequirement {
				name = tc.name + "/durable-requirement"
			}
			t.Run(name, func(t *testing.T) {
				rig := newRig(t)
				rig.os.verifyVersion = "17.14.01a"
				rig.os.individualInstall = !durableRequirement
				up := newUpgrade("upgrade-native-dual", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Spec.ImageSource = tc.source
					up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
					up.Status.IndividualSupervisorInstall = durableRequirement
				})
				r := newReconciler(t, rig, up)

				got := runReconcile(t, r, up, 1)
				if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating {
					t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
				}
				if !got.Status.IndividualSupervisorInstall {
					t.Fatal("individual-supervisor requirement was not persisted monotonically")
				}
				if rig.os.activateCalls != 0 {
					t.Fatalf("Activate calls=%d before durable activation phase, want 0", rig.os.activateCalls)
				}
			})
		}
	}
}

func TestDualSupervisorNoRebootFailsBeforeSourceMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source opsv1alpha1.UpgradeImageSource
	}{
		{name: "url", source: opsv1alpha1.UpgradeImageSource{URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64)}},
		{name: "preinstalled", source: opsv1alpha1.UpgradeImageSource{Preinstalled: &opsv1alpha1.PreinstalledImageSource{}}},
		{name: "legacy local path", source: opsv1alpha1.UpgradeImageSource{LocalPath: "flash:cat9k.bin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRig(t)
			rig.os.verifyVersion = "17.14.01a"
			rig.os.individualInstall = true
			up := newUpgrade("upgrade-dual-no-reboot", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
				up.Spec.ImageSource = tc.source
				up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
			})
			resolver := &countingImageResolver{}
			lifecycle := &fakeLifecycle{}
			r := newReconciler(t, rig, up)
			r.ImageResolver = resolver
			r.Lifecycle = lifecycle

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed || got.Status.FailureReason != "IndividualSupervisorNoRebootUnsupported" {
				t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if resolver.calls != 0 || len(lifecycle.registerCalls) != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("mutation path reached: Resolve=%d Register=%d Install=%d Activate=%d",
					resolver.calls, len(lifecycle.registerCalls), rig.os.installCalls, rig.os.activateCalls)
			}
		})
	}
}

func TestAlreadyRunningUsesDurableIndividualSupervisorRequirement(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.15.01a"
	rig.os.individualInstall = false
	up := newUpgrade("upgrade-already-running-dual", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
		up.Status.IndividualSupervisorInstall = true
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed || got.Status.FailureReason != "SupervisorTargetMismatch" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
}

func TestDeviceFileInventoryBeforeStagingIsUncorrelated(t *testing.T) {
	for _, state := range []softwarelifecycle.InventoryState{
		softwarelifecycle.InventoryStateInstalled,
		softwarelifecycle.InventoryStateInProgress,
	} {
		t.Run(string(state), func(t *testing.T) {
			rig := newRig(t)
			rig.os.verifyVersion = "17.14.01a"
			digest := strings.Repeat("a", 64)
			up := newUpgrade("upgrade-device-file-uncorrelated-"+strings.ToLower(string(state)), func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
					Path: "flash:cat9k.bin", SHA256: digest,
				}}
				up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
				up.Status.SourceDigest = "sha256:" + digest
			})
			lifecycle := &fakeLifecycle{inspectImage: softwarelifecycle.InventoryImage{
				Version: "17.15.01a", State: state, SourcePath: "flash:cat9k.bin",
			}}
			r := newReconciler(t, rig, up)
			r.Lifecycle = lifecycle

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "UncorrelatedDeviceFileInventory" {
				t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if got.Status.StagingRequested {
				t.Fatal("StagingRequested=true, want no mutation claim")
			}
			if len(lifecycle.registerCalls) != 0 || lifecycle.observeCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("register=%d observe=%d activate=%d, want no mutation or correlated observation",
					len(lifecycle.registerCalls), lifecycle.observeCalls, rig.os.activateCalls)
			}
		})
	}
}

func TestStagingRequestMarkerPreventsDeviceFileReplay(t *testing.T) {
	rig := newRig(t)
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	digest := strings.Repeat("a", 64)
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	up := newUpgrade("upgrade-staging-idempotent", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.StartTime = &now
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = operationID
		up.Status.Conditions = []metav1.Condition{{
			Type: conditionTypeStaged, Status: metav1.ConditionFalse, Reason: "StagingRequested", LastTransitionTime: now,
		}}
	})
	lifecycle := &fakeLifecycle{inspectImage: softwarelifecycle.InventoryImage{
		Version: "17.15.01a", State: softwarelifecycle.InventoryStateInstalled, SourcePath: "flash:cat9k.bin",
	}}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != installInventoryPoll {
		t.Fatalf("RequeueAfter=%v, want %v", res.RequeueAfter, installInventoryPoll)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidating {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if len(lifecycle.registerCalls) != 0 {
		t.Fatalf("RegisterDeviceFile calls=%d, want 0", len(lifecycle.registerCalls))
	}
	if lifecycle.inspectCalls != 0 {
		t.Fatalf("Inspect calls=%d, want 0 because correlated observation is required", lifecycle.inspectCalls)
	}
}

func TestConcurrentObserverWaitsForLiveDeviceFileRegistration(t *testing.T) {
	rig := newRig(t)
	body := []byte("device-image")
	rig.file.body = body
	digest := sha256Hex(body)
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	up := newUpgrade("upgrade-staging-live-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = operationID
	})
	lifecycle := &fakeLifecycle{
		inspectErr:      softwarelifecycle.ErrTargetNotFound,
		registerErr:     softwarelifecycle.ErrInvalidDevicePath,
		registerEntered: make(chan struct{}, 1),
		registerRelease: make(chan struct{}),
	}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle
	owner := *r
	observer := *r
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := owner.Reconcile(ownerCtx, req)
		ownerDone <- err
	}()

	select {
	case <-lifecycle.registerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the claimed device-file registration")
	}
	result, err := observer.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("observer Reconcile: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > time.Second {
		t.Fatalf("observer RequeueAfter=%s, want a bounded wait of at most one second", result.RequeueAfter)
	}
	var during opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &during); err != nil {
		t.Fatalf("Get during registration: %v", err)
	}
	if during.Status.Phase != opsv1alpha1.UpgradePhaseStaging || !during.Status.StagingRequested ||
		during.Status.InstallStartTime == nil || during.Status.FailureReason != "" {
		t.Fatalf("observer disturbed live registration: phase=%q requested=%t start=%v reason=%q message=%q",
			during.Status.Phase, during.Status.StagingRequested, during.Status.InstallStartTime,
			during.Status.FailureReason, during.Status.Message)
	}

	close(lifecycle.registerRelease)
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner Reconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the registration owner to finish")
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after registration: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "StagingRejected" {
		t.Fatalf("owner rejection was suppressed after peer observation: phase=%q reason=%q message=%q",
			got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if len(lifecycle.registerCalls) != 1 || lifecycle.observeCalls != 0 {
		t.Fatalf("RegisterDeviceFile calls=%d ObserveDeviceFile calls=%d, want 1/0",
			len(lifecycle.registerCalls), lifecycle.observeCalls)
	}
}

func TestAbandonedStagingClaimAdvancesToObservationAfterRPCGrace(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-(lifecycleMutationTimeout + mutationResultGrace)))
	digest := strings.Repeat("a", 64)
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	rig := newRig(t)
	up := newUpgrade("upgrade-staging-abandoned-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = operationID
		up.Status.StagingRequested = true
		up.Status.InstallStartTime = &started
	})
	lifecycle := &fakeLifecycle{observeResult: softwarelifecycle.DeviceFileObservation{
		OperationID: operationID,
		State:       softwarelifecycle.OperationStateInProgress,
	}}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidating || got.Status.FailureReason != "" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if len(lifecycle.registerCalls) != 0 || lifecycle.observeCalls != 1 {
		t.Fatalf("RegisterDeviceFile calls=%d ObserveDeviceFile calls=%d, want 0/1",
			len(lifecycle.registerCalls), lifecycle.observeCalls)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "StagingInProgress" {
		t.Fatalf("Ready reason=%q, want StagingInProgress", reason)
	}
}

func TestMutationResultPersistenceGraceAtOwnerDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	digest := strings.Repeat("a", 64)
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	tests := []struct {
		name      string
		newObject func() *opsv1alpha1.IOSXESoftwareUpgrade
		wantPhase opsv1alpha1.UpgradePhase
	}{
		{
			name: "device-file staging",
			newObject: func() *opsv1alpha1.IOSXESoftwareUpgrade {
				started := metav1.NewTime(base.Add(-lifecycleMutationTimeout))
				return newUpgrade("upgrade-staging-persistence-grace", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
						Path: "flash:cat9k.bin", SHA256: digest,
					}}
					up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
					up.Status.SourceDigest = "sha256:" + digest
					up.Status.StagingOperationID = operationID
					up.Status.StagingRequested = true
					up.Status.InstallStartTime = &started
				})
			},
			wantPhase: opsv1alpha1.UpgradePhaseStaging,
		},
		{
			name: "install",
			newObject: func() *opsv1alpha1.IOSXESoftwareUpgrade {
				started := metav1.NewTime(base.Add(-time.Minute))
				return newUpgrade("upgrade-install-persistence-grace", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Spec.InstallTimeoutSeconds = 60
					up.Status.Phase = opsv1alpha1.UpgradePhaseTransferInterrupted
					up.Status.PrimarySupervisorInstallRequested = true
					up.Status.InstallStartTime = &started
				})
			},
			wantPhase: opsv1alpha1.UpgradePhaseTransferInterrupted,
		},
		{
			name: "primary reload activation",
			newObject: func() *opsv1alpha1.IOSXESoftwareUpgrade {
				started := metav1.NewTime(base.Add(-activationRPCTimeout))
				return newUpgrade("upgrade-primary-activation-persistence-grace", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
					up.Status.PrimarySupervisorActivationRequested = true
					up.Status.ActivationStartTime = &started
				})
			},
			wantPhase: opsv1alpha1.UpgradePhaseActivating,
		},
		{
			name: "primary NoReboot activation",
			newObject: func() *opsv1alpha1.IOSXESoftwareUpgrade {
				started := metav1.NewTime(base.Add(-activationRPCTimeout))
				return newUpgrade("upgrade-noreboot-activation-persistence-grace", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
					up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
					up.Status.PrimarySupervisorActivationRequested = true
					up.Status.ActivationStartTime = &started
				})
			},
			wantPhase: opsv1alpha1.UpgradePhaseActivating,
		},
		{
			name: "standby activation",
			newObject: func() *opsv1alpha1.IOSXESoftwareUpgrade {
				started := metav1.NewTime(base.Add(-activationRPCTimeout))
				return newUpgrade("upgrade-standby-activation-persistence-grace", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
					up.Status.IndividualSupervisorInstall = true
					up.Status.StandbySupervisorActivationRequested = true
					up.Status.ActivationStartTime = &started
				})
			},
			wantPhase: opsv1alpha1.UpgradePhaseActivating,
		},
		{
			name: "rollback activation",
			newObject: func() *opsv1alpha1.IOSXESoftwareUpgrade {
				started := metav1.NewTime(base.Add(-time.Minute))
				return newUpgrade("upgrade-rollback-persistence-grace", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Spec.RebootTimeoutSeconds = 60
					up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
					up.Status.PreviousVersion = "17.14.01a"
					up.Status.RollbackActivationRequested = true
					up.Status.RollbackStartTime = &started
				})
			},
			wantPhase: opsv1alpha1.UpgradePhaseRollingBack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := tt.newObject()
			r := newReconciler(t, rig, up)
			r.Now = func() time.Time { return base }

			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: up.Namespace,
				Name:      up.Name,
			}})
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if result.RequeueAfter <= 0 || result.RequeueAfter > time.Second {
				t.Fatalf("RequeueAfter=%s, want bounded persistence wait", result.RequeueAfter)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Status.Phase != tt.wantPhase || got.Status.FailureReason != "" {
				t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("owner RPC replayed during persistence grace: Install=%d Activate=%d", rig.os.installCalls, rig.os.activateCalls)
			}
		})
	}
}

func TestDeviceFileStagingSubmitsDurableOperationOnce(t *testing.T) {
	rig := newRig(t)
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	digest := sha256Hex([]byte("device-image"))
	operationID := "b66ddfbe-959f-4831-b6e8-e72812af7819"
	up := newUpgrade("upgrade-staging-submit", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.StartTime = &now
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = operationID
	})
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidating {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if len(lifecycle.registerCalls) != 1 {
		t.Fatalf("RegisterDeviceFile calls=%d, want 1", len(lifecycle.registerCalls))
	}
	if req := lifecycle.registerCalls[0]; req.OperationID != operationID || req.Path != "flash:cat9k.bin" {
		t.Fatalf("registration request=%+v", req)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeStaged); reason != "StagingAccepted" {
		t.Fatalf("Staged reason=%q", reason)
	}
}

func TestDeviceFileStagingRehashesImmediatelyBeforeRegistration(t *testing.T) {
	rig := newRig(t)
	rig.file.body = []byte("replacement-image")
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	digest := sha256Hex([]byte("original-image"))
	up := newUpgrade("upgrade-staging-file-changed", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.StartTime = &now
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
	})
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "DeviceFileChanged" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.file.getCalls != 1 {
		t.Fatalf("File.Get calls=%d, want 1", rig.file.getCalls)
	}
	if len(lifecycle.registerCalls) != 0 {
		t.Fatalf("RegisterDeviceFile calls=%d, want 0", len(lifecycle.registerCalls))
	}
}

func TestDeviceFileStagingRefreshesDeadlinesAfterRehash(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	afterHash := base.Add(2 * time.Minute)
	digest := sha256Hex([]byte("device-image"))
	tests := []struct {
		name       string
		mutate     func(*opsv1alpha1.IOSXESoftwareUpgrade)
		wantPhase  opsv1alpha1.UpgradePhase
		wantReason string
	}{
		{
			name: "maintenance window",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				ends := metav1.NewTime(base.Add(time.Minute))
				up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotAfter: &ends}
			},
			wantPhase:  opsv1alpha1.UpgradePhaseFailed,
			wantReason: "MaintenanceWindowExpired",
		},
		{
			name: "install deadline",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				started := metav1.NewTime(base)
				up.Spec.InstallTimeoutSeconds = 60
				up.Status.InstallStartTime = &started
			},
			wantPhase:  opsv1alpha1.UpgradePhaseValidationFailed,
			wantReason: "InstallTimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-staging-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
					Path: "flash:cat9k.bin", SHA256: digest,
				}}
				up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
				started := metav1.NewTime(base)
				up.Status.StartTime = &started
				up.Status.SourceDigest = "sha256:" + digest
				up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
				tt.mutate(up)
			})
			lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
			r := newReconciler(t, rig, up)
			r.Lifecycle = lifecycle
			calls := 0
			r.Now = func() time.Time {
				calls++
				if calls == 1 {
					return base
				}
				return afterHash
			}

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != tt.wantPhase || got.Status.FailureReason != tt.wantReason {
				t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if got.Status.CompletionTime == nil || !got.Status.CompletionTime.Time.Equal(afterHash) {
				t.Fatalf("CompletionTime=%v, want refreshed time %s", got.Status.CompletionTime, afterHash)
			}
			if len(lifecycle.registerCalls) != 0 {
				t.Fatalf("RegisterDeviceFile calls=%d after deadline elapsed during rehash, want 0", len(lifecycle.registerCalls))
			}
		})
	}
}

func TestDeviceFileStagingRevalidatesMutationLeaseAfterRehash(t *testing.T) {
	rig := newRig(t)
	digest := sha256Hex([]byte("device-image"))
	up := newUpgrade("upgrade-staging-lease-loss", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-uid")
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
	})
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if owned, _, err := r.ensureMutationLease(context.Background(), up, r.now()); err != nil || !owned {
		t.Fatalf("seed mutation lease: owned=%t err=%v", owned, err)
	}
	hookResult := make(chan error, 1)
	rig.file.onGet = func() {
		leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
		var lease coordv1.Lease
		if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
			hookResult <- fmt.Errorf("get mutation lease during device-file rehash: %w", err)
			return
		}
		otherHolder := "software-upgrade/other-uid"
		renewed := metav1.NewMicroTime(time.Now())
		lease.Spec.HolderIdentity = &otherHolder
		lease.Spec.RenewTime = &renewed
		if err := r.Client.Update(context.Background(), &lease); err != nil {
			hookResult <- fmt.Errorf("transfer mutation lease during device-file rehash: %w", err)
			return
		}
		hookResult <- nil
	}

	got := runReconcile(t, r, up, 1)
	if err := <-hookResult; err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseStaging {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "MutationLeaseBlocked" {
		t.Fatalf("Ready reason=%q, want MutationLeaseBlocked", reason)
	}
	if got.Status.StagingRequested {
		t.Fatal("staging was claimed after mutation Lease ownership was lost")
	}
	if len(lifecycle.registerCalls) != 0 {
		t.Fatalf("RegisterDeviceFile calls=%d after mutation Lease ownership was lost, want 0", len(lifecycle.registerCalls))
	}
}

func TestDeviceFileInventorySourceMismatchFailsClosed(t *testing.T) {
	rig := newRig(t)
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	digest := strings.Repeat("a", 64)
	up := newUpgrade("upgrade-source-mismatch", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digest,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.StartTime = &now
		up.Status.SourceDigest = "sha256:" + digest
		up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
		up.Status.StagingRequested = true
	})
	lifecycle := &fakeLifecycle{observeResult: softwarelifecycle.DeviceFileObservation{
		OperationID: up.Status.StagingOperationID,
		State:       softwarelifecycle.OperationStateSucceeded,
		Image: &softwarelifecycle.InventoryImage{
			Version: "17.15.01a", State: softwarelifecycle.InventoryStateInstalled, SourcePath: "flash:other.bin",
		},
	}}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "InventorySourceMismatch" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if len(lifecycle.registerCalls) != 0 {
		t.Fatalf("RegisterDeviceFile calls=%d, want 0", len(lifecycle.registerCalls))
	}
	if lifecycle.inspectCalls != 0 || lifecycle.observeCalls != 1 {
		t.Fatalf("Inspect calls=%d ObserveDeviceFile calls=%d, want 0/1", lifecycle.inspectCalls, lifecycle.observeCalls)
	}
}

func TestMaintenanceWindowDefersToNotBefore(t *testing.T) {
	rig := newRig(t)
	future := metav1.Time{Time: time.Unix(2_000_000_000, 0)}
	up := newUpgrade("upgrade-window", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotBefore: &future}
	})
	r := newReconciler(t, rig, up)
	// First call adds the finalizer; later calls run Pending.
	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePending {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "maintenance window") {
		t.Fatalf("expected maintenance-window message, got %q", got.Status.Message)
	}
}

func TestFinalizerIsPersistedBeforeMutationLeaseAcquisition(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-finalizer-before-lease", nil)
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	hasFinalizer := false
	for _, finalizer := range got.Finalizers {
		hasFinalizer = hasFinalizer || finalizer == upgradeFinalizer
	}
	if !hasFinalizer {
		t.Fatalf("finalizers=%v, want %q", got.Finalizers, upgradeFinalizer)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("mutation Lease exists before finalizer was established: %v", err)
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

func TestInvalidMaintenanceWindowFailsPreflight(t *testing.T) {
	rig := newRig(t)
	notBefore := metav1.NewTime(time.Unix(1_700_000_100, 0).UTC())
	notAfter := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	up := newUpgrade("upgrade-window-inverted", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotBefore: &notBefore, NotAfter: &notAfter}
	})
	r := newReconciler(t, rig, up)
	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePreflightFailed || got.Status.FailureReason != "InvalidMaintenanceWindow" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
}

func TestPendingUpgradeWaitsForExistingDeviceOperation(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-z", nil)
	up.CreationTimestamp = metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	r := newReconciler(t, rig, up)
	owner := newUpgrade("upgrade-a", nil)
	owner.CreationTimestamp = metav1.NewTime(time.Unix(1_699_999_000, 0).UTC())
	if err := r.Client.Create(context.Background(), owner); err != nil {
		t.Fatalf("create lock owner: %v", err)
	}

	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhasePending {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "DeviceUpgradeLocked" {
		t.Fatalf("Ready reason=%q", reason)
	}
	if !strings.Contains(got.Status.Message, owner.Name) {
		t.Fatalf("message=%q, want lock owner %q", got.Status.Message, owner.Name)
	}
	if rig.os.verifyCalls != 0 || rig.os.activateCalls != 0 {
		t.Fatalf("device calls Verify=%d Activate=%d, want none while locked", rig.os.verifyCalls, rig.os.activateCalls)
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

func TestRetryableImageResolveErrorRequeuesWithoutTerminalizing(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base)
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolve-retry", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.StartTime = &started
	})
	resolver := &countingErrorImageResolver{err: MarkRetryableResolveError(errors.New("temporary network failure"))}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	r.Now = func() time.Time { return base }

	result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: up.Namespace,
		Name:      up.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > installInventoryPoll {
		t.Fatalf("RequeueAfter=%s, want bounded retry", result.RequeueAfter)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferring || got.Status.FailureReason != "" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "ImageResolveRetry" {
		t.Fatalf("Ready reason=%q, want ImageResolveRetry", reason)
	}
	if resolver.calls != 1 || rig.os.installCalls != 0 {
		t.Fatalf("Resolve calls=%d Install calls=%d, want 1/0", resolver.calls, rig.os.installCalls)
	}
}

func TestResolverInternalRetryableDeadlineDoesNotConsumeInstallDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base)
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolver-internal-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.StartTime = &started
	})
	resolver := &countingErrorImageResolver{
		err: MarkRetryableResolveError(fmt.Errorf("resolver transport deadline: %w", context.DeadlineExceeded)),
	}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	r.Now = func() time.Time { return base }

	result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: up.Namespace,
		Name:      up.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > installInventoryPoll {
		t.Fatalf("RequeueAfter=%s, want bounded retry", result.RequeueAfter)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferring || got.Status.FailureReason != "" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "ImageResolveRetry" {
		t.Fatalf("Ready reason=%q, want ImageResolveRetry", reason)
	}
	if resolver.calls != 1 || rig.os.installCalls != 0 {
		t.Fatalf("Resolve calls=%d Install calls=%d, want 1/0", resolver.calls, rig.os.installCalls)
	}
}

func TestRetryableImageResolveStopsAtInstallDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	now := base
	started := metav1.NewTime(base)
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolve-retry-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.StartTime = &started
	})
	resolver := &countingErrorImageResolver{err: MarkRetryableResolveError(errors.New("temporary network failure"))}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	r.Now = func() time.Time { return now }
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("retrying Reconcile: %v", err)
	}
	now = base.Add(time.Minute)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("deadline Reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "InstallTimeout" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if resolver.calls != 1 || rig.os.installCalls != 0 {
		t.Fatalf("Resolve calls=%d Install calls=%d, want one pre-deadline resolution and no install", resolver.calls, rig.os.installCalls)
	}
}

func TestImageResolutionUsesRemainingInstallDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-30 * time.Second))
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolve-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.StartTime = &started
	})
	resolver := &deadlineImageResolver{}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "ImageResolveFailed" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if resolver.remaining < 29*time.Second || resolver.remaining > 30*time.Second {
		t.Fatalf("resolver deadline remaining=%s, want approximately 30s", resolver.remaining)
	}
}

func TestImageResolutionDeadlineIsInstallTimeout(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-60*time.Second + 50*time.Millisecond))
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolve-timeout", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.StartTime = &started
	})
	resolver := &deadlineImageResolver{waitForDeadline: true}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "InstallTimeout" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
}

func TestStreamedSourceDoesNotConsultLifecycleInventory(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "26.01.1"
	rig.os.validatedVersion = "17.18.02"
	up := newUpgrade("upgrade-committed-not-staged", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:    "https://example.invalid/cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		}
	})
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrAmbiguousTarget}
	r.ImageResolver = resolver
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if resolver.calls != 1 {
		t.Fatalf("image resolver called %d time(s), want transfer path", resolver.calls)
	}
	if lifecycle.inspectCalls != 0 {
		t.Fatalf("lifecycle inventory inspected %d time(s), want 0 for streamed source", lifecycle.inspectCalls)
	}
}

func TestFirstContentPinWriterContinuesWithOpenResolvedImage(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.17.01"
	rig.os.validatedVersion = "17.18.02"
	up := newUpgrade("upgrade-content-pin-fast-path", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:    "https://example.invalid/cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	resolver := &countingImageResolver{}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if resolver.calls != 1 || rig.os.installCalls != 1 {
		t.Fatalf("Resolve calls=%d Install calls=%d, want one resolution feeding one install", resolver.calls, rig.os.installCalls)
	}
	if got.Status.SourceDigest != "sha256:"+strings.Repeat("a", 64) || got.Status.SourceSize != int64(len("image")) {
		t.Fatalf("content pin=%s/%d, want resolver digest and size", got.Status.SourceDigest, got.Status.SourceSize)
	}
	if !got.Status.PrimarySupervisorInstallRequested || !got.Status.PrimarySupervisorInstalled || got.Status.InstallStartTime == nil {
		t.Fatalf("install claim/result not durably recorded: status=%+v", got.Status)
	}
}

func TestNoRebootRejectsLateDualSupervisorRequirementBeforeInstall(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.17.01"
	rig.os.individualInstall = true
	up := newUpgrade("upgrade-noreboot-late-dual", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
		up.Spec.TargetVersion = "17.18.02"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:    "https://example.invalid/cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	resolver := &countingImageResolver{}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "IndividualSupervisorNoRebootUnsupported" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if resolver.calls != 0 || rig.os.installCalls != 0 {
		t.Fatalf("late supervisor discovery reached mutation path: Resolve=%d Install=%d", resolver.calls, rig.os.installCalls)
	}
}

func TestTransferPreflightVerifyWaitsBeforeResolvingImage(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.Unavailable, "connect: connection refused")
	up := newUpgrade("upgrade-gnoi-preflight", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 3)
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want 0", resolver.calls)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferring {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if !strings.Contains(got.Status.Message, "waiting for gNOI OS.Verify before image transfer") {
		t.Fatalf("expected preflight wait in message, got %q", got.Status.Message)
	}
	if gotReason := conditionReason(got.Status.Conditions, "Ready"); gotReason != "VerifyPending" {
		t.Fatalf("Ready reason=%q, want VerifyPending", gotReason)
	}
}

func TestTransferPreflightGNOIClientWaitsBeforeResolvingImage(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-gnoi-client-preflight", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	r.GNOI = unavailableGNOI{err: status.Error(codes.Unavailable, "connect: connection refused")}
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 3)
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want 0", resolver.calls)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferring {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if !strings.Contains(got.Status.Message, "waiting for gNOI client before image transfer") {
		t.Fatalf("expected preflight wait in message, got %q", got.Status.Message)
	}
	if gotReason := conditionReason(got.Status.Conditions, "Ready"); gotReason != "DeviceUnreachable" {
		t.Fatalf("Ready reason=%q, want DeviceUnreachable", gotReason)
	}
}

func TestPreflightWaitsTerminateAtInstallDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-2 * time.Minute))
	tests := []struct {
		name      string
		phase     opsv1alpha1.UpgradePhase
		configure func(*rig, *Reconciler)
	}{
		{
			name:  "resolving client acquisition",
			phase: opsv1alpha1.UpgradePhaseResolving,
			configure: func(_ *rig, r *Reconciler) {
				r.GNOI = unavailableGNOI{err: status.Error(codes.Unavailable, "connection refused")}
			},
		},
		{
			name:  "transfer verify",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			configure: func(rig *rig, _ *Reconciler) {
				rig.os.verifyErr = status.Error(codes.Unavailable, "connection refused")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-preflight-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("upgrade-preflight-deadline-uid")
				up.Finalizers = []string{Finalizer}
				up.Spec.InstallTimeoutSeconds = 60
				up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
					URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
				}
				up.Status.Phase = tt.phase
				up.Status.StartTime = &started
			})
			r := newReconciler(t, rig, up)
			r.Now = func() time.Time { return base }
			r.DeviceNamespace = "default"
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
			tt.configure(rig, r)
			if owned, _, err := r.ensureMutationLease(context.Background(), up, base); err != nil || !owned {
				t.Fatalf("seed mutation Lease: owned=%t err=%v", owned, err)
			}

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "InstallTimeout" {
				t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
				t.Fatalf("preflight timeout retained an unused mutation Lease: %v", err)
			}
		})
	}
}

func TestDeviceFileHashUsesRemainingInstallDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-60*time.Second + 50*time.Millisecond))
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.file.block = true
	up := newUpgrade("upgrade-device-file-hash-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: strings.Repeat("a", 64),
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
		up.Status.StartTime = &started
	})
	lifecycle := &fakeLifecycle{}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "InstallTimeout" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.file.getCalls > 1 || len(lifecycle.registerCalls) != 0 {
		t.Fatalf("File.Get calls=%d Register calls=%d, want at most one bounded read and no mutation",
			rig.file.getCalls, len(lifecycle.registerCalls))
	}
}

func TestDeviceFileRegistrationUsesRemainingInstallDeadline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-30 * time.Second))
	body := []byte("device-image")
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.file.body = body
	up := newUpgrade("upgrade-device-file-registration-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.InstallTimeoutSeconds = 60
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{DeviceFile: &opsv1alpha1.DeviceFileImageSource{
			Path: "flash:cat9k.bin", SHA256: digestHex,
		}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.StartTime = &started
		up.Status.InstallStartTime = &started
		up.Status.SourceDigest = "sha256:" + digestHex
		up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
	})
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.Lifecycle = lifecycle
	r.Now = func() time.Time { return base }

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidating {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if !lifecycle.registerDLSet || lifecycle.registerRemain < 29*time.Second || lifecycle.registerRemain > 30*time.Second {
		t.Fatalf("registration context remaining=%s set=%t, want approximately 30s",
			lifecycle.registerRemain, lifecycle.registerDLSet)
	}
}

func TestTransferPreflightPermanentAuthenticationFailureIsTerminal(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyErr = status.Error(codes.Unauthenticated, "invalid credentials")
	up := newUpgrade("upgrade-gnoi-auth-failure", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.FailureReason != "GNOIUnauthenticated" {
		t.Fatalf("FailureReason=%q", got.Status.FailureReason)
	}
	if resolver.calls != 0 {
		t.Fatalf("image resolver called %d time(s), want 0", resolver.calls)
	}
}

func TestTransferRefreshesDeadlinesAfterImageResolution(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	afterResolve := base.Add(2 * time.Minute)
	tests := []struct {
		name       string
		mutate     func(*opsv1alpha1.IOSXESoftwareUpgrade)
		wantPhase  opsv1alpha1.UpgradePhase
		wantReason string
	}{
		{
			name: "maintenance window",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				ends := metav1.NewTime(base.Add(time.Minute))
				up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotAfter: &ends}
			},
			wantPhase:  opsv1alpha1.UpgradePhaseFailed,
			wantReason: "MaintenanceWindowExpired",
		},
		{
			name: "install deadline",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				started := metav1.NewTime(base)
				up.Spec.InstallTimeoutSeconds = 60
				up.Status.InstallStartTime = &started
			},
			wantPhase:  opsv1alpha1.UpgradePhaseValidationFailed,
			wantReason: "InstallTimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			rig.os.verifyVersion = "17.14.01a"
			up := newUpgrade("upgrade-resolve-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
					URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
				}
				up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
				up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
				up.Status.SourceSize = int64(len("image"))
				tt.mutate(up)
			})
			r := newReconciler(t, rig, up)
			r.ImageResolver = &countingImageResolver{}
			calls := 0
			r.Now = func() time.Time {
				calls++
				if calls == 1 {
					return base
				}
				return afterResolve
			}

			got := runReconcile(t, r, up, 1)
			if got.Status.Phase != tt.wantPhase || got.Status.FailureReason != tt.wantReason {
				t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
			}
			if got.Status.CompletionTime == nil || !got.Status.CompletionTime.Time.Equal(afterResolve) {
				t.Fatalf("CompletionTime=%v, want refreshed time %s", got.Status.CompletionTime, afterResolve)
			}
			if rig.os.installCalls != 0 {
				t.Fatalf("Install calls=%d after deadline elapsed during resolve, want 0", rig.os.installCalls)
			}
		})
	}
}

func TestTransferPreservesIndividualSupervisorRequirement(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.individualInstall = false
	up := newUpgrade("upgrade-preserve-individual-supervisor", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.IndividualSupervisorInstall = true
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}

	got := runReconcile(t, r, up, 1)
	if got.Status.SourceDigest == "" {
		t.Fatal("resolved source was not pinned")
	}
	if !got.Status.IndividualSupervisorInstall {
		t.Fatal("a transient OS.Verify response erased the durable individual-supervisor requirement")
	}
}

func TestTransferRevalidatesMutationLeaseAfterImageResolution(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-resolve-lease-loss", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		started := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
		up.Status.InstallStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if owned, _, err := r.ensureMutationLease(context.Background(), up, r.now()); err != nil || !owned {
		t.Fatalf("seed mutation lease: owned=%t err=%v", owned, err)
	}
	r.ImageResolver = &countingImageResolver{onResolve: func() {
		leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
		var lease coordv1.Lease
		if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
			t.Fatalf("get mutation lease during resolve: %v", err)
		}
		otherHolder := "default/another-upgrade"
		renewed := metav1.NewMicroTime(time.Now())
		lease.Spec.HolderIdentity = &otherHolder
		lease.Spec.RenewTime = &renewed
		if err := r.Client.Update(context.Background(), &lease); err != nil {
			t.Fatalf("transfer mutation lease during resolve: %v", err)
		}
	}}

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferring {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if reason := conditionReason(got.Status.Conditions, conditionTypeReady); reason != "MutationLeaseBlocked" {
		t.Fatalf("Ready reason=%q, want MutationLeaseBlocked", reason)
	}
	if got.Status.PrimarySupervisorInstallRequested {
		t.Fatal("Install attempt was claimed after mutation lease ownership was lost")
	}
	if rig.os.installCalls != 0 {
		t.Fatalf("Install calls=%d after mutation lease ownership was lost, want 0", rig.os.installCalls)
	}
}

func TestResolvedImageDigestAndSizeArePinnedBeforeInstall(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.installEntered = make(chan struct{}, 1)
	rig.os.installRelease = make(chan struct{})
	up := newUpgrade("upgrade-digest-pin", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	resolver := &countingImageResolver{body: "first", digest: "sha256:" + strings.Repeat("a", 64)}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := r.Reconcile(ownerCtx, req)
		ownerDone <- err
	}()

	select {
	case <-rig.os.installEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gNOI OS.Install")
	}
	var during opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &during); err != nil {
		t.Fatalf("Get during Install: %v", err)
	}
	if during.Status.SourceDigest != "sha256:"+strings.Repeat("a", 64) || during.Status.SourceSize != int64(len("first")) {
		t.Fatalf("source was not pinned before Install: %q/%d", during.Status.SourceDigest, during.Status.SourceSize)
	}
	if !during.Status.PrimarySupervisorInstallRequested || during.Status.InstallStartTime == nil {
		t.Fatalf("Install claim was not durable before dispatch: status=%+v", during.Status)
	}
	close(rig.os.installRelease)
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner Reconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Install owner")
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after Install: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating || rig.os.installCalls != 1 || resolver.calls != 1 {
		t.Fatalf("phase=%q Resolve=%d Install=%d, want Activating/1/1", got.Status.Phase, resolver.calls, rig.os.installCalls)
	}
}

func TestDualSupervisorInstallPersistsBothCompletions(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.individualInstall = true
	up := newUpgrade("upgrade-dual-supervisor", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.IndividualSupervisorInstall = true
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !got.Status.PrimarySupervisorInstalled || !got.Status.StandbySupervisorInstalled {
		t.Fatalf("supervisor completion primary=%v standby=%v", got.Status.PrimarySupervisorInstalled, got.Status.StandbySupervisorInstalled)
	}
	if rig.os.installCalls != 2 {
		t.Fatalf("Install calls=%d, want 2", rig.os.installCalls)
	}
	if len(rig.os.installStandby) != 2 || rig.os.installStandby[0] || !rig.os.installStandby[1] {
		t.Fatalf("standby flags=%v, want [false true]", rig.os.installStandby)
	}
}

func TestDualSupervisorInstallRequiresSameExactValidatedVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.individualInstall = true
	rig.os.validatedVersion = "17.15.02"
	started := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	up := newUpgrade("upgrade-dual-version-mismatch", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.15"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &started
		up.Status.IndividualSupervisorInstall = true
		up.Status.PrimarySupervisorInstallRequested = true
		up.Status.PrimarySupervisorInstalled = true
		up.Status.ValidatedVersion = "17.15.01"
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "SupervisorValidatedVersionMismatch" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.installCalls != 1 || len(rig.os.installStandby) != 1 || !rig.os.installStandby[0] {
		t.Fatalf("Install calls=%d standby=%v, want one standby request", rig.os.installCalls, rig.os.installStandby)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d after supervisor version mismatch, want 0", rig.os.activateCalls)
	}
}

func TestClaimedInstallAttemptIsObservedBeforeTrustingRunningVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.15.01a"
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-install-claimed-first", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &now
		up.Status.PrimarySupervisorInstallRequested = true
	})
	resolver := &countingImageResolver{}
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.ImageResolver = resolver
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferring || !got.Status.PrimarySupervisorInstallRequested {
		t.Fatalf("phase=%q marker=%t msg=%q", got.Status.Phase, got.Status.PrimarySupervisorInstallRequested, got.Status.Message)
	}
	if rig.os.verifyCalls != 0 || resolver.calls != 0 || rig.os.installCalls != 0 {
		t.Fatalf("Verify=%d Resolve=%d Install=%d, want observation before all transfer preflight",
			rig.os.verifyCalls, resolver.calls, rig.os.installCalls)
	}
}

func TestConcurrentObserverDoesNotInterruptLiveInstall(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.installEntered = make(chan struct{}, 1)
	rig.os.installRelease = make(chan struct{})
	up := newUpgrade("upgrade-install-live-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	r.Lifecycle = &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	owner := *r
	observer := *r
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}}
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := owner.Reconcile(ownerCtx, req)
		ownerDone <- err
	}()

	select {
	case <-rig.os.installEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the claimed Install RPC")
	}
	if _, err := observer.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("observer Reconcile: %v", err)
	}
	var during opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &during); err != nil {
		t.Fatalf("Get during Install: %v", err)
	}
	if during.Status.Phase != opsv1alpha1.UpgradePhaseTransferring ||
		!during.Status.PrimarySupervisorInstallRequested || during.Status.PrimarySupervisorInstalled ||
		during.Status.InstallStartTime == nil {
		t.Fatalf("observer disturbed live install: phase=%q requested=%t installed=%t start=%v reason=%q message=%q",
			during.Status.Phase, during.Status.PrimarySupervisorInstallRequested,
			during.Status.PrimarySupervisorInstalled, during.Status.InstallStartTime,
			during.Status.FailureReason, during.Status.Message)
	}

	close(rig.os.installRelease)
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner Reconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the Install owner to finish")
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after Install: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseActivating || !got.Status.PrimarySupervisorInstalled ||
		got.Status.ValidatedVersion != "17.15.01a" {
		t.Fatalf("owner completion was rejected after peer observation: phase=%q installed=%t validated=%q reason=%q message=%q",
			got.Status.Phase, got.Status.PrimarySupervisorInstalled, got.Status.ValidatedVersion,
			got.Status.FailureReason, got.Status.Message)
	}
}

func TestInterruptedInstallAbsenceNeverClearsMarkerOrReplays(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.installErrAfterReady = status.Error(codes.Unavailable, "stream lost")
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-install-absence-retry", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &now
	})
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferInterrupted {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !got.Status.PrimarySupervisorInstallRequested {
		t.Fatal("install marker was cleared after inventory reported absence")
	}
	if rig.os.installCalls != 1 {
		t.Fatalf("Install calls=%d, want exactly one dispatched attempt", rig.os.installCalls)
	}
	if got.Status.InventoryState != opsv1alpha1.UpgradeInventoryStateAbsent {
		t.Fatalf("InventoryState=%q, want Absent", got.Status.InventoryState)
	}
	if !strings.Contains(got.Status.Message, "absence cannot prove") {
		t.Fatalf("message=%q, want explicit no-replay rationale", got.Status.Message)
	}
}

func TestInterruptedInstallWaitsWhenInventoryCannotProveOutcome(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.installErrAfterReady = status.Error(codes.Unavailable, "stream lost")
	now := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC()}
	up := newUpgrade("upgrade-install-outcome-unknown", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &now
	})
	lifecycle := &fakeLifecycle{inspectImage: softwarelifecycle.InventoryImage{
		Version: "17.15.01a", State: softwarelifecycle.InventoryStateInstalled,
	}}
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferInterrupted || !got.Status.PrimarySupervisorInstallRequested {
		t.Fatalf("phase=%q marker=%t msg=%q", got.Status.Phase, got.Status.PrimarySupervisorInstallRequested, got.Status.Message)
	}
	if got.Status.FailureReason != "" {
		t.Fatalf("failure reason=%q, want observation to remain non-terminal", got.Status.FailureReason)
	}
	if rig.os.installCalls != 1 {
		t.Fatalf("Install calls=%d, want no replay while outcome is unknown", rig.os.installCalls)
	}
}

func TestInstallInternalErrorIsObservedWithoutReplay(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.installErrAfterReady = status.Error(codes.Internal, "stream outcome unavailable")
	started := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	up := newUpgrade("upgrade-install-internal", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	r.Lifecycle = &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferInterrupted || !got.Status.PrimarySupervisorInstallRequested {
		t.Fatalf("phase=%q marker=%t reason=%q msg=%q", got.Status.Phase,
			got.Status.PrimarySupervisorInstallRequested, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.installCalls != 1 {
		t.Fatalf("Install calls=%d, want exactly one without replay", rig.os.installCalls)
	}
}

func TestLateInstallErrorUsesRefreshedClock(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	late := base.Add(10 * time.Minute)
	started := metav1.NewTime(base)
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	rig.os.installErrAfterReady = status.Error(codes.Unauthenticated, "credentials rejected after transfer began")
	up := newUpgrade("upgrade-install-late-error-clock", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &started
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	times := []time.Time{base, base, late}
	clockCalls := 0
	r.Now = func() time.Time {
		idx := clockCalls
		clockCalls++
		if idx >= len(times) {
			return times[len(times)-1]
		}
		return times[idx]
	}

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "GNOIUnauthenticated" {
		t.Fatalf("phase=%q reason=%q message=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if got.Status.CompletionTime == nil || !got.Status.CompletionTime.Time.Equal(late) {
		t.Fatalf("CompletionTime=%v, want refreshed stream-error time %s", got.Status.CompletionTime, late)
	}
}

func TestInstallInProgressWithoutInventoryBackendRemainsQuarantined(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-install-in-progress-no-inventory", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		up.Status.PrimarySupervisorInstallRequested = true
	})
	r := newReconciler(t, rig, up)
	r.Lifecycle = nil

	if _, err := r.handleInstallErr(context.Background(), up, &gnoi.InstallError{
		Type: gnoi.InstallErrorInstallInProgress,
	}, r.now()); err != nil {
		t.Fatalf("handleInstallErr: %v", err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseTransferInterrupted {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
}

func TestInterruptedInstallTerminatesUnknownAtDeadlineWithoutReplay(t *testing.T) {
	rig := newRig(t)
	start := metav1.Time{Time: time.Unix(1_700_000_000, 0).UTC().Add(-2 * time.Hour)}
	up := newUpgrade("upgrade-install-outcome-deadline", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL: "https://example.invalid/cat9k.bin", SHA256: strings.Repeat("a", 64),
		}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferInterrupted
		up.Status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
		up.Status.SourceSize = int64(len("image"))
		up.Status.InstallStartTime = &start
		up.Status.PrimarySupervisorInstallRequested = true
	})
	lifecycle := &fakeLifecycle{inspectErr: softwarelifecycle.ErrTargetNotFound}
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	r.Lifecycle = lifecycle

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "InstallOutcomeUnknown" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if !got.Status.PrimarySupervisorInstallRequested {
		t.Fatal("install attempt marker was cleared at the deadline")
	}
	if rig.os.installCalls != 0 || lifecycle.inspectCalls != 0 {
		t.Fatalf("Install calls=%d Inspect calls=%d, want no replay or post-deadline observation", rig.os.installCalls, lifecycle.inspectCalls)
	}
}

func TestDualSupervisorActivationVerifiesStandbyBeforeActive(t *testing.T) {
	rig := newRig(t)
	rig.os.individualInstall = true
	rig.os.verifyVersions = []string{"17.14.01a", "17.15.01a", "17.15.01a"}
	rig.os.verifyStandbys = []*ospb.VerifyStandby{
		readyStandby("R1", "17.15.01a"),
		readyStandby("R0", "17.15.01a"),
		readyStandby("R0", "17.15.01a"),
	}
	up := newUpgrade("upgrade-dual-activate", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.IndividualSupervisorInstall = true
		up.Status.PrimarySupervisorInstalled = true
		up.Status.StandbySupervisorInstalled = true
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 5)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if len(rig.os.activateStandby) != 2 || !rig.os.activateStandby[0] || rig.os.activateStandby[1] {
		t.Fatalf("Activate standby flags=%v, want [true false]", rig.os.activateStandby)
	}
	if !got.Status.StandbySupervisorActivationRequested || !got.Status.StandbySupervisorActivated ||
		!got.Status.PrimarySupervisorActivationRequested {
		t.Fatalf("activation milestones not persisted: %+v", got.Status)
	}
	if got.Status.ActivationStartTime == nil {
		t.Fatal("ActivationStartTime is nil")
	}
}

func TestDualSupervisorActivationWaitsForStandbyTarget(t *testing.T) {
	rig := newRig(t)
	rig.os.individualInstall = true
	rig.os.verifyStandbys = []*ospb.VerifyStandby{
		unavailableStandby(),
		readyStandby("R1", "17.14.01a"),
	}
	up := newUpgrade("upgrade-dual-standby-wait", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.IndividualSupervisorInstall = true
		up.Status.PrimarySupervisorInstalled = true
		up.Status.StandbySupervisorInstalled = true
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 3)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.os.activateCalls != 1 || len(rig.os.activateStandby) != 1 || !rig.os.activateStandby[0] {
		t.Fatalf("Activate calls=%d flags=%v, want only standby activation", rig.os.activateCalls, rig.os.activateStandby)
	}
	if got.Status.PrimarySupervisorActivationRequested {
		t.Fatal("active-supervisor activation was requested before standby reached target")
	}
}

func TestDualSupervisorRecordedStandbyActivationIsNotReplayed(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-dual-standby-recorded", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.IndividualSupervisorInstall = true
		up.Status.StandbySupervisorActivationRequested = true
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want no replay", rig.os.activateCalls)
	}
}

func TestDualSupervisorMissingStandbyFailsClosed(t *testing.T) {
	rig := newRig(t)
	rig.os.individualInstall = true
	up := newUpgrade("upgrade-dual-no-standby", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-dual-no-standby-uid")
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		up.Status.ValidatedVersion = "17.15.01a"
		up.Status.IndividualSupervisorInstall = true
		up.Status.StandbySupervisorActivationRequested = true
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if owned, _, err := r.ensureMutationLease(context.Background(), up, r.now()); err != nil || !owned {
		t.Fatalf("seed mutation Lease: owned=%t err=%v", owned, err)
	}

	got := runReconcile(t, r, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseValidationFailed || got.Status.FailureReason != "StandbyVerificationUnavailable" {
		t.Fatalf("phase=%q reason=%q msg=%q", got.Status.Phase, got.Status.FailureReason, got.Status.Message)
	}
	if rig.os.activateCalls != 0 {
		t.Fatalf("Activate calls=%d, want none", rig.os.activateCalls)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("unverified standby activation released its quarantine Lease: %v", err)
	}
}

func TestTransferProgressClampsAndDoesNotRegress(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-progress-monotonic", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	key := types.NamespacedName{Namespace: up.Namespace, Name: up.Name}
	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("Get: %v", err)
	}
	r.updateTransferProgress(context.Background(), &current, 200_000, 100_000, r.now())
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("Get after clamp: %v", err)
	}
	r.updateTransferProgress(context.Background(), &current, 50_000, 100_000, r.now())
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("Get after regression: %v", err)
	}
	if current.Status.TransferProgress == nil {
		t.Fatal("TransferProgress is nil")
	}
	if current.Status.TransferProgress.BytesTransferred != 100_000 || current.Status.TransferProgress.Percent != 100 {
		t.Fatalf("progress=%+v, want clamped 100000/100%%", current.Status.TransferProgress)
	}
}

func TestActivatesDeviceValidatedVersion(t *testing.T) {
	rig := newRig(t)
	rig.os.verifyVersions = []string{
		"17.18.02.0.4112.1766116039",
		"17.18.02.0.4112.1766116039",
		"17.18.02.0.4112.1766116039",
		"17.18.02.0.4112.1766116039",
		"17.18.03.0.5000.1234567890",
	}
	rig.os.validatedVersion = "17.18.03"
	rig.os.activateWantVersion = "17.18.03"
	up := newUpgrade("upgrade-validated-version", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Spec.TargetVersion = "17.18.03"
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:    "https://example.invalid/cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		}
	})
	r := newReconciler(t, rig, up)
	resolver := &countingImageResolver{}
	r.ImageResolver = resolver

	got := runReconcile(t, r, up, 14)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if rig.os.activateVersion != "17.18.03" {
		t.Fatalf("Activate version=%q, want validated version", rig.os.activateVersion)
	}
	if got.Status.ValidatedVersion != "17.18.03" {
		t.Fatalf("ValidatedVersion=%q", got.Status.ValidatedVersion)
	}
	if resolver.calls != 1 {
		t.Fatalf("image resolver called %d time(s), want one pinned transfer resolution", resolver.calls)
	}
}

func TestActivatingDoesNotFallBackWhenExactValidatedVersionIsRejected(t *testing.T) {
	rig := newRig(t)
	rig.os.activateWantVersion = "17.18.02"
	up := newUpgrade("upgrade-activation-fallback", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.TargetVersion = "17.18.02"
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.18.02.0.4112.1766116039"
	})
	r := newReconciler(t, rig, up)

	got := runReconcile(t, r, up, 2)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed {
		t.Fatalf("phase=%q msg=%q reason=%q", got.Status.Phase, got.Status.Message, got.Status.FailureReason)
	}
	if rig.os.activateVersion != "17.18.02.0.4112.1766116039" {
		t.Fatalf("Activate version=%q, want exact validated version", rig.os.activateVersion)
	}
	if rig.os.activateCalls != 1 {
		t.Fatalf("Activate calls=%d, want one call without fallback", rig.os.activateCalls)
	}
}

func TestFinalizerClearedOnDeleteWithoutDeviceMutation(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-delete", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-delete-uid")
		up.Finalizers = []string{upgradeFinalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(),
		devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up)); err != nil || !result.Owned {
		t.Fatalf("seed mutation Lease: result=%+v err=%v", result, err)
	}

	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := r.Client.Delete(context.Background(), &got); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: up.Namespace, Name: up.Name}})
	if err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}
	if rig.os.activateCalls != 0 || rig.os.installCalls != 0 {
		t.Fatalf("deletion dispatched device mutations: Activate=%d Install=%d", rig.os.activateCalls, rig.os.installCalls)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("unused mutation Lease remains after deletion: %v", err)
	}
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		// fake client GC removes the object once the finalizer clears
		return
	}
	for _, f := range got.Finalizers {
		if f == upgradeFinalizer {
			t.Fatalf("finalizer not cleared after deletion reconcile")
		}
	}
}

func TestDeleteAfterMutationRetainsQuarantineLease(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-delete-after-mutation", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-delete-after-mutation-uid")
		up.Finalizers = []string{upgradeFinalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		up.Status.StagingRequested = true
		up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(),
		devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up)); err != nil || !result.Owned {
		t.Fatalf("seed mutation Lease: result=%+v err=%v", result, err)
	}

	var current opsv1alpha1.IOSXESoftwareUpgrade
	key := types.NamespacedName{Namespace: up.Namespace, Name: up.Name}
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := r.Client.Delete(context.Background(), &current); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}

	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("mutation quarantine Lease was released after deletion: %v", err)
	}
}

func TestDeleteCompatibilityRiskAcquiresAndRetainsQuarantineLease(t *testing.T) {
	tests := []struct {
		name         string
		model        opsv1alpha1.UpgradeExecutionModel
		phase        opsv1alpha1.UpgradePhase
		foreignLease bool
	}{
		{name: "markerless activating", phase: opsv1alpha1.UpgradePhaseActivating},
		{name: "markerless terminal failed", phase: opsv1alpha1.UpgradePhaseFailed},
		{name: "unsupported pending", model: opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2"), phase: opsv1alpha1.UpgradePhasePending},
		{name: "unsupported future phase", model: opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2"), phase: opsv1alpha1.UpgradePhase("FutureMutating")},
		{name: "foreign lease", model: opsv1alpha1.UpgradeExecutionModel("AtMostOnceV2"), phase: opsv1alpha1.UpgradePhase("FutureMutating"), foreignLease: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-delete-compat-"+strings.ReplaceAll(tt.name, " ", "-"), func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("upgrade-delete-compat-uid-" + strings.ReplaceAll(tt.name, " ", "-"))
				up.Finalizers = []string{upgradeFinalizer}
				up.Status.ExecutionModel = tt.model
				up.Status.Phase = tt.phase
			})
			r := newReconciler(t, rig, up)
			r.DeviceNamespace = up.Namespace
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace, TTL: 26 * time.Hour}
			if tt.foreignLease {
				if result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(),
					devicecoordination.MutationLeaseFamily, "foreign/holder"); err != nil || !result.Owned {
					t.Fatalf("seed foreign Lease: result=%+v err=%v", result, err)
				}
			}

			key := client.ObjectKeyFromObject(up)
			var current opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), key, &current); err != nil {
				t.Fatalf("get upgrade: %v", err)
			}
			if err := r.Client.Delete(context.Background(), &current); err != nil {
				t.Fatalf("delete upgrade: %v", err)
			}
			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
			if err != nil {
				t.Fatalf("delete reconcile: %v", err)
			}
			if rig.os.verifyCalls != 0 || rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("deletion performed device RPCs: verify=%d install=%d activate=%d",
					rig.os.verifyCalls, rig.os.installCalls, rig.os.activateCalls)
			}

			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: leaseName}, &lease); err != nil {
				t.Fatalf("get quarantine Lease: %v", err)
			}
			wantHolder := upgradeLeaseIdentity(up)
			if tt.foreignLease {
				wantHolder = "foreign/holder"
				if result.RequeueAfter <= 0 {
					t.Fatalf("foreign Lease result=%+v, want requeue", result)
				}
			}
			if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != wantHolder {
				t.Fatalf("Lease holder=%v, want %q", lease.Spec.HolderIdentity, wantHolder)
			}

			err = r.Client.Get(context.Background(), key, &current)
			if tt.foreignLease {
				if err != nil {
					t.Fatalf("foreign Lease conflict removed upgrade/finalizer: %v", err)
				}
				if !controllerutil.ContainsFinalizer(&current, upgradeFinalizer) {
					t.Fatalf("foreign Lease conflict cleared finalizer: %v", current.Finalizers)
				}
			} else if err == nil && controllerutil.ContainsFinalizer(&current, upgradeFinalizer) {
				t.Fatalf("owned quarantine did not clear finalizer: %v", current.Finalizers)
			}
		})
	}
}

func TestMutationLeaseScopesDeviceAndHolderIdentity(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-lease-scope", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-uid-a")
		up.Status.Phase = opsv1alpha1.UpgradePhaseResolving
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "site-a"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	owned, _, err := r.ensureMutationLease(context.Background(), up, r.now())
	if err != nil {
		t.Fatalf("ensure mutation lease: %v", err)
	}
	if !owned {
		t.Fatal("mutation lease was not acquired")
	}

	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("get mutation lease: %v", err)
	}
	wantHolder := upgradeLeaseIdentity(up)
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != wantHolder {
		t.Fatalf("holder identity=%v, want %q", lease.Spec.HolderIdentity, wantHolder)
	}
	if got := r.mutationLeaseDeviceKey(); strings.Contains(got, "/") || len(got) > 63 {
		t.Fatalf("device lease key %q is not label-safe", got)
	}

	otherNamespace := *r
	otherNamespace.DeviceNamespace = "site-b"
	if otherNamespace.mutationLeaseDeviceKey() == r.mutationLeaseDeviceKey() {
		t.Fatal("same-named devices in different namespaces share a mutation lease key")
	}
	otherGeneration := up.DeepCopy()
	otherGeneration.UID = types.UID("upgrade-uid-b")
	if upgradeLeaseIdentity(otherGeneration) == upgradeLeaseIdentity(up) {
		t.Fatal("recreated same-named upgrade shares the prior CR holder identity")
	}
}

func TestTerminalStatusReleasesMutationLeaseImmediately(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-lease-release", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-uid")
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.PrimarySupervisorActivationRequested = true
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up)); err != nil || !result.Owned {
		t.Fatalf("seed mutation lease: result=%+v err=%v", result, err)
	}

	if _, err := r.terminal(context.Background(), up, opsv1alpha1.UpgradePhaseFailed, "TestFailure", "done", r.now()); err != nil {
		t.Fatalf("persist terminal status: %v", err)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("terminal mutation lease still exists: %v", err)
	}
}

func TestIndeterminateTerminalStatusRetainsMutationLease(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-lease-quarantine", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID("upgrade-uid")
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferInterrupted
		up.Status.PrimarySupervisorInstallRequested = true
	})
	r := newReconciler(t, rig, up)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up)); err != nil || !result.Owned {
		t.Fatalf("seed mutation lease: result=%+v err=%v", result, err)
	}

	if _, err := r.terminal(context.Background(), up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallOutcomeUnknown", "outcome is unknown", r.now()); err != nil {
		t.Fatalf("persist indeterminate terminal status: %v", err)
	}
	leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("indeterminate terminal status released its quarantine lease: %v", err)
	}
}

func TestStagingOperationMismatchRequiresLeaseQuarantine(t *testing.T) {
	up := newUpgrade("upgrade-staging-correlation-lost", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.StagingRequested = true
		up.Status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
		up.Status.Phase = opsv1alpha1.UpgradePhaseValidationFailed
		up.Status.FailureReason = "StagingOperationMismatch"
	})
	if !retainMutationLeaseUntilExpiry(up) {
		t.Fatal("a submitted staging operation with lost correlation did not retain the mutation Lease")
	}
}

func TestActivationControlTimeoutRequiresLeaseQuarantine(t *testing.T) {
	up := newUpgrade("upgrade-activation-control-timeout", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.PrimarySupervisorActivationRequested = true
		up.Status.Phase = opsv1alpha1.UpgradePhaseFailed
		up.Status.FailureReason = "ActivationControlTimeout"
	})
	if !retainMutationLeaseUntilExpiry(up) {
		t.Fatal("an accepted activation with unverified convergence did not retain the mutation Lease")
	}
}

func TestStaleStatusUpdateCannotRegressNewerPhase(t *testing.T) {
	for _, newerPhase := range []opsv1alpha1.UpgradePhase{
		opsv1alpha1.UpgradePhaseVerifying,
		opsv1alpha1.UpgradePhaseSucceeded,
	} {
		t.Run(string(newerPhase), func(t *testing.T) {
			rig := newRig(t)
			stale := newUpgrade("upgrade-stale-status", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
			})
			r := newReconciler(t, rig, stale)
			key := types.NamespacedName{Namespace: stale.Namespace, Name: stale.Name}
			var current opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), key, &current); err != nil {
				t.Fatalf("get current: %v", err)
			}
			current.Status.Phase = newerPhase
			current.Status.Message = "newer state"
			if err := r.Client.Status().Update(context.Background(), &current); err != nil {
				t.Fatalf("seed newer status: %v", err)
			}

			if _, err := r.requeueAwaitingReachability(context.Background(), stale, errors.New("stale observation"), r.now()); err != nil {
				t.Fatalf("stale status update: %v", err)
			}
			if err := r.Client.Get(context.Background(), key, &current); err != nil {
				t.Fatalf("get status: %v", err)
			}
			if current.Status.Phase != newerPhase || current.Status.Message != "newer state" {
				t.Fatalf("stale update regressed status: phase=%q message=%q", current.Status.Phase, current.Status.Message)
			}
		})
	}
}

func TestStaleSamePhaseTerminalCannotCrossDurableMilestone(t *testing.T) {
	tests := []struct {
		name        string
		phase       opsv1alpha1.UpgradePhase
		reason      string
		prepare     func(*opsv1alpha1.IOSXESoftwareUpgradeStatus)
		claim       func(*opsv1alpha1.IOSXESoftwareUpgradeStatus)
		claimIntact func(opsv1alpha1.IOSXESoftwareUpgradeStatus) bool
	}{
		{
			name:  "content binding",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.SourceDigest = "sha256:" + strings.Repeat("a", 64)
				status.SourceSize = 4096
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.SourceDigest == "sha256:"+strings.Repeat("a", 64) && status.SourceSize == 4096
			},
		},
		{
			name:  "previous version binding",
			phase: opsv1alpha1.UpgradePhaseResolving,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.PreviousVersion = "17.14.01a"
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.PreviousVersion == "17.14.01a"
			},
		},
		{
			name:  "validated version binding",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.ValidatedVersion = "17.15.01a.0.1"
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.ValidatedVersion == "17.15.01a.0.1"
			},
		},
		{
			name:  "individual supervisor requirement",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.IndividualSupervisorInstall = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.IndividualSupervisorInstall
			},
		},
		{
			name:   "primary install success vs stale timeout",
			phase:  opsv1alpha1.UpgradePhaseTransferring,
			reason: "InstallOutcomeUnknown",
			prepare: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.PrimarySupervisorInstallRequested = true
			},
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.PrimarySupervisorInstalled = true
				status.ValidatedVersion = "17.15.01a.0.1"
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.PrimarySupervisorInstalled && status.ValidatedVersion == "17.15.01a.0.1"
			},
		},
		{
			name:  "standby install completion",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			prepare: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StandbySupervisorInstallRequested = true
			},
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StandbySupervisorInstalled = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.StandbySupervisorInstalled
			},
		},
		{
			name:  "standby activation completion",
			phase: opsv1alpha1.UpgradePhaseAwaitingReachability,
			prepare: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StandbySupervisorActivationRequested = true
			},
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StandbySupervisorActivated = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.StandbySupervisorActivated
			},
		},
		{
			name:  "install deadline anchor",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.InstallStartTime = &metav1.Time{Time: time.Unix(1_699_999_000, 0).UTC()}
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.InstallStartTime != nil && status.InstallStartTime.Time.Equal(time.Unix(1_699_999_000, 0).UTC())
			},
		},
		{
			name:  "staging operation binding",
			phase: opsv1alpha1.UpgradePhaseStaging,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StagingOperationID = "b66ddfbe-959f-4831-b6e8-e72812af7819"
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.StagingOperationID == "b66ddfbe-959f-4831-b6e8-e72812af7819"
			},
		},
		{
			name:  "staging request",
			phase: opsv1alpha1.UpgradePhaseStaging,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StagingRequested = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.StagingRequested
			},
		},
		{
			name:  "primary install request",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.PrimarySupervisorInstallRequested = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.PrimarySupervisorInstallRequested
			},
		},
		{
			name:  "standby install request",
			phase: opsv1alpha1.UpgradePhaseTransferring,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StandbySupervisorInstallRequested = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.StandbySupervisorInstallRequested
			},
		},
		{
			name:  "primary activation request",
			phase: opsv1alpha1.UpgradePhaseActivating,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.PrimarySupervisorActivationRequested = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.PrimarySupervisorActivationRequested
			},
		},
		{
			name:  "standby activation request",
			phase: opsv1alpha1.UpgradePhaseActivating,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.StandbySupervisorActivationRequested = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.StandbySupervisorActivationRequested
			},
		},
		{
			name:  "NoReboot acceptance",
			phase: opsv1alpha1.UpgradePhaseActivating,
			prepare: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.PrimarySupervisorActivationRequested = true
			},
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.NoRebootActivationAccepted = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.NoRebootActivationAccepted
			},
		},
		{
			name:  "rollback activation request",
			phase: opsv1alpha1.UpgradePhaseRollingBack,
			claim: func(status *opsv1alpha1.IOSXESoftwareUpgradeStatus) {
				status.RollbackActivationRequested = true
			},
			claimIntact: func(status opsv1alpha1.IOSXESoftwareUpgradeStatus) bool {
				return status.RollbackActivationRequested
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-same-phase-cas", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("upgrade-same-phase-cas-uid")
				up.Status.Phase = tt.phase
				if tt.prepare != nil {
					tt.prepare(&up.Status)
				}
			})
			r := newReconciler(t, rig, up)
			r.DeviceNamespace = "default"
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
			if result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(),
				devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up)); err != nil || !result.Owned {
				t.Fatalf("seed mutation Lease: result=%+v err=%v", result, err)
			}

			stale := up.DeepCopy()
			key := types.NamespacedName{Namespace: up.Namespace, Name: up.Name}
			var current opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), key, &current); err != nil {
				t.Fatalf("get current: %v", err)
			}
			tt.claim(&current.Status)
			if err := r.Client.Status().Update(context.Background(), &current); err != nil {
				t.Fatalf("seed concurrent claim: %v", err)
			}

			reason := tt.reason
			if reason == "" {
				reason = "StaleFailure"
			}
			result, err := r.terminal(context.Background(), stale, opsv1alpha1.UpgradePhaseValidationFailed,
				reason, "stale same-phase failure", r.now())
			if err != nil {
				t.Fatalf("stale terminal: %v", err)
			}
			if result.RequeueAfter <= 0 {
				t.Fatalf("stale terminal result=%+v, want safe requeue", result)
			}
			if err := r.Client.Get(context.Background(), key, &current); err != nil {
				t.Fatalf("get status: %v", err)
			}
			if current.Status.Phase != tt.phase || !tt.claimIntact(current.Status) {
				t.Fatalf("stale terminal crossed durable claim: status=%+v", current.Status)
			}

			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
				t.Fatalf("stale terminal released active mutation Lease: %v", err)
			}
		})
	}
}

func TestClaimOwnerCanAdvanceWithMirroredMutationToken(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-claim-owner", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = "17.15.01a"
	})
	r := newReconciler(t, rig, up)

	claimed, deleting, err := r.claimActivation(context.Background(), up, false, "ActivationRequested", "claim", r.now())
	if err != nil {
		t.Fatalf("claimActivation: %v", err)
	}
	if deleting != nil {
		t.Fatalf("claimActivation unexpectedly observed deletion: %+v", deleting.DeletionTimestamp)
	}
	if !claimed || !up.Status.PrimarySupervisorActivationRequested {
		t.Fatalf("claim was not mirrored into owner status: claimed=%t status=%+v", claimed, up.Status)
	}
	if _, err := r.updateStatus(context.Background(), up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		cur.Status.Message = "activation dispatched"
	}, reconcile.Result{}); err != nil {
		t.Fatalf("owner post-claim update: %v", err)
	}

	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: up.Namespace, Name: up.Name}, &got); err != nil {
		t.Fatalf("get status: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhaseAwaitingReachability ||
		!got.Status.PrimarySupervisorActivationRequested {
		t.Fatalf("owner post-claim update was rejected: status=%+v", got.Status)
	}
}

func TestMutationClaimsRefuseFreshDeletion(t *testing.T) {
	type claimFunc func(*Reconciler, *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error)
	tests := []struct {
		name    string
		prepare func(*opsv1alpha1.IOSXESoftwareUpgrade)
		claim   claimFunc
		marked  func(*opsv1alpha1.IOSXESoftwareUpgrade) bool
	}{
		{
			name: "device-file staging",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseStaging
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimStaging(context.Background(), up, "claim staging", r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool { return up.Status.StagingRequested },
		},
		{
			name: "primary install",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimInstallAttempt(context.Background(), up, false, r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
				return up.Status.PrimarySupervisorInstallRequested
			},
		},
		{
			name: "standby install",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimInstallAttempt(context.Background(), up, true, r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
				return up.Status.StandbySupervisorInstallRequested
			},
		},
		{
			name: "primary reload activation",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimActivation(context.Background(), up, false, "ActivationRequested", "claim activation", r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
				return up.Status.PrimarySupervisorActivationRequested
			},
		},
		{
			name: "primary NoReboot activation",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimActivation(context.Background(), up, false, "ActivationRequested", "claim NoReboot activation", r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
				return up.Status.PrimarySupervisorActivationRequested
			},
		},
		{
			name: "standby activation",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
				up.Status.IndividualSupervisorInstall = true
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimActivation(context.Background(), up, true, "StandbyActivationRequested", "claim standby activation", r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
				return up.Status.StandbySupervisorActivationRequested
			},
		},
		{
			name: "rollback activation",
			prepare: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
				up.Status.PreviousVersion = "17.14.01a"
			},
			claim: func(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
				return r.claimRollbackActivation(context.Background(), up, "17.14.01a", "claim rollback", r.now())
			},
			marked: func(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
				return up.Status.RollbackActivationRequested
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			current := newUpgrade("upgrade-delete-at-claim", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{upgradeFinalizer, "test.cisco.vk/retain"}
				tt.prepare(up)
			})
			stale := current.DeepCopy()
			deletedAt := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
			current.DeletionTimestamp = &deletedAt
			r := newReconciler(t, rig, current)

			claimed, deleting, err := tt.claim(r, stale)
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			if claimed || deleting == nil || deleting.DeletionTimestamp.IsZero() {
				t.Fatalf("claimed=%t deleting=%v, want refused fresh deletion", claimed, deleting)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(current), &got); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if tt.marked(&got) {
				t.Fatalf("mutation marker was persisted after deletion: status=%+v", got.Status)
			}
			if rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatalf("device RPC dispatched after deletion: Install=%d Activate=%d", rig.os.installCalls, rig.os.activateCalls)
			}
		})
	}
}

func TestDeletionRacingSoftwareClaimCleansLeaseAccordingToPriorMutation(t *testing.T) {
	for _, tt := range []struct {
		name          string
		priorMutation bool
		vanish        bool
	}{
		{name: "first mutation deletion timestamp"},
		{name: "prior mutation deletion timestamp", priorMutation: true},
		{name: "first mutation vanished object", vanish: true},
		{name: "prior mutation vanished object", priorMutation: true, vanish: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rig := newRig(t)
			up := newUpgrade("upgrade-delete-claim-lease", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.UID = types.UID("upgrade-delete-claim-lease-uid")
				up.Finalizers = []string{upgradeFinalizer}
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
				up.Status.ValidatedVersion = "17.15.01a"
				if tt.priorMutation {
					up.Status.PrimarySupervisorInstallRequested = true
					up.Status.PrimarySupervisorInstalled = true
				}
			})
			r := newReconciler(t, rig, up)
			r.DeviceNamespace = "default"
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
			key := client.ObjectKeyFromObject(up)
			r.Reader = &deleteUpgradeOnNthGetReader{
				Reader:  r.Client,
				writer:  r.Client,
				key:     key,
				trigger: 2,
				vanish:  tt.vanish,
			}

			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if rig.os.activateCalls != 0 || rig.os.installCalls != 0 {
				t.Fatalf("device mutation dispatched after deletion: Activate=%d Install=%d", rig.os.activateCalls, rig.os.installCalls)
			}
			leaseName := engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily)
			var lease coordv1.Lease
			err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease)
			if tt.priorMutation && err != nil {
				t.Fatalf("prior mutation quarantine Lease was released: %v", err)
			}
			if !tt.priorMutation && !apierrors.IsNotFound(err) {
				t.Fatalf("unused mutation Lease remains after claim-time deletion: %v", err)
			}
		})
	}
}

func TestContentPinFirstWriterWinsWithinTransferringPhase(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-content-pin-cas", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	stale := up.DeepCopy()
	key := types.NamespacedName{Namespace: up.Namespace, Name: up.Name}

	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("get current: %v", err)
	}
	winnerDigest := "sha256:" + strings.Repeat("a", 64)
	current.Status.SourceDigest = winnerDigest
	current.Status.SourceSize = 4096
	current.Status.IndividualSupervisorInstall = true
	if err := r.Client.Status().Update(context.Background(), &current); err != nil {
		t.Fatalf("seed winning pin: %v", err)
	}

	pinned, err := r.pinResolvedImage(context.Background(), stale, &ResolvedImage{
		Digest: "sha256:" + strings.Repeat("b", 64),
		Size:   8192,
	}, false, r.now())
	if err != nil {
		t.Fatalf("stale content pin: %v", err)
	}
	if pinned {
		t.Fatal("stale resolver reported that it won the content-pin CAS")
	}
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("get status: %v", err)
	}
	if current.Status.SourceDigest != winnerDigest || current.Status.SourceSize != 4096 ||
		!current.Status.IndividualSupervisorInstall {
		t.Fatalf("stale resolver replaced content/supervisor binding: status=%+v", current.Status)
	}
}

func TestStaleInstallDeadlineCannotExtendExistingDeadline(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("upgrade-install-deadline-cas", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	stale := up.DeepCopy()
	key := types.NamespacedName{Namespace: up.Namespace, Name: up.Name}
	winner := metav1.NewTime(time.Unix(1_699_999_000, 0).UTC())
	later := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())

	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("get current: %v", err)
	}
	current.Status.InstallStartTime = &winner
	if err := r.Client.Status().Update(context.Background(), &current); err != nil {
		t.Fatalf("seed install deadline: %v", err)
	}

	if _, err := r.updateStatus(context.Background(), stale, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.InstallStartTime = &later
	}, reconcile.Result{}); err != nil {
		t.Fatalf("stale deadline update: %v", err)
	}
	if err := r.Client.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("get status: %v", err)
	}
	if current.Status.InstallStartTime == nil || !current.Status.InstallStartTime.Equal(&winner) {
		t.Fatalf("install deadline moved from %s to %v", winner.Time, current.Status.InstallStartTime)
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

func TestSoftwareUpgradeSpanOutcomeReflectsTerminalBusinessFailure(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "upgrade")
	setSoftwareUpgradeSpanOutcome(
		span,
		opsv1alpha1.UpgradePhaseValidationFailed,
		"LocalPathHashMismatch",
		"hash did not match",
	)
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans=%d, want 1", len(ended))
	}
	if got := ended[0].Status(); got.Code != otelcodes.Error || got.Description != "LocalPathHashMismatch" {
		t.Fatalf("status=%#v, want terminal business failure", got)
	}
	attrs := map[string]string{}
	for _, kv := range ended[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if attrs["cvk.softwareupgrade.phase"] != string(opsv1alpha1.UpgradePhaseValidationFailed) ||
		attrs["cvk.softwareupgrade.reason"] != "LocalPathHashMismatch" {
		t.Fatalf("terminal outcome attributes=%#v", attrs)
	}
}
