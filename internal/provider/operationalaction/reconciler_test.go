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
	"errors"
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
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
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
	rebootErr         error
	rebootEntered     chan struct{}
	rebootRelease     chan struct{}
}

func (f *fakeSys) Reboot(ctx context.Context, req *syspb.RebootRequest) (*syspb.RebootResponse, error) {
	f.rebootCalls.Add(1)
	f.rebootForce = req.Force
	f.rebootDelay = req.Delay
	if f.rebootEntered != nil {
		select {
		case f.rebootEntered <- struct{}{}:
		default:
		}
	}
	if f.rebootRelease != nil {
		select {
		case <-f.rebootRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.rebootErr != nil {
		return nil, f.rebootErr
	}
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

type deleteActionOnNthGetReader struct {
	client.Reader
	writer  client.Client
	key     types.NamespacedName
	trigger int
	vanish  bool

	mu   sync.Mutex
	gets int
}

type failActionListReader struct {
	client.Reader
}

func (r *failActionListReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("injected compatibility list failure")
}

func (r *deleteActionOnNthGetReader) Get(
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
		var current opsv1alpha1.IOSXEOperationalAction
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

func TestOperationalActionWaitsForSharedDeviceMutationLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-blocked", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	deviceKey := devicecoordination.DeviceKey("default", "dev1")
	if result, err := r.MutationLeaser.Acquire(context.Background(), deviceKey,
		devicecoordination.MutationLeaseFamily, "software-upgrade/other-uid"); err != nil || !result.Owned {
		t.Fatalf("seed shared mutation lease: result=%+v err=%v", result, err)
	}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhasePending || got.Status.InvocationID != "" {
		t.Fatalf("blocked action phase=%q invocationID=%q", got.Status.Phase, got.Status.InvocationID)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatal("blocked action reached the device")
	}
}

func TestOperationalActionQuarantinesLegacyUpgradeBeforeDeviceAccess(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-blocked-by-legacy-upgrade", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
	})
	legacy := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "legacy-upgrade", UID: types.UID("legacy-upgrade-uid")},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
		},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{Phase: opsv1alpha1.UpgradePhaseActivating},
	}
	r := newReconciler(t, rig, a, legacy)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhasePending || got.Status.InvocationID != "" {
		t.Fatalf("status=%+v, want uninvoked Pending action", got.Status)
	}
	if calls := r.GNOI.(*staticGNOI).clientCalls.Load(); calls != 0 || rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("legacy guard touched device: clientCalls=%d rebootCalls=%d", calls, rig.sys.rebootCalls.Load())
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("get compatibility Lease: %v", err)
	}
	wantHolder := devicecoordination.HolderIdentity("software-upgrade", legacy.Namespace, legacy.Name, string(legacy.UID))
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != wantHolder {
		t.Fatalf("holder=%v, want %q", lease.Spec.HolderIdentity, wantHolder)
	}
}

func TestOperationalActionQuarantinesLegacyTerminalUpgradeBeforeDeviceAccess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rig := newRig(t)
	a := newAction("reboot-blocked-by-legacy-terminal-upgrade", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
	})
	completed := metav1.NewTime(now)
	legacy := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "legacy-terminal-upgrade",
			UID:               types.UID("legacy-terminal-upgrade-uid"),
			CreationTimestamp: metav1.NewTime(now.Add(-time.Hour)),
		},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
		},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
			Phase:          opsv1alpha1.UpgradePhaseFailed,
			CompletionTime: &completed,
		},
	}
	r := newReconciler(t, rig, a, legacy)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhasePending || got.Status.InvocationID != "" {
		t.Fatalf("status=%+v, want uninvoked Pending action", got.Status)
	}
	if calls := r.GNOI.(*staticGNOI).clientCalls.Load(); calls != 0 || rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("terminal legacy guard touched device: clientCalls=%d rebootCalls=%d", calls, rig.sys.rebootCalls.Load())
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("get compatibility Lease: %v", err)
	}
	wantHolder := devicecoordination.HolderIdentity("software-upgrade", legacy.Namespace, legacy.Name, string(legacy.UID))
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != wantHolder {
		t.Fatalf("holder=%v, want %q", lease.Spec.HolderIdentity, wantHolder)
	}
}

