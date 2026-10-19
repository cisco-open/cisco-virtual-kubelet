// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"strconv"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func groupAuthorityFixture(t *testing.T) (client.Client, *cvk.NetworkController, []*ops.ControllerConfigurationReservation, time.Time) {
	t.Helper()
	ctx := context.Background()
	c, nc, now := groupReaderFixture(t)
	_ = coordination.AddToScheme(c.Scheme())
	reservations, err := ReadGroupReservationCandidates(ctx, c, nc, now)
	if err != nil {
		t.Fatal(err)
	}
	_, base, leases, _ := groupGrantFixture(t)
	for i, r := range reservations {
		r.UID = types.UID(r.Name + "-uid")
		r.Status = *base[i].Status.DeepCopy()
		holder, err := Holder(r.Namespace, r)
		if err != nil {
			t.Fatal(err)
		}
		r.Status.Grant.Holder = holder
		leases[i].Spec.HolderIdentity = &holder
		requested, ack := metav1.NewTime(now.Add(-29*time.Second)), metav1.NewTime(now.Add(-25*time.Second))
		token := "11111111-1111-4111-8111-111111111111"
		var d cvk.CiscoDevice
		var n core.Node
		if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &d); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &n); err != nil {
			t.Fatal(err)
		}
		d.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionActive, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: token, DeviceUID: string(d.UID), NodeName: n.Name, NodeUID: string(n.UID), ControlRevision: r.Spec.ConfigGeneration, RequestedAt: requested, AcknowledgedAt: &ack, Operation: cvk.DeviceMaintenanceObjectReference{Namespace: r.Namespace, Name: r.Name, UID: string(r.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: "leases", Name: leases[i].Name, UID: string(leases[i].UID)}, Holder: holder}}
		if err := c.Status().Update(ctx, &d); err != nil {
			t.Fatal(err)
		}
		n.Spec.Taints = []core.Taint{{Key: managedprotocol.MaintenanceTaintKey, Value: managedprotocol.MaintenanceTaintValue, Effect: core.TaintEffectNoSchedule}}
		if err := c.Update(ctx, &n); err != nil {
			t.Fatal(err)
		}
		leases[i].Annotations = map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: Protocol, managedprotocol.AnnotationMaintenancePurpose: managedprotocol.MaintenancePurposeConfigurationMutation, managedprotocol.AnnotationMaintenanceSessionToken: token, managedprotocol.AnnotationMaintenanceRequestedAt: requested.Format(time.RFC3339Nano), managedprotocol.AnnotationMaintenanceOperationNS: r.Namespace, managedprotocol.AnnotationMaintenanceOperationName: r.Name, managedprotocol.AnnotationMaintenanceOperationUID: string(r.UID), managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(r.Spec.ConfigGeneration, 10)}
		if err := c.Create(ctx, leases[i]); err != nil {
			t.Fatal(err)
		}
		if err := c.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
	}
	return c, nc, reservations, now
}

func TestReadGroupAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "missing-taint", "wrong-session", "active-workload", "wrong-pod", "late-alias", "late-connector", "late-intent", "changed-intent", "revoked", "partial-claims", "submitting-unclaimed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, reservations, now := groupAuthorityFixture(t)
			r := reservations[1]
			pod := r.Spec.ControllerWorkerPod
			reader := client.Reader(c)
			switch mode {
			case "missing-taint":
				var n core.Node
				if err := c.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &n); err != nil {
					t.Fatal(err)
				}
				n.Spec.Taints = nil
				if err := c.Update(ctx, &n); err != nil {
					t.Fatal(err)
				}
			case "wrong-session":
				var d cvk.CiscoDevice
				if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &d); err != nil {
					t.Fatal(err)
				}
				d.Status.MaintenanceSession.Operation.UID = "other"
				if err := c.Status().Update(ctx, &d); err != nil {
					t.Fatal(err)
				}
			case "active-workload":
				workload := &core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "workload"}, Spec: core.PodSpec{NodeName: r.Spec.Node.Name}, Status: core.PodStatus{Phase: core.PodRunning}}
				if err := c.Create(ctx, workload); err != nil {
					t.Fatal(err)
				}
			case "wrong-pod":
				pod.UID = "other"
			case "late-alias", "late-connector", "late-intent", "changed-intent":
				reader = &changingGroupReader{Reader: c, mode: mode, reads: map[string]int{}}
			case "revoked":
				r.Status.Grant.Phase = "Revoked"
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			case "partial-claims":
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now)}
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			case "submitting-unclaimed":
				nc.Status.Configuration.Operation.Phase = "Submitting"
				at := metav1.NewTime(now)
				nc.Status.Configuration.Operation.SubmittedAt = &at
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			err := ReadGroupAuthority(ctx, reader, nc, reservations, pod, "leases", now)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestGroupAuthorityThroughSubmission(t *testing.T) {
	for _, mode := range []string{"valid", "guard-lost-after-claims", "workload-after-submitting"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, reservations, now := groupAuthorityFixture(t)
			pod := reservations[0].Spec.ControllerWorkerPod
			authorize := func(ctx context.Context, members []*ops.ControllerConfigurationReservation) error {
				// A runtime authorizer must ALSO verify installed group admission and
				// worker rollout compatibility; this fixture exercises live bindings.
				return ReadGroupAuthority(ctx, c, nc, members, pod, "leases", now)
			}
			submission, err := ClaimGroupForController(ctx, c, c, nc, reservations, "leases", authorize, now)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "guard-lost-after-claims" {
				var node core.Node
				if err := c.Get(ctx, client.ObjectKey{Name: reservations[1].Spec.Node.Name}, &node); err != nil {
					t.Fatal(err)
				}
				node.Spec.Taints = nil
				if err := c.Update(ctx, &node); err != nil {
					t.Fatal(err)
				}
			}
			dispatch, err := submission.BeginSubmission(ctx, now)
			if mode == "guard-lost-after-claims" {
				if err == nil || dispatch != nil {
					t.Fatal("lost guard admitted journal transition")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "workload-after-submitting" {
				workload := &core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "late"}, Spec: core.PodSpec{NodeName: reservations[1].Spec.Node.Name}, Status: core.PodStatus{Phase: core.PodRunning}}
				if err := c.Create(ctx, workload); err != nil {
					t.Fatal(err)
				}
			}
			err = dispatch.Check(ctx, now)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if nc.Status.Configuration.Operation.Phase != "Submitting" {
				t.Fatal("journal fence lost")
			}
		})
	}
}
