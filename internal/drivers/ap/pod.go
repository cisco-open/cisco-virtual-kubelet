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
			var resources map[string]json.RawMessage
			if len(payload) == 0 || json.Unmarshal(payload["resources"], &resources) != nil || len(resources) == 0 {
				return nil, fmt.Errorf("payload must be a JSON object with a resources object")
			}
			return json.RawMessage(raw), nil
		}
	}
	return json.RawMessage(`{"resources":{"network":[{"interface-name":"eth0","network-name":"iox-nat0","port_map":{"mode":"1to1"}}]}}`), nil
}

func (d *Driver) DeployPod(ctx context.Context, pod *corev1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	d.lifecycleMu.Lock()
	defer d.lifecycleMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if pod != nil && pod.DeletionTimestamp != nil {
		return fmt.Errorf("AP app-hosting: cannot deploy a terminating pod")
	}
	if err := d.validatePod(pod); err != nil {
		return err
	}
	if _, err := activationPayloadFor(pod); err != nil {
		return fmt.Errorf("invalid %s annotation: %w", activationAnnotation, err)
	}
	if err := validateObservedImages(pod); err != nil {
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

// validateObservedImages fails closed on an in-place image update. IOx does not
// expose the source package URL; the last published status is the available
// evidence of what was installed. Replacing the Pod gives the app a new UID.
func validateObservedImages(pod *corev1.Pod) error {
	for _, container := range pod.Spec.Containers {
		for _, observed := range pod.Status.ContainerStatuses {
			if observed.Name == container.Name && observed.Image != "" && observed.Image != container.Image {
				return fmt.Errorf("AP app-hosting: changing container %s image in place is not supported; replace the Pod", container.Name)
			}
		}
	}
	return nil
}

func (d *Driver) deployApp(ctx context.Context, pod *corev1.Pod, container corev1.Container, id string) error {
	payload, err := activationPayloadFor(pod)
	if err != nil {
		return fmt.Errorf("invalid %s annotation: %w", activationAnnotation, err)
	}
	app, err := d.client.app(ctx, id)
	if err != nil && !isHTTPStatus(err, http.StatusNotFound) {
		return err
	}
	if err == nil {
		switch appState(app.State) {
		case "ERROR", "FAILED", "INVALID":
			if err := d.removeApp(ctx, id, app.State); err != nil {
				return fmt.Errorf("remove failed attempt: %w", err)
			}
			err = &ioxHTTPError{status: http.StatusNotFound}
		}
	}
	if isHTTPStatus(err, http.StatusNotFound) {
		packageBytes, _, err := fetchPackage(ctx, container.Image)
		if err != nil {
			return err
		}
		if err := d.client.install(ctx, id, packageBytes); err != nil {
			return fmt.Errorf("install IOx package: %w", err)
		}
		if err := d.waitForStateObservation(ctx, id, "DEPLOYED", packageTimeout, true); err != nil {
			return err
		}
		app.State = "DEPLOYED"
	}
	// A retry resumes the observed lifecycle stage. Removing a deployed or
	// stopped app here would discard a successful install and its local data.
	switch state := appState(app.State); state {
	case "RUNNING":
		return nil
	case "DEPLOYED":
		if err := d.client.action(ctx, id, "activate", payload); err != nil {
			return fmt.Errorf("activate IOx application: %w", err)
		}
		if err := d.waitForState(ctx, id, "ACTIVATED", packageTimeout); err != nil {
			return err
		}
		fallthrough
	case "ACTIVATED", "STOPPED":
		if err := d.client.action(ctx, id, "start", nil); err != nil {
			return fmt.Errorf("start IOx application: %w", err)
		}
		return d.waitForState(ctx, id, "RUNNING", packageTimeout)
	default:
		return fmt.Errorf("AP app-hosting: application %s is in unsupported state %q", id, state)
	}
}

func (d *Driver) UpdatePod(ctx context.Context, pod *corev1.Pod) error {
	if pod == nil {
		return fmt.Errorf("AP app-hosting: nil pod")
	}
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	return d.DeployPod(ctx, pod, nil, nil)
}

// PodNeedsReconciliation opts into the provider's guarded status reconciliation.
// Status reads themselves remain read-only, including during maintenance.
func (d *Driver) PodNeedsReconciliation(pod *corev1.Pod) bool {
	if pod == nil || pod.DeletionTimestamp != nil {
		return false
	}
	if validateObservedImages(pod) != nil {
		return true
	}
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			return true
		}
	}
	return false
}

// PodInventoryUsesUID signals that CAF preserves only the CVK application ID,
// so the provider must recover names from its informer using the exact UID.
func (d *Driver) PodInventoryUsesUID() bool { return true }

func (d *Driver) DeletePod(ctx context.Context, pod *corev1.Pod) error {
	d.lifecycleMu.Lock()
	defer d.lifecycleMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if pod == nil {
		return nil
	}
	ids := common.GenerateContainerAppIDs(pod)
	for _, id := range ids {
		if err := validateAppID(id); err != nil {
			return err
		}
	}
	var errs []error
	for _, id := range ids {
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
			if isHTTPStatus(err, http.StatusNotFound) {
				return nil
			}
			return err
		}
		if err := d.waitForState(ctx, id, "STOPPED", packageTimeout); err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return nil
			}
			return err
		}
		fallthrough
	case "STOPPED", "ACTIVATED":
		if err := d.client.action(ctx, id, "deactivate", nil); err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return nil
			}
			return err
		}
		if err := d.waitForState(ctx, id, "DEPLOYED", packageTimeout); err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return nil
			}
			return err
		}
	case "DEPLOYED", "ERROR", "FAILED", "INVALID":
	default:
		return fmt.Errorf("AP app-hosting: refusing to uninstall application %s in unknown state %q", id, state)
	}
	if err := d.client.uninstall(ctx, id); err != nil && !isHTTPStatus(err, http.StatusNotFound) {
		return err
	}
	return d.waitForState(ctx, id, "", packageTimeout)
}

