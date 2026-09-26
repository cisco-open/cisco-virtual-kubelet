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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	siruplogrus "github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	vklogrus "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
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

func TestReconcileApp_InvalidObservationFailsClosed(t *testing.T) {
	// A failed observation must not be interpreted as proof that the app is
	// absent, regardless of whether an install image is available.
	d := &XEDriver{} // nil client
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: "nginx:latest"},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}

	d.ReconcileApp(testCtx(), appCfg)
	if appCfg.Status.Phase != AppPhaseError {
		t.Fatalf("phase=%s, want %s", appCfg.Status.Phase, AppPhaseError)
	}
	if appCfg.Status.Message != "unable to observe app state" {
		t.Fatalf("message=%q, want observation failure", appCfg.Status.Message)
	}
}

func TestReconcileAppDoesNotExposeImageURLCredentials(t *testing.T) {
	const (
		username = "reconcile-user-sentinel"
		password = "reconcile-password-sentinel"
		token    = "reconcile-query-sentinel"
	)
	imagePath := "https://" + username + ":" + password + "@registry.example.com/app.tar?token=" + token
	payloadObserved := false
	d := &XEDriver{client: &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if !ok {
				t.Fatalf("unexpected GET result type %T", result)
			}
			root.App = nil
			return nil
		},
		postHook: func(_ string, payload any) error {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal install payload: %v", err)
			}
			payloadObserved = strings.Contains(string(encoded), username) &&
				strings.Contains(string(encoded), password) && strings.Contains(string(encoded), token)
			return errors.New("device rejected install")
		},
	}}
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateRunning, ImagePath: imagePath},
		Status:   AppHostingStatus{Phase: AppPhaseConverging},
	}

	var logs bytes.Buffer
	backend := siruplogrus.New()
	backend.SetOutput(&logs)
	backend.SetLevel(siruplogrus.DebugLevel)
	ctx := log.WithLogger(context.Background(), vklogrus.FromLogrus(siruplogrus.NewEntry(backend)))
	d.ReconcileApp(ctx, appCfg)
	if !payloadObserved {
		t.Fatal("reconcile test did not observe original image URL in device request payload")
	}
	combined := logs.String() + "\n" + appCfg.Status.Message
	for _, secret := range []string{username, password, token} {
		if strings.Contains(combined, secret) {
			t.Fatalf("reconcile logs/status exposed %q: %s", secret, combined)
		}
	}
}

