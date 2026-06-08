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

package nxos

import (
	"context"
	"fmt"
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

const defaultAppWaitTimeout = 5 * time.Minute

func (d *NXOSDriver) DeployPod(ctx context.Context, pod *v1.Pod, _ corev1listers.SecretNamespaceLister, _ corev1listers.ConfigMapNamespaceLister) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	appIDs := common.GenerateContainerAppIDs(pod)
	for _, container := range pod.Spec.Containers {
		appID := appIDs[container.Name]
		if err := d.ensureAppConfig(ctx, pod, container, appID); err != nil {
			return err
		}
		state, _ := d.appState(ctx, appID)
		if state == "" {
			if _, err := d.client.show(ctx, fmt.Sprintf("app-hosting install appid %s package %s", appID, container.Image)); err != nil {
				return fmt.Errorf("nxos install app %s: %w", appID, err)
			}
			if err := d.waitForState(ctx, appID, defaultAppWaitTimeout, "DEPLOYED", "ACTIVATED", "RUNNING"); err != nil {
				return err
			}
		}
		state, _ = d.appState(ctx, appID)
		if state == "DEPLOYED" {
			if _, err := d.client.show(ctx, fmt.Sprintf("app-hosting activate appid %s", appID)); err != nil {
				return fmt.Errorf("nxos activate app %s: %w", appID, err)
			}
			if err := d.waitForState(ctx, appID, defaultAppWaitTimeout, "ACTIVATED", "RUNNING"); err != nil {
				return err
			}
		}
		state, _ = d.appState(ctx, appID)
		if state != "RUNNING" {
			if _, err := d.client.show(ctx, fmt.Sprintf("app-hosting start appid %s", appID)); err != nil {
				return fmt.Errorf("nxos start app %s: %w", appID, err)
			}
			if err := d.waitForState(ctx, appID, defaultAppWaitTimeout, "RUNNING"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *NXOSDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	for _, container := range pod.Spec.Containers {
		appID := common.GenerateContainerAppIDs(pod)[container.Name]
		state, err := d.appState(ctx, appID)
		if err != nil || state == "" {
			return d.DeployPod(ctx, pod, nil, nil)
		}
	}
	return nil
}

func (d *NXOSDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var errs []string
	for _, appID := range common.GenerateContainerAppIDs(pod) {
		if err := d.deleteApp(ctx, appID); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete NX-OS app-hosting pod %s/%s: %s", pod.Namespace, pod.Name, strings.Join(errs, "; "))
	}
	return nil
}

func (d *NXOSDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	observed := map[string]nxosApp{}
	for containerName, appID := range common.GenerateContainerAppIDs(pod) {
		app, err := d.appDetail(ctx, appID)
		if err != nil {
			log.G(ctx).WithError(err).Debugf("NX-OS app detail unavailable for %s", appID)
			continue
		}
		app.ContainerName = containerName
		observed[containerName] = app
	}
	if len(observed) == 0 {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "cisco.vk", Resource: "nxos-app"}, pod.Name)
	}
	out := pod.DeepCopy()
	setPodStatus(d.config.Address, out, observed)
	return out, nil
}

func (d *NXOSDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
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
		observed := map[string]nxosApp{}
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

func (d *NXOSDriver) ensureAppConfig(ctx context.Context, pod *v1.Pod, container v1.Container, appID string) error {
	runOpts := dockerLabelRunOpts(pod, container)
	commands := []string{
		"configure terminal",
		fmt.Sprintf("app-hosting appid %s", appID),
		fmt.Sprintf("app-vnic management guest-interface %d", nxosGuestInterface(d.config)),
		"app-resource docker",
	}
	for i, opt := range runOpts {
		commands = append(commands, fmt.Sprintf("run-opts %d %q", i+1, opt))
	}
	if _, err := d.client.conf(ctx, commands...); err != nil {
		return fmt.Errorf("nxos configure app %s: %w", appID, err)
	}
	return nil
}

func dockerLabelRunOpts(pod *v1.Pod, container v1.Container) []string {
	labels := []string{
		fmt.Sprintf("--label %s=%s", common.LabelPodName, pod.Name),
		fmt.Sprintf("--label %s=%s", common.LabelPodNamespace, pod.Namespace),
		fmt.Sprintf("--label %s=%s", common.LabelPodUID, pod.UID),
		fmt.Sprintf("--label %s=%s", common.LabelContainerName, container.Name),
	}
	for _, env := range container.Env {
		if env.Value == "" {
			continue
		}
		labels = append(labels, fmt.Sprintf("--env %s=%s", env.Name, env.Value))
	}
	return labels
}

func (d *NXOSDriver) deleteApp(ctx context.Context, appID string) error {
	state, err := d.appState(ctx, appID)
	if err != nil || state == "" {
		return nil
	}
	var errs []string
	if state == "RUNNING" {
		if _, err := d.client.show(ctx, fmt.Sprintf("app-hosting stop appid %s", appID)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if _, err := d.client.show(ctx, fmt.Sprintf("app-hosting deactivate appid %s", appID)); err != nil {
		errs = append(errs, err.Error())
	}
	if _, err := d.client.show(ctx, fmt.Sprintf("app-hosting uninstall appid %s", appID)); err != nil {
		errs = append(errs, err.Error())
	}
	if _, err := d.client.conf(ctx, "configure terminal", fmt.Sprintf("no app-hosting appid %s", appID)); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("app %s: %s", appID, strings.Join(errs, "; "))
	}
	return nil
}

func (d *NXOSDriver) waitForState(ctx context.Context, appID string, timeout time.Duration, states ...string) error {
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
			return fmt.Errorf("timed out waiting for app %s state in %v; last state=%q err=%v", appID, states, state, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
