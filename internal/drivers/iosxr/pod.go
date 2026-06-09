// Copyright (c) 2026 Cisco Systems Inc.
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

package iosxr

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const defaultXRAppWaitTimeout = 5 * time.Minute

var iosxrPodResource = schema.GroupResource{Group: "cisco.vk", Resource: "iosxr-app"}

func (d *IOSXRDriver) DeployPod(ctx context.Context, pod *v1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.ensureDocker(ctx); err != nil {
		return fmt.Errorf("iosxr start docker before deploy: %w", err)
	}

	appIDs := common.GenerateContainerAppIDs(pod)
	for _, container := range pod.Spec.Containers {
		appID := appIDs[container.Name]
		ref, err := d.resolveImageRef(container.Image)
		if err != nil {
			return err
		}
		if err := d.ensureAppSource(ctx, ref); err != nil {
			return err
		}
		if err := d.ensureAppConfig(ctx, pod, container, appID, ref.Source); err != nil {
			return err
		}
		state, _ := d.appState(ctx, appID)
		if state != "RUNNING" {
			if _, err := d.client.Run(ctx, fmt.Sprintf("appmgr application start name %s", appID)); err != nil {
				return fmt.Errorf("iosxr start app %s: %w", appID, err)
			}
			if err := d.waitForState(ctx, appID, defaultXRAppWaitTimeout, "RUNNING"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *IOSXRDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	for _, container := range pod.Spec.Containers {
		appID := common.GenerateContainerAppIDs(pod)[container.Name]
		state, err := d.appState(ctx, appID)
		if err != nil || state == "" || state == "ERROR" {
			return d.DeployPod(ctx, pod, nil, nil)
		}
	}
	return nil
}

func (d *IOSXRDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var errs []string
	for _, appID := range common.GenerateContainerAppIDs(pod) {
		if err := d.deleteApp(ctx, appID); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete IOS XR app-hosting pod %s/%s: %s", pod.Namespace, pod.Name, strings.Join(errs, "; "))
	}
	return nil
}

func (d *IOSXRDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	observed := map[string]iosxrApp{}
	for containerName, appID := range common.GenerateContainerAppIDs(pod) {
		app, err := d.appDetail(ctx, appID)
		if err != nil {
			log.G(ctx).WithError(err).Debugf("IOS XR app detail unavailable for %s", appID)
			continue
		}
		app.ContainerName = containerName
		observed[containerName] = app
	}
	if len(observed) == 0 {
		return nil, apierrors.NewNotFound(iosxrPodResource, pod.Name)
	}
	out := pod.DeepCopy()
	setPodStatus(d.config.Address, out, observed)
	return out, nil
}

func (d *IOSXRDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
	apps, err := d.listApps(ctx)
	if err != nil {
		return nil, err
	}
	pods := map[string]*v1.Pod{}
	for _, app := range apps {
		if !common.IsCVKManagedApp(app.ID) {
			continue
		}
		detail, err := d.appDetail(ctx, app.ID)
		if err == nil {
			app = detail
		}
		ns, name, uid, container := common.PodIdentityFromRunOpts(app.RunOpts)
		if ns == "" || name == "" {
			continue
		}
		key := ns + "/" + name
		pod := pods[key]
		if pod == nil {
			pod = &v1.Pod{}
			pod.Namespace = ns
			pod.Name = name
			pod.UID = types.UID(uid)
			pods[key] = pod
		}
		if container == "" {
			container = app.ContainerName
		}
		if container == "" {
			container = app.ID
		}
		pod.Spec.Containers = append(pod.Spec.Containers, v1.Container{Name: container, Image: app.Image})
	}
	out := make([]*v1.Pod, 0, len(pods))
	for _, pod := range pods {
		observed := map[string]iosxrApp{}
		for _, c := range pod.Spec.Containers {
			appID := common.GenerateContainerAppIDs(pod)[c.Name]
			if app, err := d.appDetail(ctx, appID); err == nil {
				observed[c.Name] = app
			}
		}
		setPodStatus(d.config.Address, pod, observed)
		out = append(out, pod)
	}
	return out, nil
}

func (d *IOSXRDriver) ensureAppSource(ctx context.Context, ref appImageRef) error {
	sources, err := d.sourceTable(ctx)
	if err != nil {
		return err
	}
	if sources[ref.Source] {
		return nil
	}
	if ref.PackagePath == "" {
		return fmt.Errorf("iosxr appmgr source %q is not installed; use an appmgr RPM path as the pod image or preinstall the source", ref.Source)
	}
	if _, err := d.client.Run(ctx, fmt.Sprintf("appmgr package install rpm %s", ref.PackagePath)); err != nil {
		return fmt.Errorf("iosxr install appmgr package %s: %w", ref.PackagePath, err)
	}
	return d.waitForSource(ctx, ref.Source, defaultXRAppWaitTimeout)
}

func (d *IOSXRDriver) ensureAppConfig(ctx context.Context, pod *v1.Pod, container v1.Container, appID, source string) error {
	current, _ := d.appDetail(ctx, appID)
	if current.ID != "" && current.Source == source && current.State() != "ERROR" {
		return nil
	}
	if current.ID != "" {
		if err := d.deleteApp(ctx, appID); err != nil {
			return err
		}
	}
	runOpts := dockerRunOpts(pod, container, xrDefaultRunOptions(d.config))
	if _, err := d.client.Configure(ctx,
		fmt.Sprintf("appmgr application %s", appID),
		fmt.Sprintf("activate type docker source %s docker-run-opts %q", source, strings.Join(runOpts, " ")),
	); err != nil {
		return fmt.Errorf("iosxr configure app %s: %w", appID, err)
	}
	return nil
}

func dockerRunOpts(pod *v1.Pod, container v1.Container, defaults []string) []string {
	opts := append([]string{}, defaults...)
	opts = append(opts,
		fmt.Sprintf("--label %s=%s", common.LabelPodName, pod.Name),
		fmt.Sprintf("--label %s=%s", common.LabelPodNamespace, pod.Namespace),
		fmt.Sprintf("--label %s=%s", common.LabelPodUID, pod.UID),
		fmt.Sprintf("--label %s=%s", common.LabelContainerName, container.Name),
	)
	for _, env := range container.Env {
		if env.Value == "" || strings.ContainsAny(env.Value, " \t\n\r\"") {
			continue
		}
		opts = append(opts, fmt.Sprintf("--env %s=%s", env.Name, env.Value))
	}
	return opts
}

func (d *IOSXRDriver) deleteApp(ctx context.Context, appID string) error {
	app, err := d.appDetail(ctx, appID)
	if err != nil || app.ID == "" {
		return nil
	}
	var errs []string
	if app.State() == "RUNNING" {
		if _, err := d.client.Run(ctx, fmt.Sprintf("appmgr application stop name %s", appID)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if _, err := d.client.Configure(ctx, fmt.Sprintf("no appmgr application %s", appID)); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("app %s: %s", appID, strings.Join(errs, "; "))
	}
	return nil
}

func (d *IOSXRDriver) waitForSource(ctx context.Context, source string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		sources, err := d.sourceTable(ctx)
		if err == nil && sources[source] {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for IOS XR appmgr source %q", source)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *IOSXRDriver) waitForState(ctx context.Context, appID string, timeout time.Duration, states ...string) error {
	want := map[string]bool{}
	for _, state := range states {
		want[state] = true
	}
	deadline := time.Now().Add(timeout)
	for {
		state, err := d.appState(ctx, appID)
		if err == nil && want[state] {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for IOS XR app %s state in %v; last state=%q err=%v", appID, states, state, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

type appImageRef struct {
	Original    string
	PackagePath string
	Source      string
}

func (d *IOSXRDriver) resolveImageRef(image string) (appImageRef, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		if source := xrDefaultSource(d.config); source != "" {
			return appImageRef{Source: source}, nil
		}
		return appImageRef{}, fmt.Errorf("iosxr pod container image must name an appmgr RPM or source")
	}
	ref := appImageRef{Original: image}
	if strings.HasSuffix(strings.ToLower(image), ".rpm") {
		ref.PackagePath = normalizeXRPackagePath(image, xrPackageInstallPath(d.config))
		ref.Source = sourceFromRPMPath(image)
		return ref, nil
	}
	ref.Source = strings.TrimSuffix(image, ":latest")
	return ref, nil
}

func normalizeXRPackagePath(image, prefix string) string {
	if strings.Contains(image, ":/") || strings.HasPrefix(image, "/") {
		return image
	}
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		prefix = "/harddisk:"
	}
	return prefix + "/" + filepath.Base(image)
}

func sourceFromRPMPath(image string) string {
	base := filepath.Base(image)
	base = strings.TrimSuffix(base, ".rpm")
	base = strings.TrimSuffix(base, ".x86_64")
	base = strings.TrimSuffix(base, ".aarch64")
	if m := regexp.MustCompile(`^(.+)-[0-9]+(?:\.[0-9A-Za-z]+)*-.+$`).FindStringSubmatch(base); len(m) > 1 {
		return m[1]
	}
	return base
}
