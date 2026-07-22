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

package iosxe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

// DeployPod creates and deploys all containers in a pod to the device
func (d *XEDriver) DeployPod(ctx context.Context, pod *v1.Pod, secretLister corev1listers.SecretNamespaceLister, configMapLister corev1listers.ConfigMapNamespaceLister) error {
	log.G(ctx).WithFields(log.Fields{
		"pod_namespace": pod.Namespace,
		"pod_name":      pod.Name,
		"pod_uid":       pod.UID,
	}).Debug("Pod DeployContainer request received")

	log.G(ctx).Infof("Deploying pod: %s/%s", pod.Namespace, pod.Name)

	d.secretLister = secretLister
	d.configMapLister = configMapLister

	// Convert pod spec to app hosting configurations
	appConfigs, err := d.ConvertPodToAppConfigs(pod)
	if err != nil {
		return fmt.Errorf("failed to convert pod to app configs: %w", err)
	}

	// Deploy each app configuration sequentially. CreateAppHostingApp owns the
	// full lifecycle through to RUNNING (or fallback copy recovery), so no
	// separate DEPLOYED wait is needed here.
	for i := range appConfigs {
		appConfig := &appConfigs[i]
		log.G(ctx).Infof("Deploying app: %s for container: %s", appConfig.AppName(), appConfig.ContainerName())

		err = d.CreateAppHostingApp(ctx, appConfig)
		if err != nil {
			return fmt.Errorf("failed to deploy app for container %s: %w", appConfig.ContainerName(), err)
		}

		log.G(ctx).Infof("Successfully deployed app %s for container %s", appConfig.AppName(), appConfig.ContainerName())
	}

	log.G(ctx).Infof("Successfully deployed all apps for pod: %s/%s", pod.Namespace, pod.Name)
	return nil
}

// UpdatePod handles pod update requests.
// Apps that are already RUNNING are left untouched (benign metadata-only change).
// Apps in any other state (or missing) are deleted and redeployed.
func (d *XEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	if pod.DeletionTimestamp != nil {
		log.G(ctx).Infof("UpdatePod: pod %s/%s is deleting; driving cleanup instead of redeploy", pod.Namespace, pod.Name)
		return d.DeletePod(ctx, pod)
	}

	discoveredContainers, err := d.GetPodContainers(ctx, pod)
	if err != nil || len(discoveredContainers) == 0 {
		log.G(ctx).Infof("UpdatePod: no existing apps found for pod %s/%s, deploying fresh", pod.Namespace, pod.Name)
		if err := d.DeletePod(ctx, pod); err != nil {
			log.G(ctx).Warnf("UpdatePod: cleanup had errors (will attempt redeploy): %v", err)
		}
		return d.DeployPod(ctx, pod, d.secretLister, d.configMapLister)
	}

	allOperData, operErr := d.GetAppOperationalData(ctx)
	if operErr != nil {
		log.G(ctx).Warnf("UpdatePod: failed to fetch oper data for pod %s/%s: %v", pod.Namespace, pod.Name, operErr)
		allOperData = make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)
	}

	var appsNeedingRedeploy []string
	for _, appID := range discoveredContainers {
		operData, exists := allOperData[appID]
		if !exists {
			// App is in config but not yet in oper data — normal during
			// INSTALLING/DEPLOYED. The reconciler handles this case.
			continue
		}
		if operData.Details == nil || operData.Details.State == nil {
			continue
		}
		// Leave the app alone if it is making normal forward progress.
		// The reconciler in GetPodStatus advances DEPLOYED → ACTIVATED → RUNNING.
		// Only trigger a redeploy for states the reconciler cannot recover from.
		state := *operData.Details.State
		switch state {
		case "RUNNING", "ACTIVATED", "DEPLOYED", "INSTALLING":
			continue
		default:
			log.G(ctx).Infof("UpdatePod: app %s is in state %q (not a healthy transitional state), scheduling redeploy", appID, state)
			appsNeedingRedeploy = append(appsNeedingRedeploy, appID)
		}
	}

	if len(appsNeedingRedeploy) == 0 {
		log.G(ctx).Debugf("UpdatePod: all apps RUNNING for pod %s/%s, skipping redeploy", pod.Namespace, pod.Name)
		return nil
	}

	log.G(ctx).Infof("UpdatePod: %d app(s) not RUNNING for pod %s/%s, redeploying", len(appsNeedingRedeploy), pod.Namespace, pod.Name)

	for _, appID := range appsNeedingRedeploy {
		if err := d.DeleteApp(ctx, appID); err != nil {
			log.G(ctx).Warnf("UpdatePod: failed to delete app %s (will attempt redeploy anyway): %v", appID, err)
		}
	}

	appConfigs, err := d.ConvertPodToAppConfigs(pod)
	if err != nil {
		return fmt.Errorf("failed to convert pod to app configs: %w", err)
	}

	needsRedeploy := make(map[string]bool, len(appsNeedingRedeploy))
	for _, id := range appsNeedingRedeploy {
		needsRedeploy[id] = true
	}

	containerAppIDs := common.GenerateContainerAppIDs(pod)
	for i := range appConfigs {
		appConfig := &appConfigs[i]
		appID := containerAppIDs[appConfig.ContainerName()]
		if !needsRedeploy[appID] {
			continue
		}
		if err := d.CreateAppHostingApp(ctx, appConfig); err != nil {
			return fmt.Errorf("UpdatePod: failed to redeploy app %s: %w", appConfig.AppName(), err)
		}
	}

	return nil
}

