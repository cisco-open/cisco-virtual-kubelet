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

package nexusdashboard

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newRemovalSyncer(t *testing.T, removal *ciskov1.NetworkControllerDeviceRemoval, objs ...ctrlclient.Object) (*deviceSyncer, ctrlclient.Client, *testClock) {
	t.Helper()
	s, cl := newSyncer(t, objs...)
	s.adoption.Removal = removal
	clk := &testClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s.now = clk.now
	return s, cl, clk
}

func deviceExists(t *testing.T, cl ctrlclient.Client, name string) bool {
	t.Helper()
	var d ciskov1.CiscoDevice
	err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, &d)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

func TestRetainOnlyAnnotatesAndClearsOnReturn(t *testing.T) {
	s, cl, clk := newRemovalSyncer(t, nil)
	ctx := context.Background()
	both := []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f")}
	one := []InventoryItem{item("S1", "a", "f")}
	if _, err := s.Sync(ctx, both); err != nil {
		t.Fatal(err)
	}

	res, err := s.Sync(ctx, one)
	if err != nil || res.Missing != 1 || res.Pruned != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	first := getDevice(t, cl, "nd-s2").Annotations[annotationMissingSince]
	if first == "" || getDevice(t, cl, "nd-s1").Annotations[annotationMissingSince] != "" {
		t.Fatalf("only the missing device must be annotated, got %q", first)
	}

	// The first-seen time is kept across refreshes, and nothing is deleted
	// however long the switch stays away.
	clk.advance(1000 * time.Hour)
	if _, err := s.Sync(ctx, one); err != nil {
		t.Fatal(err)
	}
	if got := getDevice(t, cl, "nd-s2").Annotations[annotationMissingSince]; got != first {
		t.Fatalf("annotation was reset: %q -> %q", first, got)
	}

	res, err = s.Sync(ctx, both)
	if err != nil || res.Missing != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, ok := getDevice(t, cl, "nd-s2").Annotations[annotationMissingSince]; ok {
		t.Fatal("annotation must be cleared when the switch returns")
	}
}

func TestPruneAfterGracePeriodOnly(t *testing.T) {
	s, cl, clk := newRemovalSyncer(t, &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: time.Hour},
	})
	ctx := context.Background()
	if _, err := s.Sync(ctx, []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f")}); err != nil {
		t.Fatal(err)
	}
	one := []InventoryItem{item("S1", "a", "f")}

	if res, _ := s.Sync(ctx, one); res.Pruned != 0 || !deviceExists(t, cl, "nd-s2") {
		t.Fatalf("must only annotate on first sight: %+v", res)
	}
	clk.advance(59 * time.Minute)
	if res, _ := s.Sync(ctx, one); res.Pruned != 0 || !deviceExists(t, cl, "nd-s2") {
		t.Fatalf("must wait for the grace period: %+v", res)
	}
	clk.advance(2 * time.Minute)
	res, err := s.Sync(ctx, one)
	if err != nil || res.Pruned != 1 || deviceExists(t, cl, "nd-s2") || !deviceExists(t, cl, "nd-s1") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestPruneNeverTouchesUnownedOrStillListedDevices(t *testing.T) {
	foreign := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nd-foreign"}}
	otherCtl := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "ns", Name: "nd-other", Labels: map[string]string{labelController: "someone-else"},
	}}
	s, cl, clk := newRemovalSyncer(t, &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: 10 * time.Minute},
	}, foreign, otherCtl)
	ctx := context.Background()

	unreachable := item("S2", "b", "f")
	unreachable.Reachable = false
	skipped := item("S3", "c", "f")
	skipped.SkipReason = "not NX-OS"
	inv := []InventoryItem{item("S1", "a", "f"), unreachable}
	if _, err := s.Sync(ctx, []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f"), item("S3", "c", "f")}); err != nil {
		t.Fatal(err)
	}
	inv = append(inv, skipped)

	clk.advance(24 * time.Hour)
	for i := 0; i < 2; i++ {
		if res, err := s.Sync(ctx, inv); err != nil || res.Missing != 0 || res.Pruned != 0 {
			t.Fatalf("listed switches are not missing: %+v err=%v", res, err)
		}
	}
	for _, n := range []string{"nd-foreign", "nd-other", "nd-s1", "nd-s2", "nd-s3"} {
		if !deviceExists(t, cl, n) {
			t.Fatalf("%s must survive", n)
		}
	}
}

func TestEmptyInventoryChangesNothing(t *testing.T) {
	s, cl, clk := newRemovalSyncer(t, &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: 10 * time.Minute},
	})
	ctx := context.Background()
	if _, err := s.Sync(ctx, []InventoryItem{item("S1", "a", "f")}); err != nil {
		t.Fatal(err)
	}
	clk.advance(48 * time.Hour)
	for i := 0; i < 2; i++ {
		res, err := s.Sync(ctx, nil)
		if err != nil || res.Missing != 0 || res.Pruned != 0 {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	}
	if d := getDevice(t, cl, "nd-s1"); d.Annotations[annotationMissingSince] != "" {
		t.Fatal("an empty inventory must not mark everything missing")
	}
}

