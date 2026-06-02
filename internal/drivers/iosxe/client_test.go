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
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openconfig/ygot/ygot"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

// ── Test infrastructure ───────────────────────────────────────────────────────

type fakeNetworkClient struct {
	mu         sync.Mutex
	postHook   func(path string, payload any) error
	getHook    func(path string, result any) error
	patchHook  func(path string, payload any) error
	putHook    func(path string, payload any) error
	deleteHook func(path string) error
}

func (f *fakeNetworkClient) Post(_ context.Context, path string, payload any, _ func(any) ([]byte, error)) error {
	f.mu.Lock()
	h := f.postHook
	f.mu.Unlock()
	if h != nil {
		return h(path, payload)
	}
	return nil
}

func (f *fakeNetworkClient) Get(_ context.Context, path string, result any, _ func([]byte, any) error) error {
	f.mu.Lock()
	h := f.getHook
	f.mu.Unlock()
	if h != nil {
		return h(path, result)
	}
	return nil
}

func (f *fakeNetworkClient) Patch(_ context.Context, path string, payload any, _ func(any) ([]byte, error)) error {
	f.mu.Lock()
	h := f.patchHook
	f.mu.Unlock()
	if h != nil {
		return h(path, payload)
	}
	return nil
}

func (f *fakeNetworkClient) Put(_ context.Context, path string, payload any, _ func(any) ([]byte, error)) error {
	f.mu.Lock()
	h := f.putHook
	f.mu.Unlock()
	if h != nil {
		return h(path, payload)
	}
	return nil
}

func (f *fakeNetworkClient) Delete(_ context.Context, path string) error {
	f.mu.Lock()
	h := f.deleteHook
	f.mu.Unlock()
	if h != nil {
		return h(path)
	}
	return nil
}

type fakeSecretNamespaceLister struct {
	secrets map[string]*v1.Secret
}

func (f *fakeSecretNamespaceLister) List(_ labels.Selector) ([]*v1.Secret, error) {
	out := make([]*v1.Secret, 0, len(f.secrets))
	for _, s := range f.secrets {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeSecretNamespaceLister) Get(name string) (*v1.Secret, error) {
	s, ok := f.secrets[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return s, nil
}

// fakeSecretNamespaceLister implements corev1listers.SecretNamespaceLister
var _ corev1listers.SecretNamespaceLister = (*fakeSecretNamespaceLister)(nil)

// operResponse builds a minimal oper-data struct with the given app name and state.
func operResponse(appName, state string) *Cisco_IOS_XEAppHostingOper_AppHostingOperData {
	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{
		App: map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
			appName: {
				Name: ygot.String(appName),
				Details: &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_Details{
					State: ygot.String(state),
				},
			},
		},
	}
	return root
}

// minimalAppConfig returns a stripped-down AppHostingConfig suitable for tests.
func minimalAppConfig(imagePath string, policy v1.PullPolicy, timeout time.Duration) *AppHostingConfig {
	appName := "test-app"
	apps := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{}
	gapp, _ := apps.NewApp(appName)
	trueVal := true
	gapp.Start = &trueVal

	return &AppHostingConfig{
		Metadata: AppHostingMetadata{
			AppName:       appName,
			ContainerName: "ctr",
			PodName:       "test-pod",
			PodNamespace:  "default",
			PodUID:        "test-uid",
		},
		Spec: AppHostingSpec{
			ImagePath:       imagePath,
			DesiredState:    AppDesiredStateRunning,
			DeviceConfig:    apps,
			ImagePullPolicy: policy,
			PackageTimeout:  timeout,
		},
		Status: AppHostingStatus{Phase: AppPhaseConverging},
	}
}

// newTestDriver returns a driver wired up to the given fake client.
func newTestDriver(fc *fakeNetworkClient) *XEDriver {
	return &XEDriver{
		client:         fc,
		recoveringPods: make(map[string]bool),
	}
}

// ── Auth unit tests ───────────────────────────────────────────────────────────

func TestAuthFromSecret_Token(t *testing.T) {
	secret := &v1.Secret{Data: map[string][]byte{"token": []byte("mytoken")}}
	auth, err := authFromSecret(secret)
	if err != nil || auth == nil || auth.Token != "mytoken" {
		t.Errorf("expected token=mytoken, got %+v, err=%v", auth, err)
	}
}

func TestAuthFromSecret_DockerConfigJSON_UsernamePassword(t *testing.T) {
	dcj := map[string]any{
		"auths": map[string]any{
			"registry.example.com": map[string]any{
				"username": "user",
				"password": "pass",
			},
		},
	}
	raw, _ := json.Marshal(dcj)
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "reg"},
		Data:       map[string][]byte{".dockerconfigjson": raw},
	}
	auth, err := authFromSecret(secret)
	if err != nil || auth == nil || auth.Username != "user" || auth.Password != "pass" {
		t.Errorf("expected user/pass, got %+v, err=%v", auth, err)
	}
}

