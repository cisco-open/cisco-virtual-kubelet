package iosxe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

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

var _ corev1listers.SecretNamespaceLister = (*fakeSecretNamespaceLister)(nil)

type fakeNetworkClient struct {
	postHook   func(path string, payload any) error
	getHook    func(path string, result any) error
	deleteHook func(path string) error
}

func (f *fakeNetworkClient) Get(ctx context.Context, path string, result any, unmarshal func([]byte, any) error) error {
	if f.getHook != nil {
		return f.getHook(path, result)
	}
	return nil
}

func (f *fakeNetworkClient) Post(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	if f.postHook != nil {
		return f.postHook(path, payload)
	}
	return nil
}

func (f *fakeNetworkClient) Patch(ctx context.Context, path string, payload any, marshal func(any) ([]byte, error)) error {
	return nil
}

func (f *fakeNetworkClient) Delete(ctx context.Context, path string) error {
	if f.deleteHook != nil {
		return f.deleteHook(path)
	}
	return nil
}

func TestAuthFromSecret_Token(t *testing.T) {
	sec := &v1.Secret{Data: map[string][]byte{"token": []byte("abc")}}
	a, err := authFromSecret(sec)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if a == nil || a.Token != "abc" {
		t.Fatalf("expected token auth, got %#v", a)
	}
}

func TestAuthFromSecret_DockerConfigJSON_UsernamePassword(t *testing.T) {
	cfg := map[string]any{
		"auths": map[string]any{
			"example.com": map[string]any{
				"username": "u",
				"password": "p",
			},
		},
	}
	b, _ := json.Marshal(cfg)
	sec := &v1.Secret{Data: map[string][]byte{".dockerconfigjson": b}}
	a, err := authFromSecret(sec)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if a == nil || a.Username != "u" || a.Password != "p" {
		t.Fatalf("expected basic auth, got %#v", a)
	}
}

func TestAuthFromSecret_DockerConfigJSON_AuthField(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("u:p"))
	cfg := map[string]any{
		"auths": map[string]any{
			"example.com": map[string]any{"auth": auth},
		},
	}
	b, _ := json.Marshal(cfg)
	sec := &v1.Secret{Data: map[string][]byte{".dockerconfigjson": b}}
	a, err := authFromSecret(sec)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if a == nil || a.Username != "u" || a.Password != "p" {
		t.Fatalf("expected decoded basic auth, got %#v", a)
	}
}

func TestAuthFromSecret_DockerConfigJSON_IdentityTokenPreferred(t *testing.T) {
	cfg := map[string]any{
		"auths": map[string]any{
			"example.com": map[string]any{"identitytoken": "tok"},
		},
	}
	b, _ := json.Marshal(cfg)
	sec := &v1.Secret{Data: map[string][]byte{".dockerconfigjson": b}}
	a, err := authFromSecret(sec)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if a == nil || a.Token != "tok" {
		t.Fatalf("expected identity token, got %#v", a)
	}
}

func TestInstallWithRecovery_CopySuccessThenInstallDest(t *testing.T) {
	calls := []string{}
	client := &fakeNetworkClient{postHook: func(path string, payload any) error {
		calls = append(calls, path)
		if path == "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting" {
			// first install fails, second succeeds
			if len(calls) == 1 {
				return errors.New("install failed")
			}
			return nil
		}
		if path == "/restconf/operations/Cisco-IOS-XE-rpc:copy" {
			return nil
		}
		return nil
	}}

	d := &XEDriver{client: client, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}
	cfg := AppHostingConfig{AppName: "app1", ImagePath: "https://example.com/app.tar", ImagePullPolicy: "Always"}

	if err := d.installWithRecovery(context.Background(), cfg); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// expect install, copy, install
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(calls), calls)
	}
	if calls[0] != "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting" ||
		calls[1] != "/restconf/operations/Cisco-IOS-XE-rpc:copy" ||
		calls[2] != "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting" {
		t.Fatalf("unexpected call sequence: %v", calls)
	}
}

