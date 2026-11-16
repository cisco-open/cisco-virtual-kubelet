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

package ap

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestLiveAPHelloApp runs the actual sample package against a reachable AP.
// Set CVK_AP_LIVE_TEST=1, CVK_AP_ADDRESS, CVK_AP_USERNAME,
// CVK_AP_PASSWORD, and CVK_AP_PACKAGE to opt in. It installs the app, checks
// its IOx state and HTTP endpoint on port 8000, then uninstalls it.
func TestLiveAPHelloApp(t *testing.T) {
	if os.Getenv("CVK_AP_LIVE_TEST") != "1" {
		t.Skip("set CVK_AP_LIVE_TEST=1 to run the AP hardware smoke test")
	}
	address := strings.TrimSpace(os.Getenv("CVK_AP_ADDRESS"))
	username := os.Getenv("CVK_AP_USERNAME")
	password := os.Getenv("CVK_AP_PASSWORD")
	packagePath := strings.TrimSpace(os.Getenv("CVK_AP_PACKAGE"))
	if address == "" || username == "" || password == "" || packagePath == "" {
		t.Fatal("CVK_AP_ADDRESS, CVK_AP_USERNAME, CVK_AP_PASSWORD, and CVK_AP_PACKAGE are required")
	}
	packageData, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatalf("read IOx package: %v", err)
	}
	packageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(packageData)
	}))
	defer packageServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	driver, err := NewDriver(ctx, &v1alpha1.DeviceSpec{
		Driver:   v1alpha1.DeviceDriverAP,
		Address:  address,
		Port:     8443,
		Username: username,
		Password: password,
		TLS:      &v1alpha1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
	})
	if err != nil {
		t.Fatalf("connect to AP: %v", err)
	}
	initialApps, err := driver.client.listApps(ctx)
	if err != nil {
		t.Fatalf("read initial AP application inventory: %v", err)
	}
	initialAppIDs := make(map[string]bool, len(initialApps))
	for _, app := range initialApps {
		initialAppIDs[app.ID] = true
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cvk-hello-app-smoke",
			Namespace: "default",
			UID:       types.UID(uuid.NewString()),
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "hello",
			Image: packageServer.URL + "/package.tar",
		}}},
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		if err := driver.DeletePod(cleanupCtx, pod); err != nil {
			t.Errorf("uninstall hello app during cleanup: %v", err)
		}
		apps, err := driver.client.listApps(cleanupCtx)
		if err != nil {
			t.Errorf("read AP application inventory during cleanup: %v", err)
			return
		}
		for _, app := range apps {
			if initialAppIDs[app.ID] {
				continue
			}
			if err := driver.removeApp(cleanupCtx, app.ID, app.State); err != nil {
				t.Errorf("remove test-created IOx app %s during cleanup: %v", app.ID, err)
			}
		}
	})
	if err := driver.DeployPod(ctx, pod, nil, nil); err != nil {
		apps, listErr := driver.client.listApps(ctx)
		if listErr != nil {
			t.Logf("inspect AP application inventory after failed deploy: %v", listErr)
		} else {
			t.Logf("AP application inventory after failed deploy: %+v", apps)
		}
		t.Fatalf("deploy hello app: %v", err)
	}
	status, err := driver.GetPodStatus(ctx, pod)
	if err != nil {
		t.Fatalf("read hello app status: %v", err)
	}
	if status.Status.Phase != corev1.PodRunning || len(status.Status.ContainerStatuses) != 1 || !status.Status.ContainerStatuses[0].Ready {
		t.Fatalf("hello app status is not ready/running: %#v", status.Status)
	}
	apps, err := driver.client.listApps(ctx)
	if err != nil {
		t.Fatalf("inspect running hello app: %v", err)
	}
	for _, app := range apps {
		if !initialAppIDs[app.ID] {
			t.Logf("running AP app %s state=%s network=%s", app.ID, app.State, string(app.NetworkInfo))
		}
	}

	endpoint := (&url.URL{Scheme: "http", Host: net.JoinHostPort(address, "8000"), Path: "/"}).String()
	var resp *http.Response
	var requestErr error
	startupDeadline := time.Now().Add(20 * time.Second)
	for {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if reqErr != nil {
			t.Fatalf("create hello app HTTP request: %v", reqErr)
		}
		resp, requestErr = (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if requestErr == nil || time.Now().After(startupDeadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if requestErr != nil {
		t.Fatalf("request hello app endpoint %s: %v", endpoint, requestErr)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("read hello app response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("hello app endpoint returned HTTP %d: %s", resp.StatusCode, responseBody)
	}
	if !strings.Contains(string(responseBody), "Hello Cisco Live!") {
		t.Fatalf("hello app response did not contain its expected greeting: %s", responseBody)
	}
	t.Logf("hello app is RUNNING and %s returned HTTP %d (%d response bytes)", endpoint, resp.StatusCode, len(responseBody))
	if err := driver.DeletePod(ctx, pod); err != nil {
		t.Fatalf("uninstall hello app: %v", err)
	}
	apps, err = driver.client.listApps(ctx)
	if err != nil {
		t.Fatalf("verify hello app uninstall: %v", err)
	}
	for _, app := range apps {
		if !initialAppIDs[app.ID] {
			t.Fatalf("test-created IOx app %s remains installed after cleanup", app.ID)
		}
	}
	t.Log("hello app uninstalled; no test-created apps remain")
}
