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

package deviceoperation

import (
	"context"
	"encoding/json"
	"fmt"
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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"
)

// --- minimal fake gNOI server (reused shape from gnoi package tests) ---

type opsFakeSystem struct {
	syspb.UnimplementedSystemServer
}

func (opsFakeSystem) Time(context.Context, *syspb.TimeRequest) (*syspb.TimeResponse, error) {
	return &syspb.TimeResponse{Time: uint64(time.Unix(7, 0).UnixNano())}, nil
}

func (opsFakeSystem) RebootStatus(context.Context, *syspb.RebootStatusRequest) (*syspb.RebootStatusResponse, error) {
	return &syspb.RebootStatusResponse{Active: false}, nil
}

type opsFakeFile struct {
	filepb.UnimplementedFileServer
}

func (opsFakeFile) Stat(context.Context, *filepb.StatRequest) (*filepb.StatResponse, error) {
	return &filepb.StatResponse{
		Stats: []*filepb.StatInfo{{Path: "flash:cat9k.bin", Size: 42, Permissions: 0o644}},
	}, nil
}

type opsFakeCert struct {
	certpb.UnimplementedCertificateManagementServer
}

func (opsFakeCert) GetCertificates(context.Context, *certpb.GetCertificatesRequest) (*certpb.GetCertificatesResponse, error) {
	return &certpb.GetCertificatesResponse{}, nil
}

func (opsFakeCert) CanGenerateCSR(context.Context, *certpb.CanGenerateCSRRequest) (*certpb.CanGenerateCSRResponse, error) {
	return &certpb.CanGenerateCSRResponse{CanGenerate: true}, nil
}

type opsFakeOS struct {
	ospb.UnimplementedOSServer
}

func (opsFakeOS) Verify(context.Context, *ospb.VerifyRequest) (*ospb.VerifyResponse, error) {
	return &ospb.VerifyResponse{Version: "17.15.01a"}, nil
}

func newOpsGNOIClient(t *testing.T) *gnoi.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	syspb.RegisterSystemServer(srv, opsFakeSystem{})
	filepb.RegisterFileServer(srv, opsFakeFile{})
	certpb.RegisterCertificateManagementServer(srv, opsFakeCert{})
	ospb.RegisterOSServer(srv, opsFakeOS{})
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
		t.Fatalf("dial fake gNOI: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c, err := gnoi.New(conn, gnoi.Options{})
	if err != nil {
		t.Fatalf("gnoi.New: %v", err)
	}
	return c
}

type staticGNOI struct{ c *gnoi.Client }

func (s *staticGNOI) GNOIClient(context.Context) (*gnoi.Client, error) { return s.c, nil }

type failingGNOI struct {
	err                    error
	provisioningInProgress bool
}

func (f failingGNOI) GNOIClient(context.Context) (*gnoi.Client, error) { return nil, f.err }

func (f failingGNOI) GNOICertificateProvisioningInProgress() bool {
	return f.provisioningInProgress
}

func runGNOIOperation(t *testing.T, op *opsv1alpha1.DeviceOperation) opsv1alpha1.DeviceOperation {
	t.Helper()
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{
		Client:     c,
		DeviceName: "dev1",
		GNOI:       &staticGNOI{c: newOpsGNOIClient(t)},
		Now:        func() time.Time { return time.Unix(100, 0).UTC() },
	}
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get back: %v", err)
	}
	return got
}

func TestReconcileGNOITime(t *testing.T) {
	op := newOperation("time", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOITime
	})
	got := runGNOIOperation(t, op)
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if len(got.Status.Outputs) != 1 {
		t.Fatalf("outputs: %+v", got.Status.Outputs)
	}
	var payload struct {
		Time string `json:"time"`
	}
	if err := json.Unmarshal([]byte(got.Status.Outputs[0].Output), &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if !strings.HasPrefix(payload.Time, "1970-01-01T00:00:07") {
		t.Fatalf("device time encoded as %q", payload.Time)
	}
}

func TestReconcileGNOIFileStat(t *testing.T) {
	op := newOperation("stat", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOIFileStat
		op.Spec.Operation.GNOI = &opsv1alpha1.GNOIArgs{
			File: &opsv1alpha1.GNOIFileArgs{Path: "flash:cat9k.bin"},
		}
	})
	got := runGNOIOperation(t, op)
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Outputs[0].Output, "flash:cat9k.bin") {
		t.Fatalf("expected file path in output, got %q", got.Status.Outputs[0].Output)
	}
}

func TestReconcileGNOIFileStatRequiresPath(t *testing.T) {
	op := newOperation("stat-empty", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOIFileStat
	})
	got := runGNOIOperation(t, op)
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("expected Failed, got %q", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "path is required") {
		t.Fatalf("expected path-required error, got %q", got.Status.Message)
	}
}

func TestReconcileGNOIOSVerify(t *testing.T) {
	op := newOperation("verify", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOIOSVerify
	})
	got := runGNOIOperation(t, op)
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Outputs[0].Output, "17.15.01a") {
		t.Fatalf("missing version in output: %q", got.Status.Outputs[0].Output)
	}
}

