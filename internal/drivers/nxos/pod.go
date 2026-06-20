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
	"strconv"
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
const defaultNXOSMaxAppSlots = 4
const nxosMaxAppsResourceKey = "maxApps"

func (d *NXOSDriver) DeployPod(ctx context.Context, pod *v1.Pod, secretLister corev1listers.SecretNamespaceLister, configMapLister corev1listers.ConfigMapNamespaceLister) error {
	d.secretLister = secretLister
	d.configLister = configMapLister
	appConfigs, err := d.convertPodToAppConfigs(pod)
	if err != nil {
		return fmt.Errorf("failed to convert pod to NX-OS app configs: %w", err)
	}
	if err := d.validateAppSlotCapacity(ctx, appConfigs); err != nil {
		return err
	}
	for i := range appConfigs {
		if err := d.createApp(ctx, &appConfigs[i]); err != nil {
			return fmt.Errorf("failed to deploy NX-OS app for container %s: %w", appConfigs[i].Container, err)
		}
	}
	return nil
}

func (d *NXOSDriver) validateAppSlotCapacity(ctx context.Context, appConfigs []nxosAppConfig) error {
	maxSlots := d.maxAppSlots()
	if maxSlots <= 0 {
		return nil
	}
	desired := make(map[string]struct{}, len(appConfigs))
	for _, cfg := range appConfigs {
		desired[cfg.AppID] = struct{}{}
	}
	requiredNew := len(desired)
	usedByOtherApps := 0
	if apps, err := d.listApps(ctx); err == nil {
		for _, app := range apps {
			if _, isDesired := desired[app.ID]; isDesired {
				requiredNew--
				continue
			}
			usedByOtherApps++
		}
	} else {
		log.G(ctx).WithError(err).Debug("NX-OS app slot preflight could not list apps; falling back to pod container count")
	}
	available := maxSlots - usedByOtherApps
	if available < 0 {
		available = 0
	}
	if requiredNew > available {
		return fmt.Errorf("NX-OS app-hosting capacity exceeded: pod requires %d new app slots, %d available, max %d; each container consumes one NX-OS app-hosting slot (override with resourceLimits.others.%s when the device has a higher validated limit)", requiredNew, available, maxSlots, nxosMaxAppsResourceKey)
	}
	return nil
}

func (d *NXOSDriver) maxAppSlots() int {
	if d == nil || d.config == nil {
		return defaultNXOSMaxAppSlots
	}
	if raw := strings.TrimSpace(d.config.ResourceLimits.Others[nxosMaxAppsResourceKey]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultNXOSMaxAppSlots
}

func (d *NXOSDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		return d.DeletePod(ctx, pod)
	}
	appConfigs, err := d.convertPodToAppConfigs(pod)
	if err != nil {
		return fmt.Errorf("failed to convert pod to NX-OS app configs: %w", err)
	}
	if err := d.validateAppSlotCapacity(ctx, appConfigs); err != nil {
		return err
	}
	for i := range appConfigs {
		cfg := &appConfigs[i]
		state, err := d.appState(ctx, cfg.AppID)
		if err != nil || state == "" {
			if err := d.createApp(ctx, cfg); err != nil {
				return fmt.Errorf("failed to recover missing NX-OS app %s: %w", cfg.AppID, err)
			}
			continue
		}
		if err := d.convergeAppState(ctx, cfg, state); err != nil {
			return err
		}
	}
	return nil
}

func (d *NXOSDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	var errs []string
	timeout := getPackageTimeout(pod)
	for _, appID := range common.GenerateContainerAppIDs(pod) {
		if err := d.deleteApp(ctx, appID, timeout); err != nil {
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
	desiredConfigs, desiredErr := d.desiredConfigsByContainer(pod)
	if desiredErr != nil {
		log.G(ctx).WithError(desiredErr).Debugf("NX-OS status: failed to render desired config for pod %s/%s", pod.Namespace, pod.Name)
	}
	for containerName, appID := range common.GenerateContainerAppIDs(pod) {
		app, err := d.appDetail(ctx, appID)
		if err != nil {
			log.G(ctx).WithError(err).Debugf("NX-OS app detail unavailable for %s", appID)
			continue
		}
		if app.State == "" && app.Image == "" && len(app.RunOpts) == 0 {
			continue
		}
		app.ContainerName = containerName
		observed[containerName] = app
	}
	if len(observed) == 0 {
		if pod.DeletionTimestamp == nil && d.secretLister != nil && d.configLister != nil {
			d.recoverMissingContainers(ctx, pod, observed)
			out := pod.DeepCopy()
			setPodStatus(d.config.Address, out, observed)
			return out, nil
		}
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "cisco.vk", Resource: "nxos-app"}, pod.Name)
	}
	if pod.DeletionTimestamp == nil {
		d.recoverMissingContainers(ctx, pod, observed)
		for _, app := range observed {
			cfg, ok := desiredConfigs[app.ContainerName]
			if !ok {
				cfg = nxosAppConfig{
					AppID:     app.ID,
					Container: app.ContainerName,
					ImagePath: app.Image,
					Timeout:   defaultPackageTimeout,
				}
			}
			if err := d.advanceAppState(ctx, &cfg, app.State); err != nil {
				log.G(ctx).WithError(err).Warnf("NX-OS app %s convergence from status failed", app.ID)
			}
		}
	}
	out := pod.DeepCopy()
	setPodStatus(d.config.Address, out, observed)
	return out, nil
}