func TestInstallWithRecovery_CopyFailsThenRetryOriginalFails(t *testing.T) {
	calls := []string{}
	client := &fakeNetworkClient{postHook: func(path string, payload any) error {
		calls = append(calls, path)
		if path == "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting" {
			return errors.New("install failed")
		}
		if path == "/restconf/operations/Cisco-IOS-XE-rpc:copy" {
			return errors.New("copy failed")
		}
		return nil
	}}

	d := &XEDriver{client: client, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}
	cfg := AppHostingConfig{AppName: "app1", ImagePath: "https://example.com/app.tar", ImagePullPolicy: "Always"}

	if err := d.installWithRecovery(context.Background(), cfg); err == nil {
		t.Fatalf("expected error")
	}

	// expect install, copy, install
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(calls), calls)
	}
}

// helper to build oper data with a given app name and state
func makeOperData(appName string, state string) *Cisco_IOS_XEAppHostingOper_AppHostingOperData {
	return &Cisco_IOS_XEAppHostingOper_AppHostingOperData{
		App: map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
			appName: {
				Name: &appName,
				Details: &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_Details{
					State: &state,
				},
			},
		},
	}
}

func TestCreateAppHostingApp_WaitsForRunning(t *testing.T) {
	getCalls := 0
	client := &fakeNetworkClient{
		postHook: func(path string, payload any) error {
			return nil // install RPC succeeds
		},
		getHook: func(path string, result any) error {
			getCalls++
			root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if !ok {
				return nil
			}
			// Simulate state progression: first call DEPLOYING, second ACTIVATED, third RUNNING
			var state string
			switch {
			case getCalls <= 1:
				state = "DEPLOYED"
			case getCalls == 2:
				state = "ACTIVATED"
			default:
				state = "RUNNING"
			}
			oper := makeOperData("testapp", state)
			*root = *oper
			return nil
		},
	}

	d := &XEDriver{client: client, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}
	cfg := AppHostingConfig{
		AppName:        "testapp",
		ContainerName:  "test-container",
		ImagePath:      "http://example.com/app.tar",
		PackageTimeout: 30 * time.Second,
		Apps:           &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{},
	}

	err := d.CreateAppHostingApp(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if getCalls < 3 {
		t.Fatalf("expected at least 3 GET calls for oper polling, got %d", getCalls)
	}
}

func TestCreateAppHostingApp_TimeoutWhenNeverRunning(t *testing.T) {
	client := &fakeNetworkClient{
		postHook: func(path string, payload any) error {
			return nil // install RPC succeeds
		},
		getHook: func(path string, result any) error {
			// Never return any oper data (simulate missing app / image never pulled)
			root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if !ok {
				return nil
			}
			root.App = nil
			return nil
		},
	}

	d := &XEDriver{client: client, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}
	cfg := AppHostingConfig{
		AppName:        "testapp",
		ContainerName:  "test-container",
		ImagePath:      "http://example.com/app.tar",
		PackageTimeout: 5 * time.Second, // short timeout for test speed
		Apps:           &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{},
	}

	err := d.CreateAppHostingApp(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error when app never reaches RUNNING, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		// The error should mention timeout / not reaching RUNNING
		t.Logf("got expected error: %v", err)
	}
}

func TestCreateAppHostingApp_TimeoutWhenStuckAtActivated(t *testing.T) {
	client := &fakeNetworkClient{
		postHook: func(path string, payload any) error {
			return nil
		},
		getHook: func(path string, result any) error {
			root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
			if !ok {
				return nil
			}
			// Always return ACTIVATED, never RUNNING
			oper := makeOperData("testapp", "ACTIVATED")
			*root = *oper
			return nil
		},
	}

	d := &XEDriver{client: client, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}
	cfg := AppHostingConfig{
		AppName:        "testapp",
		ContainerName:  "test-container",
		ImagePath:      "http://example.com/app.tar",
		PackageTimeout: 5 * time.Second,
		Apps:           &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{},
	}

	err := d.CreateAppHostingApp(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error when app stuck at ACTIVATED, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestGetIOSXEAppHostPackageTimeout(t *testing.T) {
	tests := []struct {
		name     string
		ann      map[string]string
		expected time.Duration
	}{
		{
			name:     "no annotations",
			ann:      nil,
			expected: 180 * time.Second,
		},
		{
			name:     "annotation not set",
			ann:      map[string]string{"other": "value"},
			expected: 180 * time.Second,
		},
		{
			name:     "empty value",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: ""},
			expected: 180 * time.Second,
		},
		{
			name:     "go duration 180s",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "180s"},
			expected: 180 * time.Second,
		},
		{
			name:     "go duration 3m",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "3m"},
			expected: 3 * time.Minute,
		},
		{
			name:     "go duration 2m30s",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "2m30s"},
			expected: 2*time.Minute + 30*time.Second,
		},
		{
			name:     "bare integer seconds",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "120"},
			expected: 120 * time.Second,
		},
		{
			name:     "invalid falls back to default",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "abc"},
			expected: 180 * time.Second,
		},
		{
			name:     "below minimum clamped to 10s",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "1s"},
			expected: 10 * time.Second,
		},
		{
			name:     "above maximum clamped to 30m",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "1h"},
			expected: 30 * time.Minute,
		},
		{
			name:     "whitespace trimmed",
			ann:      map[string]string{podAnnotationIOSXEAppHostPackageTimeout: "  60s  "},
			expected: 60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.ann,
				},
			}
			got := getIOSXEAppHostPackageTimeout(pod)
			if got != tt.expected {
				t.Errorf("getIOSXEAppHostPackageTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetIOSXEAppHostPackageTimeout_NilPod(t *testing.T) {
	got := getIOSXEAppHostPackageTimeout(nil)
	if got != 180*time.Second {
		t.Errorf("expected default 180s for nil pod, got %v", got)
	}
}

// makeCfgData builds fake device config data that GetPodContainers can match
// against the given pod. It creates an app with RunOpts labels containing the
// pod name, namespace, UID, and container name.
func makeCfgData(appName, containerName string, pod *v1.Pod) *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData {
	lineIdx := uint16(1)
	labelsLine := fmt.Sprintf("--label %s=%s --label %s=%s --label %s=%s --label %s=%s",
		common.LabelPodName, pod.Name,
		common.LabelPodNamespace, pod.Namespace,
		common.LabelPodUID, pod.UID,
		common.LabelContainerName, containerName,
	)
	return &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData{
		Apps: &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{
			App: map[string]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App{
				appName: {
					ApplicationName: &appName,
					RunOptss: &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{
						RunOpts: map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
							lineIdx: {
								LineIndex:   &lineIdx,
								LineRunOpts: &labelsLine,
							},
						},
					},
				},
			},
		},
	}
}

