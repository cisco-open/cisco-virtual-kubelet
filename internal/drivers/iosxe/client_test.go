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
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	siruplogrus "github.com/sirupsen/logrus"
	vklog "github.com/virtual-kubelet/virtual-kubelet/log"
	vklogrus "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/record"
)

// ── Test infrastructure ───────────────────────────────────────────────────────

type fakeNetworkClient struct {
	mu                 sync.Mutex
	postHook           func(path string, payload any) error
	postWithResultHook func(path string, payload, result any) error
	getHook            func(path string, result any) error
	patchHook          func(path string, payload any) error
	putHook            func(path string, payload any) error
	deleteHook         func(path string) error
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

func (f *fakeNetworkClient) PostWithResult(_ context.Context, path string, payload, result any,
	_ func(any) ([]byte, error), _ func([]byte, any) error,
) error {
	f.mu.Lock()
	resultHook := f.postWithResultHook
	postHook := f.postHook
	f.mu.Unlock()
	if resultHook != nil {
		return resultHook(path, payload, result)
	}
	// Existing lifecycle tests reason about the RPC input choice. Keep that
	// convenience while dedicated wire-contract tests inspect the outer root.
	if outer, ok := payload.(map[string]any); ok {
		if inner, exists := outer["Cisco-IOS-XE-rpc:app-hosting"]; exists {
			payload = inner
		}
	}
	if postHook != nil {
		return postHook(path, payload)
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

func TestImageReferenceForLogRedactsURLCredentials(t *testing.T) {
	const raw = "https://registry-user:registry-password@registry.example.com/apps/hello.tar?token=signed-secret#fragment"
	got := imageReferenceForLog(raw)
	if got != "https://registry.example.com/apps/hello.tar" {
		t.Fatalf("imageReferenceForLog()=%q", got)
	}
	for _, secret := range []string{"registry-user", "registry-password", "signed-secret", "fragment"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted image reference contains %q: %q", secret, got)
		}
	}
	if got := imageReferenceForLog("flash:/hello.tar"); got != "flash:/hello.tar" {
		t.Fatalf("local image reference=%q, want unchanged", got)
	}
	if got := imageReferenceForLog("FTP://registry-user:registry-password@registry.example.com/apps/hello.tar?token=signed-secret#fragment"); got != "ftp://registry.example.com/apps/hello.tar" {
		t.Fatalf("non-HTTP URL reference=%q, want credentials and query removed", got)
	}
	if got := imageReferenceForLog("ftp:registry-user:registry-password@registry.example.com/apps/hello.tar?token=signed-secret"); got != "<redacted-image-reference>" {
		t.Fatalf("opaque credentialed URL reference=%q, want placeholder", got)
	}
	if got := imageReferenceForLog("flash:/hello.tar?token=signed-secret#fragment"); got != "flash:/hello.tar" {
		t.Fatalf("local reference query redaction=%q", got)
	}
	for _, unsafe := range []string{
		"flash:/hello.tar\nlevel=error msg=forged",
		"flash:/hello.tar\rforged",
		"flash:/hello.tar\x1b[31m",
	} {
		if got := imageReferenceForLog(unsafe); got != "<redacted-image-reference>" {
			t.Fatalf("control-bearing image reference=%q, want placeholder", got)
		}
	}
}

func TestRESTCONFDataExistsRequiresConflictStatus(t *testing.T) {
	serviceUnavailable := &common.RESTCONFError{
		StatusCode: 503,
		Status:     "503 Service Unavailable",
		ErrorTags:  []string{"data-exists"},
	}
	if isRESTCONFDataExists(serviceUnavailable) {
		t.Fatal("503 data-exists error was treated as an idempotent conflict")
	}
	conflict := &common.RESTCONFError{
		StatusCode: 409,
		Status:     "409 Conflict",
		ErrorTags:  []string{"data-exists"},
	}
	if !isRESTCONFDataExists(conflict) {
		t.Fatal("409 data-exists error was not recognized")
	}
}

func TestCopyRPCDoesNotExposeImagePullSecretCredentials(t *testing.T) {
	const (
		username   = "registry-user-sentinel"
		password   = "registry-password-sentinel"
		queryToken = "signed-query-sentinel"
		destToken  = "destination-log-injection-sentinel"
	)
	source := "https://" + username + ":" + password + "@registry.example.com/apps/hello.tar?token=" + queryToken
	destination := "flash:/hello.tar\r" + destToken
	payloadObserved := false
	driver := &XEDriver{client: &fakeNetworkClient{postHook: func(_ string, payload any) error {
		outer, ok := payload.(map[string]interface{})
		if !ok {
			t.Fatalf("copy payload type=%T", payload)
		}
		copyInput, ok := outer["Cisco-IOS-XE-rpc:copy"].(map[string]string)
		if !ok {
			t.Fatalf("copy payload body=%#v", outer)
		}
		payloadObserved = copyInput["source-drop-node-name"] == source &&
			copyInput["destination-drop-node-name"] == destination
		return errors.New("device rejected copy")
	}}}

	var logs bytes.Buffer
	backend := siruplogrus.New()
	backend.SetOutput(&logs)
	backend.SetLevel(siruplogrus.DebugLevel)
	ctx := vklog.WithLogger(context.Background(), vklogrus.FromLogrus(siruplogrus.NewEntry(backend)))
	err := driver.copyRPC(ctx, source, destination)
	if err == nil {
		t.Fatal("copyRPC succeeded, want error")
	}
	if !payloadObserved {
		t.Fatal("copyRPC test did not observe credentialed source in the device request payload")
	}
	combined := err.Error() + "\n" + logs.String()
	for _, secret := range []string{username, password, queryToken, destToken} {
		if strings.Contains(combined, secret) {
			t.Fatalf("copyRPC error/logs exposed %q: %s", secret, combined)
		}
	}
}

func TestCreateAppHostingAppPullingEventRedactsImageCredentials(t *testing.T) {
	const (
		username = "event-user-sentinel"
		password = "event-password-sentinel"
		token    = "event-query-sentinel"
	)
	image := "https://" + username + ":" + password + "@registry.example.com/apps/hello.tar?token=" + token
	driver := newTestDriver(&fakeNetworkClient{getHook: func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
		if !ok {
			t.Fatalf("unexpected GET result type %T", result)
		}
		*root = *operResponse("test-app", "RUNNING")
		return nil
	}})
	recorder := record.NewFakeRecorder(10)
	driver.SetEventRecorder(recorder)
	appConfig := minimalAppConfig(image, v1.PullAlways, time.Second)
	appConfig.Metadata.Pod = &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}

	if err := driver.CreateAppHostingApp(context.Background(), appConfig); err != nil {
		t.Fatalf("CreateAppHostingApp: %v", err)
	}
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "Pulling image https://registry.example.com/apps/hello.tar") {
			t.Fatalf("unexpected Pulling event: %q", event)
		}
		for _, secret := range []string{username, password, token} {
			if strings.Contains(event, secret) {
				t.Fatalf("Pulling event exposed %q: %q", secret, event)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Pulling event")
	}
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

func TestAppHostingRPCWireContractAndResult(t *testing.T) {
	fc := &fakeNetworkClient{postWithResultHook: func(path string, payload, result any) error {
		if path != appHostingRPCPath {
			t.Fatalf("path=%q, want %q", path, appHostingRPCPath)
		}
		outer, ok := payload.(map[string]any)
		if !ok || len(outer) != 1 {
			t.Fatalf("payload=%#v, want one namespaced RPC root", payload)
		}
		input, ok := outer["Cisco-IOS-XE-rpc:app-hosting"].(map[string]any)
		if !ok {
			t.Fatalf("payload=%#v, missing app-hosting RPC root", payload)
		}
		activate, ok := input["activate"].(map[string]string)
		if !ok || activate["appid"] != "test-app" {
			t.Fatalf("activate input=%#v", input["activate"])
		}
		response := result.(*appHostingRPCOutput)
		response.Result = "test-app activated successfullyCurrent state is: ACTIVATED"
		response.Present = true
		return nil
	}}

	if err := newTestDriver(fc).ActivateApp(context.Background(), "test-app"); err != nil {
		t.Fatalf("ActivateApp: %v", err)
	}
}

func TestAppHostingRPCExplicitRejectionOmitsDeviceResult(t *testing.T) {
	const sentinel = "rpc-result-secret-sentinel"
	fc := &fakeNetworkClient{postWithResultHook: func(_ string, _, result any) error {
		response := result.(*appHostingRPCOutput)
		response.Result = "% Error: rejected " + sentinel
		response.Present = true
		return nil
	}}
	err := newTestDriver(fc).ActivateApp(context.Background(), "test-app")
	if err == nil {
		t.Fatal("ActivateApp succeeded after explicit device rejection")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error exposed device result: %v", err)
	}
}

func TestAppHostingRPCDoesNotClassifyEchoedPathAsFailure(t *testing.T) {
	fc := &fakeNetworkClient{postWithResultHook: func(_ string, _, result any) error {
		response := result.(*appHostingRPCOutput)
		response.Result = "flash:/failure-analysis.tar installed successfully"
		response.Present = true
		return nil
	}}
	if err := newTestDriver(fc).InstallApp(context.Background(), "test-app", "flash:/failure-analysis.tar"); err != nil {
		t.Fatalf("InstallApp rejected a successful result containing an operator-controlled path: %v", err)
	}
}

func TestAppHostingRPCRejectedUsesTokenBoundaries(t *testing.T) {
	tests := []struct {
		result string
		want   bool
	}{
		{result: "% Error: rejected", want: true},
		{result: "error: rejected", want: true},
		{result: "error rejected", want: true},
		{result: "failed: rejected", want: true},
		{result: "failed rejected", want: true},
		{result: "errorless completion", want: false},
		{result: "failed-over application started", want: false},
		{result: "flash:/failure-analysis.tar installed successfully", want: false},
	}
	for _, tc := range tests {
		if got := appHostingRPCRejected(tc.result); got != tc.want {
			t.Errorf("appHostingRPCRejected(%q)=%v, want %v", tc.result, got, tc.want)
		}
	}
}

func TestDecodeAppHostingRPCOutput(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		present bool
		wantErr bool
	}{
		{name: "no content", body: "", present: false},
		{name: "canonical", body: `{"Cisco-IOS-XE-rpc:output":{"result":" accepted ","future":"value"}}`, want: "accepted", present: true},
		{name: "unexpected sibling", body: `{"Cisco-IOS-XE-rpc:output":{"result":"accepted"},"unexpected":{}}`, wantErr: true},
		{name: "malformed", body: `{`, wantErr: true},
		{name: "wrong envelope", body: `{"output":{"result":"accepted"}}`, wantErr: true},
		{name: "missing result", body: `{"Cisco-IOS-XE-rpc:output":{}}`, wantErr: true},
		{name: "null result", body: `{"Cisco-IOS-XE-rpc:output":{"result":null}}`, wantErr: true},
		{name: "numeric result", body: `{"Cisco-IOS-XE-rpc:output":{"result":3}}`, wantErr: true},
		{name: "blank result", body: `{"Cisco-IOS-XE-rpc:output":{"result":"  "}}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got appHostingRPCOutput
			err := decodeAppHostingRPCOutput([]byte(tc.body), &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
			if got.Result != tc.want || got.Present != tc.present {
				t.Fatalf("output=%#v, want result=%q present=%v", got, tc.want, tc.present)
			}
		})
	}
}

func TestActivateAndStartWaitsForActivatedBeforeStart(t *testing.T) {
	state := "DEPLOYED"
	activateRequested := false
	activatedObserved := false
	var order []string

	fc := &fakeNetworkClient{}
	fc.postHook = func(_ string, payload any) error {
		request := payload.(map[string]interface{})
		switch {
		case request["activate"] != nil:
			order = append(order, "activate")
			activateRequested = true
		case request["start"] != nil:
			if !activatedObserved {
				return errors.New("start sent before ACTIVATED was observed")
			}
			order = append(order, "start")
			state = "RUNNING"
		}
		return nil
	}
	fc.getHook = func(_ string, result any) error {
		if activateRequested && state == "DEPLOYED" {
			state = "ACTIVATED"
		}
		if state == "ACTIVATED" {
			activatedObserved = true
		}
		order = append(order, "observe:"+state)
		*result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData) = *operResponse("test-app", state)
		return nil
	}

	d := newTestDriver(fc)
	if err := d.activateAndStart(context.Background(), minimalAppConfig("flash:app.tar", v1.PullIfNotPresent, time.Second), time.Second); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(order, ","), "activate,observe:ACTIVATED,start,observe:RUNNING"; got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
}

func TestActivateAndStartDoesNotStartBeforeActivated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	startCalls := 0
	fc := &fakeNetworkClient{
		postHook: func(_ string, payload any) error {
			if request := payload.(map[string]interface{}); request["start"] != nil {
				startCalls++
			}
			return nil
		},
		getHook: func(_ string, result any) error {
			*result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData) = *operResponse("test-app", "DEPLOYED")
			cancel()
			return nil
		},
	}

	d := newTestDriver(fc)
	err := d.activateAndStart(ctx, minimalAppConfig("flash:app.tar", v1.PullIfNotPresent, time.Second), time.Second)
	if err == nil || !strings.Contains(err.Error(), "did not reach ACTIVATED") {
		t.Fatalf("activateAndStart error = %v, want ACTIVATED wait failure", err)
	}
	if startCalls != 0 {
		t.Fatalf("start calls = %d, want 0", startCalls)
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
	// stage 2 → ACTIVATED (after ActivateApp)
	// stage 3 → RUNNING (after StartApp)
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
			// Distinguish install RPC (stays stage 1) from activate/start.
			m, ok := payload.(map[string]interface{})
			if ok {
				if _, isActivate := m["activate"]; isActivate {
					stage = 2
				}
				if _, isStart := m["start"]; isStart {
					stage = 3
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
			*root = *operResponse("test-app", "ACTIVATED")
		case 3:
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
	if finalStage < 3 {
		t.Errorf("expected to reach stage 3 (copy + activate/start), got stage %d", finalStage)
	}
	if d.isPodRecovering("test-uid") {
		t.Error("recovering flag should be cleared after successful fallback")
	}
}

func TestCopyFallbackDoesNotDisruptAcceptedCachedInstall(t *testing.T) {
	const cfgPath = "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"
	cacheSubmitted := false
	installs, copies, deletes := 0, 0, 0
	fc := &fakeNetworkClient{
		getHook: func(_ string, result any) error {
			root := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if cacheSubmitted {
				*root = *operResponse("test-app", "INSTALLING")
			}
			return nil
		},
		postHook: func(path string, payload any) error {
			switch path {
			case appHostingRPCPath:
				if payload.(map[string]interface{})["install"] != nil {
					installs++
					cacheSubmitted = true
				}
			case "/restconf/operations/Cisco-IOS-XE-rpc:copy":
				copies++
			}
			return nil
		},
		deleteHook: func(string) error { deletes++; return nil },
	}
	d := newTestDriver(fc)
	cfg := minimalDockerResourceConfig("https://registry.example/app.tar", v1.PullIfNotPresent, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := d.copyFallbackToFlash(ctx, cfg, cfgPath, v1.PullIfNotPresent, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "refusing destructive fallback") {
		t.Fatalf("error=%v, want accepted-install convergence failure", err)
	}
	if installs != 1 || copies != 0 || deletes != 1 {
		t.Fatalf("installs=%d copies=%d deletes=%d, want 1/0/1 (initial cleanup only)", installs, copies, deletes)
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
	// DockerResource + flash path: wait DEPLOYED → ActivateApp → wait ACTIVATED → StartApp → RUNNING
	var (
		mu        sync.Mutex
		rpcOrder  []string
		activated bool
		started   bool
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
		a, s := activated, started
		mu.Unlock()
		if !a {
			*root = *operResponse("test-app", "DEPLOYED")
		} else if !s {
			*root = *operResponse("test-app", "ACTIVATED")
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

func TestCreateAppHostingApp_ConfigAlreadyExistsFailsClosedWithoutValidState(t *testing.T) {
	tests := []struct {
		name    string
		getHook func(string, any) error
	}{
		{
			name:    "observation error",
			getHook: func(string, any) error { return errors.New("temporary oper-data failure") },
		},
		{
			name: "unsupported present state",
			getHook: func(_ string, result any) error {
				result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData).App = map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
					"test-app": makeOperData("UNKNOWN"),
				}
				return nil
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lifecyclePosts := 0
			fc := &fakeNetworkClient{
				getHook: tc.getHook,
				postHook: func(path string, _ any) error {
					if path == "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps" {
						return &common.RESTCONFError{StatusCode: http.StatusConflict, Status: "409 Conflict", ErrorTags: []string{"data-exists"}}
					}
					lifecyclePosts++
					return nil
				},
			}
			d := newTestDriver(fc)
			cfg := minimalDockerResourceConfig("flash:app.tar", v1.PullIfNotPresent, 200*time.Millisecond)
			if err := d.CreateAppHostingApp(context.Background(), cfg); err == nil {
				t.Fatal("CreateAppHostingApp succeeded without a safe existing-app observation")
			}
			if lifecyclePosts != 0 {
				t.Fatalf("lifecycle POST count=%d, want 0", lifecyclePosts)
			}
		})
	}
}

func TestCreateAppHostingApp_DockerResource_HTTPPrimarySuccess(t *testing.T) {
	// DockerResource + HTTP: device pull succeeds → DEPLOYED → ActivateApp → wait ACTIVATED → StartApp → RUNNING
	var (
		mu        sync.Mutex
		rpcOrder  []string
		activated bool
		started   bool
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
		a, s := activated, started
		mu.Unlock()
		if !a {
			*root = *operResponse("test-app", "DEPLOYED")
		} else if !s {
			*root = *operResponse("test-app", "ACTIVATED")
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
	// DockerResource + HTTP: device pull times out → copy fallback → ActivateApp → ACTIVATED → StartApp → RUNNING
	var (
		mu    sync.Mutex
		stage int // 0=empty, 1=DEPLOYED (after copy), 2=ACTIVATED, 3=RUNNING
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
					stage = 3
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
			*root = *operResponse("test-app", "ACTIVATED")
		case 3:
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
	if finalStage < 3 {
		t.Errorf("expected stage 3, got %d", finalStage)
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
		activateCount, startCount := 0, 0
		for _, op := range rpcOrder {
			if op == "activate" {
				activateCount++
			} else if op == "start" {
				startCount++
			}
		}
		mu.Unlock()
		if activateCount == 0 {
			*root = *operResponse("test-app", "DEPLOYED")
		} else if startCount == 0 {
			*root = *operResponse("test-app", "ACTIVATED")
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