func TestAuthFromSecret_DockerConfigJSON_AuthField(t *testing.T) {
	encoded := "dXNlcjpwYXNz" // base64("user:pass")
	dcj := map[string]any{
		"auths": map[string]any{
			"registry.example.com": map[string]any{"auth": encoded},
		},
	}
	raw, _ := json.Marshal(dcj)
	secret := &v1.Secret{Data: map[string][]byte{".dockerconfigjson": raw}}
	auth, err := authFromSecret(secret)
	if err != nil || auth == nil || auth.Username != "user" || auth.Password != "pass" {
		t.Errorf("expected user/pass from auth field, got %+v, err=%v", auth, err)
	}
}

func TestAuthFromSecret_DockerConfigJSON_IdentityTokenPreferred(t *testing.T) {
	dcj := map[string]any{
		"auths": map[string]any{
			"registry.example.com": map[string]any{
				"username":      "user",
				"password":      "pass",
				"identitytoken": "idtoken",
			},
		},
	}
	raw, _ := json.Marshal(dcj)
	secret := &v1.Secret{Data: map[string][]byte{".dockerconfigjson": raw}}
	auth, err := authFromSecret(secret)
	if err != nil || auth == nil || auth.Token != "idtoken" {
		t.Errorf("expected identity token preferred, got %+v, err=%v", auth, err)
	}
}

func TestRestconfUnmarshallerDecodesIOSXE2601AppHostingOperFields(t *testing.T) {
	d := &XEDriver{}
	var root Cisco_IOS_XEAppHostingOper_AppHostingOperData
	raw := []byte(`{
		"Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data": {
			"app-globals": {
				"iox-enabled": true,
				"iox-version": "26.01",
				"iox-dir": "flash:/iox",
				"iox-dockerd-status": "iox-stat-run",
				"iox-caf-health": "ioxcaf-stat-stbl",
				"iox-app-sign-verify": "iox-app-sign-stat-en"
			}
		}
	}`)

	if err := d.getRestconfUnmarshaller()(raw, &root); err != nil {
		t.Fatalf("unmarshal IOS XE 26.01 app-hosting oper fields: %v", err)
	}
	if root.AppGlobals == nil || root.AppGlobals.IoxEnabled == nil || !*root.AppGlobals.IoxEnabled {
		t.Fatalf("expected known iox-enabled field to be preserved, got %#v", root.AppGlobals)
	}
	if root.AppGlobals.IoxVersion == nil || *root.AppGlobals.IoxVersion != "26.01" {
		got := "<nil>"
		if root.AppGlobals.IoxVersion != nil {
			got = *root.AppGlobals.IoxVersion
		}
		t.Fatalf("IoxVersion=%q, want 26.01", got)
	}
	if root.AppGlobals.IoxDir == nil || *root.AppGlobals.IoxDir != "flash:/iox" {
		got := "<nil>"
		if root.AppGlobals.IoxDir != nil {
			got = *root.AppGlobals.IoxDir
		}
		t.Fatalf("IoxDir=%q, want flash:/iox", got)
	}
	if got := root.AppGlobals.IoxDockerdStatus; got != Cisco_IOS_XEAppHostingOper_IoxRunStatus_iox_stat_run {
		t.Fatalf("IoxDockerdStatus=%v, want iox-stat-run", got)
	}
	if got := root.AppGlobals.IoxCafHealth; got != Cisco_IOS_XEAppHostingOper_IoxHealthStatus_ioxcaf_stat_stbl {
		t.Fatalf("IoxCafHealth=%v, want ioxcaf-stat-stbl", got)
	}
	if got := root.AppGlobals.IoxAppSignVerify; got != Cisco_IOS_XEAppHostingOper_IoxAppSignStatus_iox_app_sign_stat_en {
		t.Fatalf("IoxAppSignVerify=%v, want iox-app-sign-stat-en", got)
	}
}

