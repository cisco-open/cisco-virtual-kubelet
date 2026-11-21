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
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type apLifecycleTransport func(*http.Request) (*http.Response, error)

func (f apLifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func lifecyclePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "recovery", Namespace: "workloads", UID: "123e4567-e89b-12d3-a456-426614174000"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "https://packages.example/hello.tar"}}},
	}
}

// The mock only advances an action once its resulting state has been read.
// This catches command sequencing that relies solely on an accepted HTTP reply.
func lifecycleDriver(t *testing.T, state *string, actions *[]string) *Driver {
	t.Helper()
	observed := true
	client := &ioxClient{baseURL: "https://ap.example" + ioxAPIPrefix, token: "test-token"}
	client.http = &http.Client{Transport: apLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		code := http.StatusOK
		var body []byte
		if r.Method == http.MethodGet {
			observed = true
			if *state == "" {
				code = http.StatusNotFound
			} else {
				body, _ = json.Marshal(appInfo{State: *state})
			}
		} else {
			if !observed {
				t.Errorf("issued %s before observing the preceding state transition", r.URL.Query().Get("action"))
			}
			observed = false
			if r.Method == http.MethodDelete {
				*actions = append(*actions, "uninstall")
				*state = ""
			} else {
				action := r.URL.Query().Get("action")
				*actions = append(*actions, action)
				switch action {
				case "activate":
					*state = "ACTIVATED"
				case "start":
					*state = "RUNNING"
				case "stop":
					*state = "STOPPED"
				case "deactivate":
					*state = "DEPLOYED"
				default:
					t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
				}
			}
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	return &Driver{config: &v1alpha1.DeviceSpec{Address: "192.0.2.1"}, client: client}
}

func TestDeployPodResumesObservedStage(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  []string
	}{
		{"DEPLOYED", []string{"activate", "start"}},
		{"ACTIVATED", []string{"start"}},
		{"STOPPED", []string{"start"}},
		{"RUNNING", nil},
	} {
		t.Run(tc.state, func(t *testing.T) {
			state := tc.state
			var actions []string
			d := lifecycleDriver(t, &state, &actions)
			if err := d.DeployPod(context.Background(), lifecyclePod(), nil, nil); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actions, tc.want) || state != "RUNNING" {
				t.Fatalf("actions=%v state=%s, want %v RUNNING", actions, state, tc.want)
			}
			if err := d.DeployPod(context.Background(), lifecyclePod(), nil, nil); err != nil || !reflect.DeepEqual(actions, tc.want) {
				t.Fatalf("repeated deploy performed mutations: %v, %v", actions, err)
			}
		})
	}
}

func TestDeletePodObservesEveryTransitionAndIsIdempotent(t *testing.T) {
	state := "RUNNING"
	var actions []string
	d := lifecycleDriver(t, &state, &actions)
	for range 2 {
		if err := d.DeletePod(context.Background(), lifecyclePod()); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"stop", "deactivate", "uninstall"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions=%v, want %v", actions, want)
	}
}

func TestLifecycleRejectsUnknownStateWithoutMutation(t *testing.T) {
	state := "SOMETHING_NEW"
	var actions []string
	d := lifecycleDriver(t, &state, &actions)
	if err := d.DeployPod(context.Background(), lifecyclePod(), nil, nil); err == nil {
		t.Fatal("deploy accepted unknown application state")
	}
	if err := d.DeletePod(context.Background(), lifecyclePod()); err == nil {
		t.Fatal("delete accepted unknown application state")
	}
	if len(actions) != 0 {
		t.Fatalf("mutated unknown state: %v", actions)
	}
}

func TestActivationPayloadValidatedBeforeAnyDeviceRequest(t *testing.T) {
	for _, payload := range []string{`null`, `[]`, `{}`, `{"resources":null}`, `{"resources":{}}`, `{"resources":[]}`, `{"resources":"bad"}`} {
		t.Run(payload, func(t *testing.T) {
			pod := lifecyclePod()
			pod.Annotations = map[string]string{activationAnnotation: payload}
			d := &Driver{}
			// No client: a request before validation would panic.
			if err := d.DeployPod(context.Background(), pod, nil, nil); err == nil {
				t.Fatal("invalid activation payload accepted")
			}
		})
	}
}

