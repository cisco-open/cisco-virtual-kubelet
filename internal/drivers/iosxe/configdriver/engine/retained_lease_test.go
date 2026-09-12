// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func retainedLeaseFixture() *coordv1.Lease {
	now := metav1.NewMicroTime(time.Now())
	return &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: LeaseName("device", devicecoordination.MutationLeaseFamily), UID: "canonical-uid", Annotations: map[string]string{
		devicecoordination.RetainLeaseAnnotation:             "true",
		managedprotocol.AnnotationDeviceUID:                  "device-uid",
		managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.Version,
		managedprotocol.AnnotationMaintenanceSessionToken:    "session-token",
		managedprotocol.AnnotationMaintenanceRequestedAt:     now.Format(time.RFC3339),
		managedprotocol.AnnotationMaintenanceOperationNS:     "edge",
		managedprotocol.AnnotationMaintenanceOperationName:   "upgrade",
		managedprotocol.AnnotationMaintenanceOperationUID:    "leaf-uid",
		managedprotocol.AnnotationMaintenanceControlRevision: "7",
		"topology.cisco.vk/maintenance-future-manager-field": "preserve",
	}}, Spec: coordv1.LeaseSpec{HolderIdentity: strPtr("old-owner"), AcquireTime: &now, RenewTime: &now, LeaseDurationSeconds: int32Ptr(30), LeaseTransitions: int32Ptr(1)}}
}

func TestRetainedLeaseOnlyClearsExactRequestOnReleaseOrTakeover(t *testing.T) {
	for _, operation := range []string{"release", "expired takeover", "empty takeover", "renew", "foreign release"} {
		t.Run(operation, func(t *testing.T) {
			seed := retainedLeaseFixture()
			if operation == "expired takeover" {
				seed.Spec.RenewTime = &metav1.MicroTime{Time: time.Now().Add(-time.Hour)}
			}
			if operation == "empty takeover" {
				seed.Spec.HolderIdentity = nil
			}
			c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
			l := &FamilyLeaser{Client: c, Namespace: seed.Namespace}
			ctx := context.Background()
			switch operation {
			case "release":
				if err := l.Release(ctx, "device", devicecoordination.MutationLeaseFamily, "old-owner"); err != nil {
					t.Fatal(err)
				}
			case "foreign release":
				if err := l.Release(ctx, "device", devicecoordination.MutationLeaseFamily, "other"); err != nil {
					t.Fatal(err)
				}
			case "empty takeover":
				if result, err := l.AcquireIfFree(ctx, "device", devicecoordination.MutationLeaseFamily, "new-owner"); err != nil || !result.Owned {
					t.Fatalf("acquire: %+v %v", result, err)
				}
			default:
				owner := "new-owner"
				if operation == "renew" {
					owner = "old-owner"
				}
				if result, err := l.Acquire(ctx, "device", devicecoordination.MutationLeaseFamily, owner); err != nil || !result.Owned {
					t.Fatalf("acquire: %+v %v", result, err)
				}
			}
			var got coordv1.Lease
			if err := c.Get(ctx, client.ObjectKeyFromObject(seed), &got); err != nil {
				t.Fatal(err)
			}
			if got.UID != seed.UID {
				t.Fatal("retained canonical Lease identity changed")
			}
			cleared := operation != "renew" && operation != "foreign release"
			for key, value := range seed.Annotations {
				switch key {
				case devicecoordination.RetainLeaseAnnotation, managedprotocol.AnnotationDeviceUID, "topology.cisco.vk/maintenance-future-manager-field":
					if got.Annotations[key] != value {
						t.Fatalf("binding/unknown annotation %q changed", key)
					}
				default:
					if cleared {
						if _, found := got.Annotations[key]; found {
							t.Fatalf("old request annotation %q remained", key)
						}
					} else if got.Annotations[key] != value {
						t.Fatalf("same holder request %q changed", key)
					}
				}
			}
			if operation == "release" && (got.Spec.HolderIdentity != nil || got.Spec.AcquireTime != nil || got.Spec.RenewTime != nil || got.Spec.LeaseDurationSeconds != nil) {
				t.Fatal("release did not atomically clear holder timing fields")
			}
			if operation == "release" && (got.Spec.LeaseTransitions == nil || *got.Spec.LeaseTransitions != 1) {
				t.Fatal("mutation release changed its durable leaseTransitions counter")
			}
			if operation == "renew" && (got.Spec.AcquireTime == nil || !got.Spec.AcquireTime.Equal(seed.Spec.AcquireTime) ||
				got.Spec.LeaseTransitions == nil || *got.Spec.LeaseTransitions != *seed.Spec.LeaseTransitions) {
				t.Fatal("same-holder renewal changed acquisition identity")
			}
		})
	}
}

