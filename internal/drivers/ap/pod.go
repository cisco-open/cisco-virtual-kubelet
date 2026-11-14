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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const (
	packageTimeout       = 5 * time.Minute
	packagePoll          = 2 * time.Second
	activationAnnotation = "cisco.io/ap-activation-payload"
)

func activationPayloadFor(pod *corev1.Pod) (json.RawMessage, error) {
	if pod.Annotations != nil {
		if raw := strings.TrimSpace(pod.Annotations[activationAnnotation]); raw != "" {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				return nil, err
			}
			if len(payload) == 0 || payload["resources"] == nil {
				return nil, fmt.Errorf("payload must be a JSON object with a resources field")
			}
			return json.RawMessage(raw), nil
		}
	}
	return json.RawMessage(`{"resources":{"network":[{"interface-name":"eth0","network-name":"iox-nat0","port_map":{"mode":"1to1"}}]}}`), nil
}

func (d *Driver) DeployPod(ctx context.Context, pod *corev1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	if err := d.validatePod(pod); err != nil {
		return err
	}
	ids := common.GenerateContainerAppIDs(pod)
	for _, container := range pod.Spec.Containers {
		id := ids[container.Name]
		if err := validateAppID(id); err != nil {
			return err
		}
		if err := d.deployApp(ctx, pod, container, id); err != nil {
			return fmt.Errorf("AP app-hosting: deploy container %s: %w", container.Name, err)
		}
	}
	return nil
}

func (d *Driver) deployApp(ctx context.Context, pod *corev1.Pod, container corev1.Container, id string) error {
	if existing, err := d.client.app(ctx, id); err == nil {
		if appState(existing.State) == "RUNNING" {
			return nil
		}
		if err := d.removeApp(ctx, id, existing.State); err != nil {
			return fmt.Errorf("remove previous attempt: %w", err)
		}
	} else if !isHTTPStatus(err, http.StatusNotFound) {
		return err
	}
	packageBytes, filename, err := fetchPackage(ctx, container.Image)
	if err != nil {
		return err
	}
	if err := d.client.install(ctx, id, filename, packageBytes); err != nil {
		return fmt.Errorf("install IOx package: %w", err)
	}
	payload, err := activationPayloadFor(pod)
	if err != nil {
		return fmt.Errorf("invalid %s annotation: %w", activationAnnotation, err)
	}
	if err := d.client.action(ctx, id, "activate", payload); err != nil {
		return fmt.Errorf("activate IOx application: %w", err)
	}
	if err := d.client.action(ctx, id, "start", nil); err != nil {
		return fmt.Errorf("start IOx application: %w", err)
	}
	if err := d.waitForState(ctx, id, "RUNNING", packageTimeout); err != nil {
		return err
	}
	return nil
}

func (d *Driver) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	if pod == nil {
		return fmt.Errorf("AP app-hosting: nil pod")
	}
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	if err := d.validatePod(pod); err != nil {
		return err
	}
	ids := common.GenerateContainerAppIDs(pod)
	for _, container := range pod.Spec.Containers {
		id := ids[container.Name]
		app, err := d.client.app(ctx, id)
		if isHTTPStatus(err, http.StatusNotFound) {
			if err := d.deployApp(ctx, pod, container, id); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		switch state := appState(app.State); state {
		case "RUNNING":
			continue
		case "DEPLOYED":
			payload, err := activationPayloadFor(pod)
			if err != nil {
				return fmt.Errorf("invalid %s annotation: %w", activationAnnotation, err)
			}
			if err := d.client.action(ctx, id, "activate", payload); err != nil {
				return err
			}
			fallthrough
		case "ACTIVATED", "STOPPED":
			if err := d.client.action(ctx, id, "start", nil); err != nil {
				return err
			}
			if err := d.waitForState(ctx, id, "RUNNING", packageTimeout); err != nil {
				return err
			}
		default:
			return fmt.Errorf("AP app-hosting: application %s is in unsupported state %q", id, state)
		}
	}
	return nil
}