func TestPruneMassDeleteGuard(t *testing.T) {
	removal := &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: 10 * time.Minute},
	}
	s, cl, clk := newRemovalSyncer(t, removal)
	ctx := context.Background()
	all := []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f"), item("S3", "c", "f"), item("S4", "d", "f")}
	if _, err := s.Sync(ctx, all); err != nil {
		t.Fatal(err)
	}

	// Two of four due at once is 50%, above the default 25%: nothing goes.
	twoLeft := all[:2]
	_, _ = s.Sync(ctx, twoLeft)
	clk.advance(time.Hour)
	res, err := s.Sync(ctx, twoLeft)
	if err != nil || res.Pruned != 0 || res.PruneGuarded != 2 {
		t.Fatalf("guard must hold back a mass deletion: %+v err=%v", res, err)
	}
	for _, n := range []string{"nd-s1", "nd-s2", "nd-s3", "nd-s4"} {
		if !deviceExists(t, cl, n) {
			t.Fatalf("%s must survive the guard", n)
		}
	}

	// One device is always allowed, even if it exceeds the percentage.
	threeLeft := all[:3]
	res, err = s.Sync(ctx, threeLeft) // S3 returns, S4 is still due
	if err != nil || res.Pruned != 1 || deviceExists(t, cl, "nd-s4") {
		t.Fatalf("a single removal must go through: %+v err=%v", res, err)
	}

	// A raised percentage lets a larger batch through.
	removal.MaxPrunePercent = func() *int32 { v := int32(100); return &v }()
	_, _ = s.Sync(ctx, all[:1])
	clk.advance(time.Hour)
	res, err = s.Sync(ctx, all[:1])
	if err != nil || res.Pruned != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestOperatorLabelsAppliedMergedAndRemoved(t *testing.T) {
	s, cl, _ := newRemovalSyncer(t, nil)
	s.adoption.Defaults.Labels = map[string]string{"upgrade-wave": "1", "owner": "netops"}
	s.adoption.ScopeOverrides[0].Labels = map[string]string{"upgrade-wave": "2"} // fab-b
	ctx := context.Background()
	inv := []InventoryItem{item("S1", "a", "fab-a"), item("S2", "b", "fab-b")}
	if _, err := s.Sync(ctx, inv); err != nil {
		t.Fatal(err)
	}

	a, b := getDevice(t, cl, "nd-s1"), getDevice(t, cl, "nd-s2")
	for _, tc := range []struct {
		d    *ciskov1.CiscoDevice
		wave string
	}{{a, "1"}, {b, "2"}} {
		for _, labels := range []map[string]string{tc.d.Labels, tc.d.Spec.Labels} {
			if labels["upgrade-wave"] != tc.wave || labels["owner"] != "netops" || labels[labelFabric] == "" {
				t.Fatalf("%s labels = %v", tc.d.Name, labels)
			}
		}
	}

	// A user label is kept; dropping an operator label removes it from both
	// label sets, and the user label is untouched.
	a.Labels["user-label"] = "keep"
	a.Spec.Labels["user-label"] = "keep"
	if err := cl.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	s.adoption.Defaults.Labels = map[string]string{"owner": "netops"}
	res, err := s.Sync(ctx, inv)
	if err != nil || res.Updated != 1 || res.Unchanged != 1 { // fab-b keeps its own wave
		t.Fatalf("res=%+v err=%v", res, err)
	}
	a = getDevice(t, cl, "nd-s1")
	for _, labels := range []map[string]string{a.Labels, a.Spec.Labels} {
		if _, ok := labels["upgrade-wave"]; ok || labels["user-label"] != "keep" || labels["owner"] != "netops" {
			t.Fatalf("labels after removal = %v", labels)
		}
	}
	if b = getDevice(t, cl, "nd-s2"); b.Labels["upgrade-wave"] != "2" {
		t.Fatalf("override must still apply: %v", b.Labels)
	}
	if res, _ = s.Sync(ctx, inv); res.Updated != 0 || res.Unchanged != 2 {
		t.Fatalf("must settle: %+v", res)
	}
}

func TestDeviceWithoutManagedAnnotationIsAdoptedWithoutLosingLabels(t *testing.T) {
	old := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "ns", Name: "nd-s1", Labels: map[string]string{labelController: testUID, "legacy": "x"},
	}, Spec: ciskov1.DeviceSpec{Labels: map[string]string{"legacy": "x"}}}
	s, cl, _ := newRemovalSyncer(t, nil, old)
	if _, err := s.Sync(context.Background(), []InventoryItem{item("S1", "a", "f")}); err != nil {
		t.Fatal(err)
	}
	d := getDevice(t, cl, "nd-s1")
	if d.Labels["legacy"] != "x" || d.Spec.Labels["legacy"] != "x" || d.Annotations[annotationManagedLabels] == "" {
		t.Fatalf("got labels=%v spec=%v ann=%v", d.Labels, d.Spec.Labels, d.Annotations)
	}
}