func TestOperationalActionAPIScanFailureFailsClosedBeforeDeviceAccess(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-list-failure", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	r.Reader = &failActionListReader{Reader: r.Client}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhasePending || got.Status.InvocationID != "" {
		t.Fatalf("status=%+v, want uninvoked Pending action", got.Status)
	}
	if got.Status.Message == "" || !strings.Contains(got.Status.Message, "compatibility list failure") {
		t.Fatalf("status message=%q, want API scan failure", got.Status.Message)
	}
	if calls := r.GNOI.(*staticGNOI).clientCalls.Load(); calls != 0 || rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("failed API scan touched device: clientCalls=%d rebootCalls=%d", calls, rig.sys.rebootCalls.Load())
	}
}

func TestSuccessfulRebootQuarantinesSharedMutationLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-quarantine", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "convergence is not observed") {
		t.Fatalf("successful reboot did not report quarantine semantics: %q", got.Status.Message)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("successful reboot did not quarantine mutation lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != actionLeaseIdentity(got) {
		t.Fatalf("lease holder=%v, want %q", lease.Spec.HolderIdentity, actionLeaseIdentity(got))
	}
	_ = runReconcile(t, r, a)
	if rig.sys.rebootCalls.Load() != 1 {
		t.Fatalf("terminal reboot redispatched; calls=%d", rig.sys.rebootCalls.Load())
	}
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("terminal reconcile released reboot quarantine: %v", err)
	}
}

func TestDelayedRebootLeaseCoversDelayAndQuarantine(t *testing.T) {
	rig := newRig(t)
	const delay = 48 * time.Hour
	const quarantine = 26 * time.Hour
	a := newAction("reboot-delayed", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
		a.Spec.Action.Reboot.DelaySeconds = int64(delay / time.Second)
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: quarantine}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("read delayed reboot lease: %v", err)
	}
	wantSeconds := int32((delay + quarantine) / time.Second)
	if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds != wantSeconds {
		t.Fatalf("lease duration=%v, want %d seconds", lease.Spec.LeaseDurationSeconds, wantSeconds)
	}
}

func TestSuccessfulCompletedActionReleasesSharedMutationLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("remove-release", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:       opsv1alpha1.ActionKindFileRemove,
			FileRemove: &opsv1alpha1.FileRemoveArgs{Path: "flash:old.bin"},
		}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("completed action retained mutation lease: %v", err)
	}
}

