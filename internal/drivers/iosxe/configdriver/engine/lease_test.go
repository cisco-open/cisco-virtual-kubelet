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
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newLeaseScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := coordv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func TestLeaseAcquireCreatesNew(t *testing.T) {
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
		types.NamespacedName{Namespace: "cisco-vk", Name: "cvk-edge-01-vlan"},
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

func TestLeaseAcquireReportsForeignHolder(t *testing.T) {
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
	scheme := newLeaseScheme(t)
	// Pre-create an expired lease held by owner-1.
	renewed := metav1.NewMicroTime(time.Now().Add(-1 * time.Hour))
	ttl := int32(30)
	seed := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "cvk-edge-01-vlan", Namespace: "cisco-vk"},
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
		types.NamespacedName{Namespace: "cisco-vk", Name: "cvk-edge-01-vlan"},
		&got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.LeaseTransitions == nil || *got.Spec.LeaseTransitions != 2 {
		t.Errorf("transitions=%v, want 2", got.Spec.LeaseTransitions)
	}
}

func TestLeaseReleaseClearsOwnLease(t *testing.T) {
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
		types.NamespacedName{Namespace: "cisco-vk", Name: "cvk-edge-01-vlan"},
		&got)
	if err == nil {
		t.Fatalf("lease still exists after Release: %+v", got)
	}
}

func TestLeaseReleaseByNonOwnerIsNoop(t *testing.T) {
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
		types.NamespacedName{Namespace: "cisco-vk", Name: "cvk-edge-01-vlan"},
		&got); err != nil {
		t.Fatalf("lease was deleted by non-owner Release: %v", err)
	}
	if *got.Spec.HolderIdentity != "real-owner" {
		t.Errorf("holder tampered: %q", *got.Spec.HolderIdentity)
	}
}

func TestLeaseReleaseOnMissingIsNoop(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newLeaseScheme(t)).Build()
	l := &FamilyLeaser{Client: c, Namespace: "cisco-vk"}
	if err := l.Release(context.Background(), "edge-01", "vlan", "any"); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
