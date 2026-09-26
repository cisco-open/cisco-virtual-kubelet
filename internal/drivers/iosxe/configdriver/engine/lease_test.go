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

package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type takeoverOnLeaseUpdateClient struct {
	client.Client
	once               sync.Once
	takeoverIdentity   string
	takeoverRenewedAt  metav1.MicroTime
	takeoverTTLSeconds int32
}

type conflictOnceOnLeaseUpdateClient struct {
	client.Client
	once sync.Once
}

func (c *conflictOnceOnLeaseUpdateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	injected := false
	c.once.Do(func() { injected = true })
	if injected {
		return apierrors.NewConflict(
			schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
			obj.GetName(),
			errors.New("injected transient conflict"),
		)
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *takeoverOnLeaseUpdateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	lease, ok := obj.(*coordv1.Lease)
	if !ok {
		return c.Client.Update(ctx, obj, opts...)
	}
	injected := false
	c.once.Do(func() {
		injected = true
	})
	if !injected {
		return c.Client.Update(ctx, obj, opts...)
	}

	var current coordv1.Lease
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
		return err
	}
	current.Spec.HolderIdentity = strPtr(c.takeoverIdentity)
	current.Spec.RenewTime = &c.takeoverRenewedAt
	current.Spec.LeaseDurationSeconds = &c.takeoverTTLSeconds
	if err := c.Client.Update(ctx, &current); err != nil {
		return err
	}
	return apierrors.NewConflict(
		schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
		lease.Name,
		errors.New("injected foreign takeover"),
	)
}

type takeoverOnLeaseDeleteClient struct {
	client.Client
	once             sync.Once
	takeoverIdentity string
}

func (c *takeoverOnLeaseDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	lease, ok := obj.(*coordv1.Lease)
	if !ok {
		return c.Client.Delete(ctx, obj, opts...)
	}
	injected := false
	c.once.Do(func() {
		injected = true
	})
	if !injected {
		return c.Client.Delete(ctx, obj, opts...)
	}

	var current coordv1.Lease
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
		return err
	}
	now := metav1.NewMicroTime(time.Now())
	current.Spec.HolderIdentity = strPtr(c.takeoverIdentity)
	current.Spec.RenewTime = &now
	if err := c.Client.Update(ctx, &current); err != nil {
		return err
	}

	deleteOpts := (&client.DeleteOptions{}).ApplyOptions(opts)
	if deleteOpts.Preconditions == nil || deleteOpts.Preconditions.UID == nil ||
		deleteOpts.Preconditions.ResourceVersion == nil {
		return errors.New("delete omitted UID/resourceVersion preconditions")
	}
	if *deleteOpts.Preconditions.UID == current.UID &&
		*deleteOpts.Preconditions.ResourceVersion != current.ResourceVersion {
		return apierrors.NewConflict(
			schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
			lease.Name,
			errors.New("resourceVersion changed during release"),
		)
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func newLeaseScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := coordv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func TestLeaseAcquireCreatesNew(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}

	res, err := l.Acquire(context.Background(), "edge-01", "vlan", "network/a")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !res.Owned || res.Holder != "network/a" {
		t.Fatalf("got %+v, want Owned=true Holder=network/a", res)
	}

	var got coordv1.Lease
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "cisco-vk", Name: leaseName("edge-01", "vlan")},
		&got); err != nil {
		t.Fatalf("Get lease: %v", err)
	}
	if got.Spec.HolderIdentity == nil || *got.Spec.HolderIdentity != "network/a" {
		t.Fatalf("holder=%v", got.Spec.HolderIdentity)
	}
	if got.Labels["cisco.vk/device"] != "edge-01" {
		t.Errorf("device label=%q", got.Labels["cisco.vk/device"])
	}
}

func TestLeaseAcquireRenewsSelf(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}

	_, err := l.Acquire(context.Background(), "edge-01", "vlan", "a")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	res, err := l.Acquire(context.Background(), "edge-01", "vlan", "a")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if !res.Owned {
		t.Fatalf("renewal should still be Owned: %+v", res)
	}
}

func TestLeaseRenewConflictNeverAuthorizesStaleHolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		acquire func(*FamilyLeaser, context.Context, string, string, string) (LeaseResult, error)
	}{
		{
			name: "Acquire",
			acquire: func(l *FamilyLeaser, ctx context.Context, device, family, identity string) (LeaseResult, error) {
				return l.Acquire(ctx, device, family, identity)
			},
		},
		{
			name: "AcquireIfFree",
			acquire: func(l *FamilyLeaser, ctx context.Context, device, family, identity string) (LeaseResult, error) {
				return l.AcquireIfFree(ctx, device, family, identity)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			const (
				originalHolder = "owner-a"
				takeoverHolder = "owner-b"
			)
			expiredAt := metav1.NewMicroTime(time.Now().Add(-time.Hour))
			takeoverAt := metav1.NewMicroTime(time.Now())
			ttlSeconds := int32(30)
			seed := &coordv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      leaseName("edge-01", "mutation"),
					Namespace: "cisco-vk",
				},
				Spec: coordv1.LeaseSpec{
					HolderIdentity:       strPtr(originalHolder),
					LeaseDurationSeconds: &ttlSeconds,
					AcquireTime:          &expiredAt,
					RenewTime:            &expiredAt,
					LeaseTransitions:     int32Ptr(1),
				},
			}
			base := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
			raceClient := &takeoverOnLeaseUpdateClient{
				Client:             base,
				takeoverIdentity:   takeoverHolder,
				takeoverRenewedAt:  takeoverAt,
				takeoverTTLSeconds: ttlSeconds,
			}
			leaser := &FamilyLeaser{Client: raceClient, Namespace: "cisco-vk"}

			result, err := tt.acquire(leaser, context.Background(), "edge-01", "mutation", originalHolder)
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if result.Owned {
				t.Fatalf("%s returned Owned=true after a conflicting foreign takeover: %+v", tt.name, result)
			}

			var got coordv1.Lease
			if err := base.Get(context.Background(), types.NamespacedName{
				Namespace: "cisco-vk",
				Name:      leaseName("edge-01", "mutation"),
			}, &got); err != nil {
				t.Fatalf("read lease after takeover: %v", err)
			}
			if got.Spec.HolderIdentity == nil || *got.Spec.HolderIdentity != takeoverHolder {
				t.Fatalf("holder=%v, want takeover holder %q", got.Spec.HolderIdentity, takeoverHolder)
			}
		})
	}
}