// ── Install / recovery integration tests ─────────────────────────────────────

func TestCreateAppHostingApp_PrimaryPullSucceeds(t *testing.T) {
	fc := &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if !ok {
				return nil
			}
			*root = *operResponse("test-app", "RUNNING")
			return nil
		},
	}
	d := newTestDriver(fc)
	cfg := minimalAppConfig("http://registry.example.com/app.tar", v1.PullIfNotPresent, 200*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Errorf("expected success on primary pull, got: %v", err)
	}
	if d.isPodRecovering("test-uid") {
		t.Error("pod should not be marked recovering after primary success")
	}
}

func TestCreateAppHostingApp_PolicyNeverNoFallback(t *testing.T) {
	fc := &fakeNetworkClient{
		getHook: func(_ string, result any) error { return nil }, // always empty → timeout
	}
	d := newTestDriver(fc)
	cfg := minimalAppConfig("http://registry.example.com/app.tar", v1.PullNever, 50*time.Millisecond)

	err := d.CreateAppHostingApp(context.Background(), cfg)
	if err == nil {
		t.Error("expected error with PullNever after timeout, got nil")
	}
	if d.isPodRecovering("test-uid") {
		t.Error("pod should not be marked recovering when PullNever")
	}
}

func TestCreateAppHostingApp_FallbackCopyFailure(t *testing.T) {
	copyErr := errors.New("network unreachable")
	fc := &fakeNetworkClient{
		getHook: func(_ string, result any) error { return nil }, // empty → timeout
		postHook: func(path string, _ any) error {
			if path == "/restconf/operations/Cisco-IOS-XE-rpc:copy" {
				return copyErr
			}
			return nil
		},
	}
	d := newTestDriver(fc)
	cfg := minimalAppConfig("http://registry.example.com/app.tar", v1.PullAlways, 50*time.Millisecond)

	err := d.CreateAppHostingApp(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when copy RPC fails")
	}
	if d.isPodRecovering("test-uid") {
		t.Error("recovering flag should be cleared after failure")
	}
}

func TestCreateAppHostingApp_FallbackCopyAfterPrimaryTimeout(t *testing.T) {
	// stage 0 → empty oper data (primary RUNNING wait times out)
	// stage 1 → DEPLOYED (after copy RPC + install; during DEPLOYED wait in copyFallbackToFlash)
	// stage 2 → RUNNING (after ActivateApp/StartApp RPCs)
	var (
		mu    sync.Mutex
		stage int
	)

	copyPath := "/restconf/operations/Cisco-IOS-XE-rpc:copy"
	rpcPath := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	fc := &fakeNetworkClient{}

	fc.postHook = func(path string, payload any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == copyPath {
			stage = 1
		} else if path == rpcPath && stage >= 1 {
			// Distinguish install RPC (stays stage 1) from activate/start (stage 2).
			m, ok := payload.(map[string]interface{})
			if ok {
				if _, isActivate := m["activate"]; isActivate {
					stage = 2
				}
				if _, isStart := m["start"]; isStart {
					stage = 2
				}
			}
		}
		return nil
	}

	fc.getHook = func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			return nil
		}
		mu.Lock()
		s := stage
		mu.Unlock()
		switch s {
		case 1:
			*root = *operResponse("test-app", "DEPLOYED")
		case 2:
			*root = *operResponse("test-app", "RUNNING")
		}
		return nil
	}

	d := newTestDriver(fc)
	cfg := minimalAppConfig("http://registry.example.com/app.tar", v1.PullAlways, 50*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Errorf("expected success via copy fallback, got: %v", err)
	}

	mu.Lock()
	finalStage := stage
	mu.Unlock()
	if finalStage < 2 {
		t.Errorf("expected to reach stage 2 (copy + activate/start), got stage %d", finalStage)
	}
	if d.isPodRecovering("test-uid") {
		t.Error("recovering flag should be cleared after successful fallback")
	}
}