// GetPodContainers retrieves all containers belonging to a specific pod from the device.
// It queries all apps on the device, filters them by pod UID and labels, and verifies
// that all expected containers are found.
// Returns a map of containerName -> appID and an error if verification fails.
func (d *XEDriver) GetPodContainers(ctx context.Context, pod *v1.Pod) (map[string]string, error) {
	log.G(ctx).Debugf("Getting containers for pod: %s/%s", pod.Namespace, pod.Name)

	// Get all apps from the device (config endpoint)
	apps, err := d.ListAppHostingApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	// Clean the pod UID (remove hyphens) as that's how it appears in app names
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")

	// If no config-endpoint apps match our UID, check oper data as well.
	// Apps in DEPLOYED state may not appear in the config endpoint but
	// are still visible in oper data.
	hasMatch := false
	for _, app := range apps {
		if app.ApplicationName != nil && strings.Contains(*app.ApplicationName, cleanUID) {
			hasMatch = true
			break
		}
	}
	if !hasMatch {
		allAppOperData, operErr := d.GetAppOperationalData(ctx)
		if operErr == nil {
			for appName := range allAppOperData {
				if strings.Contains(appName, cleanUID) && common.IsCVKManagedApp(appName) {
					log.G(ctx).Infof("GetPodContainers: app %s found in oper data but not config; adding for cleanup", appName)
					name := appName
					apps = append(apps, &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App{
						ApplicationName: &name,
					})
				}
			}
		}
	}

	containerToAppID := make(map[string]string)

	// Filter apps by pod UID and extract container names
	for _, app := range apps {
		if app.ApplicationName == nil {
			continue
		}

		appName := *app.ApplicationName

		// Check if app name contains the cleaned pod UID
		if !strings.Contains(appName, cleanUID) {
			continue
		}

		log.G(ctx).Debugf("Found app %s with matching pod UID", appName)

		// Extract pod identity by accumulating labels across every RunOpts
		// line — distributeRunOpts may split labels across lines, so a
		// single-line check would silently drop the container.
		var containerName string
		var runOptsLines []string
		if app.RunOptss != nil {
			for _, opt := range app.RunOptss.RunOpts {
				if opt.LineRunOpts != nil {
					runOptsLines = append(runOptsLines, *opt.LineRunOpts)
				}
			}
		}
		if len(runOptsLines) > 0 {
			ns, name, uid, ctr := common.PodIdentityFromRunOpts(runOptsLines)
			if ns == pod.Namespace && name == pod.Name && uid == string(pod.UID) {
				containerName = ctr
				if containerName == "" {
					log.G(ctx).Warnf("App %s has pod labels but no container name label across %d RunOpts lines", appName, len(runOptsLines))
				}
			}
		}

		// If RunOpts labels are missing but the app name matches the CVK
		// naming convention with this pod's UID, use the container index
		// from the app name as a synthetic container name.  This handles
		// apps stuck in DEPLOYED/ACTIVATED states where RunOpts haven't
		// materialised yet.
		if containerName == "" {
			if idx, _, isCVK := common.ParseCVKAppName(appName); isCVK {
				containerName = fmt.Sprintf("container-%d", idx)
				log.G(ctx).Infof("App %s has no RunOpts labels; derived synthetic container name %s from CVK naming convention", appName, containerName)
			}
		}

		if containerName != "" {
			containerToAppID[containerName] = appName
			log.G(ctx).Infof("Found container %s -> app %s", containerName, appName)
		} else {
			log.G(ctx).Warnf("Found app %s with pod UID but couldn't extract container name from %d RunOpts lines",
				appName, len(runOptsLines))
		}
	}

	// Verify all expected containers are found
	expectedCount := len(pod.Spec.Containers)
	foundCount := len(containerToAppID)

	if foundCount != expectedCount {
		missingContainers := []string{}
		for _, container := range pod.Spec.Containers {
			if _, found := containerToAppID[container.Name]; !found {
				missingContainers = append(missingContainers, container.Name)
			}
		}

		if len(missingContainers) > 0 {
			log.G(ctx).Warnf("Container count mismatch for pod %s/%s: expected %d, found %d. Missing: %v",
				pod.Namespace, pod.Name, expectedCount, foundCount, missingContainers)
			return containerToAppID, fmt.Errorf("missing containers: %v", missingContainers)
		}
	}

	log.G(ctx).Infof("Found all %d expected containers for pod %s/%s", foundCount, pod.Namespace, pod.Name)
	return containerToAppID, nil
}

