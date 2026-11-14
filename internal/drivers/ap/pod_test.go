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
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHelloAppLifecycleThroughNativeCAF(t *testing.T) {
	packageData := helloPackageTar(t)
	packageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(packageData)
	}))
	defer packageServer.Close()

	states := map[string]string{}
	var calls []string
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "ap-test" || password != "ap-test-password" {
			t.Errorf("request did not use configured basic auth")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		prefix := ioxAPIPrefix + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected API path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		resource := strings.TrimPrefix(r.URL.Path, prefix)
		if resource == "apps" && r.Method == http.MethodGet {
			apps := make([]appInfo, 0, len(states))
			for id, state := range states {
				apps = append(apps, appInfo{ID: id, State: state})
			}
			writeJSON(t, w, apps)
			return
		}
		parts := strings.Split(resource, "/")
		if len(parts) < 2 || parts[0] != "apps" {
			http.NotFound(w, r)
			return
		}
		id, err := url.PathUnescape(parts[1])
		if err != nil {
			t.Errorf("invalid escaped app ID: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(parts) == 2 {
			switch r.Method {
			case http.MethodGet:
				state, found := states[id]
				if !found {
					http.NotFound(w, r)
					return
				}
				writeJSON(t, w, appInfo{ID: id, State: state})
			case http.MethodPost:
				reader, err := r.MultipartReader()
				if err != nil {
					t.Errorf("install request was not multipart: %v", err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				part, err := reader.NextPart()
				if err != nil || part.FormName() != "file" || part.FileName() != "hello.tar" {
					t.Errorf("install upload has unexpected multipart fields")
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				upload, err := io.ReadAll(part)
				if err != nil || !bytes.Equal(upload, packageData) {
					t.Errorf("install upload did not contain the downloaded IOx package")
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				states[id] = "DEPLOYED"
				calls = append(calls, "install")
				w.WriteHeader(http.StatusCreated)
			case http.MethodDelete:
				delete(states, id)
				calls = append(calls, "uninstall")
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if len(parts) != 3 || parts[2] != "state" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		action := r.URL.Query().Get("action")
		switch action {
		case "activate":
			var payload struct {
				Resources struct {
					Network []struct {
						InterfaceName string `json:"interface-name"`
						NetworkName   string `json:"network-name"`
					} `json:"network"`
				} `json:"resources"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Resources.Network) != 1 ||
				payload.Resources.Network[0].InterfaceName != "eth0" || payload.Resources.Network[0].NetworkName != "iox-nat0" {
				t.Errorf("activation request did not include the expected hello-app network mapping")
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			states[id] = "ACTIVATED"
		case "start":
			states[id] = "RUNNING"
		case "stop":
			states[id] = "STOPPED"
		case "deactivate":
			states[id] = "DEPLOYED"
		default:
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		calls = append(calls, action)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer apiServer.Close()

	client := &ioxClient{
		baseURL:  apiServer.URL + ioxAPIPrefix,
		username: "ap-test",
		password: "ap-test-password",
		http:     apiServer.Client(),
	}
	driver := &Driver{config: &v1alpha1.DeviceSpec{Address: "127.0.0.1"}, client: client}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hello-app",
			Namespace: "default",
			UID:       "123e4567-e89b-12d3-a456-426614174000",
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "hello",
			Image: packageServer.URL + "/hello.tar",
		}}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := driver.DeployPod(ctx, pod, nil, nil); err != nil {
		t.Fatalf("deploy hello app: %v", err)
	}
	id := common.GenerateContainerAppIDs(pod)["hello"]
	if states[id] != "RUNNING" {
		t.Fatalf("app state = %q, want RUNNING", states[id])
	}

	status, err := driver.GetPodStatus(ctx, pod)
	if err != nil {
		t.Fatalf("get hello app status: %v", err)
	}
	if status.Status.Phase != corev1.PodRunning || len(status.Status.ContainerStatuses) != 1 || !status.Status.ContainerStatuses[0].Ready {
		t.Fatalf("hello app status = %#v, want one ready running container", status.Status)
	}

	listed, err := driver.ListPods(ctx)
	if err != nil {
		t.Fatalf("list hello app: %v", err)
	}
	if len(listed) != 1 || string(listed[0].UID) != string(pod.UID) {
		t.Fatalf("listed pods = %#v, want the hello app UID %s", listed, pod.UID)
	}

	if err := driver.DeletePod(ctx, pod); err != nil {
		t.Fatalf("delete hello app: %v", err)
	}
	if _, found := states[id]; found {
		t.Fatalf("application %s still exists after uninstall", id)
	}
	wantCalls := []string{"install", "activate", "start", "stop", "deactivate", "uninstall"}
	if fmt.Sprint(calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("CAF lifecycle calls = %v, want %v", calls, wantCalls)
	}
}

func helloPackageTar(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	descriptor := []byte("descriptor-schema-version: \"2.10\"\ninfo:\n  name: Hello-App\n")
	if err := w.WriteHeader(&tar.Header{Name: "package.yaml", Mode: 0o600, Size: int64(len(descriptor))}); err != nil {
		t.Fatalf("write hello package header: %v", err)
	}
	if _, err := w.Write(descriptor); err != nil {
		t.Fatalf("write hello package descriptor: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("finish hello package tar: %v", err)
	}
	return data.Bytes()
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("write JSON response: %v", err)
	}
}
