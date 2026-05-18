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

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
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
// ReconcileApp reads device state via getAppObservation (which calls
// GetAppOperationalData).  These tests validate the status updates
// without a real device by using a nil client: since ReconcileApp
// reads state first, and getAppObservation returns an empty observation
// when the client is nil, the reconciler enters the "no oper data" path.
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

func makeOperDataWithPkgPolicy(state string, policy E_Cisco_IOS_XEAppHostingOper_IoxPkgPolicy) *Cisco_IOS_XEAppHostingOper_AppHostingOperData_App {
	operData := makeOperData(state)
	operData.PkgPolicy = policy
	return operData
}

func newReconcilePkgPolicyTestDriver(t *testing.T, allowUnsignedApps bool, operData *Cisco_IOS_XEAppHostingOper_AppHostingOperData_App, notification string) *XEDriver {
	t.Helper()
	const appID = "app1"
	fc := &fakeNetworkClient{
		getHook: func(path string, result any) error {
			root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if !ok {
				t.Fatalf("unexpected GET result type %T", result)
			}

			switch path {
			case "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app":
				root.App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
					appID: operData,
				}
			case "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app-notifications":
				if notification == "" {
					return nil
				}
				name := "install-note-1"
				appName := appID
				msg := notification
				root.AppNotifications = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_AppNotifications{
					name: {
						Name:    &name,
						AppId:   &appName,
						Message: &msg,
					},
				}
			default:
				t.Fatalf("unexpected GET path %q", path)
			}
			return nil
		},
	}
	return &XEDriver{
		config: &v1alpha1.DeviceSpec{
			Address:           "10.0.0.1",
			AllowUnsignedApps: allowUnsignedApps,
		},
		client: fc,
	}
}

func newRunningAppConfig(appID string) *AppHostingConfig {
	return &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: appID},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: "app.tar"},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}
}

// TestReconcileApp_RunningDesiredRunning_IsReady verifies that an app already
// in RUNNING state with desired=Running is marked Ready with no RPCs issued.
// (We can't easily inject a fake getAppObservation here without a mock client, so
// this test validates the "no oper data + no image" error path instead.)
func TestReconcileApp_NoOperDataNoImage_Error(t *testing.T) {
	d := &XEDriver{}
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: ""},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}
	// nil client means getAppObservation returns empty observation (no oper data).
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

func TestReconcileApp_InstallingPkgPolicyInvalidWithoutNotification_Waits(t *testing.T) {
	d := newReconcilePkgPolicyTestDriver(t, false,
		makeOperDataWithPkgPolicy("INSTALLING", Cisco_IOS_XEAppHostingOper_IoxPkgPolicy_iox_pkg_policy_invalid),
		"")
	appCfg := newRunningAppConfig("app1")

	d.ReconcileApp(testCtx(), appCfg)

	if appCfg.Status.Phase != AppPhaseConverging {
		t.Errorf("expected phase Converging, got %s", appCfg.Status.Phase)
	}
	if appCfg.Status.Message != "Install in progress, waiting" {
		t.Errorf("expected install-wait message, got %q", appCfg.Status.Message)
	}
}

func TestReconcileApp_InstallingPkgPolicyInvalidWithNotification_Fails(t *testing.T) {
	d := newReconcilePkgPolicyTestDriver(t, false,
		makeOperDataWithPkgPolicy("INSTALLING", Cisco_IOS_XEAppHostingOper_IoxPkgPolicy_iox_pkg_policy_invalid),
		"signature validation failed")
	appCfg := newRunningAppConfig("app1")

	d.ReconcileApp(testCtx(), appCfg)

	if appCfg.Status.Phase != AppPhaseError {
		t.Errorf("expected phase Error, got %s", appCfg.Status.Phase)
	}
	if appCfg.Status.Message != "install blocked: signature validation failed" {
		t.Errorf("expected install-blocked message, got %q", appCfg.Status.Message)
	}
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
// getAppObservation
// ─────────────────────────────────────────────────────────────────────────────

func TestGetAppObservation_NilClient(t *testing.T) {
	d := &XEDriver{} // nil client
	obs := d.getAppObservation(testCtx(), "app1")
	if obs.State != "" {
		t.Errorf("expected empty state with nil client, got %q", obs.State)
	}
	if obs.PkgPolicy != Cisco_IOS_XEAppHostingOper_IoxPkgPolicy_UNSET {
		t.Errorf("expected UNSET pkg policy with nil client, got %v", obs.PkgPolicy)
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