func (d *NXOSDriver) desiredConfigsByContainer(pod *v1.Pod) (map[string]nxosAppConfig, error) {
	configs, err := d.convertPodToAppConfigs(pod)
	if err != nil {
		return nil, err
	}
	out := make(map[string]nxosAppConfig, len(configs))
	for _, cfg := range configs {
		out[cfg.Container] = cfg
	}
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

func (d *NXOSDriver) createApp(ctx context.Context, cfg *nxosAppConfig) error {
	if cfg.PullPolicy == v1.PullNever && isHTTPURL(cfg.ImagePath) {
		return fmt.Errorf("app %s: imagePullPolicy is Never but image is an HTTP URL %q; image must be pre-loaded on bootflash", cfg.AppID, cfg.ImagePath)
	}
	state, _ := d.appState(ctx, cfg.AppID)
	return d.advanceAppState(ctx, cfg, state)
}

func (d *NXOSDriver) convergeAppState(ctx context.Context, cfg *nxosAppConfig, state string) error {
	return d.advanceAppState(ctx, cfg, state)
}

func (d *NXOSDriver) advanceAppState(ctx context.Context, cfg *nxosAppConfig, state string) error {
	switch state {
	case "RUNNING":
		d.clearAppAction(cfg.AppID)
		return nil
	case "INSTALLING":
		return nil
	case "DEPLOYED":
		return d.runAppAction(ctx, cfg.AppID, "activate", func(actionCtx context.Context) error {
			if err := d.appCommand(actionCtx, fmt.Sprintf("app-hosting activate appid %s", cfg.AppID)); err != nil {
				return fmt.Errorf("nxos activate app %s: %w", cfg.AppID, err)
			}
			return nil
		})
	case "ACTIVATED", "STOPPED":
		return d.runAppAction(ctx, cfg.AppID, "start", func(actionCtx context.Context) error {
			if err := d.appCommand(actionCtx, fmt.Sprintf("app-hosting start appid %s", cfg.AppID)); err != nil {
				return fmt.Errorf("nxos start app %s: %w", cfg.AppID, err)
			}
			return nil
		})
	case "", "UNINSTALLED":
		if cfg.ImagePath == "" {
			return fmt.Errorf("nxos app %s has no observed state and no image path for install", cfg.AppID)
		}
		return d.runAppAction(ctx, cfg.AppID, "install", func(actionCtx context.Context) error {
			if err := d.ensureAppConfig(actionCtx, cfg); err != nil {
				return err
			}
			if err := d.appCommand(actionCtx, fmt.Sprintf("app-hosting install appid %s package %s", cfg.AppID, cfg.ImagePath)); err != nil {
				return fmt.Errorf("nxos install app %s: %w", cfg.AppID, err)
			}
			return nil
		})
	default:
		return fmt.Errorf("nxos app %s is in unsupported state %q", cfg.AppID, state)
	}
}

func (d *NXOSDriver) ensureAppConfig(ctx context.Context, cfg *nxosAppConfig) error {
	if err := d.appCommand(ctx,
		"configure terminal",
		fmt.Sprintf("app-hosting appid %s", cfg.AppID),
		"no app-resource docker",
	); err != nil {
		log.G(ctx).WithError(err).Debugf("NX-OS app %s had no existing docker run-opts to clear", cfg.AppID)
	}
	commands := []string{
		"configure terminal",
		fmt.Sprintf("app-hosting appid %s", cfg.AppID),
		fmt.Sprintf("app-vnic management guest-interface %d", nxosGuestInterface(d.config)),
		"app-resource profile custom",
		fmt.Sprintf("cpu %d", cfg.Resources.cpuUnits),
		fmt.Sprintf("memory %d", cfg.Resources.memoryMB),
		"exit",
		"app-resource docker",
	}
	for i, opt := range cfg.RunOpts {
		commands = append(commands, fmt.Sprintf("run-opts %d %q", i+1, opt))
	}
	if err := d.appCommand(ctx, commands...); err != nil {
		return fmt.Errorf("nxos configure app %s: %w", cfg.AppID, err)
	}
	return nil
}

func (d *NXOSDriver) appCommand(ctx context.Context, commands ...string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.client.conf(ctx, commands...)
	return err
}

func serviceLinkPrefixes(envs []v1.EnvVar) map[string]struct{} {
	prefixes := map[string]struct{}{}
	for _, env := range envs {
		prefix, ok := strings.CutSuffix(env.Name, "_SERVICE_HOST")
		if !ok || prefix == "" {
			continue
		}
		prefixes[prefix] = struct{}{}
	}
	return prefixes
}

func isServiceLinkEnv(name string, prefixes map[string]struct{}) bool {
	for prefix := range prefixes {
		switch {
		case name == prefix+"_SERVICE_HOST":
			return true
		case name == prefix+"_SERVICE_PORT":
			return true
		case strings.HasPrefix(name, prefix+"_SERVICE_PORT_"):
			return true
		case name == prefix+"_PORT":
			return true
		case strings.HasPrefix(name, prefix+"_PORT_"):
			return true
		}
	}
	return false
}

func (d *NXOSDriver) deleteApp(ctx context.Context, appID string, timeout time.Duration) error {
	defer d.clearAppAction(appID)
	if timeout <= 0 {
		timeout = defaultPackageTimeout
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		state, err := d.appState(ctx, appID)
		if err != nil || state == "" || state == "UNINSTALLED" {
			if cfgErr := d.removeAppConfig(ctx, appID); cfgErr != nil {
				return fmt.Errorf("app %s: remove stale config: %w", appID, cfgErr)
			}
			return nil
		}
		switch state {
		case "RUNNING":
			if err := d.appCommand(ctx, fmt.Sprintf("app-hosting stop appid %s", appID)); err != nil {
				lastErr = err
			}
		case "ACTIVATED", "STOPPED":
			if err := d.appCommand(ctx, fmt.Sprintf("app-hosting deactivate appid %s", appID)); err != nil {
				lastErr = err
			}
		case "DEPLOYED":
			if err := d.appCommand(ctx, fmt.Sprintf("app-hosting uninstall appid %s", appID)); err != nil {
				lastErr = err
			}
		case "INSTALLING":
			// Wait for the install transaction to reach a removable state.
		default:
			if err := d.appCommand(ctx, fmt.Sprintf("app-hosting uninstall appid %s", appID)); err != nil {
				lastErr = err
			}
		}
		if time.Now().After(deadline) {
			finalState, finalErr := d.appState(ctx, appID)
			if finalErr != nil || finalState == "" || finalState == "UNINSTALLED" {
				if cfgErr := d.removeAppConfig(ctx, appID); cfgErr != nil {
					return fmt.Errorf("app %s: remove stale config after timeout: %w", appID, cfgErr)
				}
				return nil
			}
			if lastErr != nil {
				return fmt.Errorf("app %s: timed out after %s deleting from state %q: %w", appID, timeout, finalState, lastErr)
			}
			return fmt.Errorf("app %s: timed out after %s deleting from state %q", appID, timeout, finalState)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *NXOSDriver) removeAppConfig(ctx context.Context, appID string) error {
	return d.appCommand(ctx, "configure terminal", fmt.Sprintf("no app-hosting appid %s", appID))
}

func (d *NXOSDriver) recoverMissingContainers(ctx context.Context, pod *v1.Pod, observed map[string]nxosApp) {
	if pod.DeletionTimestamp != nil {
		return
	}
	appConfigs, err := d.convertPodToAppConfigs(pod)
	if err != nil {
		log.G(ctx).WithError(err).Warnf("NX-OS recovery: failed to render pod %s/%s", pod.Namespace, pod.Name)
		return
	}
	missingConfigs := make([]nxosAppConfig, 0, len(appConfigs))
	for _, cfg := range appConfigs {
		if _, found := observed[cfg.Container]; !found {
			missingConfigs = append(missingConfigs, cfg)
		}
	}
	if len(missingConfigs) == 0 {
		return
	}
	if err := d.validateAppSlotCapacity(ctx, missingConfigs); err != nil {
		log.G(ctx).WithError(err).Warnf("NX-OS recovery: insufficient app-hosting slots for pod %s/%s", pod.Namespace, pod.Name)
		return
	}
	for i := range missingConfigs {
		cfg := missingConfigs[i]
		if err := d.createApp(ctx, &cfg); err != nil {
			log.G(ctx).WithError(err).Warnf("NX-OS recovery: failed to deploy missing container %s for pod %s/%s", cfg.Container, pod.Namespace, pod.Name)
		}
	}
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
