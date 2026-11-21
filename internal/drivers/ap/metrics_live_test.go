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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestLiveAPMetricsExporter runs the ARM64 metrics package on a real AP using
// the native CAF driver. The package must contain its test package_config.ini.
// Set CVK_AP_METRICS_LIVE_TEST=1, CVK_AP_ADDRESS, CVK_AP_USERNAME,
// CVK_AP_PASSWORD, and CVK_AP_METRICS_PACKAGE to opt in. The package must
// contain direct AP SSH settings; WLC credentials are neither used nor needed.
// Set CVK_AP_METRICS_KEEP=1 to leave the deployed app running for inspection,
// including when the metrics check fails. Set CVK_AP_METRICS_REPLACE=1 to
// replace only the exact existing app named by CVK_AP_METRICS_REPLACE_APP_ID.
func TestLiveAPMetricsExporter(t *testing.T) {
	if os.Getenv("CVK_AP_METRICS_LIVE_TEST") != "1" {
		t.Skip("set CVK_AP_METRICS_LIVE_TEST=1 to run the AP metrics hardware test")
	}
	address := strings.TrimSpace(os.Getenv("CVK_AP_ADDRESS"))
	username := os.Getenv("CVK_AP_USERNAME")
	password := os.Getenv("CVK_AP_PASSWORD")
	packagePath := strings.TrimSpace(os.Getenv("CVK_AP_METRICS_PACKAGE"))
	if address == "" || username == "" || password == "" || packagePath == "" {
		t.Fatal("CVK_AP_ADDRESS, CVK_AP_USERNAME, CVK_AP_PASSWORD, and CVK_AP_METRICS_PACKAGE are required")
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
	replacementID := strings.TrimSpace(os.Getenv("CVK_AP_METRICS_REPLACE_APP_ID"))
	if os.Getenv("CVK_AP_METRICS_REPLACE") == "1" {
		if err := validateIOxAppID(replacementID); err != nil {
			t.Fatal("CVK_AP_METRICS_REPLACE requires a valid exact CVK_AP_METRICS_REPLACE_APP_ID")
		}
	}
	initialAppIDs := make(map[string]bool, len(initialApps))
	for _, app := range initialApps {
		if os.Getenv("CVK_AP_METRICS_REPLACE") == "1" &&
			app.ID == replacementID {
			if err := driver.removeApp(ctx, app.ID, app.State); err != nil {
				t.Fatalf("remove existing metrics app for replacement: %v", err)
			}
			t.Logf("removed existing metrics app %s for replacement", app.ID)
			continue
		}
		initialAppIDs[app.ID] = true
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cvk-ap-metrics-smoke",
			Namespace: "default",
			UID:       types.UID(uuid.NewString()),
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "metrics",
			Image: packageServer.URL + "/ap-metrics.tar",
		}}},
	}
	testAppIDs := map[string]bool{common.GetAppHostingName(pod, 0): true}
	for id := range testAppIDs {
		if initialAppIDs[id] {
			t.Fatalf("test application ID %s already exists", id)
		}
	}
	keepApp := os.Getenv("CVK_AP_METRICS_KEEP") == "1"
	t.Cleanup(func() {
		if keepApp {
			t.Log("leaving AP metrics app installed for manual inspection")
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		apps, err := driver.client.listApps(cleanupCtx)
		if err != nil {
			t.Errorf("read AP application inventory during cleanup: %v", err)
			return
		}
		for _, app := range apps {
			if !testAppIDs[app.ID] {
				continue
			}
			if err := driver.removeApp(cleanupCtx, app.ID, app.State); err != nil {
				t.Errorf("remove test-created IOx app %s during cleanup: %v", app.ID, err)
			}
		}
	})

	if err := driver.DeployPod(ctx, pod, nil, nil); err != nil {
		t.Fatalf("deploy AP metrics app: %v", err)
	}
	status, err := driver.GetPodStatus(ctx, pod)
	if err != nil {
		t.Fatalf("read AP metrics app status: %v", err)
	}
	if status.Status.Phase != corev1.PodRunning || len(status.Status.ContainerStatuses) != 1 || !status.Status.ContainerStatuses[0].Ready {
		t.Fatalf("AP metrics app is not ready/running: %#v", status.Status)
	}
	apps, err := driver.client.listApps(ctx)
	if err != nil {
		t.Fatalf("inspect AP metrics app network: %v", err)
	}
	for _, app := range apps {
		if testAppIDs[app.ID] {
			t.Logf("AP metrics app %s state=%s network=%s", app.ID, app.State, string(app.NetworkInfo))
		}
	}
	endpoint := (&url.URL{Scheme: "http", Host: net.JoinHostPort(address, "9100"), Path: "/metrics"}).String()
	var body string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if reqErr != nil {
			t.Fatalf("create metrics request: %v", reqErr)
		}
		resp, requestErr := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if requestErr == nil {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
			resp.Body.Close()
			if readErr != nil {
				t.Fatalf("read metrics response: %v", readErr)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("metrics endpoint returned HTTP %d: %s", resp.StatusCode, data)
			}
			body = string(data)
			if strings.Contains(body, "ap_exporter_scrape_success 1") &&
				strings.Contains(body, "cisco_ap_cpu_usage_percent") &&
				strings.Contains(body, "cisco_ap_memory_usage_percent") &&
				strings.Contains(body, "cisco_ap_clients_associated") &&
				strings.Contains(body, "cisco_ap_radio_frequency_mhz") &&
				strings.Contains(body, "cisco_ap_radio_rx_packets_total") {
				break
			}
		} else {
			t.Logf("waiting for AP metrics endpoint: %v", requestErr)
		}
		time.Sleep(3 * time.Second)
	}
	if !strings.Contains(body, "ap_exporter_scrape_success 1") {
		t.Fatalf("direct AP scrape did not succeed; response: %s", body)
	}
	for _, metric := range []string{
		"cisco_ap_cpu_usage_percent",
		"cisco_ap_memory_usage_percent",
		"cisco_ap_clients_associated",
		"cisco_ap_radio_frequency_mhz",
		"cisco_ap_radio_rx_packets_total",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics response is missing %q: %s", metric, body)
		}
	}
	t.Logf("AP metrics endpoint %s returned direct AP CPU, memory, client, and radio metrics", endpoint)

	if os.Getenv("CVK_AP_METRICS_KEEP") == "1" {
		keepApp = true
		t.Log("AP metrics app left running for manual inspection")
		return
	}
	if err := driver.DeletePod(ctx, pod); err != nil {
		t.Fatalf("uninstall AP metrics app: %v", err)
	}
	apps, err = driver.client.listApps(ctx)
	if err != nil {
		t.Fatalf("verify AP metrics app uninstall: %v", err)
	}
	for _, app := range apps {
		if testAppIDs[app.ID] {
			t.Fatalf("test-created IOx app %s remains installed after cleanup", app.ID)
		}
	}
	t.Log("AP metrics app uninstalled; no test-created apps remain")
}