func (d *Driver) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	if pod == nil {
		return nil
	}
	var errs []error
	for _, id := range common.GenerateContainerAppIDs(pod) {
		app, err := d.client.app(ctx, id)
		if isHTTPStatus(err, http.StatusNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := d.removeApp(ctx, id, app.State); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *Driver) removeApp(ctx context.Context, id, state string) error {
	switch appState(state) {
	case "RUNNING":
		if err := d.client.action(ctx, id, "stop", nil); err != nil {
			return err
		}
		fallthrough
	case "STOPPED", "ACTIVATED":
		if err := d.client.action(ctx, id, "deactivate", nil); err != nil {
			return err
		}
	}
	return d.client.uninstall(ctx, id)
}

func (d *Driver) waitForState(ctx context.Context, id, target string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(packagePoll)
	defer ticker.Stop()
	for {
		app, err := d.client.app(ctx, id)
		if err != nil {
			return fmt.Errorf("read IOx application state: %w", err)
		}
		state := appState(app.State)
		if state == target {
			return nil
		}
		if state == "ERROR" || state == "FAILED" || state == "INVALID" {
			return fmt.Errorf("IOx application %s entered state %s", id, state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("IOx application %s did not reach %s before timeout", id, target)
		case <-ticker.C:
		}
	}
}

func (d *Driver) GetPodStatus(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	if pod == nil {
		return nil, fmt.Errorf("AP app-hosting: nil pod")
	}
	out := pod.DeepCopy()
	ids := common.GenerateContainerAppIDs(pod)
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Spec.Containers))
	allRunning := true
	anyFound := false
	for _, container := range pod.Spec.Containers {
		id := ids[container.Name]
		app, err := d.client.app(ctx, id)
		if isHTTPStatus(err, http.StatusNotFound) {
			allRunning = false
			statuses = append(statuses, corev1.ContainerStatus{Name: container.Name, Image: container.Image, Ready: false, RestartCount: 0, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "IOxNotInstalled"}}})
			continue
		}
		if err != nil {
			return nil, err
		}
		anyFound = true
		running := appState(app.State) == "RUNNING"
		allRunning = allRunning && running
		status := corev1.ContainerStatus{Name: container.Name, Image: container.Image, Ready: running, RestartCount: 0}
		if running {
			status.State.Running = &corev1.ContainerStateRunning{StartedAt: metav1.Now()}
		} else if appState(app.State) == "ERROR" || appState(app.State) == "FAILED" {
			status.State.Terminated = &corev1.ContainerStateTerminated{Reason: "IOxAppFailed", Message: "IOx application entered a failure state"}
		} else {
			status.State.Waiting = &corev1.ContainerStateWaiting{Reason: "IOx" + appState(app.State)}
		}
		statuses = append(statuses, status)
	}
	if !anyFound {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "cisco.vk", Resource: "ap-app"}, pod.Name)
	}
	out.Status.ContainerStatuses = statuses
	out.Status.HostIP = d.config.Address
	now := metav1.Now()
	out.Status.StartTime = &now
	out.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: now},
		{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: now},
		{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: now},
		{Type: corev1.ContainersReady, Status: corev1.ConditionFalse, LastTransitionTime: now},
	}
	if allRunning {
		out.Status.Phase = corev1.PodRunning
		for i := range out.Status.Conditions {
			if out.Status.Conditions[i].Type == corev1.PodReady || out.Status.Conditions[i].Type == corev1.ContainersReady {
				out.Status.Conditions[i].Status = corev1.ConditionTrue
			}
		}
	} else {
		out.Status.Phase = corev1.PodPending
	}
	return out, nil
}

func (d *Driver) ListPods(ctx context.Context) ([]*corev1.Pod, error) {
	apps, err := d.client.listApps(ctx)
	if err != nil {
		return nil, err
	}
	byUID := map[string]map[int8]appInfo{}
	for _, app := range apps {
		index, uid, ok := common.ParseCVKAppName(app.ID)
		if !ok {
			continue
		}
		if byUID[uid] == nil {
			byUID[uid] = map[int8]appInfo{}
		}
		byUID[uid][index] = app
	}
	uids := make([]string, 0, len(byUID))
	for uid := range byUID {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	out := make([]*corev1.Pod, 0, len(uids))
	for _, uid := range uids {
		indexes := make([]int, 0, len(byUID[uid]))
		for index := range byUID[uid] {
			indexes = append(indexes, int(index))
		}
		sort.Ints(indexes)
		podUID := uidWithHyphens(uid)
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podUID, Namespace: "default", UID: types.UID(podUID)}}
		if len(indexes) == 0 {
			continue
		}
		for index := 0; index <= indexes[len(indexes)-1]; index++ {
			pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: fmt.Sprintf("container-%04d", index)})
		}
		pod.Status.HostIP = d.config.Address
		out = append(out, pod)
	}
	return out, nil
}

func uidWithHyphens(uid string) string {
	if len(uid) != 32 {
		return uid
	}
	return uid[:8] + "-" + uid[8:12] + "-" + uid[12:16] + "-" + uid[16:20] + "-" + uid[20:]
}

func isHTTPStatus(err error, status int) bool {
	var httpErr *ioxHTTPError
	return errors.As(err, &httpErr) && httpErr.status == status
}

func packageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("image must be an HTTP(S) URL to a prebuilt IOx package archive")
	}
	name := strings.ToLower(path.Base(u.Path))
	if !strings.HasSuffix(name, ".tar") && !strings.HasSuffix(name, ".tar.gz") {
		return nil, fmt.Errorf("image URL must point to a .tar or .tar.gz IOx package")
	}
	return u, nil
}

func fetchPackage(ctx context.Context, raw string) ([]byte, string, error) {
	u, err := packageURL(raw)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("AP app-hosting: build package request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("AP app-hosting: fetch IOx package")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("AP app-hosting: package server returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxPackageSize {
		return nil, "", fmt.Errorf("AP app-hosting: IOx package exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPackageSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("AP app-hosting: read IOx package")
	}
	if len(data) == 0 || len(data) > maxPackageSize {
		return nil, "", fmt.Errorf("AP app-hosting: IOx package is empty or exceeds size limit")
	}
	filename := path.Base(u.Path)
	if err := validatePackageArchive(data, filename); err != nil {
		return nil, "", err
	}
	return data, filename, nil
}

func validatePackageArchive(data []byte, filename string) error {
	var reader io.Reader = bytes.NewReader(data)
	if strings.HasSuffix(strings.ToLower(filename), ".tar.gz") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return fmt.Errorf("AP app-hosting: package is not a valid gzip archive")
		}
		defer gz.Close()
		reader = gz
	}
	tarReader := tar.NewReader(reader)
	if _, err := tarReader.Next(); err != nil {
		return fmt.Errorf("AP app-hosting: package is not a valid tar archive")
	}
	return nil
}