// DeletePod removes all containers in a pod from the device
func (d *XEDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod_namespace": pod.Namespace,
		"pod_name":      pod.Name,
		"pod_uid":       pod.UID,
	}).Debugf("DeletePod request received for pod: %s", pod.Name)

	// Get all containers for this pod
	discoveredContainers, err := d.GetPodContainers(ctx, pod)
	if err != nil {
		log.G(ctx).Warnf("Failed to get all containers for pod %s/%s: %v. Continuing with partial deletion.", pod.Namespace, pod.Name, err)
		// Don't return error here - we'll delete what we found
	}
	deletionTargets := podDeletionTargets(ctx, pod, discoveredContainers)

	deletionErrors := []string{}

	for containerName, appID := range deletionTargets {
		log.G(ctx).Infof("Deleting container %s (app: %s)", containerName, appID)

		err = d.DeleteApp(ctx, appID)
		if err != nil {
			errMsg := fmt.Sprintf("failed to delete container %s (app %s): %v", containerName, appID, err)
			log.G(ctx).Error(errMsg)
			deletionErrors = append(deletionErrors, errMsg)
			continue
		}

		log.G(ctx).Infof("Successfully deleted container %s (app: %s)", containerName, appID)
	}

	if len(deletionErrors) > 0 {
		return fmt.Errorf("encountered %d errors during pod cleanup: %s",
			len(deletionErrors), strings.Join(deletionErrors, "; "))
	}

	log.G(ctx).Infof("Pod %s/%s cleanup successfully completed", pod.Namespace, pod.Name)
	return nil
}

