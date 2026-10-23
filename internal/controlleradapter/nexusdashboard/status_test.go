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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func capNames(nc *ciskov1.NetworkController) []string {
	var out []string
	for _, c := range nc.Status.Capabilities {
		out = append(out, c.Name)
	}
	return out
}

func TestPublishReplacesTheWholeCapabilityList(t *testing.T) {
	a, orig, get := newTestAdapter(t, func(context.Context) error { return nil })
	ctx := context.Background()
	// Entries left by an earlier run, including one this adapter no longer owns.
	stale := orig.DeepCopy()
	stale.Status.Capabilities = []ciskov1.NetworkControllerCapabilityStatus{
		{Name: CapabilityDeviceAdoption, Supported: true, Message: "1 created"},
		{Name: "leftover", Supported: true},
	}
	if err := a.statusWriter.Status().Update(ctx, stale); err != nil {
		t.Fatal(err)
	}

	a.check(ctx)
	if err := a.publishCapability(ctx, CapabilityInventory, true, "1 switches in 1 fabrics"); err != nil {
		t.Fatal(err)
	}
	if got, want := capNames(get()), []string{CapabilityHealth, CapabilityInventory}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %v, want exactly %v (stale and foreign entries must be gone)", got, want)
	}

	if err := a.publishCapability(ctx, CapabilityDeviceAdoption, true, "1 created"); err != nil {
		t.Fatal(err)
	}
	want := []string{CapabilityHealth, CapabilityInventory, CapabilityDeviceAdoption}
	if got := capNames(get()); !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %v, want %v in a fixed order", got, want)
	}
}

func TestLoopsKeepEachOthersLastResult(t *testing.T) {
	var fail bool
	a, _, get := newTestAdapter(t, func(context.Context) error {
		if fail {
			return errAuthentication
		}
		return nil
	})
	ctx := context.Background()
	a.check(ctx)
	if err := a.publishCapability(ctx, CapabilityInventory, true, "1 switches in 1 fabrics"); err != nil {
		t.Fatal(err)
	}

	fail = true
	a.check(ctx) // health fails; the inventory result must survive in the list
	nc := get()
	if got, want := capNames(nc), []string{CapabilityHealth, CapabilityInventory}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
	if nc.Status.Capabilities[0].Supported || !nc.Status.Capabilities[1].Supported {
		t.Fatalf("health must be unsupported and inventory keep its last result: %+v", nc.Status.Capabilities)
	}
}

func TestInventoryPublishDoesNotTouchHealthStatus(t *testing.T) {
	a, _, get := newTestAdapter(t, func(context.Context) error { return nil })
	ctx := context.Background()
	a.check(ctx)
	before := get().Status.LastAttemptTime
	if err := a.publishCapability(ctx, CapabilityInventory, true, "x"); err != nil {
		t.Fatal(err)
	}
	if after := get().Status.LastAttemptTime; !after.Equal(before) {
		t.Fatalf("an inventory publish must not bump the health attempt time: %v -> %v", before, after)
	}
}

func TestStillBound(t *testing.T) {
	now := metav1.Now()
	a := &adapter{uid: "u1", generation: 3}
	mk := func(uid string, gen int64, paused bool, deleting bool) *ciskov1.NetworkController {
		nc := &ciskov1.NetworkController{ObjectMeta: metav1.ObjectMeta{UID: types.UID(uid), Generation: gen}}
		nc.Spec.Paused = paused
		if deleting {
			nc.DeletionTimestamp = &now
		}
		return nc
	}
	for name, tc := range map[string]struct {
		nc   *ciskov1.NetworkController
		want bool
	}{
		"current":          {mk("u1", 3, false, false), true},
		"replaced (UID)":   {mk("u2", 3, false, false), false},
		"newer generation": {mk("u1", 4, false, false), false},
		"paused":           {mk("u1", 3, true, false), false},
		"deleting":         {mk("u1", 3, false, true), false},
	} {
		err := a.stillBound(tc.nc)
		if (err == nil) != tc.want || (err != nil && !errors.Is(err, errBindingInactive)) {
			t.Errorf("%s: err=%v, want bound=%v", name, err, tc.want)
		}
	}
}

func TestSupersededWorkerPublishesNothing(t *testing.T) {
	a, _, get := newTestAdapter(t, func(context.Context) error { return nil })
	a.generation = 2 // the object is at generation 3: this worker is stale
	err := a.publishCapability(context.Background(), CapabilityInventory, true, "x")
	if !errors.Is(err, errBindingInactive) {
		t.Fatalf("err = %v, want errBindingInactive", err)
	}
	if len(get().Status.Capabilities) != 0 {
		t.Fatalf("a stale worker must not write status: %+v", get().Status)
	}
}
