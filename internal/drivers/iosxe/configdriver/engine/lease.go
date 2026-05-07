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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

// AcquireIfFree is the takeover-safe variant of Acquire used by the
// CR-delete relinquish path. It returns Owned=true only when the
// lease is unheld OR already held by `identity` — never when a
// foreign holder is present, whether the lease is expired or not.
//
// Why a separate path: Acquire actively takes over an expired
// lease (RenewTime + LeaseDuration in the past), which is the
// correct policy for crashed-holder recovery during normal
// reconcile. But it is the WRONG policy at delete time: a foreign
// reconcile that hasn't heartbeat through a long Fetch/Apply call
// can look expired while still in flight; takeover would let the
// terminating CR DELETE keys the live CR is mid-reconcile against.
// Codex /codex:adversarial-review (2026-05-02) B1.
//
// Stale-but-in-flight recovery stays the responsibility of the
// normal-reconcile path, which holds and renews the lease through
// its own critical section.
func (l *FamilyLeaser) AcquireIfFree(ctx context.Context, device, family, identity string) (LeaseResult, error) {
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
		// Free → create + own.
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
			if !apierrors.IsAlreadyExists(err) {
				return LeaseResult{}, fmt.Errorf("create lease: %w", err)
			}
			// Lost the create race — re-read and re-evaluate.
			if err := l.Client.Get(ctx, types.NamespacedName{Namespace: l.Namespace, Name: name}, &lease); err != nil {
				return LeaseResult{}, fmt.Errorf("re-read lease after conflict: %w", err)
			}
			break
		}
		return LeaseResult{Owned: true, Holder: identity}, nil
	case err != nil:
		return LeaseResult{}, fmt.Errorf("get lease: %w", err)
	}

	// Existing lease: only acceptable if we are the holder.
	holder := ""
	if lease.Spec.HolderIdentity != nil {
		holder = *lease.Spec.HolderIdentity
	}
	if holder == "" || holder == identity {
		// Empty holder is treated as free; renew with our identity.
		lease.Spec.HolderIdentity = strPtr(identity)
		lease.Spec.RenewTime = &now
		lease.Spec.LeaseDurationSeconds = &ttlSeconds
		if err := l.Client.Update(ctx, &lease); err != nil {
			if apierrors.IsConflict(err) {
				return LeaseResult{Owned: true, Holder: identity}, nil
			}
			return LeaseResult{}, fmt.Errorf("renew lease: %w", err)
		}
		return LeaseResult{Owned: true, Holder: identity}, nil
	}
	// Foreign holder, regardless of expiry → blocked.
	return LeaseResult{Owned: false, Holder: holder}, nil
}

// LeaseName returns the canonical Lease name FamilyLeaser uses for
// (device, family). Exported so tests and operator tooling can
// look up or seed the right object without guessing the hashing
// rules in leaseName().
func LeaseName(device, family string) string { return leaseName(device, family) }

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

// leaseName composes a stable, DNS-1123-subdomain-safe Lease name.
//
// Wave 8.1 (external-review-wave7-residuals Finding #1). The
// previous implementation produced literal "cvk-<device>-<family>".
// Underscore-bearing family names — interface_ethernet,
// access_list_extended, interface_switchport, ip_name_server, and
// most of the shipped IOS-XE family set — violate DNS-1123 subdomain
// rules ([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*).
// Real apiserver rejects every such Lease.create. fake.Client
// skipped name validation so existing tests passed; the failure
// only surfaced in a live cluster.
//
// The sanitised composition has three parts:
//
//   - "cvk-" prefix (stable, identifies these leases as ours).
//   - Sanitised device + sanitised family. Each is lowercased and
//     non-DNS-1123 characters are replaced with '-'; runs of '-'
//     collapse and leading/trailing '-' is trimmed. The original
//     unsanitised values are still preserved on the lease's labels
//     ("cisco.vk/device", "cisco.vk/family") so operators can
//     filter with kubectl get leases -l cisco.vk/family=<orig>.
//   - 8-char SHA-256 prefix of the original "<device>/<family>"
//     pair. Two distinct inputs that fold to the same sanitised
//     prefix (e.g. "interface_ethernet" and "interface-ethernet")
//     get distinct leases.
//
// The result fits well under the 253-byte K8s name limit even with
// long family names — typical lengths are around 50-70 chars.
func leaseName(device, family string) string {
	d := sanitiseLeaseSegment(device)
	f := sanitiseLeaseSegment(family)
	hash := shortLeaseHash(device + "/" + family)
	name := fmt.Sprintf("cvk-%s-%s-%s", d, f, hash)
	// Defence in depth: K8s names cap at 253 bytes. Even pathological
	// inputs (very long device names) are unlikely to overrun, but
	// truncate at 253 by trimming the human-readable middle while
	// keeping prefix + hash intact.
	const maxNameLen = 253
	if len(name) > maxNameLen {
		// Reserve "cvk-" (4) + "-" + hash (8) = 13; leave the rest
		// for the device/family combo and truncate it.
		head := "cvk-"
		tail := "-" + hash
		room := maxNameLen - len(head) - len(tail)
		middle := d + "-" + f
		if len(middle) > room {
			middle = middle[:room]
		}
		// Trim a possible trailing '-' left after slicing.
		middle = strings.TrimRight(middle, "-")
		name = head + middle + tail
	}
	return name
}

// sanitiseLeaseSegment maps an arbitrary identifier into the
// DNS-1123 subdomain alphabet. Lowercases ASCII letters, passes
// digits and '-' through, replaces every other byte with '-', then
// folds runs of '-' and trims leading/trailing '-'. An empty result
// (input was all-disallowed bytes) returns "x" so the composed
// name still has a non-empty middle segment.
func sanitiseLeaseSegment(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c >= '0' && c <= '9':
			out = append(out, c)
		default:
			// Includes '_', '/', '.', whitespace, and any other byte
			// outside the DNS-1123 label alphabet. We use '-' as the
			// universal replacement so the dedup pass below collapses
			// runs cleanly.
			out = append(out, '-')
		}
	}
	// Collapse runs of '-' so "_a__b_" → "-a--b-" → "-a-b-".
	collapsed := make([]byte, 0, len(out))
	prevDash := false
	for _, c := range out {
		if c == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		collapsed = append(collapsed, c)
	}
	result := strings.Trim(string(collapsed), "-")
	if result == "" {
		return "x"
	}
	return result
}

// shortLeaseHash returns the first 8 hex chars of the SHA-256 of
// the input. 8 hex chars (4 bytes / 32 bits) gives a collision
// probability negligible for the per-(device, family) cardinality
// realistic CVK fleets see.
func shortLeaseHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
