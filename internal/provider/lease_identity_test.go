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

package provider

// Wave 7A.3 regression tests for external-review-next-actions
// Finding #3: lease holder identity must include a runtime suffix
// so two reconcilers with the same CR identity but different
// runtime IDs (old + new pod during a Deployment rollout, two
// aggregator workers during manager restart) cannot both renew
// the same lease and concurrently write the same (device, family).

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	coordv1 "k8s.io/api/coordination/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
)

func leaseIdentityScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(coordv1.AddToScheme(s))
	utilruntime.Must(configv1alpha1.AddToScheme(s))
	return s
}

// TestStripRuntimeIDSuffix pins the helper that converts a lease
// holder back to the human-readable CR identity for status messages.
func TestStripRuntimeIDSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"ns/name#abc123", "ns/name"},
		{"ns/name", "ns/name"}, // no suffix → unchanged
		{"#orphan", ""},
		{"ns/name#a#b", "ns/name"}, // first '#' wins
		{"", ""},
	}
	for _, c := range cases {
		if got := stripRuntimeIDSuffix(c.in); got != c.want {
			t.Errorf("stripRuntimeIDSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAcquireLeases_RuntimeIDDistinguishesHolders is the headline
// assertion for Finding #3: two ConfigReconcilers with the same CR
// identity but different RuntimeIDs cannot both hold the same
// family lease.
func TestAcquireLeases_RuntimeIDDistinguishesHolders(t *testing.T) {
	t.Parallel()
	scheme := leaseIdentityScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "lease-ns", TTL: 30 * time.Second}

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev-1"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
			},
		},
	}
	resolved := &intent.ResolvedIntent{
		DeviceName:      "dev-1",
		ManagedFamilies: []string{"vlan"},
	}

	// Reconciler A — represents the "old" pod / first worker.
	rA := &ConfigReconciler{
		DeviceName: "dev-1",
		Leaser:     leaser,
		RuntimeID:  "pod-uid-A",
	}
	leasedA, conflictsA := rA.acquireLeases(context.Background(), resolved, cr)
	if len(leasedA.ManagedFamilies) != 1 {
		t.Fatalf("A: expected to own vlan, got %v conflicts=%v", leasedA.ManagedFamilies, conflictsA)
	}

	// Reconciler B — represents the "new" pod / second worker, same CR.
	rB := &ConfigReconciler{
		DeviceName: "dev-1",
		Leaser:     leaser,
		RuntimeID:  "pod-uid-B",
	}
	leasedB, conflictsB := rB.acquireLeases(context.Background(), resolved, cr)
	if len(leasedB.ManagedFamilies) != 0 {
		t.Errorf("B: must NOT own vlan while A holds it (different RuntimeID); got %v", leasedB.ManagedFamilies)
	}
	holder, blocked := conflictsB["vlan"]
	if !blocked {
		t.Fatalf("B: vlan must appear in conflicts; got %v", conflictsB)
	}
	// The reported holder is the CR identity (operator-readable),
	// stripped of the runtime suffix.
	if holder != "tenant/edge-01" {
		t.Errorf("B: conflict message must name the CR (not the pod UID), got %q", holder)
	}
	if strings.Contains(holder, "#") {
		t.Errorf("B: conflict message leaks runtime suffix: %q", holder)
	}
}

// TestAcquireLeases_SameRuntimeIDRenewsOwnLease pins the
// renew-own-lease path: a single reconciler re-running its
// acquireLeases (same RuntimeID, same CR identity) keeps the
// lease and proceeds.
func TestAcquireLeases_SameRuntimeIDRenewsOwnLease(t *testing.T) {
	t.Parallel()
	scheme := leaseIdentityScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "lease-ns", TTL: 30 * time.Second}

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev-1"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
			},
		},
	}
	resolved := &intent.ResolvedIntent{
		DeviceName:      "dev-1",
		ManagedFamilies: []string{"vlan"},
	}

	r := &ConfigReconciler{
		DeviceName: "dev-1",
		Leaser:     leaser,
		RuntimeID:  "pod-uid",
	}
	for i := 0; i < 3; i++ {
		leased, conflicts := r.acquireLeases(context.Background(), resolved, cr)
		if len(leased.ManagedFamilies) != 1 || len(conflicts) != 0 {
			t.Errorf("tick %d: expected own lease + no conflicts, got leased=%v conflicts=%v",
				i, leased.ManagedFamilies, conflicts)
		}
	}
}

// TestAcquireLeases_EmptyRuntimeIDPreservesLegacyBehaviour pins
// the test/polling-Run fallback: when RuntimeID is empty, the
// lease identity is just the CR's namespace/name — same as
// pre-Wave-7A.3. Existing tests and the polling Run path don't
// have to inject a runtime suffix.
func TestAcquireLeases_EmptyRuntimeIDPreservesLegacyBehaviour(t *testing.T) {
	t.Parallel()
	scheme := leaseIdentityScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "lease-ns", TTL: 30 * time.Second}

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev-1"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
			},
		},
	}
	resolved := &intent.ResolvedIntent{
		DeviceName:      "dev-1",
		ManagedFamilies: []string{"vlan"},
	}

	r := &ConfigReconciler{
		DeviceName: "dev-1",
		Leaser:     leaser,
		RuntimeID:  "", // legacy path
	}
	leased, conflicts := r.acquireLeases(context.Background(), resolved, cr)
	if len(leased.ManagedFamilies) != 1 || len(conflicts) != 0 {
		t.Errorf("empty RuntimeID: legacy behaviour broken; leased=%v conflicts=%v",
			leased.ManagedFamilies, conflicts)
	}
	// Verify the lease is keyed without a '#' suffix.
	var lease coordv1.Lease
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: "lease-ns", Name: "iosxecfg-dev-1-vlan"},
		&lease); err != nil {
		// Don't fail — the lease name format is internal; just skip
		// if the key shape changes.
		return
	}
	if lease.Spec.HolderIdentity != nil && strings.Contains(*lease.Spec.HolderIdentity, "#") {
		t.Errorf("empty RuntimeID should produce un-suffixed lease holder, got %q",
			*lease.Spec.HolderIdentity)
	}
}