// ── DockerResource (two-phase) tests ─────────────────────────────────────────

func minimalDockerResourceConfig(imagePath string, policy v1.PullPolicy, timeout time.Duration) *AppHostingConfig {
	cfg := minimalAppConfig(imagePath, policy, timeout)
	cfg.Spec.RequiresTwoPhaseStart = true
	gapp := cfg.Spec.DeviceConfig.App["test-app"]
	falseVal := false
	gapp.Start = &falseVal
	return cfg
}

func TestCreateAppHostingApp_DockerResource_FlashImage(t *testing.T) {
	// DockerResource + flash path: wait DEPLOYED → ActivateApp → StartApp → RUNNING
	var (
		mu        sync.Mutex
		rpcOrder  []string
		activated bool
	)
	rpcPath := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	fc := &fakeNetworkClient{}
	fc.postHook = func(path string, payload any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == rpcPath {
			m, ok := payload.(map[string]interface{})
			if ok {
				if _, isActivate := m["activate"]; isActivate {
					rpcOrder = append(rpcOrder, "activate")
					activated = true
				}
				if _, isStart := m["start"]; isStart {
					rpcOrder = append(rpcOrder, "start")
				}
			}
		}
		return nil
	}
	fc.getHook = func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			return nil
		}
		mu.Lock()
		a := activated
		mu.Unlock()
		if !a {
			*root = *operResponse("test-app", "DEPLOYED")
		} else {
			*root = *operResponse("test-app", "RUNNING")
		}
		return nil
	}

	d := newTestDriver(fc)
	cfg := minimalDockerResourceConfig("flash:app.tar", v1.PullIfNotPresent, 200*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Errorf("expected success, got: %v", err)
	}

	mu.Lock()
	order := rpcOrder
	mu.Unlock()
	if len(order) < 2 || order[0] != "activate" || order[1] != "start" {
		t.Errorf("expected RPC order [activate, start], got %v", order)
	}
}

func TestCreateAppHostingApp_ConfigAlreadyExistsActivatedStartsAndWaits(t *testing.T) {
	var (
		mu      sync.Mutex
		started bool
	)
	cfgPath := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"
	rpcPath := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	fc := &fakeNetworkClient{}
	fc.postHook = func(path string, payload any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == cfgPath {
			return errors.New(`request failed with status 409 Conflict: {"ietf-restconf:errors":{"error":[{"error-tag":"data-exists"}]}}`)
		}
		if path == rpcPath {
			if m, ok := payload.(map[string]interface{}); ok {
				if _, isStart := m["start"]; isStart {
					started = true
				}
			}
		}
		return nil
	}
	fc.getHook = func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			return nil
		}
		mu.Lock()
		s := started
		mu.Unlock()
		if s {
			*root = *operResponse("test-app", "RUNNING")
		} else {
			*root = *operResponse("test-app", "ACTIVATED")
		}
		return nil
	}

	d := newTestDriver(fc)
	cfg := minimalDockerResourceConfig("flash:app.tar", v1.PullIfNotPresent, 200*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Fatalf("CreateAppHostingApp: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !started {
		t.Fatal("expected existing ACTIVATED app to be started")
	}
}

func TestCreateAppHostingApp_DockerResource_HTTPPrimarySuccess(t *testing.T) {
	// DockerResource + HTTP: device pull succeeds → DEPLOYED → ActivateApp → StartApp → RUNNING
	var (
		mu        sync.Mutex
		rpcOrder  []string
		activated bool
	)
	rpcPath := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	fc := &fakeNetworkClient{}
	fc.postHook = func(path string, payload any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == rpcPath {
			m, ok := payload.(map[string]interface{})
			if ok {
				if _, isActivate := m["activate"]; isActivate {
					rpcOrder = append(rpcOrder, "activate")
					activated = true
				}
				if _, isStart := m["start"]; isStart {
					rpcOrder = append(rpcOrder, "start")
				}
			}
		}
		return nil
	}
	fc.getHook = func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			return nil
		}
		mu.Lock()
		a := activated
		mu.Unlock()
		if !a {
			*root = *operResponse("test-app", "DEPLOYED")
		} else {
			*root = *operResponse("test-app", "RUNNING")
		}
		return nil
	}

	d := newTestDriver(fc)
	cfg := minimalDockerResourceConfig("http://registry.example.com/app.tar", v1.PullAlways, 200*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Errorf("expected success, got: %v", err)
	}

	mu.Lock()
	order := rpcOrder
	mu.Unlock()
	if len(order) < 2 || order[0] != "activate" || order[1] != "start" {
		t.Errorf("expected RPC order [activate, start], got %v", order)
	}
}

