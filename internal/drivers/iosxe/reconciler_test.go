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
// ReconcileApp — declarative reconciler tests.
//
// ReconcileApp reads device state via getAppState (which calls
// GetAppOperationalData).  These tests validate the status updates
// without a real device by using a nil client: since ReconcileApp
// reads state first, and getAppState returns "" when the client
// fails, the reconciler enters the "no oper data" path.
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

// TestReconcileApp_RunningDesiredRunning_IsReady verifies that an app already
// in RUNNING state with desired=Running is marked Ready with no RPCs issued.
// (We can't easily inject a fake getAppState here without a mock client, so
// this test validates the "no oper data + no image" error path instead.)
func TestReconcileApp_NoOperDataNoImage_Error(t *testing.T) {
	d := &XEDriver{}
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: ""},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}
	// nil client means getAppState returns "" (no oper data).
	// No image path → should set Phase=Error.
	d.ReconcileApp(testCtx(), appCfg)
	if appCfg.Status.Phase != AppPhaseError {
		t.Errorf("expected phase Error, got %s", appCfg.Status.Phase)
	}
	if appCfg.Status.Message == "" {
		t.Error("expected non-empty error message")
	}
}

func TestReconcileApp_NoOperDataWithImage_AttemptsInstall(t *testing.T) {
	// With no oper data and an image path, ReconcileApp should attempt install.
	// Since we don't have a mock client, we verify the intent by checking that
	// the message indicates a re-install was attempted. We use recover to catch
	// the nil client panic — this confirms the correct code path was entered.
	d := &XEDriver{} // nil client
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: "nginx:latest"},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}

	// The install RPC will panic on nil client — we expect that as proof the
	// correct branch was taken.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from nil client install RPC, but did not panic")
		}
	}()
	d.ReconcileApp(testCtx(), appCfg)
}

func TestReconcileApp_DeletedDesired_NoOperData_AttemptsConfigDelete(t *testing.T) {
	// With DesiredState=Deleted and no oper data, ReconcileApp should attempt
	// to delete the config. Nil client causes a panic, confirming the path.
	d := &XEDriver{} // nil client
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateDeleted},
		Status:   AppHostingStatus{Phase: AppPhaseDeleting},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from nil client config delete, but did not panic")
		}
	}()
	d.ReconcileApp(testCtx(), appCfg)
}

func TestReconcileApp_ObservedStateUpdated(t *testing.T) {
	d := &XEDriver{} // nil client
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: ""},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}
	// No image → takes the error path (no RPC calls), but still sets observed state
	d.ReconcileApp(testCtx(), appCfg)
	// ObservedState should be set (to "" since nil client)
	if appCfg.Status.ObservedState != "" {
		t.Errorf("expected empty observed state with nil client, got %q", appCfg.Status.ObservedState)
	}
	// LastTransition should be set
	if appCfg.Status.LastTransition.IsZero() {
		t.Error("expected LastTransition to be set")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// getAppState
// ─────────────────────────────────────────────────────────────────────────────

func TestGetAppState_NilClient(t *testing.T) {
	d := &XEDriver{} // nil client
	state := d.getAppState(testCtx(), "app1")
	if state != "" {
		t.Errorf("expected empty state with nil client, got %q", state)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// containerImagePath
// ─────────────────────────────────────────────────────────────────────────────

func TestContainerImagePath_EmptyPod(t *testing.T) {
	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{},
		},
	}
	if got := containerImagePath(pod, "any"); got != "" {
		t.Errorf("expected empty string for empty pod, got %q", got)
	}
}