func TestFailedInvokedActionQuarantinesSharedMutationLease(t *testing.T) {
	rig := newRig(t)
	rig.sys.rebootErr = status.Error(codes.Unavailable, "connection lost")
	a := newAction("reboot-unknown", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-uid")
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed || got.Status.InvocationID == "" {
		t.Fatalf("phase=%q invocationID=%q", got.Status.Phase, got.Status.InvocationID)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("indeterminate action did not quarantine mutation lease: %v", err)
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

func TestCancelRebootBypassesAndPreservesSharedMutationLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("cancel-bypass", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("cancel-uid")
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:         opsv1alpha1.ActionKindCancelReboot,
			CancelReboot: &opsv1alpha1.CancelRebootArgs{Message: "abort pending reboot"},
		}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	deviceKey := devicecoordination.DeviceKey("default", "dev1")
	const existingHolder = "operational-action/reboot-owner"
	if result, err := r.MutationLeaser.Acquire(context.Background(), deviceKey,
		devicecoordination.MutationLeaseFamily, existingHolder); err != nil || !result.Owned {
		t.Fatalf("seed shared mutation lease: result=%+v err=%v", result, err)
	}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded || rig.sys.cancelRebootCalls.Load() != 1 {
		t.Fatalf("cancel phase=%q calls=%d message=%q", got.Status.Phase, rig.sys.cancelRebootCalls.Load(), got.Status.Message)
	}
	leaseName := engine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("read existing lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != existingHolder {
		t.Fatalf("cancel changed lease holder=%v, want %q", lease.Spec.HolderIdentity, existingHolder)
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
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected {
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
	if !strings.Contains(got.Status.Result, `"status":"serviceAlreadyProvisioned"`) ||
		!strings.Contains(got.Status.Result, `"certificateChanged":false`) ||
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
		!strings.Contains(got.Status.Result, `"certificateChanged":true`) ||
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
	a := newAction("reboot-running", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("released-running-action-uid")
	})
	started := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	a.Status = opsv1alpha1.IOSXEOperationalActionStatus{
		Phase:        opsv1alpha1.ActionPhaseRunning,
		InvocationID: "already-invoked",
		StartTime:    &started,
	}
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseRunning {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("running action re-dispatched reboot; calls=%d", rig.sys.rebootCalls.Load())
	}
	if calls := r.GNOI.(*staticGNOI).clientCalls.Load(); calls != 0 {
		t.Fatalf("running action acquired gNOI client; calls=%d", calls)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("released Running action did not seed quarantine Lease: %v", err)
	}
}

func TestConcurrentObserverWaitsForLiveOperationalActionOwner(t *testing.T) {
	rig := newRig(t)
	rig.sys.rebootEntered = make(chan struct{}, 1)
	rig.sys.rebootRelease = make(chan struct{})
	a := newAction("reboot-live-owner", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-live-owner-uid")
		a.Finalizers = []string{finalizerName}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	owner := *r
	observer := *r
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: a.Namespace, Name: a.Name}}
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := owner.Reconcile(ownerCtx, req)
		ownerDone <- err
	}()

	select {
	case <-rig.sys.rebootEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the claimed Reboot RPC")
	}
	result, err := observer.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("observer Reconcile: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > time.Second {
		t.Fatalf("observer RequeueAfter=%s, want a bounded wait of at most one second", result.RequeueAfter)
	}
	var during opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), req.NamespacedName, &during); err != nil {
		t.Fatalf("Get during Reboot: %v", err)
	}
	if during.Status.Phase != opsv1alpha1.ActionPhaseRunning || during.Status.InvocationID == "" ||
		during.Status.StartTime == nil || !controllerutil.ContainsFinalizer(&during, finalizerName) {
		t.Fatalf("observer disturbed live action: status=%+v finalizers=%v", during.Status, during.Finalizers)
	}

	close(rig.sys.rebootRelease)
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner Reconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the Reboot owner to finish")
	}
	var got opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after Reboot: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded || got.Status.InvocationID == "" ||
		controllerutil.ContainsFinalizer(&got, finalizerName) {
		t.Fatalf("owner completion was rejected after peer observation: status=%+v finalizers=%v", got.Status, got.Finalizers)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("successful reboot did not retain the mutation quarantine: %v", err)
	}
	if rig.sys.rebootCalls.Load() != 1 {
		t.Fatalf("Reboot calls=%d, want exactly one", rig.sys.rebootCalls.Load())
	}
}

func TestExpiredRunningActionBecomesUnknownAndRetainsLease(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-operationalActionRPCTimeout - actionResultPersistenceGrace))
	rig := newRig(t)
	a := newAction("reboot-expired-owner", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-expired-owner-uid")
		a.Finalizers = []string{finalizerName}
		a.Status.Phase = opsv1alpha1.ActionPhaseRunning
		a.Status.InvocationID = "expired-invocation"
		a.Status.StartTime = &started
	})
	r := newReconciler(t, rig, a)
	r.Now = func() time.Time { return base }
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	if result, err := r.MutationLeaser.Acquire(context.Background(), devicecoordination.DeviceKey("default", "dev1"),
		devicecoordination.MutationLeaseFamily, actionLeaseIdentity(a)); err != nil || !result.Owned {
		t.Fatalf("seed mutation Lease: result=%+v err=%v", result, err)
	}

	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed || got.Status.FailureReason != "ActionOutcomeUnknown" {
		t.Fatalf("status=%+v, want failed ActionOutcomeUnknown", got.Status)
	}
	if controllerutil.ContainsFinalizer(got, finalizerName) {
		t.Fatalf("expired action retained finalizer: %v", got.Finalizers)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("unknown action outcome released its mutation quarantine: %v", err)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("expired action replayed Reboot; calls=%d", rig.sys.rebootCalls.Load())
	}
}