// An empty target waits for disappearance after uninstall. The timeout bounds
// each HTTP read as well as the delay between observations.
func (d *Driver) waitForState(ctx context.Context, id, target string, timeout time.Duration) error {
	return d.waitForStateObservation(ctx, id, target, timeout, false)
}

func (d *Driver) waitForStateObservation(ctx context.Context, id, target string, timeout time.Duration, installing bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(packagePoll)
	defer ticker.Stop()
	for {
		app, err := d.client.app(ctx, id)
		if target == "" && isHTTPStatus(err, http.StatusNotFound) {
			return nil
		}
		if err != nil && !(installing && isHTTPStatus(err, http.StatusNotFound)) {
			return fmt.Errorf("read IOx application state: %w", err)
		}
		// CAF can acknowledge installation before the app is visible. Keep
		// waiting instead of returning absence and allowing another install.
		state := appState(app.State)
		// CAF versions can expose a stopped app as ACTIVATED; either is
		// a valid stable precondition for the following deactivate action.
		if target != "" && (state == target || target == "STOPPED" && state == "ACTIVATED") {
			return nil
		}
		if target != "" && (state == "ERROR" || state == "FAILED" || state == "INVALID") {
			return fmt.Errorf("IOx application %s entered state %s", id, state)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("IOx application %s did not reach %q: %w", id, target, ctx.Err())
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
	for _, id := range ids {
		if err := validateAppID(id); err != nil {
			return nil, err
		}
	}
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Spec.Containers))
	allRunning := true
	anyFound := false
	now := metav1.Now()
	previous := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		previous[status.Name] = status
	}
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
		prior := previous[container.Name]
		status := corev1.ContainerStatus{Name: container.Name, Image: container.Image, Ready: running, RestartCount: prior.RestartCount,
			ContainerID: "iox://" + id, LastTerminationState: prior.LastTerminationState}
		// Do not turn desired image edits into false evidence of an update.
		if prior.Image != "" {
			status.Image = prior.Image
		}
		if running {
			status.State.Running = &corev1.ContainerStateRunning{StartedAt: now}
			if prior.State.Running != nil && !prior.State.Running.StartedAt.IsZero() {
				status.State.Running.StartedAt = prior.State.Running.StartedAt
			}
		} else if appState(app.State) == "ERROR" || appState(app.State) == "FAILED" || appState(app.State) == "INVALID" {
			status.State.Waiting = &corev1.ContainerStateWaiting{Reason: "IOxAppFailed", Message: "IOx application requires recovery"}
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
	if out.Status.StartTime == nil {
		out.Status.StartTime = &now
	}
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
	if pod.DeletionTimestamp != nil {
		for i := range out.Status.Conditions {
			if out.Status.Conditions[i].Type == corev1.PodReady || out.Status.Conditions[i].Type == corev1.ContainersReady {
				out.Status.Conditions[i].Status = corev1.ConditionFalse
			}
		}
		for i := range out.Status.ContainerStatuses {
			out.Status.ContainerStatuses[i].Ready = false
		}
	}
	// Unchanged observations must not move lifecycle timestamps on every poll.
	for i := range out.Status.Conditions {
		for _, prior := range pod.Status.Conditions {
			if prior.Type == out.Status.Conditions[i].Type && prior.Status == out.Status.Conditions[i].Status && !prior.LastTransitionTime.IsZero() {
				out.Status.Conditions[i].LastTransitionTime = prior.LastTransitionTime
			}
		}
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
	// Limit expanded data as well as downloaded bytes: a small gzip stream
	// must not consume unbounded memory or CPU during package validation.
	limited := &io.LimitedReader{R: reader, N: maxPackageSize + 1}
	tarReader := tar.NewReader(limited)
	seen := make(map[string]bool)
	entries := 0
	hasDescriptor := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("AP app-hosting: package is not a complete valid tar archive")
		}
		name := path.Clean(header.Name)
		if path.IsAbs(header.Name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(header.Name, "\\") {
			return fmt.Errorf("AP app-hosting: package contains an unsafe archive path")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("AP app-hosting: package contains an unsupported archive entry")
		}
		if seen[name] {
			return fmt.Errorf("AP app-hosting: package contains duplicate archive paths")
		}
		seen[name] = true
		if name == "package.yaml" && header.Typeflag == tar.TypeReg && header.Size > 0 {
			hasDescriptor = true
		}
		entries++
		if header.Size < 0 || header.Size > maxPackageSize {
			return fmt.Errorf("AP app-hosting: expanded package exceeds size limit")
		}
		if _, err := io.Copy(io.Discard, tarReader); err != nil {
			return fmt.Errorf("AP app-hosting: package contains truncated archive data")
		}
	}
	// Consume gzip trailers too, validating their checksum and bounding any
	// trailing padding that archive/tar did not need to read.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return fmt.Errorf("AP app-hosting: package archive integrity check failed")
	}
	if limited.N == 0 {
		return fmt.Errorf("AP app-hosting: expanded package exceeds size limit")
	}
	if entries == 0 {
		return fmt.Errorf("AP app-hosting: package archive is empty")
	}
	if !hasDescriptor {
		return fmt.Errorf("AP app-hosting: package archive requires a nonempty root package.yaml descriptor")
	}
	return nil
}
