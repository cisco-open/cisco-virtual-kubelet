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
	mu       sync.Mutex
	postHook func(path string, payload any) error
	getHook  func(path string, result any) error
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

func (f *fakeNetworkClient) Patch(_ context.Context, _ string, _ any, _ func(any) ([]byte, error)) error {
	return nil
}

func (f *fakeNetworkClient) Delete(_ context.Context, _ string) error {
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
	cfg := minimalAppConfig("http://registry.example.com/app.tar", v1.PullIfNotPresent, 50*time.Millisecond)

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
	// stage 1 → DEPLOYED (after copy RPC; during DEPLOYED wait)
	// stage 2 → RUNNING (after Start=true config re-POST; during final RUNNING wait)
	var (
		mu    sync.Mutex
		stage int // 0, 1, 2
	)

	cfgPath := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"
	copyPath := "/restconf/operations/Cisco-IOS-XE-rpc:copy"

	fc := &fakeNetworkClient{}

	fc.postHook = func(path string, _ any) error {
		mu.Lock()
		defer mu.Unlock()
		if path == copyPath {
			stage = 1
		} else if path == cfgPath && stage == 1 {
			// Start=true re-POST after DEPLOYED wait
			stage = 2
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
		// stage 0: leave root empty (device cannot pull)
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
		t.Errorf("expected to reach stage 2 (copy + Start=true), got stage %d", finalStage)
	}
	if d.isPodRecovering("test-uid") {
		t.Error("recovering flag should be cleared after successful fallback")
	}
}

