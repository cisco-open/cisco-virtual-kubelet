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

package configdriver

import (
	"context"
	"errors"
	"testing"
)

func TestStubWritePathReturnsErrNotImplemented(t *testing.T) {
	ctx := context.Background()
	d := NewStubDriver()

	// Lifecycle methods succeed so callers can wire them unconditionally.
	if err := d.Open(ctx, nil, DeviceInfo{}); err != nil {
		t.Fatalf("Open: unexpected err: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: unexpected err: %v", err)
	}

	// Write-path methods must all surface ErrNotImplemented as a sentinel
	// so provider-level status code can distinguish "not yet built" from
	// "real device error".
	cases := []struct {
		name string
		run  func() error
	}{
		{"Validate", func() error { return d.Validate(ctx, ResolvedIntent{}) }},
		{"Fetch", func() error { _, err := d.Fetch(ctx, nil); return err }},
		{"Plan", func() error { _, err := d.Plan(ctx, ResolvedIntent{}, Observed{}); return err }},
		{"Apply", func() error { _, err := d.Apply(ctx, Plan{}); return err }},
		{"Rollback", func() error { return d.Rollback(ctx, "") }},
		{"SaveStartup", func() error { return d.SaveStartup(ctx) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrNotImplemented) {
				t.Fatalf("%s: got %v, want ErrNotImplemented", tc.name, err)
			}
		})
	}
}