func TestReconcileApp_DeletedDesired_NoOperData_AttemptsConfigDelete(t *testing.T) {
	deletes := 0
	d := &XEDriver{client: &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData).App = nil
			return nil
		},
		deleteHook: func(string) error { deletes++; return nil },
	}}
	appCfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateDeleted},
		Status:   AppHostingStatus{Phase: AppPhaseDeleting},
	}

	d.ReconcileApp(testCtx(), appCfg)
	if deletes != 1 || appCfg.Status.Phase != AppPhaseDeleted {
		t.Fatalf("deletes=%d phase=%s, want one delete and Deleted", deletes, appCfg.Status.Phase)
	}
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
	const notificationSentinel = "PACKAGE_POLICY_NOTIFICATION_SECRET_DO_NOT_EXPOSE"
	d := newReconcilePkgPolicyTestDriver(t, false,
		makeOperDataWithPkgPolicy("INSTALLING", Cisco_IOS_XEAppHostingOper_IoxPkgPolicy_iox_pkg_policy_invalid),
		"signature validation failed: https://user:password@device.local/?token="+notificationSentinel)
	appCfg := newRunningAppConfig("app1")

	var logs bytes.Buffer
	backend := siruplogrus.New()
	backend.SetOutput(&logs)
	backend.SetLevel(siruplogrus.DebugLevel)
	ctx := log.WithLogger(context.Background(), vklogrus.FromLogrus(siruplogrus.NewEntry(backend)))
	d.ReconcileApp(ctx, appCfg)

	if appCfg.Status.Phase != AppPhaseError {
		t.Errorf("expected phase Error, got %s", appCfg.Status.Phase)
	}
	if appCfg.Status.Message != "install blocked by device package policy; inspect the device locally for details" {
		t.Errorf("expected install-blocked message, got %q", appCfg.Status.Message)
	}
	if combined := logs.String() + "\n" + appCfg.Status.Message; strings.Contains(combined, notificationSentinel) || strings.Contains(combined, "user:password") {
		t.Fatalf("package-policy notification leaked into logs/status: %s", combined)
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

func TestReconcileAppDoesNotReplayAcceptedAsyncOperation(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		desired   AppDesiredState
		operation string
		image     string
	}{
		{name: "install", desired: AppDesiredStateRunning, operation: "install", image: "flash:/app.tar"},
		{name: "activate", state: "DEPLOYED", desired: AppDesiredStateRunning, operation: "activate"},
		{name: "start", state: "ACTIVATED", desired: AppDesiredStateRunning, operation: "start"},
		{name: "stop", state: "RUNNING", desired: AppDesiredStateDeleted, operation: "stop"},
		{name: "deactivate", state: "STOPPED", desired: AppDesiredStateDeleted, operation: "deactivate"},
		{name: "uninstall", state: "DEPLOYED", desired: AppDesiredStateDeleted, operation: "uninstall"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			posts := 0
			d := &XEDriver{client: &fakeNetworkClient{
				getHook: func(_ string, result any) error {
					root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
					if tc.state != "" {
						root.App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{"app1": makeOperData(tc.state)}
					}
					return nil
				},
				postHook: func(_ string, payload any) error {
					request := payload.(map[string]interface{})
					if request[tc.operation] == nil {
						t.Fatalf("request=%#v, want %s", request, tc.operation)
					}
					posts++
					return nil
				},
			}}
			cfg := &AppHostingConfig{
				Metadata: AppHostingMetadata{AppName: "app1"},
				Spec: AppHostingSpec{
					DesiredState: tc.desired,
					ImagePath:    tc.image,
				},
				Status: AppHostingStatus{Phase: AppPhaseConverging},
			}

			d.ReconcileApp(testCtx(), cfg)
			d.ReconcileApp(testCtx(), cfg)

			if posts != 1 {
				t.Fatalf("%s POST count=%d, want 1 while oper state is unchanged", tc.operation, posts)
			}
			wantPresent := tc.state != ""
			if cfg.Status.PendingOperation != tc.operation || cfg.Status.PendingState != tc.state || cfg.Status.PendingPresent != wantPresent {
				t.Fatalf("pending operation/state/present=%q/%q/%v, want %q/%q/%v", cfg.Status.PendingOperation, cfg.Status.PendingState, cfg.Status.PendingPresent, tc.operation, tc.state, wantPresent)
			}
		})
	}
}

func TestReconcileAppTreatsAmbiguousRPCResultAsPending(t *testing.T) {
	posts := 0
	d := &XEDriver{client: &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData).App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{"app1": makeOperData("DEPLOYED")}
			return nil
		},
		postWithResultHook: func(string, any, any) error {
			posts++
			return &common.RESTCONFMutationAmbiguousError{}
		},
	}}
	cfg := newRunningAppConfig("app1")
	d.ReconcileApp(testCtx(), cfg)
	d.ReconcileApp(testCtx(), cfg)
	if posts != 1 || cfg.Status.Phase != AppPhaseConverging || cfg.Status.PendingOperation != "activate" {
		t.Fatalf("posts=%d phase=%s pending=%q, want 1/Converging/activate", posts, cfg.Status.Phase, cfg.Status.PendingOperation)
	}
}

