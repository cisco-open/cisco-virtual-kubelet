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
	"fmt"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FamilyLeaser claims coordination.k8s.io/v1 Leases to serialise
// per-family writes across IOSXEConfig CRs targeting the same device.
//
// The contract is cooperative: Acquire either wins the lease
// (returning Owned=true) or reports who currently holds it
// (Owned=false, Holder). Callers that lose the race surface the
// conflict on status rather than writing to the family.
//
// Lease naming: "cvk-<device>-<family>", scoped to a single namespace
// supplied at construction time. Lease TTL is 2× the reconcile
// interval so a crashed holder releases automatically.
type FamilyLeaser struct {
	Client    client.Client
	Namespace string
	// TTL controls spec.leaseDurationSeconds. Zero means 30s.
	TTL time.Duration
}

// LeaseResult describes the outcome of an Acquire call. Owned==true
// means the caller may proceed to apply. Owned==false surfaces the
// current holder's identity so the reconciler can set a Conflict
// condition with a useful message.
type LeaseResult struct {
	Owned  bool
	Holder string
}

// Acquire creates or renews the lease for (device, family) with
// identity as the holder. If the lease already exists with a
// different, unexpired holder, Acquire returns Owned=false and does
// NOT mutate the object.
func (l *FamilyLeaser) Acquire(ctx context.Context, device, family, identity string) (LeaseResult, error) {
	if l.Client == nil {
		return LeaseResult{}, fmt.Errorf("FamilyLeaser: nil Client")
	}
	if l.Namespace == "" {
		return LeaseResult{}, fmt.Errorf("FamilyLeaser: empty Namespace")
	}

	ttl := l.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	ttlSeconds := int32(ttl / time.Second)
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}

	name := leaseName(device, family)
	now := metav1.NewMicroTime(time.Now())

	var lease coordv1.Lease
	err := l.Client.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: name}, &lease)
	switch {
	case apierrors.IsNotFound(err):
		lease = coordv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: l.Namespace,
				Labels: map[string]string{
					"cisco.vk/device": device,
					"cisco.vk/family": family,
				},
			},
			Spec: coordv1.LeaseSpec{
				HolderIdentity:       strPtr(identity),
				LeaseDurationSeconds: &ttlSeconds,
				AcquireTime:          &now,
				RenewTime:            &now,
				LeaseTransitions:     int32Ptr(1),
			},
		}
		if err := l.Client.Create(ctx, &lease); err != nil {
			// Benign: another reconciler beat us to create. Fall through
			// and re-read to determine ownership.
			if !apierrors.IsAlreadyExists(err) {
				return LeaseResult{}, fmt.Errorf("create lease: %w", err)
			}
			if err := l.Client.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: name}, &lease); err != nil {
				return LeaseResult{}, fmt.Errorf("re-read lease after conflict: %w", err)
			}
			// Re-enter the normal path.
			return l.renewOrReport(ctx, &lease, identity, ttlSeconds, now)
		}
		return LeaseResult{Owned: true, Holder: identity}, nil
	case err != nil:
		return LeaseResult{}, fmt.Errorf("get lease: %w", err)
	}

	return l.renewOrReport(ctx, &lease, identity, ttlSeconds, now)
}

// Release clears our holder identity if we still own the lease. A
// Release by a non-owner is a no-op (not an error) so a stale CR that
// lost the lease to another can still call Release on delete.
func (l *FamilyLeaser) Release(ctx context.Context, device, family, identity string) error {
	name := leaseName(device, family)
	var lease coordv1.Lease
	err := l.Client.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: name}, &lease)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get lease: %w", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != identity {
		return nil
	}
	if err := l.Client.Delete(ctx, &lease); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete lease: %w", err)
	}
	return nil
}

// renewOrReport is the shared path for an existing lease: if we
// already hold it (or the prior holder expired) we renew; otherwise
// we report the current holder.
func (l *FamilyLeaser) renewOrReport(
	ctx context.Context,
	lease *coordv1.Lease,
	identity string,
	ttlSeconds int32,
	now metav1.MicroTime,
) (LeaseResult, error) {
	holder := ""
	if lease.Spec.HolderIdentity != nil {
		holder = *lease.Spec.HolderIdentity
	}
	expired := leaseExpired(lease, now.Time)

	if holder == identity {
		// We already own it — renew.
		lease.Spec.RenewTime = &now
		lease.Spec.LeaseDurationSeconds = &ttlSeconds
		if err := l.Client.Update(ctx, lease); err != nil {
			if apierrors.IsConflict(err) {
				// Another caller renewed concurrently; treat as still
				// owned by us because the identity matched on the read.
				return LeaseResult{Owned: true, Holder: identity}, nil
			}
			return LeaseResult{}, fmt.Errorf("renew lease: %w", err)
		}
		return LeaseResult{Owned: true, Holder: identity}, nil
	}
	if expired {
		// Previous holder timed out — take over.
		lease.Spec.HolderIdentity = strPtr(identity)
		lease.Spec.AcquireTime = &now
		lease.Spec.RenewTime = &now
		lease.Spec.LeaseDurationSeconds = &ttlSeconds
		transitions := int32(1)
		if lease.Spec.LeaseTransitions != nil {
			transitions = *lease.Spec.LeaseTransitions + 1
		}
		lease.Spec.LeaseTransitions = &transitions
		if err := l.Client.Update(ctx, lease); err != nil {
			if apierrors.IsConflict(err) {
				// Conflict on takeover: another caller may have taken
				// the lease; be pessimistic and report as not-owned so
				// the next tick re-evaluates.
				return LeaseResult{Owned: false, Holder: holder}, nil
			}
			return LeaseResult{}, fmt.Errorf("takeover lease: %w", err)
		}
		return LeaseResult{Owned: true, Holder: identity}, nil
	}
	// Someone else owns it and it is still valid.
	return LeaseResult{Owned: false, Holder: holder}, nil
}

// leaseExpired checks whether the lease's renew+duration window has
// elapsed relative to now. Leases without RenewTime are treated as
// expired; a valid holder always sets RenewTime.
func leaseExpired(lease *coordv1.Lease, now time.Time) bool {
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true
	}
	expiry := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return now.After(expiry)
}

func leaseName(device, family string) string {
	return fmt.Sprintf("cvk-%s-%s", device, family)
}

func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