// testPod creates a pod for use in UpdatePod tests.
func testPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       types.UID("aaaabbbb-cccc-dddd-eeee-ffffffffffff"),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "test-container",
					Image: "http://example.com/app.tar",
				},
			},
		},
	}
}

func TestUpdatePod_NoActionWhenRunning(t *testing.T) {
	pod := testPod()
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")
	appName := fmt.Sprintf("cvk0000_%s", cleanUID)

	cfgData := makeCfgData(appName, "test-container", pod)

	client := &fakeNetworkClient{
		getHook: func(path string, result any) error {
			if strings.Contains(path, "app-hosting-cfg") {
				cfg, ok := result.(*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData)
				if ok {
					*cfg = *cfgData
				}
			}
			if strings.Contains(path, "app-hosting-oper") {
				root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
				if ok {
					oper := makeOperData(appName, "RUNNING")
					*root = *oper
				}
			}
			return nil
		},
	}

	d := &XEDriver{client: client, config: &v1alpha1.DeviceSpec{}, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}

	err := d.UpdatePod(context.Background(), pod)
	if err != nil {
		t.Fatalf("expected no error when app is RUNNING, got: %v", err)
	}
}

func TestUpdatePod_RedeploysWhenNoOperData(t *testing.T) {
	pod := testPod()
	// Set a short package timeout so CreateAppHostingApp wait is fast
	pod.Annotations = map[string]string{
		podAnnotationIOSXEAppHostPackageTimeout: "5s",
	}
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")
	appName := fmt.Sprintf("cvk0000_%s", cleanUID)

	cfgData := makeCfgData(appName, "test-container", pod)
	deleteCount := 0
	postCount := 0

	// Track phases of the UpdatePod lifecycle:
	//   initial: UpdatePod checks oper data, finds none → triggers redeploy
	//   stopping: DeleteApp called StopApp RPC → return ACTIVATED for the wait
	//   deactivating: DeleteApp called DeactivateApp RPC → return DEPLOYED for the wait
	//   uninstalling: DeleteApp called UninstallApp RPC → return no oper for WaitForAppNotPresent
	//   redeploying: CreateAppHostingApp config POST → return RUNNING for the wait
	phase := "initial"

	client := &fakeNetworkClient{
		getHook: func(path string, result any) error {
			if strings.Contains(path, "app-hosting-cfg") {
				cfg, ok := result.(*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData)
				if ok {
					*cfg = *cfgData
				}
			}
			if strings.Contains(path, "app-hosting-oper") {
				root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
				if ok {
					switch phase {
					case "stopping":
						oper := makeOperData(appName, "ACTIVATED")
						*root = *oper
					case "deactivating":
						oper := makeOperData(appName, "DEPLOYED")
						*root = *oper
					case "uninstalling":
						root.App = nil // app gone
					case "redeploying":
						oper := makeOperData(appName, "RUNNING")
						*root = *oper
					default:
						root.App = nil // initial: no oper data
					}
				}
			}
			return nil
		},
		postHook: func(path string, payload any) error {
			postCount++
			// Detect which RPC is being called by inspecting the payload
			if strings.Contains(path, "app-hosting-cfg") {
				// Config POST during CreateAppHostingApp
				phase = "redeploying"
				return nil
			}
			if strings.Contains(path, "Cisco-IOS-XE-rpc:app-hosting") {
				// RPC calls during DeleteApp or install
				payloadMap, ok := payload.(map[string]interface{})
				if ok {
					if _, has := payloadMap["stop"]; has {
						phase = "stopping"
					} else if _, has := payloadMap["deactivate"]; has {
						phase = "deactivating"
					} else if _, has := payloadMap["uninstall"]; has {
						phase = "uninstalling"
					}
				}
			}
			return nil
		},
		deleteHook: func(path string) error {
			deleteCount++
			return nil
		},
	}

	d := &XEDriver{client: client, config: &v1alpha1.DeviceSpec{}, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}

	err := d.UpdatePod(context.Background(), pod)
	if err != nil {
		t.Fatalf("expected successful redeploy, got: %v", err)
	}

	if deleteCount == 0 {
		t.Error("expected at least one delete call to clean up stale app")
	}
	if postCount == 0 {
		t.Error("expected post calls for app redeploy")
	}
}

