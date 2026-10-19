// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type groupCreateWriter struct {
	client.Client
	calls int
	lose  bool
}

func (w *groupCreateWriter) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	w.calls++
	obj.SetUID(types.UID(obj.GetName() + "-uid"))
	if err := w.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if w.lose {
		return errors.New("lost creation response")
	}
	return nil
}

func TestEnsureGroupReservations(t *testing.T)          { testEnsureGroupReservations(t, false) }
func TestEnsureCancelledGroupReservations(t *testing.T) { testEnsureGroupReservations(t, true) }
func testEnsureGroupReservations(t *testing.T, cancelling bool) {
	ensure := EnsureGroupReservations
	if cancelling {
		ensure = EnsureCancelledGroupReservations
	}
	for _, mode := range []string{"valid", "claimed-cancel-partial", "denied", "missing-role", "lost-first", "partial", "claimed-partial", "revoked-partial", "collision", "late-fence", "late-intent"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "claimed-cancel-partial" && !cancelling {
				t.Skip("cancellation recovery case")
			}
			ctx := context.Background()
			c, nc, now := groupReaderFixture(t)
			_ = rbac.AddToScheme(c.Scheme())
			_ = coordination.AddToScheme(c.Scheme())
			candidates, err := ReadGroupReservationCandidates(ctx, c, nc, now)
			if err != nil {
				t.Fatal(err)
			}
			if cancelling {
				at := metav1.NewTime(now.Add(-time.Second))
				nc.Status.Configuration.Operation.Phase = "Cancelling"
				nc.Status.Configuration.Operation.CancellationRequestedAt = &at
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if _, err := ReadGroupReservationCandidates(ctx, c, nc, now); err == nil {
					t.Fatal("cancelled journal produced preparation candidates")
				}
			}
			if mode != "missing-role" {
				for _, obj := range []client.Object{
					&rbac.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: managedprotocol.NetworkManagementReadWriteClusterRole, UID: "role"}, Rules: managedprotocol.WorkerClusterRoleContracts()[managedprotocol.NetworkManagementReadWriteClusterRole]},
					&rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "network", Namespace: nc.Namespace, UID: "binding"}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadWriteClusterRole}, Subjects: []rbac.Subject{{Kind: rbac.ServiceAccountKind, Namespace: nc.Namespace, Name: "network"}}},
				} {
					if err := c.Create(ctx, obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "claimed-cancel-partial" || mode == "partial" || mode == "claimed-partial" || mode == "revoked-partial" || mode == "collision" {
				r := candidates[0].DeepCopy()
				r.UID = "retained"
				if mode == "claimed-cancel-partial" {
					holder, err := Holder(r.Namespace, r)
					if err != nil {
						t.Fatal(err)
					}
					r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", Holder: holder, GrantedAt: metav1.NewTime(now.Add(-time.Minute))}
					r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-time.Second))}
				}
				if mode == "claimed-partial" {
					r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: "claimed"}
				}
				if mode == "revoked-partial" {
					r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Revoked"}
				}
				if mode == "collision" {
					r.Spec.InventoryID = "foreign"
				}
				if err := c.Create(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			writer := &groupCreateWriter{Client: c, lose: mode == "lost-first"}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("group admission unavailable")
				}
				if mode == "late-fence" {
					copy := nc.DeepCopy()
					copy.Status.Configuration.Operation.Phase = "Submitting"
					return c.Status().Update(ctx, copy)
				}
				if mode == "late-intent" {
					copy := nc.DeepCopy()
					copy.Spec.Paused = true
					return c.Update(ctx, copy)
				}
				return nil
			}
			rows, err := ensure(ctx, c, writer, nc, authorize, now)
			valid := mode == "valid" || mode == "partial" || mode == "claimed-cancel-partial"
			if (err == nil) != valid {
				t.Fatal("group creation", err)
			}
			if valid {
				_, bindingErr := ValidateGroupReservationSet(nc, rows)
				if len(rows) != 2 || bindingErr != nil {
					t.Fatal("incomplete group")
				}
				existing, err := ensure(ctx, c, nil, nc, nil, now)
				if err != nil || len(existing) != 2 {
					t.Fatal("read-only recovery", err)
				}
			}
			if cancelling && (mode == "valid" || mode == "partial") {
				actor := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
				if err := FinishCancelledControllerGroup(ctx, c, c, nc, actor, now); err == nil {
					t.Fatal("unacknowledged group cleared")
				}
				for _, row := range rows {
					if err := AcknowledgeGroupCancellation(ctx, c, c, nc, row, row.Spec.DeviceWorkerPod.UID, actor, now); err != nil {
						t.Fatal(err)
					}
					if err := c.Get(ctx, client.ObjectKeyFromObject(row), row); err != nil {
						t.Fatal(err)
					}
					if err := PublishCancelledGroupManagerSettlement(ctx, c, c, nc, row, "leases", actor, now); err != nil {
						t.Fatal(err)
					}
				}
				if err := FinishCancelledControllerGroup(ctx, c, c, nc, actor, now); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.GroupCancellations) != 1 || len(nc.Status.Configuration.GroupCancellations[0].Members) != 2 {
					t.Fatal("complete cancellation receipt missing")
				}
			}
			wantCreates := 0
			switch mode {
			case "valid":
				wantCreates = 2
			case "partial", "lost-first", "claimed-cancel-partial":
				wantCreates = 1
			}
			if writer.calls != wantCreates {
				t.Fatal("unexpected creation/retry", writer.calls)
			}
			if mode == "lost-first" {
				var retained ops.ControllerConfigurationReservationList
				if err := c.List(ctx, &retained); err != nil {
					t.Fatal(err)
				}
				if len(retained.Items) != 1 {
					t.Fatal("partial evidence lost")
				}
				originalUID := retained.Items[0].UID
				writer.lose = false
				recovered, err := ensure(ctx, c, writer, nc, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, now)
				if err != nil || len(recovered) != 2 || writer.calls != 2 {
					t.Fatalf("creation recovery: %v", err)
				}
				if recovered[0].UID != originalUID {
					t.Fatal("recreated retained member")
				}
			}
		})
	}
}