// GetPodStatus retrieves the current status of a pod by querying the device
func (d *XEDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	log.G(ctx).Debug("GetPodStatus request received")

	// While copy-recovery is in progress, return a Waiting status so the VK
	// framework does not interpret the intermediate state as a missing pod and
	// trigger pod deletion.
	if d.isPodRecovering(string(pod.UID)) {
		statusPod := pod.DeepCopy()
		waiting := v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  "PullingImage",
				Message: "Copying image to device flash; this may take several minutes",
			},
		}
		for i := range statusPod.Status.ContainerStatuses {
			statusPod.Status.ContainerStatuses[i].State = waiting
		}
		return statusPod, nil
	}

	// Get containers for this pod. GetPodContainers may return a partial map
	// alongside an error when some expected containers have not yet been
	// created on the device (e.g. multi-container pods mid-deployment, or the
	// two-phase DockerResource flow where alpha is in DEPLOYED while beta has
	// not been installed yet). The partial map is still useful — we surface
	// a Pending pod with the in-flight containers as ContainerCreating
	// instead of returning NotFound, which the VK framework would otherwise
	// interpret as a missing pod and try to recreate.
	discoveredContainers, err := d.GetPodContainers(ctx, pod)
	if err != nil {
		log.G(ctx).Debugf("partial container discovery for pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}

	if len(discoveredContainers) == 0 {
		// All containers are missing. If the pod is still alive in K8s and the
		// driver has the listers it needs, spawn recovery installs and surface a
		// synthesised Pending pod so the VK framework does not loop on
		// NotFound. The next status cycle will pick up the apps via normal
		// discovery once installs land.
		if pod.DeletionTimestamp == nil && d.secretLister != nil && d.configMapLister != nil {
			log.G(ctx).Infof("All containers missing for pod %s/%s; driving full recovery", pod.Namespace, pod.Name)
			d.recoverMissingContainers(ctx, pod, discoveredContainers)
			statusPod := pod.DeepCopy()
			if statusErr := d.GetContainerStatus(ctx, statusPod, discoveredContainers, nil); statusErr != nil {
				return nil, fmt.Errorf("failed to synthesise status for recovering pod: %w", statusErr)
			}
			return statusPod, nil
		}
		log.G(ctx).Warnf("No containers found on device for pod %s/%s", pod.Namespace, pod.Name)
		return nil, fmt.Errorf("no containers found for pod %s/%s", pod.Namespace, pod.Name)
	}

	// Fetch operational data for all apps.
	// A failure here (e.g. device returns 404 while an app is still installing)
	// is transient — treat it the same way ListPods does: continue with an empty
	// map so the pod remains Pending rather than being erroneously deleted by the
	// VK library interpreting a hard error as "pod not found".
	allAppOperData, err := d.GetAppOperationalData(ctx)
	if err != nil {
		log.G(ctx).Warnf("Failed to fetch app operational data for pod %s/%s, will retry: %v", pod.Namespace, pod.Name, err)
		allAppOperData = make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)
	}

	// Filter operational data to only the apps for this pod
	appOperDataMap := make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)
	for containerName, appID := range discoveredContainers {
		if operData, ok := allAppOperData[appID]; ok {
			appOperDataMap[appID] = operData
		} else {
			log.G(ctx).Warnf("App %s for container %s configured but no operational data found", appID, containerName)
		}
	}

	// ── Lifecycle reconciliation ────────────────────────────────────────
	// For each container, build an AppHostingConfig with DesiredState=Running
	// and run a single reconcile pass. This replaces the old ensureAppRunning
	// and can also advance apps stuck in DEPLOYED or ACTIVATED.
	//
	// Skip forward reconciliation when the pod is being deleted
	// (DeletionTimestamp is set). DeletePod is already driving the teardown
	// via its own reconcile loop; interfering here would race against it
	// and potentially re-install an app that was just uninstalled.
	if pod.DeletionTimestamp == nil {
		for containerName, appID := range discoveredContainers {
			imagePath := containerImagePath(pod, containerName)
			appCfg := &AppHostingConfig{
				Metadata: AppHostingMetadata{
					AppName:       appID,
					ContainerName: containerName,
					PodName:       pod.Name,
					PodNamespace:  pod.Namespace,
					PodUID:        string(pod.UID),
				},
				Spec: AppHostingSpec{
					ImagePath:    imagePath,
					DesiredState: AppDesiredStateRunning,
				},
				Status: AppHostingStatus{Phase: AppPhaseConverging},
			}
			d.ReconcileApp(ctx, appCfg)
		}

		// Drive recovery for any spec containers that are missing from the
		// device. Without this, a container that failed to install or was
		// deleted out-of-band would stay in ContainerCreating indefinitely
		// because the loop above only iterates discoveredContainers.
		// Each missing container is installed in a background goroutine so
		// the status path stays non-blocking; tryMarkInstallInFlight prevents
		// duplicate installs from concurrent status cycles.
		d.recoverMissingContainers(ctx, pod, discoveredContainers)
	} else {
		log.G(ctx).Debugf("Pod %s/%s has DeletionTimestamp set; skipping forward reconciliation", pod.Namespace, pod.Name)
	}

	// Create a copy of the pod and update its status
	statusPod := pod.DeepCopy()

	err = d.GetContainerStatus(ctx, statusPod, discoveredContainers, appOperDataMap)
	if err != nil {
		return nil, fmt.Errorf("failed to get container status: %w", err)
	}

	return statusPod, nil
}