func TestCreateAppHostingApp_DockerResource_HTTPFallbackCopy(t *testing.T) {
	// DockerResource + HTTP: device pull times out → copy fallback → ActivateApp + StartApp → RUNNING
	var (
		mu    sync.Mutex
		stage int // 0=empty, 1=DEPLOYED (after copy), 2=RUNNING (after activate/start)
	)

	copyPath := "/restconf/operations/Cisco-IOS-XE-rpc:copy"
	rpcPath := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	fc := &fakeNetworkClient{}
	fc.postHook = func(path string, payload any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == copyPath {
			stage = 1
		} else if path == rpcPath && stage >= 1 {
			m, ok := payload.(map[string]interface{})
			if ok {
				if _, isActivate := m["activate"]; isActivate {
					stage = 2
				}
				if _, isStart := m["start"]; isStart {
					stage = 2
				}
			}
		}
		return nil
	}
	fc.getHook = func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			return nil
		}
		mu.Lock()
		s := stage
		mu.Unlock()
		switch s {
		case 1:
			*root = *operResponse("test-app", "DEPLOYED")
		case 2:
			*root = *operResponse("test-app", "RUNNING")
		}
		return nil
	}

	d := newTestDriver(fc)
	cfg := minimalDockerResourceConfig("http://registry.example.com/app.tar", v1.PullAlways, 50*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Errorf("expected success via DockerResource copy fallback, got: %v", err)
	}

	mu.Lock()
	finalStage := stage
	mu.Unlock()
	if finalStage < 2 {
		t.Errorf("expected stage 2, got %d", finalStage)
	}
	if d.isPodRecovering("test-uid") {
		t.Error("recovering flag should be cleared")
	}
}

func TestCreateAppHostingApp_DockerResource_MultiContainer(t *testing.T) {
	// Verifies two containers in a pod both go through activate→start
	var (
		mu       sync.Mutex
		rpcOrder []string
	)
	rpcPath := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	fc := &fakeNetworkClient{}
	fc.postHook = func(path string, payload any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == rpcPath {
			m, ok := payload.(map[string]interface{})
			if ok {
				if _, isActivate := m["activate"]; isActivate {
					rpcOrder = append(rpcOrder, "activate")
				}
				if _, isStart := m["start"]; isStart {
					rpcOrder = append(rpcOrder, "start")
				}
			}
		}
		return nil
	}
	fc.getHook = func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			return nil
		}
		mu.Lock()
		activateCount := 0
		for _, op := range rpcOrder {
			if op == "activate" {
				activateCount++
			}
		}
		mu.Unlock()
		if activateCount == 0 {
			*root = *operResponse("test-app", "DEPLOYED")
		} else {
			*root = *operResponse("test-app", "RUNNING")
		}
		return nil
	}

	d := newTestDriver(fc)

	cfg1 := minimalDockerResourceConfig("flash:app1.tar", v1.PullIfNotPresent, 200*time.Millisecond)
	cfg2 := minimalDockerResourceConfig("flash:app2.tar", v1.PullIfNotPresent, 200*time.Millisecond)

	if err := d.CreateAppHostingApp(context.Background(), cfg1); err != nil {
		t.Fatalf("container 1: %v", err)
	}

	mu.Lock()
	rpcOrder = nil
	mu.Unlock()

	if err := d.CreateAppHostingApp(context.Background(), cfg2); err != nil {
		t.Fatalf("container 2: %v", err)
	}

	mu.Lock()
	order := rpcOrder
	mu.Unlock()
	if len(order) < 2 || order[0] != "activate" || order[1] != "start" {
		t.Errorf("container 2: expected RPC order [activate, start], got %v", order)
	}
}