func TestUpdatePod_RedeploysWhenStuckState(t *testing.T) {
	pod := testPod()
	// Set a short package timeout so CreateAppHostingApp wait is fast
	pod.Annotations = map[string]string{
		podAnnotationIOSXEAppHostPackageTimeout: "5s",
	}
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")
	appName := fmt.Sprintf("cvk0000_%s", cleanUID)

	cfgData := makeCfgData(appName, "test-container", pod)
	phase := "initial"

	client := &fakeNetworkClient{
		getHook: func(path string, result any) error {
			if strings.Contains(path, "app-hosting-cfg") {
				cfg, ok := result.(*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData)
				if ok {
					*cfg = *cfgData
				}
			}
			if strings.Contains(path, "app-hosting-oper") {
				root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData)
				if ok {
					switch phase {
					case "stopping":
						oper := makeOperData(appName, "ACTIVATED")
						*root = *oper
					case "deactivating":
						oper := makeOperData(appName, "DEPLOYED")
						*root = *oper
					case "uninstalling":
						root.App = nil
					case "redeploying":
						oper := makeOperData(appName, "RUNNING")
						*root = *oper
					default:
						// Stuck at DEPLOYED
						oper := makeOperData(appName, "DEPLOYED")
						*root = *oper
					}
				}
			}
			return nil
		},
		postHook: func(path string, payload any) error {
			if strings.Contains(path, "app-hosting-cfg") {
				phase = "redeploying"
				return nil
			}
			if strings.Contains(path, "Cisco-IOS-XE-rpc:app-hosting") {
				payloadMap, ok := payload.(map[string]interface{})
				if ok {
					if _, has := payloadMap["stop"]; has {
						phase = "stopping"
					} else if _, has := payloadMap["deactivate"]; has {
						phase = "deactivating"
					} else if _, has := payloadMap["uninstall"]; has {
						phase = "uninstalling"
					}
				}
			}
			return nil
		},
		deleteHook: func(path string) error {
			return nil
		},
	}

	d := &XEDriver{client: client, config: &v1alpha1.DeviceSpec{}, secretLister: &fakeSecretNamespaceLister{secrets: map[string]*v1.Secret{}}}

	err := d.UpdatePod(context.Background(), pod)
	if err != nil {
		t.Fatalf("expected successful redeploy of stuck app, got: %v", err)
	}
}