func TestReconcileAppRetainsPendingOperationWhenObservationFails(t *testing.T) {
	tests := []struct {
		name    string
		desired AppDesiredState
		pending string
		state   string
	}{
		{name: "activate", desired: AppDesiredStateRunning, pending: "activate", state: "DEPLOYED"},
		{name: "uninstall", desired: AppDesiredStateDeleted, pending: "uninstall", state: "DEPLOYED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutations := 0
			d := &XEDriver{client: &fakeNetworkClient{
				getHook:    func(string, any) error { return errors.New("temporary observation failure") },
				postHook:   func(string, any) error { mutations++; return nil },
				deleteHook: func(string) error { mutations++; return nil },
			}}
			cfg := &AppHostingConfig{
				Metadata: AppHostingMetadata{AppName: "app1"},
				Spec:     AppHostingSpec{DesiredState: tc.desired, ImagePath: "flash:/app.tar"},
				Status: AppHostingStatus{
					Phase:            AppPhaseConverging,
					PendingOperation: tc.pending,
					PendingState:     tc.state,
					PendingPresent:   true,
				},
			}
			d.ReconcileApp(testCtx(), cfg)
			if mutations != 0 || cfg.Status.PendingOperation != tc.pending {
				t.Fatalf("mutations=%d pending=%q, want no mutation and retained %q", mutations, cfg.Status.PendingOperation, tc.pending)
			}
		})
	}
}

func TestReconcileAppPendingOperationAdvancesOnlyAfterObservedTransition(t *testing.T) {
	state := "DEPLOYED"
	var posts []string
	d := &XEDriver{client: &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData).App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{"app1": makeOperData(state)}
			return nil
		},
		postHook: func(_ string, payload any) error {
			for _, operation := range []string{"activate", "start"} {
				if payload.(map[string]interface{})[operation] != nil {
					posts = append(posts, operation)
				}
			}
			return nil
		},
	}}
	cfg := newRunningAppConfig("app1")
	d.ReconcileApp(testCtx(), cfg)
	state = "ACTIVATED"
	d.ReconcileApp(testCtx(), cfg)
	d.ReconcileApp(testCtx(), cfg)
	if got := strings.Join(posts, ","); got != "activate,start" {
		t.Fatalf("operations=%q, want activate,start exactly once each", got)
	}
}

func TestReconcileAppPendingUninstallRequiresObservedAbsenceBeforeConfigDelete(t *testing.T) {
	present := true
	posts, deletes := 0, 0
	d := &XEDriver{client: &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if present {
				root.App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{"app1": makeOperData("DEPLOYED")}
			}
			return nil
		},
		postHook:   func(string, any) error { posts++; return nil },
		deleteHook: func(string) error { deletes++; return nil },
	}}
	cfg := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: "app1"},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateDeleted},
		Status:   AppHostingStatus{Phase: AppPhaseDeleting},
	}
	d.ReconcileApp(testCtx(), cfg)
	present = false
	d.ReconcileApp(testCtx(), cfg)
	if posts != 1 || deletes != 1 || cfg.Status.Phase != AppPhaseDeleted {
		t.Fatalf("posts=%d deletes=%d phase=%s, want 1/1/Deleted", posts, deletes, cfg.Status.Phase)
	}
}

func TestDeleteAppStopsAfterExplicitLifecycleResultFailure(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		operation string
	}{
		{name: "stop", state: "RUNNING", operation: "stop"},
		{name: "deactivate", state: "STOPPED", operation: "deactivate"},
		{name: "uninstall", state: "DEPLOYED", operation: "uninstall"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			posts, deletes := 0, 0
			d := &XEDriver{client: &fakeNetworkClient{
				getHook: func(_ string, result any) error {
					result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData).App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{"app1": makeOperData(tc.state)}
					return nil
				},
				postWithResultHook: func(_ string, payload, result any) error {
					input := payload.(map[string]any)["Cisco-IOS-XE-rpc:app-hosting"].(map[string]any)
					if input[tc.operation] == nil {
						t.Fatalf("request=%#v, want %s", input, tc.operation)
					}
					posts++
					response := result.(*appHostingRPCOutput)
					response.Result = "No action is taken"
					response.Present = true
					return nil
				},
				deleteHook: func(string) error { deletes++; return nil },
			}}

			err := d.DeleteApp(testCtx(), "app1")
			if err == nil || posts != 1 || deletes != 0 {
				t.Fatalf("DeleteApp error=%v posts=%d deletes=%d, want error/1/0", err, posts, deletes)
			}
		})
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