func TestRetainedLeaseReleaseRetriesTransientConflict(t *testing.T) {
	seed := retainedLeaseFixture()
	base := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
	c := &conflictOnceOnLeaseUpdateClient{Client: base}
	leaser := &FamilyLeaser{Client: c, Namespace: seed.Namespace, RequireExisting: true}

	if err := leaser.Release(context.Background(), "device", devicecoordination.MutationLeaseFamily, "old-owner"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	var got coordv1.Lease
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(seed), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.HolderIdentity != nil || got.Spec.AcquireTime != nil || got.Spec.RenewTime != nil ||
		got.Spec.LeaseDurationSeconds != nil || got.Spec.LeaseTransitions == nil || *got.Spec.LeaseTransitions != 1 {
		t.Fatalf("conflict retry did not release canonical Lease: %#v", got.Spec)
	}
}

func TestRequiredRetainedLeaseMissingDuringReleaseFailsClosed(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	leaser := &FamilyLeaser{Client: c, Namespace: "edge", RequireExisting: true}
	if err := leaser.Release(context.Background(), "device", devicecoordination.MutationLeaseFamily, "old-owner"); err == nil {
		t.Fatal("missing canonical managed Lease was reported as released")
	}
}

func TestRetainedConfigLeaseReleaseReturnsToEmptyCanonicalSpec(t *testing.T) {
	now := metav1.NewMicroTime(time.Now())
	seed := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: "edge", Name: LeaseName("device", "vlan"), UID: "canonical-uid",
		Annotations: map[string]string{devicecoordination.RetainLeaseAnnotation: "true"},
	}, Spec: coordv1.LeaseSpec{
		HolderIdentity: strPtr("edge/config#00000000-0000-4000-8000-000000000001"),
		AcquireTime:    &now, RenewTime: &now, LeaseDurationSeconds: int32Ptr(30), LeaseTransitions: int32Ptr(1),
	}}
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
	leaser := &FamilyLeaser{Client: c, Namespace: seed.Namespace, RequireExisting: true}
	if err := leaser.Release(context.Background(), "device", "vlan", *seed.Spec.HolderIdentity); err != nil {
		t.Fatal(err)
	}
	var got coordv1.Lease
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(seed), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.HolderIdentity != nil || got.Spec.AcquireTime != nil || got.Spec.RenewTime != nil ||
		got.Spec.LeaseDurationSeconds != nil || got.Spec.LeaseTransitions != nil {
		t.Fatalf("released config Lease retained active spec: %#v", got.Spec)
	}
}

func TestRetainedLeaseConflictCannotClearNewOwnersRequest(t *testing.T) {
	for _, operation := range []string{"release", "takeover"} {
		t.Run(operation, func(t *testing.T) {
			seed := retainedLeaseFixture()
			seed.Spec.RenewTime = &metav1.MicroTime{Time: time.Now().Add(-time.Hour)}
			base := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
			c := &takeoverOnLeaseUpdateClient{Client: base, takeoverIdentity: "new-owner", takeoverRenewedAt: metav1.NewMicroTime(time.Now()), takeoverTTLSeconds: 30}
			l := &FamilyLeaser{Client: c, Namespace: seed.Namespace}
			ctx := context.Background()
			if operation == "release" {
				if err := l.Release(ctx, "device", devicecoordination.MutationLeaseFamily, "old-owner"); err != nil {
					t.Fatal(err)
				}
			} else if result, err := l.Acquire(ctx, "device", devicecoordination.MutationLeaseFamily, "candidate"); err != nil || result.Owned {
				t.Fatalf("conflicting acquire: %+v %v", result, err)
			}
			var got coordv1.Lease
			if err := base.Get(ctx, client.ObjectKeyFromObject(seed), &got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.HolderIdentity == nil || *got.Spec.HolderIdentity != "new-owner" {
				t.Fatal("stale request cleared newer holder")
			}
			for key, value := range seed.Annotations {
				if got.Annotations[key] != value {
					t.Fatalf("conflicting cleanup overwrote %s", key)
				}
			}
		})
	}
}

func TestRequiredLeaseCannotBeCreatedByAcquire(t *testing.T) {
	for _, onlyFree := range []bool{false, true} {
		c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
		l := &FamilyLeaser{Client: c, Namespace: "edge", RequireExisting: true}
		acquire := l.Acquire
		if onlyFree {
			acquire = l.AcquireIfFree
		}
		result, err := acquire(context.Background(), "device", "mutation", "worker")
		if err == nil || result.Owned {
			t.Fatalf("missing required Lease acquired: %+v %v", result, err)
		}
		var lease coordv1.Lease
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: "edge", Name: LeaseName("device", "mutation")}, &lease); !apierrors.IsNotFound(err) {
			t.Fatalf("worker created missing canonical Lease: %v", err)
		}
	}
}

func TestRequiredLeaseUsesManagerBoundDeviceKey(t *testing.T) {
	const (
		displayName = "switch"
		deviceKey   = "device-0123456789abcdef"
		family      = "interface_ethernet"
	)
	seed := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: "edge", Name: LeaseName(deviceKey, family), UID: "bound-uid",
		Annotations: map[string]string{devicecoordination.RetainLeaseAnnotation: "true"},
		Labels:      map[string]string{"cisco.vk/device": deviceKey, "cisco.vk/family": family},
	}}
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
	leaser := &FamilyLeaser{
		Client: c, Namespace: seed.Namespace, DeviceKey: deviceKey, RequireExisting: true,
	}
	result, err := leaser.Acquire(context.Background(), displayName, family, "edge/config#pod-uid")
	if err != nil || !result.Owned {
		t.Fatalf("Acquire manager-bound Lease = %+v, %v", result, err)
	}
	var bound coordv1.Lease
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(seed), &bound); err != nil {
		t.Fatal(err)
	}
	if bound.Spec.HolderIdentity == nil || *bound.Spec.HolderIdentity != "edge/config#pod-uid" {
		t.Fatalf("bound holderIdentity = %v", bound.Spec.HolderIdentity)
	}
	if bound.Spec.AcquireTime == nil || bound.Spec.RenewTime == nil ||
		!bound.Spec.AcquireTime.Equal(bound.Spec.RenewTime) ||
		bound.Spec.LeaseTransitions == nil || *bound.Spec.LeaseTransitions != 1 {
		t.Fatalf("first retained acquisition has invalid timing/transitions: %#v", bound.Spec)
	}
	var legacy coordv1.Lease
	err = c.Get(context.Background(), client.ObjectKey{Namespace: seed.Namespace, Name: LeaseName(displayName, family)}, &legacy)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("managed leaser used legacy display-name identity: %v", err)
	}
}