func TestExpiredDeletingRunningActionFinalizesWithoutReleasingLease(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	started := metav1.NewTime(base.Add(-operationalActionRPCTimeout - actionResultPersistenceGrace))
	deleting := metav1.NewTime(base.Add(-time.Minute))
	rig := newRig(t)
	a := newAction("reboot-expired-deleting", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("action-expired-deleting-uid")
		a.Finalizers = []string{finalizerName}
		a.DeletionTimestamp = &deleting
		a.Status.Phase = opsv1alpha1.ActionPhaseRunning
		a.Status.InvocationID = "expired-deleting-invocation"
		a.Status.StartTime = &started
	})
	r := newReconciler(t, rig, a)
	r.Now = func() time.Time { return base }
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: a.Namespace, Name: a.Name}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.IOSXEOperationalAction
	err = r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expired deleting action still exists: %v status=%+v", err, got.Status)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("deleting unknown action released its mutation quarantine: %v", err)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("expired deleting action replayed Reboot; calls=%d", rig.sys.rebootCalls.Load())
	}
}

func TestLegacyTerminalDelayedRebootKeepsStableFenceUntilDeadline(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	clock := base
	started := metav1.NewTime(base)
	completed := metav1.NewTime(base)
	rig := newRig(t)
	a := newAction("legacy-succeeded-delayed-reboot", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("legacy-succeeded-delayed-reboot-uid")
		a.Spec.Action.Reboot.DelaySeconds = 7 * 24 * 60 * 60
		a.Status.Phase = opsv1alpha1.ActionPhaseSucceeded
		a.Status.InvocationID = "released-invocation"
		a.Status.StartTime = &started
		a.Status.CompletionTime = &completed
	})
	r := newReconciler(t, rig, a)
	r.Now = func() time.Time { return clock }
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("install terminal compatibility finalizer: %v", err)
	}
	var afterFinalizer opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), req.NamespacedName, &afterFinalizer); err != nil {
		t.Fatalf("get action after finalizer: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&afterFinalizer, finalizerName) {
		t.Fatalf("finalizers=%v, want compatibility finalizer", afterFinalizer.Finalizers)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("seed terminal compatibility Lease: %v", err)
	}
	var fenced opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), req.NamespacedName, &fenced); err != nil {
		t.Fatalf("get fenced action: %v", err)
	}
	resourceVersion := fenced.ResourceVersion
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("renew terminal compatibility Lease: %v", err)
	}
	var renewed opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), req.NamespacedName, &renewed); err != nil {
		t.Fatalf("get renewed action: %v", err)
	}
	if renewed.ResourceVersion != resourceVersion || !controllerutil.ContainsFinalizer(&renewed, finalizerName) {
		t.Fatalf("terminal compatibility metadata churned: beforeRV=%q afterRV=%q finalizers=%v",
			resourceVersion, renewed.ResourceVersion, renewed.Finalizers)
	}
	if rig.sys.rebootCalls.Load() != 0 || r.GNOI.(*staticGNOI).clientCalls.Load() != 0 {
		t.Fatal("terminal compatibility recovery touched the device")
	}

	clock = base.Add(7*24*time.Hour + 26*time.Hour + time.Second)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("age out terminal compatibility fence: %v", err)
	}
	var agedOut opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), req.NamespacedName, &agedOut); err != nil {
		t.Fatalf("get aged-out action: %v", err)
	}
	if controllerutil.ContainsFinalizer(&agedOut, finalizerName) {
		t.Fatalf("aged-out action retained finalizer: %v", agedOut.Finalizers)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("aged-out action retained Lease: %v", err)
	}
}

