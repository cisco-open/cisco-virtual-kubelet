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
)

// DeployPod creates and deploys all containers in a pod to the device
func (d *XEDriver) DeployPod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debug("Pod DeployContainer request received")

	log.G(ctx).Infof("Deploying pod: %s/%s", pod.Namespace, pod.Name)

	// Convert pod spec to app hosting configurations
	appConfigs, err := d.ConvertPodToAppConfigs(pod)
	if err != nil {
		return fmt.Errorf("failed to convert pod to app configs: %w", err)
	}

	// Deploy each app configuration sequentially, waiting for each to reach
	// DEPLOYED before starting the next.  IOS-XE cannot reliably handle
	// concurrent install operations and may silently fail.
	for i := range appConfigs {
		appConfig := &appConfigs[i]
		log.G(ctx).Infof("Deploying app: %s for container: %s", appConfig.AppName(), appConfig.ContainerName())

		err = d.CreateAppHostingApp(ctx, appConfig)
		if err != nil {
			return fmt.Errorf("failed to deploy app for container %s: %w", appConfig.ContainerName(), err)
		}

		// Wait for the device to finish installing before submitting the next app.
		if err := d.WaitForAppStatus(ctx, appConfig.AppName(), "DEPLOYED", 120*time.Second); err != nil {
			return fmt.Errorf("app %s did not reach DEPLOYED status: %w", appConfig.AppName(), err)
		}

		log.G(ctx).Infof("Successfully deployed app %s for container %s", appConfig.AppName(), appConfig.ContainerName())
	}

	log.G(ctx).Infof("Successfully deployed all apps for pod: %s/%s", pod.Namespace, pod.Name)
	return nil
}

// UpdatePod handles pod update requests by performing a delete-and-redeploy cycle.
// IOS-XE AppHosting does not support in-place updates to running applications;
// changes to image, resources, or environment require a full teardown and reinstall.
func (d *XEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).Infof("UpdatePod: delete-and-redeploy for pod %s/%s", pod.Namespace, pod.Name)

	if err := d.DeletePod(ctx, pod); err != nil {
		// Log but don't block — partial cleanup is acceptable.
		// DeployPod's CreateAppHostingApp will fail on conflict if
		// any app still exists, which is a clearer error than aborting here.
		log.G(ctx).Warnf("UpdatePod: cleanup had errors (will attempt redeploy): %v", err)
	}

	return d.DeployPod(ctx, pod)
}

// GetPodContainers retrieves all containers belonging to a specific pod from the device.
// It queries all apps on the device, filters them by pod UID and labels, and verifies
// that all expected containers are found.
// Returns a map of containerName -> appID and an error if verification fails.
func (d *XEDriver) GetPodContainers(ctx context.Context, pod *v1.Pod) (map[string]string, error) {
	log.G(ctx).Debugf("Getting containers for pod: %s/%s", pod.Namespace, pod.Name)

	// Get all apps from the device
	apps, err := d.ListAppHostingApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	// Clean the pod UID (remove hyphens) as that's how it appears in app names
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")

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

		// Extract container name from RunOpts labels
		var containerName string
		var runOptsLine string

		if app.RunOptss != nil {
			for _, opt := range app.RunOptss.RunOpts {
				if opt.LineRunOpts != nil {
					line := *opt.LineRunOpts
					runOptsLine = line

					log.G(ctx).Debugf("App %s RunOpts: %s", appName, line)

					// Verify this app belongs to our pod by checking all pod labels
					if strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodName, pod.Name)) &&
						strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodNamespace, pod.Namespace)) &&
						strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodUID, pod.UID)) {

						// Extract the container name from the label
						containerName = common.ExtractContainerNameFromLabels(line)

						if containerName != "" {
							log.G(ctx).Debugf("Extracted container name: %s from app %s", containerName, appName)
						} else {
							log.G(ctx).Warnf("App %s has pod labels but no container name label in line: %s", appName, line)
						}
						break
					}
				}
			}
		}

		if containerName != "" {
			containerToAppID[containerName] = appName
			log.G(ctx).Infof("Found container %s -> app %s", containerName, appName)
		} else {
			log.G(ctx).Warnf("Found app %s with pod UID but couldn't extract container name from labels. RunOpts: %s",
				appName, runOptsLine)
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
		"pod": pod,
	}).Debugf("DeletePod request received for pod: %s", pod.Name)

	// Get all containers for this pod
	discoveredContainers, err := d.GetPodContainers(ctx, pod)
	if err != nil {
		log.G(ctx).Warnf("Failed to get all containers for pod %s/%s: %v. Continuing with partial deletion.", pod.Namespace, pod.Name, err)
		// Don't return error here - we'll delete what we found
	}

	deletionErrors := []string{}

	for containerName, appID := range discoveredContainers {
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

	// Get containers for this pod
	discoveredContainers, err := d.GetPodContainers(ctx, pod)
	if err != nil {
		log.G(ctx).Debugf("failed to get pod containers: %v", err)
		return nil, fmt.Errorf("apps for pod %s/%s not found on device", pod.Namespace, pod.Name)
	}

	if len(discoveredContainers) == 0 {
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

// ListPods discovers all pods currently running on the device by analyzing app configurations.
// It reconstructs skeleton pods from the device state including namespace, name, UID, and container status.
func (d *XEDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
	log.G(ctx).Info("ListPods: discovering pods from device")

	// Get all apps from the device
	apps, err := d.ListAppHostingApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	if len(apps) == 0 {
		log.G(ctx).Debug("No apps found on device")
		return []*v1.Pod{}, nil
	}

	// Fetch operational data for all apps
	allAppOperData, err := d.GetAppOperationalData(ctx)
	if err != nil {
		log.G(ctx).Warnf("Failed to fetch app operational data: %v", err)
		// Continue without operational data
		allAppOperData = make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)
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
			for _, opt := range app.RunOptss.RunOpts {
				if opt.LineRunOpts != nil {
					line := *opt.LineRunOpts

					// Extract pod labels
					podNamespace = common.ExtractLabelValue(line, common.LabelPodNamespace)
					podName = common.ExtractLabelValue(line, common.LabelPodName)
					podUID = common.ExtractLabelValue(line, common.LabelPodUID)
					containerName = common.ExtractContainerNameFromLabels(line)
					break
				}
			}
		}

		// Skip apps that don't have pod metadata
		if podUID == "" || podName == "" || containerName == "" {
			log.G(ctx).Debugf("Skipping app %s: missing pod metadata", appName)
			continue
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