func TestUpdateRejectsImageChangeBeforeAnyDeviceRequest(t *testing.T) {
	pod := lifecyclePod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", Image: "https://packages.example/old.tar"}}
	if err := (&Driver{}).UpdatePod(context.Background(), pod); err == nil || !strings.Contains(err.Error(), "replace the Pod") {
		t.Fatalf("image update error=%v", err)
	}
}

func TestPodStatusPreservesTimesAndObservedImage(t *testing.T) {
	state := "RUNNING"
	var actions []string
	d := lifecycleDriver(t, &state, &actions)
	pod := lifecyclePod()
	first, err := d.GetPodStatus(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	first.Spec.Containers[0].Image = "https://packages.example/new.tar"
	second, err := d.GetPodStatus(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Status, second.Status) {
		t.Fatalf("unchanged observation changed status:\n%#v\n%#v", first.Status, second.Status)
	}
	if !d.PodNeedsReconciliation(second) {
		t.Fatal("unsupported desired-image change not surfaced for reconciliation")
	}
	if len(actions) != 0 {
		t.Fatalf("status read mutated device: %v", actions)
	}
}

func TestStoppedAndFailedAppsRequestGuardedRecovery(t *testing.T) {
	for _, value := range []string{"STOPPED", "DEPLOYED", "ACTIVATED", "ERROR", "FAILED", "INVALID"} {
		t.Run(value, func(t *testing.T) {
			state := value
			var actions []string
			d := lifecycleDriver(t, &state, &actions)
			status, err := d.GetPodStatus(context.Background(), lifecyclePod())
			if err != nil || !d.PodNeedsReconciliation(status) {
				t.Fatalf("status=%#v err=%v", status, err)
			}
			if status.Status.ContainerStatuses[0].State.Terminated != nil || len(actions) != 0 {
				t.Fatal("status falsely reported completed termination or mutated device")
			}
			status.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			if d.PodNeedsReconciliation(status) {
				t.Fatal("terminating Pod requested forward recovery")
			}
		})
	}
}

func TestWaitForStateBoundsInflightHTTPRead(t *testing.T) {
	d := &Driver{client: &ioxClient{baseURL: "https://ap.example", token: "token", http: &http.Client{Transport: apLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}}}
	id := common.GenerateContainerAppIDs(lifecyclePod())["app"]
	started := time.Now()
	err := d.waitForState(context.Background(), id, "RUNNING", 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("wait error=%v duration=%s", err, time.Since(started))
	}
}

func TestValidatePackageArchiveRejectsMalformedContent(t *testing.T) {
	good := helloPackageTar(t)
	for _, tc := range []struct {
		name string
		data []byte
		file string
	}{
		{"empty", nil, "app.tar"},
		{"truncated body", good[:520], "app.tar"},
		{"invalid later header", append(append([]byte{}, good[:1024]...), bytes.Repeat([]byte{'x'}, 512)...), "app.tar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePackageArchive(tc.data, tc.file); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	for _, name := range []string{"../escape", "/absolute", `dir\escape`} {
		var data bytes.Buffer
		w := tar.NewWriter(&data)
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1}); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
		_ = w.Close()
		if err := validatePackageArchive(data.Bytes(), "app.tar"); err == nil {
			t.Fatalf("unsafe path %q accepted", name)
		}
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write(good)
	_ = gz.Close()
	validGzip := append([]byte{}, compressed.Bytes()...)
	if err := validatePackageArchive(validGzip, "app.tar.gz"); err != nil {
		t.Fatalf("valid gzip rejected: %v", err)
	}
	validGzip[len(validGzip)-8] ^= 0xff
	if err := validatePackageArchive(validGzip, "app.tar.gz"); err == nil {
		t.Fatal("invalid gzip checksum accepted")
	}
	compressed.Reset()
	gz = gzip.NewWriter(&compressed)
	_, _ = gz.Write(good)
	_, _ = gz.Write(make([]byte, maxPackageSize))
	_ = gz.Close()
	if err := validatePackageArchive(compressed.Bytes(), "app.tar.gz"); err == nil {
		t.Fatal("oversized expanded archive accepted")
	}
}

func TestValidatePackageArchiveRequiresRootDescriptor(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
		size int64
	}{
		{"other.yaml", tar.TypeReg, 1},
		{"nested/package.yaml", tar.TypeReg, 1},
		{"package.yaml", tar.TypeReg, 0},
		{"package.yaml", tar.TypeDir, 0},
	} {
		var data bytes.Buffer
		w := tar.NewWriter(&data)
		if err := w.WriteHeader(&tar.Header{Name: tc.name, Typeflag: tc.kind, Mode: 0600, Size: tc.size}); err != nil {
			t.Fatal(err)
		}
		if tc.size > 0 {
			_, _ = w.Write([]byte("x"))
		}
		_ = w.Close()
		if err := validatePackageArchive(data.Bytes(), "app.tar"); err == nil {
			t.Fatalf("accepted missing descriptor: %#v", tc)
		}
	}
}