func TestDeletingLegacyTerminalDelayedRebootSeedsExtendedFence(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	started := metav1.NewTime(base)
	completed := metav1.NewTime(base)
	deleting := metav1.NewTime(base)
	rig := newRig(t)
	a := newAction("deleting-legacy-succeeded-reboot", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("deleting-legacy-succeeded-reboot-uid")
		a.Finalizers = []string{finalizerName}
		a.DeletionTimestamp = &deleting
		a.Spec.Action.Reboot.DelaySeconds = 7 * 24 * 60 * 60
		a.Status.Phase = opsv1alpha1.ActionPhaseSucceeded
		a.Status.InvocationID = "released-invocation"
		a.Status.StartTime = &started
		a.Status.CompletionTime = &completed
	})
	r := newReconciler(t, rig, a)
	r.Now = func() time.Time { return base }
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var gone opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &gone); !apierrors.IsNotFound(err) {
		t.Fatalf("deleting terminal action still exists: err=%v finalizers=%v", err, gone.Finalizers)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("get retained deletion fence: %v", err)
	}
	wantSeconds := int32((7*24*time.Hour + 26*time.Hour) / time.Second)
	if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds != wantSeconds {
		t.Fatalf("LeaseDurationSeconds=%v, want %d", lease.Spec.LeaseDurationSeconds, wantSeconds)
	}
	if rig.sys.rebootCalls.Load() != 0 || r.GNOI.(*staticGNOI).clientCalls.Load() != 0 {
		t.Fatal("deleting terminal compatibility recovery touched the device")
	}
}

func TestOperationalActionRefreshesClockAroundDispatch(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	times := []time.Time{base, base.Add(10 * time.Second), base.Add(20 * time.Second), base.Add(5 * time.Minute)}
	clockCalls := 0
	rig := newRig(t)
	a := newAction("reboot-clock-refresh", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.Finalizers = []string{finalizerName}
	})
	r := newReconciler(t, rig, a)
	r.Now = func() time.Time {
		idx := clockCalls
		clockCalls++
		if idx >= len(times) {
			return times[len(times)-1]
		}
		return times[idx]
	}

	got := runReconcile(t, r, a)
	if got.Status.StartTime == nil || !got.Status.StartTime.Time.Equal(times[2]) {
		t.Fatalf("StartTime=%v, want refreshed pre-claim time %s", got.Status.StartTime, times[2])
	}
	if got.Status.CompletionTime == nil || !got.Status.CompletionTime.Time.Equal(times[3]) {
		t.Fatalf("CompletionTime=%v, want refreshed post-dispatch time %s", got.Status.CompletionTime, times[3])
	}
}

