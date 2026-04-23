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

package writers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
)

// phase1Families enumerates the family set promised for Phase 1. The test
// guards against silent drift: adding a family without updating this list
// (or removing one without a matching code change) fails.
var phase1Families = []string{
	"access_list_extended",
	"dhcp",
	"interface_ethernet",
	"interface_loopback",
	"interface_virtual_port_group",
	"system",
	"vlan",
	"vrf",
}

func TestPhase1FamiliesRegistered(t *testing.T) {
	got := Families()
	if !reflect.DeepEqual(got, phase1Families) {
		t.Fatalf("registered families = %v, want %v", got, phase1Families)
	}
	if Len() != len(phase1Families) {
		t.Fatalf("Len() = %d, want %d", Len(), len(phase1Families))
	}
}

func TestGetReturnsRegisteredWriter(t *testing.T) {
	for _, fam := range phase1Families {
		t.Run(fam, func(t *testing.T) {
			w := Get(fam)
			if w == nil {
				t.Fatalf("Get(%q) returned nil", fam)
			}
			if w.Family() != fam {
				t.Fatalf("Family() = %q, want %q", w.Family(), fam)
			}
			if len(w.YANGPaths()) == 0 {
				t.Fatalf("YANGPaths() = empty for %q", fam)
			}
		})
	}
}

func TestGetReturnsNilForUnknown(t *testing.T) {
	if w := Get("not-a-real-family"); w != nil {
		t.Fatalf("Get(unknown) = %v, want nil", w)
	}
}

// TestSkeletonWritePathReturnsSentinel pins the contract that every
// skeleton error is errors.Is-matchable against configdriver.ErrNotImplemented
// so provider status code can distinguish scaffold from live device failures.
// Every Phase-1 family now ships a real writer; this test pulls a
// Phase-2 family that is still a skeleton and exercises its skeleton
// stub. Switch to a family that remains unimplemented as each family's
// real writer lands.
func TestSkeletonWritePathReturnsSentinel(t *testing.T) {
	// Register a skeleton explicitly so the test is stable against
	// Phase-2 writers landing in any order and replacing entries.
	skelName := "_test_skeleton_family_"
	registerSkeleton(skelName, "/Cisco-IOS-XE-native:native/test-only")
	t.Cleanup(func() {
		mu.Lock()
		delete(registry, skelName)
		mu.Unlock()
	})
	w := Get(skelName)
	if w == nil {
		t.Fatal("skeleton writer unexpectedly unregistered")
	}
	if _, err := w.Fetch(context.Background(), nil); !errors.Is(err, configdriver.ErrNotImplemented) {
		t.Fatalf("Fetch: got %v, want configdriver.ErrNotImplemented", err)
	}
	if _, err := w.Diff(nil, nil); !errors.Is(err, configdriver.ErrNotImplemented) {
		t.Fatalf("Diff: got %v, want configdriver.ErrNotImplemented", err)
	}
	if err := w.Apply(context.Background(), nil, nil); !errors.Is(err, configdriver.ErrNotImplemented) {
		t.Fatalf("Apply: got %v, want configdriver.ErrNotImplemented", err)
	}
}

func TestRegisterNilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(nil): expected panic")
		}
	}()
	Register(nil)
}
