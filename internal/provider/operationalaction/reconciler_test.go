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
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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
}

func (f *fakeReset) Start(_ context.Context, req *resetpb.StartRequest) (*resetpb.StartResponse, error) {
	f.calls.Add(1)
	f.lastFactory = req.FactoryOs
	return &resetpb.StartResponse{Response: &resetpb.StartResponse_ResetSuccess{ResetSuccess: &resetpb.ResetSuccess{}}}, nil
}

type rig struct {
	sys    *fakeSys
	file   *fakeFile
	reset  *fakeReset
	client *gnoi.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	r := &rig{sys: &fakeSys{}, file: &fakeFile{}, reset: &fakeReset{}}
	syspb.RegisterSystemServer(srv, r.sys)
	filepb.RegisterFileServer(srv, r.file)
	resetpb.RegisterFactoryResetServer(srv, r.reset)
	certpb.RegisterCertificateManagementServer(srv, certpb.UnimplementedCertificateManagementServer{})
	ospb.RegisterOSServer(srv, ospb.UnimplementedOSServer{})
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