func TestStalePreclaimTerminalCannotOverwriteRunningAction(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-terminal-race", nil)
	a.Finalizers = []string{finalizerName}
	a.Status = opsv1alpha1.IOSXEOperationalActionStatus{
		Phase:              opsv1alpha1.ActionPhaseRunning,
		InvocationID:       "active-invocation",
		ObservedGeneration: a.Generation,
	}
	r := newReconciler(t, rig, a)

	// Model a reconcile that read Pending before a concurrent reconcile
	// durably claimed this action and recorded the invocation above.
	stale := a.DeepCopy()
	stale.Status = opsv1alpha1.IOSXEOperationalActionStatus{
		Phase: opsv1alpha1.ActionPhasePending,
	}
	result, err := r.terminal(
		context.Background(),
		stale,
		opsv1alpha1.ActionPhaseFailed,
		"GNOIClient",
		"stale pre-dispatch failure",
		nil,
		time.Unix(1_700_000_000, 0).UTC(),
	)
	if err != nil {
		t.Fatalf("terminal: %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("stale terminal result=%+v, want a safe requeue", result)
	}

	var got opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.ActionPhaseRunning || got.Status.InvocationID != "active-invocation" {
		t.Fatalf("stale terminal overwrote active invocation: status=%+v", got.Status)
	}
	if !controllerutil.ContainsFinalizer(&got, finalizerName) {
		t.Fatal("stale terminal removed the active invocation finalizer")
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
			claimed, _, err := r.markRunning(context.Background(), a, time.Unix(1_700_000_000, 0).UTC())
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

	claimed, deleting, err := r.markRunning(context.Background(), a, deletedAt.Time)
	if err != nil {
		t.Fatalf("markRunning: %v", err)
	}
	if claimed {
		t.Fatal("deleting action was claimed for dispatch")
	}
	if deleting == nil {
		t.Fatal("fresh deleting action was not returned for safe Lease cleanup")
	}

	var got opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != "" || got.Status.InvocationID != "" {
		t.Fatalf("status=%+v, want unclaimed deleting action", got.Status)
	}
}

func TestDeletionRacingMarkRunningReleasesUnusedLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-delete-claim-lease", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("reboot-delete-claim-lease-uid")
		a.Finalizers = []string{finalizerName}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	key := client.ObjectKeyFromObject(a)
	r.Reader = &deleteActionOnNthGetReader{
		Reader:  r.Client,
		writer:  r.Client,
		key:     key,
		trigger: 2,
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("Reboot calls=%d after claim-time deletion, want 0", rig.sys.rebootCalls.Load())
	}
	leaseName := engine.LeaseName(
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
	)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("unused mutation Lease remains after claim-time deletion: %v", err)
	}
}

func TestMissingActionAtMarkRunningReleasesUnusedLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-missing-at-claim", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("reboot-missing-at-claim-uid")
		a.Finalizers = []string{finalizerName}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	key := client.ObjectKeyFromObject(a)
	r.Reader = &deleteActionOnNthGetReader{
		Reader:  r.Client,
		writer:  r.Client,
		key:     key,
		trigger: 2,
		vanish:  true,
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("Reboot calls=%d after claim-time disappearance, want 0", rig.sys.rebootCalls.Load())
	}
	leaseName := engine.LeaseName(
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
	)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("unused mutation Lease remains after action disappeared: %v", err)
	}
}

func TestMarkRunningDeletingPeerDoesNotAuthorizeLeaseCleanup(t *testing.T) {
	rig := newRig(t)
	current := newAction("reboot-delete-running-peer", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("reboot-delete-running-peer-uid")
		a.Finalizers = []string{finalizerName, "test.cisco.vk/retain"}
	})
	stale := current.DeepCopy()
	deletedAt := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
	startedAt := metav1.NewTime(deletedAt.Add(-time.Second))
	current.DeletionTimestamp = &deletedAt
	current.Status.Phase = opsv1alpha1.ActionPhaseRunning
	current.Status.InvocationID = "peer-invocation"
	current.Status.StartTime = &startedAt
	r := newReconciler(t, rig, current)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	leaseResult, err := r.MutationLeaser.Acquire(
		context.Background(),
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
		actionLeaseIdentity(current),
	)
	if err != nil || !leaseResult.Owned {
		t.Fatalf("seed mutation Lease: result=%+v err=%v", leaseResult, err)
	}

	claimed, deleting, err := r.markRunning(context.Background(), stale, deletedAt.Time)
	if err != nil {
		t.Fatalf("markRunning: %v", err)
	}
	if claimed || deleting != nil {
		t.Fatalf("claimed=%t deleting=%v, want peer invocation preserved without cleanup authority", claimed, deleting)
	}
	leaseName := engine.LeaseName(
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
	)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("peer mutation quarantine Lease was released: %v", err)
	}
	if rig.sys.rebootCalls.Load() != 0 {
		t.Fatalf("Reboot calls=%d, want 0", rig.sys.rebootCalls.Load())
	}
}

