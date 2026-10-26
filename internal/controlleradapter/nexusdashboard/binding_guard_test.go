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
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// setLive edits the NetworkController the way an operator would.
func setLive(t *testing.T, a *adapter, edit func(*ciskov1.NetworkController)) {
	t.Helper()
	ctx := context.Background()
	var nc ciskov1.NetworkController
	if err := a.statusWriter.Get(ctx, a.key, &nc); err != nil {
		t.Fatal(err)
	}
	edit(&nc)
	if err := a.statusWriter.Update(ctx, &nc); err != nil {
		t.Fatal(err)
	}
}

// adoptingAdapter is a worker started with adoption enabled, wired as in
// SetupWithManager (guarded device syncer), against a live spec that agrees.
func adoptingAdapter(t *testing.T, items ...InventoryItem) (*adapter, *testClock) {
	t.Helper()
	a, _, _ := newTestAdapter(t, func(context.Context) error { return nil })
	spec := adoptionSpec()
	setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Spec.DeviceAdoption = spec.DeepCopy() })
	clk := &testClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	a.now = clk.now
	a.adoption = spec
	a.setupDevices()
	a.list = func(context.Context) ([]InventoryItem, error) { return items, nil }
	return a, clk
}

func deviceCount(t *testing.T, a *adapter) int {
	t.Helper()
	var list ciskov1.CiscoDeviceList
	if err := a.statusWriter.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	return len(list.Items)
}

func TestActiveWorkerStillAdopts(t *testing.T) {
	a, _ := adoptingAdapter(t, item("S1", "a", "f"), item("S2", "b", "f"))
	a.syncInventory(context.Background())
	if n := deviceCount(t, a); n != 2 {
		t.Fatalf("an active, bound worker must adopt: %d devices", n)
	}
}

// A worker that is no longer the active binding must perform no device write,
// whatever its startup adoption settings say.
func TestInactiveWorkerPerformsNoDeviceWrites(t *testing.T) {
	for name, deactivate := range map[string]func(*adapter){
		"superseded generation": func(a *adapter) {
			setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Generation = 4 })
		},
		"replaced (UID changed)": func(a *adapter) { a.uid = "some-other-controller" },
		"paused": func(a *adapter) {
			setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Spec.Paused = true })
		},
		"adoption disabled in the live spec": func(a *adapter) {
			setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Spec.DeviceAdoption.Enabled = false })
		},
		"adoption removed from the live spec": func(a *adapter) {
			setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Spec.DeviceAdoption = nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := adoptingAdapter(t, item("S1", "a", "f"))
			deactivate(a)
			a.syncInventory(context.Background())
			if n := deviceCount(t, a); n != 0 {
				t.Fatalf("a stale worker created %d device(s)", n)
			}
		})
	}
}

func TestInactiveWorkerDoesNotUpdateAnnotateOrPrune(t *testing.T) {
	a, clk := adoptingAdapter(t, item("S1", "a", "f"), item("S2", "b", "f"))
	a.adoption.Removal = &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: time.Hour},
	}
	setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Spec.DeviceAdoption = a.adoption.DeepCopy() })
	a.setupDevices()
	ctx := context.Background()
	a.syncInventory(ctx)
	if deviceCount(t, a) != 2 {
		t.Fatal("setup: both devices should exist")
	}
	before := getDevice(t, a.statusWriter, "nd-s1").DeepCopy()

	// The controller is paused during a rollout while the old worker still runs
	// with its startup settings, ND changed S1's address and S2 has left.
	setLive(t, a, func(nc *ciskov1.NetworkController) { nc.Spec.Paused = true })
	moved := item("S1", "a", "f")
	moved.MgmtAddress = "192.0.2.99"
	a.list = func(context.Context) ([]InventoryItem, error) { return []InventoryItem{moved}, nil }
	for i := 0; i < 3; i++ {
		clk.advance(2 * time.Hour)
		a.syncInventory(ctx)
	}
	if !deviceExists(t, a.statusWriter, "nd-s2") {
		t.Fatal("a stale worker pruned a device")
	}
	if got := getDevice(t, a.statusWriter, "nd-s2").Annotations[annotationMissingSince]; got != "" {
		t.Fatalf("a stale worker annotated a device (%q)", got)
	}
	if got := getDevice(t, a.statusWriter, "nd-s1"); got.Spec.Address != before.Spec.Address {
		t.Fatalf("a stale worker updated a device: %q -> %q", before.Spec.Address, got.Spec.Address)
	}
}

// The guard runs before every mutation, so a sync that is superseded halfway
// stops at once instead of finishing its batch.
func TestSyncStopsAtTheFirstDeniedMutation(t *testing.T) {
	s, cl := newSyncer(t)
	calls := 0
	s.guard = func(context.Context) error {
		calls++
		if calls > 1 {
			return errBindingInactive
		}
		return nil
	}
	res, err := s.Sync(context.Background(), []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f"), item("S3", "c", "f")})
	if !errors.Is(err, errBindingInactive) {
		t.Fatalf("err = %v, want errBindingInactive", err)
	}
	var list ciskov1.CiscoDeviceList
	if lerr := cl.List(context.Background(), &list); lerr != nil || len(list.Items) != 1 || res.Created != 1 || res.Failed != 0 {
		t.Fatalf("devices=%d res=%+v err=%v: want exactly one created and no failures", len(list.Items), res, lerr)
	}
}

func TestUnverifiableBindingFailsClosed(t *testing.T) {
	a, _ := adoptingAdapter(t, item("S1", "a", "f"))
	a.reader = errReader{} // the API server cannot be reached
	a.syncInventory(context.Background())
	if n := deviceCount(t, a); n != 0 {
		t.Fatalf("a worker that cannot confirm its binding created %d device(s)", n)
	}
}

type errReader struct{}

func (errReader) Get(context.Context, ctrlclient.ObjectKey, ctrlclient.Object, ...ctrlclient.GetOption) error {
	return errors.New("connection refused")
}

func (errReader) List(context.Context, ctrlclient.ObjectList, ...ctrlclient.ListOption) error {
	return errors.New("connection refused")
}
