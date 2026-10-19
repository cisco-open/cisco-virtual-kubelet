// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

func grantFixture(t *testing.T) (client.Client, *ops.ControllerConfigurationReservation, time.Time) {
	t.Helper()
	r, d, n, p, now := deviceBindingFixture(t)
	original, _, op, _ := claimFixture(t)
	var lease coordination.Lease
	if err := original.Get(context.Background(), client.ObjectKey{Namespace: r.Status.Grant.LeaseNamespace, Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
		t.Fatal(err)
	}
	r.Status = ops.ControllerConfigurationReservationStatus{}
	r.Spec.Controller.Name = "cc"
	op.TargetSerial = r.Spec.PhysicalIdentity
	r.Spec.OperationDigest, _ = OperationDigest(op)
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: r.Namespace, UID: types.UID(r.Spec.Controller.UID), Generation: r.Spec.ControllerGeneration}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op}}}
	scheme := runtime.NewScheme()
	_ = ops.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(r, nc).WithObjects(r, d, n, p, nc, &lease).Build()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	return c, r, now
}
func TestPublishGrant(t *testing.T) {
	ctx := context.Background()
	c, r, now := grantFixture(t)
	if PublishGrant(ctx, c, c, r, "leases", "wrong-pod", now) == nil {
		t.Fatal("foreign process granted")
	}
	if err := PublishGrant(ctx, c, c, r, "leases", r.Spec.DeviceWorkerPod.UID, now); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Grant == nil || r.Status.Grant.Phase != "Held" || r.Status.Claim != nil {
		t.Fatal("invalid grant")
	}
	if PublishGrant(ctx, c, c, r, "leases", r.Spec.DeviceWorkerPod.UID, now) == nil {
		t.Fatal("regrant accepted")
	}
}
func TestPublishGrantRefusesUncertainEvidence(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"expired", "unready", "changed-journal", "changed-serial", "foreign-lease", "lost-write"} {
		t.Run(kind, func(t *testing.T) {
			c, r, now := grantFixture(t)
			var writer client.StatusClient = c
			switch kind {
			case "expired":
				now = now.Add(time.Minute)
			case "unready":
				var p core.Pod
				if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.DeviceWorkerPod.Name}, &p); err != nil {
					t.Fatal(err)
				}
				p.Status.Conditions = nil
				if err := c.Status().Update(ctx, &p); err != nil {
					t.Fatal(err)
				}
			case "changed-journal", "changed-serial":
				var n cvk.NetworkController
				if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: "cc"}, &n); err != nil {
					t.Fatal(err)
				}
				if kind == "changed-journal" {
					n.Status.Configuration.Operation.Phase = "Accepted"
				} else {
					n.Status.Configuration.Operation.TargetSerial = "other"
				}
				if err := c.Status().Update(ctx, &n); err != nil {
					t.Fatal(err)
				}
			case "foreign-lease":
				var leases coordination.LeaseList
				if err := c.List(ctx, &leases); err != nil {
					t.Fatal(err)
				}
				l := &leases.Items[0]
				v := "foreign"
				l.Spec.HolderIdentity = &v
				if err := c.Update(ctx, l); err != nil {
					t.Fatal(err)
				}
			case "lost-write":
				writer = ambiguousStatus{Client: c}
			}
			if PublishGrant(ctx, c, writer, r, "leases", r.Spec.DeviceWorkerPod.UID, now) == nil {
				t.Fatal("uncertain grant returned success")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			if kind != "lost-write" && r.Status.Grant != nil {
				t.Fatal("invalid grant persisted")
			}
			if kind == "lost-write" && r.Status.Grant == nil {
				t.Fatal("fixture did not persist ambiguous write")
			}
		})
	}
}
