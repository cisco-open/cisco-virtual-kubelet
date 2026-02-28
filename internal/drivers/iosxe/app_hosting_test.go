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

package iosxe

import (
	"context"
	"testing"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// testCtx returns a context with a no-op logger so log.G(ctx) works in tests.
func testCtx() context.Context {
	return log.WithLogger(context.Background(), log.L)
}

// ─────────────────────────────────────────────────────────────────────────────
// containerImagePath
// ─────────────────────────────────────────────────────────────────────────────

func TestContainerImagePath_Found(t *testing.T) {
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "sidecar", Image: "busybox:latest"},
				{Name: "app", Image: "myapp:v1"},
			},
		},
	}
	if got := containerImagePath(pod, "app"); got != "myapp:v1" {
		t.Errorf("expected myapp:v1, got %q", got)
	}
}

func TestContainerImagePath_NotFound(t *testing.T) {
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "app", Image: "myapp:v1"},
			},
		},
	}
	if got := containerImagePath(pod, "missing"); got != "" {
		t.Errorf("expected empty string for missing container, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ensureAppRunning
//
// The device auto-advances the app lifecycle via `start: true` in the config.
// ensureAppRunning therefore only acts when there is NO operational data at all
// (silent install failure). Any app that has oper data (regardless of state)
// is left alone.
// ─────────────────────────────────────────────────────────────────────────────

func makeOperData(state string) *Cisco_IOS_XEAppHostingOper_AppHostingOperData_App {
	if state == "" {
		return nil
	}
	s := state
	return &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		Details: &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_Details{
			State: &s,
		},
	}
}

// TestEnsureAppRunning_HasOperDataIsNoop verifies that any app with operational
// data (regardless of state) is left untouched — the device drives the lifecycle.
func TestEnsureAppRunning_HasOperDataIsNoop(t *testing.T) {
	states := []string{"RUNNING", "DEPLOYED", "ACTIVATED", "STOPPED", "Uninstalled", "UNKNOWN_STATE"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			// nil client — any RPC attempt would panic, proving no call is made.
			d := &XEDriver{}
			d.ensureAppRunning(testCtx(), "app1", makeOperData(state), "img")
		})
	}
}

func TestEnsureAppRunning_NoOperDataNoImage(t *testing.T) {
	d := &XEDriver{}
	// No oper data and no image path — should log a warning but not panic or call client.
	d.ensureAppRunning(testCtx(), "app1", nil, "")
}
