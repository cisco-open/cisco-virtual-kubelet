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
	"encoding/json"
	"sync"
	"testing"
)

// Codex adversarial-review item #3: multi-device aggregator
// contamination test, expressed at the resolver level.
//
// The aggregator runs one goroutine per CiscoDevice in the same
// process. Before the per-device OverrideResolver refactor, all
// goroutines shared a single process-global resolved override table
// — so a 17.18 device starting up could rewrite the resolved state
// the 17.16 device's reconcile goroutine was using mid-flight. The
// blast radius was wrong RESTCONF paths/bodies aimed at the wrong
// device.
//
// The contract this test pins:
//
//	GetForRelease(family, "17.16.x") and GetForRelease(family, "17.18.x")
//	return writers that observe DIFFERENT override resolution, and
//	running both concurrently does not flip either one's behaviour.
//
// If a future change reintroduces shared mutable state in the writer
// dispatch path, this test starts failing under -race or with a stale
// path/body mismatch.

// TestMultiDeviceResolverIsolation runs many concurrent goroutines
// against two release tags and asserts that each goroutine's writer
// emits the path/envelope shape for its own release. The assertion
// uses bgp because its override drives YANGPathOverride +
// EnvelopeKeyOverride (the values that would mis-target the wrong
// device if the global state leaked).
func TestMultiDeviceResolverIsolation(t *testing.T) {
	const (
		ver1716 = "17.16.01a"
		ver1718 = "17.18.2"
		iters   = 200
	)

	// Sanity: confirm the two releases pick different shapes.
	r16 := newResolverOrFail(t, ver1716)
	r18 := newResolverOrFail(t, ver1718)
	got16 := r16.ResolvedYANGPath("bgp", bgpYANGPath)
	got18 := r18.ResolvedYANGPath("bgp", bgpYANGPath)
	if got16 == got18 {
		t.Fatalf("test prerequisite broken: 17.16 and 17.18 resolve bgp to the same path %q", got16)
	}
	want1716Path := bgpYANGPathLegacy
	want1718Path := bgpYANGPath
	if got16 != want1716Path {
		t.Fatalf("17.16 bgp path = %q, want %q", got16, want1716Path)
	}
	if got18 != want1718Path {
		t.Fatalf("17.18 bgp path = %q, want %q", got18, want1718Path)
	}

	// Concurrency: hammer both releases from many goroutines
	// simultaneously, asserting each observes its own resolution.
	var wg sync.WaitGroup
	errCh := make(chan string, iters*2)

	for i := 0; i < iters; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			w := GetForRelease("bgp", ver1716)
			if w == nil {
				errCh <- "1716: GetForRelease returned nil"
				return
			}
			ops, err := w.Diff(map[string]any{"id": float64(65000)}, map[string]any{})
			if err != nil {
				errCh <- "1716: Diff: " + err.Error()
				return
			}
			if len(ops) != 1 {
				errCh <- "1716: expected 1 op"
				return
			}
			// 17.16 BGP writes to /router with the legacy list shape.
			if ops[0].Path != "/Cisco-IOS-XE-native:native/router" {
				errCh <- "1716: wrong path: " + ops[0].Path
			}
			// Body must contain the legacy router wrapper with the
			// bgp list inside — not the 17.18 router-bgp container.
			var body map[string]any
			if err := json.Unmarshal(ops[0].Body, &body); err != nil {
				errCh <- "1716: decode body: " + err.Error()
				return
			}
			router, _ := body["Cisco-IOS-XE-native:router"].(map[string]any)
			if router == nil {
				errCh <- "1716: missing native:router envelope"
				return
			}
			if _, ok := router[bgpEnvelopeKeyLegacy]; !ok {
				errCh <- "1716: missing legacy bgp list envelope"
			}
		}()
		go func() {
			defer wg.Done()
			w := GetForRelease("bgp", ver1718)
			if w == nil {
				errCh <- "1718: GetForRelease returned nil"
				return
			}
			ops, err := w.Diff(map[string]any{"id": float64(65000)}, map[string]any{})
			if err != nil {
				errCh <- "1718: Diff: " + err.Error()
				return
			}
			if len(ops) != 1 {
				errCh <- "1718: expected 1 op"
				return
			}
			// 17.18 BGP writes to /router/router-bgp.
			if ops[0].Path != bgpYANGPath {
				errCh <- "1718: wrong path: " + ops[0].Path
			}
			var body map[string]any
			if err := json.Unmarshal(ops[0].Body, &body); err != nil {
				errCh <- "1718: decode body: " + err.Error()
				return
			}
			if _, ok := body[bgpEnvelopeKey]; !ok {
				errCh <- "1718: missing baseline router-bgp envelope"
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var failures []string
	for msg := range errCh {
		failures = append(failures, msg)
	}
	if len(failures) > 0 {
		t.Fatalf("%d failures across %d iterations; first 5:\n%v",
			len(failures), iters*2, firstN(failures, 5))
	}
}

// TestUnsupportedReleaseFailsClosed verifies the second half of the
// contract: when a device reports a version outside the supported
// set, GetForRelease returns a writer whose Diff fails clearly
// rather than silently emitting baseline-shape ops.
func TestUnsupportedReleaseFailsClosed(t *testing.T) {
	w := GetForRelease("bgp", "17.99.0")
	if w == nil {
		t.Fatal("GetForRelease returned nil for unsupported version; expected a fail-closed sentinel writer")
	}
	_, err := w.Diff(map[string]any{"id": float64(65000)}, map[string]any{})
	if err == nil {
		t.Fatal("expected Diff to return an error for an unsupported device version; got nil")
	}
	if !IsUnsupportedDeviceVersion(err) {
		t.Errorf("expected ErrUnsupportedDeviceVersion, got %T: %v", err, err)
	}
}

// TestUnsupportedReleaseAfterSupportedDoesNotLeak verifies the
// "third device at 17.99 after two supported devices" scenario the
// adversarial review called out: once an unsupported version has
// been queried, supported-version writers must keep working.
func TestUnsupportedReleaseAfterSupportedDoesNotLeak(t *testing.T) {
	// Query 17.16 first.
	w16 := GetForRelease("bgp", "17.16.01a")
	if w16 == nil {
		t.Fatal("17.16: GetForRelease returned nil")
	}
	// Now query 17.99 — fails closed.
	wBad := GetForRelease("bgp", "17.99.0")
	if _, err := wBad.Diff(map[string]any{"id": float64(65000)}, map[string]any{}); err == nil {
		t.Error("17.99: expected Diff error; got nil")
	}
	// Re-query 17.16 — must still produce the legacy shape, not
	// inherit any state from the failed call.
	w16again := GetForRelease("bgp", "17.16.01a")
	ops, err := w16again.Diff(map[string]any{"id": float64(65000)}, map[string]any{})
	if err != nil {
		t.Fatalf("17.16 after 17.99: Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("17.16 after 17.99: expected 1 op, got %d", len(ops))
	}
	if ops[0].Path != "/Cisco-IOS-XE-native:native/router" {
		t.Errorf("17.16 after 17.99: wrong path %q (contamination?)", ops[0].Path)
	}
}

func newResolverOrFail(t *testing.T, version string) *OverrideResolver {
	t.Helper()
	r, err := NewOverrideResolver(version)
	if err != nil {
		t.Fatalf("NewOverrideResolver(%q): %v", version, err)
	}
	return r
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