func TestReconcileGNOIProvisioningInProgressRequeues(t *testing.T) {
	op := newOperation("verify-provisioning", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOIOSVerify
	})
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{
		Client:     c,
		DeviceName: "dev1",
		GNOI: failingGNOI{err: &gnoi.ErrProvisioningInProgress{
			CertificateID: "cvk-gnoi",
		}},
		Now: func() time.Time { return time.Unix(100, 0).UTC() },
	}

	result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.RequeueAfter != 10*time.Second {
		t.Fatalf("RequeueAfter=%v, want 10s", result.RequeueAfter)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get back: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhasePending {
		t.Fatalf("phase=%q, want Pending", got.Status.Phase)
	}
	var ready *metav1.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == "Ready" {
			ready = &got.Status.Conditions[i]
			break
		}
	}
	if ready == nil || ready.Reason != "GNOIProvisioning" {
		t.Fatalf("Ready condition=%+v, want reason GNOIProvisioning", ready)
	}
	if got.Status.CompletionTime != nil {
		t.Fatalf("CompletionTime=%v, want nil", got.Status.CompletionTime)
	}
}

func TestReconcileGNOIProvisioningRestartErrorsRequeue(t *testing.T) {
	for _, tt := range []struct {
		name string
		code codes.Code
	}{
		{name: "unavailable", code: codes.Unavailable},
		{name: "IOS XE event aborted", code: codes.Aborted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := newOperation("verify-provisioning-restart", func(op *opsv1alpha1.DeviceOperation) {
				op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOIOSVerify
			})
			scheme := newScheme(t)
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(op).
				WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
				Build()
			r := &Reconciler{
				Client:     c,
				DeviceName: "dev1",
				GNOI: failingGNOI{
					err:                    status.Error(tt.code, "gNXI restarting"),
					provisioningInProgress: true,
				},
				Now: func() time.Time { return time.Unix(100, 0).UTC() },
			}

			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: op.Namespace,
				Name:      op.Name,
			}})
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if result.RequeueAfter != 10*time.Second {
				t.Fatalf("RequeueAfter=%v, want 10s", result.RequeueAfter)
			}
			var got opsv1alpha1.DeviceOperation
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
				t.Fatalf("get back: %v", err)
			}
			if got.Status.Phase != opsv1alpha1.OperationPhasePending {
				t.Fatalf("phase=%q, want Pending", got.Status.Phase)
			}
		})
	}
}

func TestGNOIProvisioningRestartRetryRequiresActiveBootstrap(t *testing.T) {
	err := status.Error(codes.Unavailable, "ordinary outage")
	provider := failingGNOI{err: err, provisioningInProgress: false}
	if gnoiProvisioningRestartPending(provider, opsv1alpha1.OperationKindGNOIOSVerify, err) {
		t.Fatal("configured but inactive provisioning changed ordinary OS.Verify outage handling")
	}
	provider.provisioningInProgress = true
	if gnoiProvisioningRestartPending(provider, opsv1alpha1.OperationKindGNOITime, err) {
		t.Fatal("active OS provisioning changed a non-OS operation's outage handling")
	}

	provider.provisioningInProgress = true
	wrappedDeadline := fmt.Errorf("verify after restart: %w", context.DeadlineExceeded)
	if !gnoiProvisioningRestartPending(provider, opsv1alpha1.OperationKindGNOIOSVerify, wrappedDeadline) {
		t.Fatal("active provisioning did not classify a wrapped local deadline as restart pending")
	}
}

func TestReconcileGNOIRebootStatus(t *testing.T) {
	op := newOperation("reboot-status", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOIRebootStatus
	})
	got := runGNOIOperation(t, op)
	if got.Status.Phase != opsv1alpha1.OperationPhaseSucceeded {
		t.Fatalf("phase=%q msg=%q", got.Status.Phase, got.Status.Message)
	}
	if !strings.Contains(got.Status.Message, "reboot inactive") {
		t.Fatalf("expected reboot inactive message, got %q", got.Status.Message)
	}
}

func TestReconcileGNOIWithoutProviderFailsFast(t *testing.T) {
	op := newOperation("time-noprov", func(op *opsv1alpha1.DeviceOperation) {
		op.Spec.Operation.Kind = opsv1alpha1.OperationKindGNOITime
	})
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(op).
		WithStatusSubresource(&opsv1alpha1.DeviceOperation{}).
		Build()
	r := &Reconciler{
		Client:     c,
		DeviceName: "dev1",
		Now:        func() time.Time { return time.Unix(100, 0).UTC() },
	}
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: op.Namespace,
		Name:      op.Name,
	}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got opsv1alpha1.DeviceOperation
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got); err != nil {
		t.Fatalf("get back: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.OperationPhaseFailed {
		t.Fatalf("expected Failed without provider, got %q", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "gnoi provider is not configured") {
		t.Fatalf("expected provider-missing message, got %q", got.Status.Message)
	}
}
