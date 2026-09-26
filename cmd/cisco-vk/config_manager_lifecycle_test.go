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

package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigManagerLifecycleProbeTransitions(t *testing.T) {
	lifecycle := newConfigManagerLifecycle()
	req := httptest.NewRequest("GET", "http://worker/readyz", nil)

	if err := lifecycle.healthCheck(req); err != nil {
		t.Fatalf("health before cache sync = %v, want healthy startup", err)
	}
	if err := lifecycle.readyCheck(req); err == nil || !strings.Contains(err.Error(), "have not started") {
		t.Fatalf("readiness before cache sync = %v, want not started", err)
	}

	lifecycle.markReady()
	if err := lifecycle.readyCheck(req); err != nil {
		t.Fatalf("readiness after manager election = %v, want ready", err)
	}

	runErr := errors.New("controller failed")
	lifecycle.markStopped(runErr)
	select {
	case <-lifecycle.Done():
	default:
		t.Fatal("manager stop did not close Done")
	}
	if !errors.Is(lifecycle.Err(), runErr) {
		t.Fatalf("manager error = %v, want %v", lifecycle.Err(), runErr)
	}
	if err := lifecycle.healthCheck(req); !errors.Is(err, runErr) {
		t.Fatalf("health after manager failure = %v, want wrapped manager error", err)
	}
	if err := lifecycle.readyCheck(req); !errors.Is(err, runErr) {
		t.Fatalf("readiness after manager failure = %v, want wrapped manager error", err)
	}
}

func TestConfigManagerHealthProbeAddress(t *testing.T) {
	if got := configManagerHealthProbeAddress(nil); got != "0" {
		t.Fatalf("standalone probe address = %q, want disabled", got)
	}
	if got := configManagerHealthProbeAddress(newConfigManagerLifecycle()); got != networkManagerHealthProbeAddress {
		t.Fatalf("managed network probe address = %q, want %q", got, networkManagerHealthProbeAddress)
	}
}

func TestConfigManagerLifecycleStopIsTerminal(t *testing.T) {
	lifecycle := newConfigManagerLifecycle()
	lifecycle.markStopped(nil)
	lifecycle.markReady()
	lifecycle.markStopped(errors.New("late error"))

	if err := lifecycle.readyCheck(httptest.NewRequest("GET", "http://worker/readyz", nil)); err == nil {
		t.Fatal("readiness became healthy after manager stopped")
	}
	if err := lifecycle.Err(); err != nil {
		t.Fatalf("first terminal result was replaced: %v", err)
	}
}

func TestWaitForNetworkManager(t *testing.T) {
	t.Run("manager failure exits worker", func(t *testing.T) {
		lifecycle := newConfigManagerLifecycle()
		runErr := errors.New("cache watch failed")
		lifecycle.markStopped(runErr)

		err := waitForNetworkManager(context.Background(), lifecycle)
		if !errors.Is(err, runErr) {
			t.Fatalf("wait error = %v, want wrapped manager error", err)
		}
	})

	t.Run("clean manager exit before cancellation is failure", func(t *testing.T) {
		lifecycle := newConfigManagerLifecycle()
		lifecycle.markStopped(nil)

		if err := waitForNetworkManager(context.Background(), lifecycle); err == nil || !strings.Contains(err.Error(), "unexpectedly") {
			t.Fatalf("wait error = %v, want unexpected exit", err)
		}
	})

	t.Run("context cancellation is graceful", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := waitForNetworkManager(ctx, newConfigManagerLifecycle()); err != nil {
			t.Fatalf("wait after cancellation = %v, want nil", err)
		}
	})
}