func TestMissingInvokedActionAtMarkRunningPreservesLease(t *testing.T) {
	rig := newRig(t)
	a := newAction("reboot-missing-invoked", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("reboot-missing-invoked-uid")
		a.Status.Phase = opsv1alpha1.ActionPhaseRunning
		a.Status.InvocationID = "recorded-invocation"
		startedAt := metav1.NewTime(time.Unix(1_700_000_000, 0).UTC())
		a.Status.StartTime = &startedAt
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	leaseResult, err := r.MutationLeaser.Acquire(
		context.Background(),
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
		actionLeaseIdentity(a),
	)
	if err != nil || !leaseResult.Owned {
		t.Fatalf("seed mutation Lease: result=%+v err=%v", leaseResult, err)
	}
	var current opsv1alpha1.IOSXEOperationalAction
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(a), &current); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := r.Client.Delete(context.Background(), &current); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	claimed, deleting, err := r.markRunning(context.Background(), a, r.now())
	if err != nil {
		t.Fatalf("markRunning: %v", err)
	}
	if claimed || deleting != nil {
		t.Fatalf("claimed=%t deleting=%v, want missing invoked action to retain quarantine", claimed, deleting)
	}
	leaseName := engine.LeaseName(
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
	)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("invoked action quarantine Lease was released: %v", err)
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
		a.UID = types.UID("action-uid")
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:    opsv1alpha1.ActionKindFilePut,
			FilePut: &opsv1alpha1.FilePutArgs{Path: "flash:dropoff.bin", ConfigMapName: "missing-cm", Permissions: 0o644},
		}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseFailed || got.Status.FailureReason != "ActionPreparationFailed" {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "get ConfigMap") {
		t.Fatalf("expected ConfigMap lookup error, got %q", got.Status.Message)
	}
	if rig.file.putCalls.Load() != 0 {
		t.Fatalf("File.Put called despite missing ConfigMap; calls=%d", rig.file.putCalls.Load())
	}
	if got.Status.InvocationID != "" {
		t.Fatalf("local preparation failure was marked invoked: %q", got.Status.InvocationID)
	}
	if calls := r.GNOI.(*staticGNOI).clientCalls.Load(); calls != 0 {
		t.Fatalf("local preparation failure acquired gNOI client; calls=%d", calls)
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("local preparation failure created mutation lease: %v", err)
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
	if got.Status.InvocationID != "" {
		t.Fatalf("local preparation failure was marked invoked: %q", got.Status.InvocationID)
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
	if got.Status.Phase != opsv1alpha1.ActionPhaseRejected {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Status.InvocationID != "" || r.GNOI.(*staticGNOI).clientCalls.Load() != 0 {
		t.Fatalf("invalid local path touched dispatch path: invocationID=%q clientCalls=%d",
			got.Status.InvocationID, r.GNOI.(*staticGNOI).clientCalls.Load())
	}
}

func TestFactoryResetDefaultsRetainCertsTrue(t *testing.T) {
	rig := newRig(t)
	a := newAction("fr-1", func(a *opsv1alpha1.IOSXEOperationalAction) {
		a.UID = types.UID("factory-reset-uid")
		a.Spec.Action = opsv1alpha1.ActionRequest{
			Kind:         opsv1alpha1.ActionKindFactoryReset,
			FactoryReset: &opsv1alpha1.FactoryResetArgs{},
		}
	})
	r := newReconciler(t, rig, a)
	r.DeviceNamespace = "default"
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: "default", TTL: 26 * time.Hour}
	got := runReconcile(t, r, a)
	if got.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if rig.reset.calls.Load() != 1 {
		t.Fatalf("FactoryReset call count=%d", rig.reset.calls.Load())
	}
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("default", "dev1"), devicecoordination.MutationLeaseFamily)
	var lease coordv1.Lease
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &lease); err != nil {
		t.Fatalf("successful factory reset did not quarantine mutation lease: %v", err)
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