// recoverMissingContainers kicks off a background install for any container
// in pod.Spec that is not present in discoveredContainers. This complements
// the synthesised ContainerCreating status emitted by GetContainerStatus —
// without an actual install the container would stay Waiting forever.
//
// Each install is dedup'd by appID via tryMarkInstallInFlight so concurrent
// GetPodStatus calls do not stack duplicate goroutines. CreateAppHostingApp
// is non-blocking from the caller's perspective (runs in its own goroutine
// with a fresh context); a failed install clears the in-flight flag so the
// next status cycle retries.
//
// If the secret/configmap listers haven't been populated yet (cvk just
// started up and DeployPod hasn't run for this pod), recovery is skipped —
// the necessary env-var resolution would fail. The next status cycle will
// retry once listers are wired up.
func (d *XEDriver) recoverMissingContainers(ctx context.Context, pod *v1.Pod, discoveredContainers map[string]string) {
	if pod.DeletionTimestamp != nil {
		log.G(ctx).Debugf("Recovery: pod %s/%s is deleting; skipping missing-container recovery", pod.Namespace, pod.Name)
		return
	}

	var missingNames []string
	for i := range pod.Spec.Containers {
		name := pod.Spec.Containers[i].Name
		if _, found := discoveredContainers[name]; !found {
			missingNames = append(missingNames, name)
		}
	}
	if len(missingNames) == 0 {
		return
	}

	if d.secretLister == nil || d.configMapLister == nil {
		log.G(ctx).Warnf("Cannot drive recovery for missing containers in pod %s/%s: secret/configmap listers not yet initialised (will retry next status cycle)",
			pod.Namespace, pod.Name)
		return
	}

	appConfigs, err := d.ConvertPodToAppConfigs(pod)
	if err != nil {
		log.G(ctx).Warnf("Failed to build recovery app configs for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}

	byContainer := make(map[string]*AppHostingConfig, len(appConfigs))
	for i := range appConfigs {
		byContainer[appConfigs[i].ContainerName()] = &appConfigs[i]
	}

	for _, name := range missingNames {
		cfg, ok := byContainer[name]
		if !ok {
			log.G(ctx).Warnf("Recovery: no app config produced for missing container %s of pod %s/%s",
				name, pod.Namespace, pod.Name)
			continue
		}
		if !d.tryMarkInstallInFlight(cfg.AppName()) {
			log.G(ctx).Debugf("Recovery: install already in flight for container %s (appID=%s)", name, cfg.AppName())
			continue
		}
		// Copy the config — the slice it points into is local to this
		// function and the goroutine outlives the call.
		cfgCopy := *cfg
		log.G(ctx).Infof("Recovery: spawning install for missing container %s (appID=%s) of pod %s/%s",
			name, cfgCopy.AppName(), pod.Namespace, pod.Name)
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			defer d.clearInstallInFlight(cfgCopy.AppName())
			if err := d.CreateAppHostingApp(bgCtx, &cfgCopy); err != nil {
				log.G(bgCtx).Warnf("Recovery: install failed for container %s of pod %s/%s: %v",
					cfgCopy.ContainerName(), pod.Namespace, pod.Name, err)
				return
			}
			log.G(bgCtx).Infof("Recovery: install succeeded for container %s of pod %s/%s",
				cfgCopy.ContainerName(), pod.Namespace, pod.Name)
		}()
	}
}

func podDeletionTargets(ctx context.Context, pod *v1.Pod, discoveredContainers map[string]string) map[string]string {
	targets := make(map[string]string, len(discoveredContainers)+len(pod.Spec.Containers))
	seenAppIDs := make(map[string]struct{}, len(discoveredContainers)+len(pod.Spec.Containers))

	for containerName, appID := range discoveredContainers {
		targets[containerName] = appID
		seenAppIDs[appID] = struct{}{}
	}

	for containerName, appID := range common.GenerateContainerAppIDs(pod) {
		if _, seen := seenAppIDs[appID]; seen {
			continue
		}
		log.G(ctx).Infof("DeletePod: adding deterministic cleanup target for container %s (app: %s)", containerName, appID)
		targets[containerName] = appID
		seenAppIDs[appID] = struct{}{}
	}

	return targets
}

// ListPods discovers all pods currently running on the device by analyzing app configurations.
// It reconstructs skeleton pods from the device state including namespace, name, UID, and container status.
func (d *XEDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
	log.G(ctx).Info("ListPods: discovering pods from device")

	// Get all apps from the device (config endpoint)
	apps, err := d.ListAppHostingApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	// Fetch operational data for all apps
	allAppOperData, err := d.GetAppOperationalData(ctx)
	if err != nil {
		log.G(ctx).Warnf("Failed to fetch app operational data: %v", err)
		// Continue without operational data
		allAppOperData = make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)
	}

	// Build a set of app names already known from the config endpoint
	configAppNames := make(map[string]bool, len(apps))
	for _, app := range apps {
		if app.ApplicationName != nil {
			configAppNames[*app.ApplicationName] = true
		}
	}

	// Check for CVK-managed apps visible only in oper data (e.g. apps in
	// DEPLOYED state where the config endpoint returns an empty body).
	// These apps must still be discoverable so deleteDanglingPods can
	// clean them up.
	for appName := range allAppOperData {
		if configAppNames[appName] {
			continue // already discovered via config
		}
		if !common.IsCVKManagedApp(appName) {
			continue // not a CVK-managed app
		}
		log.G(ctx).Infof("App %s found in oper data but not config data; adding to discovery", appName)
		name := appName
		apps = append(apps, &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App{
			ApplicationName: &name,
		})
	}

	if len(apps) == 0 {
		log.G(ctx).Debug("No apps found on device")
		return []*v1.Pod{}, nil
	}

	// Group apps by pod UID
	podGroups := make(map[string]*podDiscoveryInfo)

	for _, app := range apps {
		if app.ApplicationName == nil {
			continue
		}

		appName := *app.ApplicationName

		// Extract pod metadata from RunOpts labels
		var podNamespace, podName, podUID, containerName string

		if app.RunOptss != nil {
			lines := make([]string, 0, len(app.RunOptss.RunOpts))
			for _, opt := range app.RunOptss.RunOpts {
				if opt.LineRunOpts != nil {
					lines = append(lines, *opt.LineRunOpts)
				}
			}
			podNamespace, podName, podUID, containerName = common.PodIdentityFromRunOpts(lines)
			log.G(ctx).Debugf("Discovery: App %s final extracted namespace=%s, name=%s, uid=%s, container=%s",
				appName, podNamespace, podName, podUID, containerName)
		} else {
			log.G(ctx).Debugf("Discovery: App %s has no RunOptss", appName)
		}

		// If RunOpts labels are missing (e.g. app is in DEPLOYED state and
		// runtime labels haven't materialised yet), fall back to parsing the
		// CVK naming convention to identify CVK-managed apps.  This ensures
		// orphaned apps stuck mid-lifecycle are still discovered and cleaned up.
		if podUID == "" || podName == "" || containerName == "" {
			idx, uid, isCVK := common.ParseCVKAppName(appName)
			if !isCVK {
				log.G(ctx).Debugf("Skipping app %s: not CVK-managed and missing pod metadata", appName)
				continue
			}
			log.G(ctx).Infof("App %s matches CVK naming convention but has no RunOpts labels; using app name to derive metadata", appName)
			podUID = uid
			podName = appName // use the app name as a synthetic pod name
			if podNamespace == "" {
				podNamespace = "default"
			}
			containerName = fmt.Sprintf("container-%d", idx)
		}

		// Group by pod UID
		if _, exists := podGroups[podUID]; !exists {
			podGroups[podUID] = &podDiscoveryInfo{
				namespace:  podNamespace,
				name:       podName,
				uid:        podUID,
				containers: make(map[string]string),
			}
		}

		podGroups[podUID].containers[containerName] = appName
	}

	log.G(ctx).Infof("Discovered %d pods from %d apps", len(podGroups), len(apps))

	// Build skeleton pods with container status
	pods := make([]*v1.Pod, 0, len(podGroups))

	for _, podInfo := range podGroups {
		// Create skeleton pod
		pod := &v1.Pod{}
		pod.Namespace = podInfo.namespace
		pod.Name = podInfo.name
		pod.UID = types.UID(podInfo.uid)

		// Populate Spec.Containers so that GetContainerStatus can match
		// discovered containers against the spec and produce ContainerStatuses.
		// Without this, ContainerStatuses stays empty and the upstream VK
		// considers the pod "not running", causing it to skip DeletePod and
		// force-remove the pod from the API server without cleaning up the
		// app on the device.
		for containerName := range podInfo.containers {
			pod.Spec.Containers = append(pod.Spec.Containers, v1.Container{
				Name: containerName,
			})
		}

		// Filter operational data for this pod's apps
		appOperDataMap := make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)
		for containerName, appID := range podInfo.containers {
			if operData, ok := allAppOperData[appID]; ok {
				appOperDataMap[appID] = operData
			} else {
				log.G(ctx).Debugf("App %s for container %s has no operational data", appID, containerName)
			}
		}

		// Update container status
		err = d.GetContainerStatus(ctx, pod, podInfo.containers, appOperDataMap)
		if err != nil {
			log.G(ctx).Warnf("Failed to get container status for pod %s/%s: %v", podInfo.namespace, podInfo.name, err)
		}

		pods = append(pods, pod)
	}

	log.G(ctx).Infof("Returning %d pods", len(pods))
	return pods, nil
}

// podDiscoveryInfo holds information about a discovered pod
type podDiscoveryInfo struct {
	namespace  string
	name       string
	uid        string
	containers map[string]string // containerName -> appID
}