func TestInstallWaitToleratesTransientAbsence(t *testing.T) {
	state := ""
	var actions []string
	d := lifecycleDriver(t, &state, &actions)
	id := common.GenerateContainerAppIDs(lifecyclePod())["app"]
	err := d.waitForStateObservation(context.Background(), id, "DEPLOYED", 20*time.Millisecond, true)
	if !errors.Is(err, context.DeadlineExceeded) || isHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("install observation returned an early absence instead of waiting: %v", err)
	}
}

func TestDeletePodRejectsMalformedIdentityBeforeDeviceAccess(t *testing.T) {
	pod := lifecyclePod()
	pod.UID = "arbitrary-app"
	if err := (&Driver{}).DeletePod(context.Background(), pod); err == nil {
		t.Fatal("malformed UID accepted for deletion")
	}
}

func TestDeletePodAcceptsActivatedAfterStop(t *testing.T) {
	state := "RUNNING"
	var actions []string
	d := lifecycleDriver(t, &state, &actions)
	transport := d.client.http.Transport
	d.client.http.Transport = apLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if r.URL.Query().Get("action") == "stop" {
			state = "ACTIVATED"
		}
		return response, err
	})
	if err := d.DeletePod(context.Background(), lifecyclePod()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actions, []string{"stop", "deactivate", "uninstall"}) {
		t.Fatalf("actions=%v", actions)
	}
}

func TestDeployRetryResumesAfterAmbiguousActivationFailure(t *testing.T) {
	state := "DEPLOYED"
	var actions []string
	d := lifecycleDriver(t, &state, &actions)
	transport := d.client.http.Transport
	failed := false
	d.client.http.Transport = apLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if r.URL.Query().Get("action") == "activate" && !failed {
			failed = true
			response.StatusCode = http.StatusInternalServerError
		}
		return response, err
	})
	if err := d.DeployPod(context.Background(), lifecyclePod(), nil, nil); err == nil {
		t.Fatal("failed activation response ignored")
	}
	if err := d.DeployPod(context.Background(), lifecyclePod(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actions, []string{"activate", "start"}) {
		t.Fatalf("retry repeated an accepted action: %v", actions)
	}
}

func TestSparseInventoryCanDeleteRemainingAppAfterRestart(t *testing.T) {
	pod := lifecyclePod()
	id := common.GetAppHostingName(pod, 2)
	exists := true
	var deletes []string
	client := &ioxClient{baseURL: "https://ap.example" + ioxAPIPrefix, token: "token"}
	client.http = &http.Client{Transport: apLifecycleTransport(func(r *http.Request) (*http.Response, error) {
		code := http.StatusOK
		var body []byte
		requested := strings.TrimPrefix(r.URL.Path, ioxAPIPrefix+"/apps/")
		switch {
		case r.URL.Path == ioxAPIPrefix+"/apps":
			body, _ = json.Marshal([]appInfo{{ID: id, State: "DEPLOYED"}})
		case requested != id || !exists:
			code = http.StatusNotFound
		case r.Method == http.MethodDelete:
			deletes = append(deletes, requested)
			exists = false
			code = http.StatusNoContent
		default:
			body, _ = json.Marshal(appInfo{ID: id, State: "DEPLOYED"})
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	d := &Driver{config: &v1alpha1.DeviceSpec{Address: "192.0.2.1"}, client: client}
	pods, err := d.ListPods(context.Background())
	if err != nil || len(pods) != 1 || pods[0].UID != pod.UID {
		t.Fatalf("pods=%#v err=%v", pods, err)
	}
	if err := d.DeletePod(context.Background(), pods[0]); err != nil {
		t.Fatal(err)
	}
	if exists || !reflect.DeepEqual(deletes, []string{id}) {
		t.Fatalf("sparse orphan cleanup=%v, exists=%v", deletes, exists)
	}
}