func TestLeaseAcquireReportsForeignHolder(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}

	if _, err := l.Acquire(context.Background(), "edge-01", "vlan", "owner-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := l.Acquire(context.Background(), "edge-01", "vlan", "owner-2")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if res.Owned || res.Holder != "owner-1" {
		t.Fatalf("got %+v, want Owned=false Holder=owner-1", res)
	}
}

func TestLeaseAcquireTakesOverExpired(t *testing.T) {
	t.Parallel()
	scheme := newLeaseScheme(t)
	// Pre-create an expired lease held by owner-1.
	renewed := metav1.NewMicroTime(time.Now().Add(-1 * time.Hour))
	ttl := int32(30)
	seed := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: leaseName("edge-01", "vlan"), Namespace: "cisco-vk"},
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       strPtr("owner-1"),
			LeaseDurationSeconds: &ttl,
			RenewTime:            &renewed,
			AcquireTime:          &renewed,
			LeaseTransitions:     int32Ptr(1),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(seed).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}

	res, err := l.Acquire(context.Background(), "edge-01", "vlan", "owner-2")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !res.Owned || res.Holder != "owner-2" {
		t.Fatalf("takeover failed: %+v", res)
	}
	var got coordv1.Lease
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "cisco-vk", Name: leaseName("edge-01", "vlan")},
		&got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.LeaseTransitions == nil || *got.Spec.LeaseTransitions != 2 {
		t.Errorf("transitions=%v, want 2", got.Spec.LeaseTransitions)
	}
}

func TestLeaseReleaseClearsOwnLease(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}

	if _, err := l.Acquire(context.Background(), "edge-01", "vlan", "a"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(context.Background(), "edge-01", "vlan", "a"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	var got coordv1.Lease
	err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "cisco-vk", Name: leaseName("edge-01", "vlan")},
		&got)
	if err == nil {
		t.Fatalf("lease still exists after Release: %+v", got)
	}
}

func TestLeaseReleaseByNonOwnerIsNoop(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}

	if _, err := l.Acquire(context.Background(), "edge-01", "vlan", "real-owner"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(context.Background(), "edge-01", "vlan", "not-owner"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	var got coordv1.Lease
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "cisco-vk", Name: leaseName("edge-01", "vlan")},
		&got); err != nil {
		t.Fatalf("lease was deleted by non-owner Release: %v", err)
	}
	if *got.Spec.HolderIdentity != "real-owner" {
		t.Errorf("holder tampered: %q", *got.Spec.HolderIdentity)
	}
}

func TestLeaseReleaseCannotDeleteConcurrentTakeover(t *testing.T) {
	t.Parallel()
	const (
		originalHolder = "owner-a"
		takeoverHolder = "owner-b"
	)
	now := metav1.NewMicroTime(time.Now())
	ttlSeconds := int32(30)
	seed := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName("edge-01", "mutation"),
			Namespace: "cisco-vk",
			UID:       types.UID("lease-uid"),
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       strPtr(originalHolder),
			LeaseDurationSeconds: &ttlSeconds,
			AcquireTime:          &now,
			RenewTime:            &now,
			LeaseTransitions:     int32Ptr(1),
		},
	}
	base := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).WithObjects(seed).Build()
	raceClient := &takeoverOnLeaseDeleteClient{Client: base, takeoverIdentity: takeoverHolder}
	leaser := &FamilyLeaser{Client: raceClient, Namespace: "cisco-vk"}

	if err := leaser.Release(context.Background(), "edge-01", "mutation", originalHolder); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var got coordv1.Lease
	if err := base.Get(context.Background(), types.NamespacedName{
		Namespace: "cisco-vk",
		Name:      leaseName("edge-01", "mutation"),
	}, &got); err != nil {
		t.Fatalf("concurrent takeover was deleted by stale Release: %v", err)
	}
	if got.Spec.HolderIdentity == nil || *got.Spec.HolderIdentity != takeoverHolder {
		t.Fatalf("holder=%v, want takeover holder %q", got.Spec.HolderIdentity, takeoverHolder)
	}
}

func TestLeaseReleaseOnMissingIsNoop(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}
	if err := l.Release(context.Background(), "edge-01", "vlan", "any"); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
