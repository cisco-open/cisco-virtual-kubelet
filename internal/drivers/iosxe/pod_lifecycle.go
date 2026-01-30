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

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// DeployPod creates and deploys all containers in a pod to the device
func (d *XEDriver) DeployPod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debug("Pod DeployContainer request received")

	err := d.CreatePodApps(ctx, pod)
	if err != nil {
		return fmt.Errorf("app deployment failed: %v", err)
	}

	return nil
}

// UpdatePod handles pod update requests
func (d *XEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).Info("Pod UpdateContainer request received")
	return nil
}

// DeletePod removes all containers in a pod from the device
func (d *XEDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod,
	}).Debugf("DeletePod request received for pod: %s", pod.Name)

	discoveredContainers, err := d.DiscoverPodContainersOnDevice(ctx, pod)
	if err != nil {
		log.G(ctx).Errorf("Failed to discover containers for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return fmt.Errorf("failed to discover containers for pod: %w", err)
	}

	foundCount := len(discoveredContainers)
	expectedCount := len(pod.Spec.Containers)

	log.G(ctx).Infof("Found %d containers on device for pod %s/%s (expected %d)",
		foundCount, pod.Namespace, pod.Name, expectedCount)

	if foundCount != expectedCount {
		log.G(ctx).Errorf("Container count mismatch for pod %s/%s: expected %d, found %d",
			pod.Namespace, pod.Name, expectedCount, foundCount)

		for _, container := range pod.Spec.Containers {
			if _, found := discoveredContainers[container.Name]; !found {
				log.G(ctx).Errorf("Container %s not found on device", container.Name)
			}
		}
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

	discoveredContainers, err := d.DiscoverPodContainersOnDevice(ctx, pod)
	if err != nil {
		log.G(ctx).Debugf("failed to discover containers: %v", err)
		return nil, fmt.Errorf("apps for pod %s/%s not found on device", pod.Namespace, pod.Name)
	}

	if len(discoveredContainers) == 0 {
		log.G(ctx).Warnf("No containers found on device for pod %s/%s", pod.Namespace, pod.Name)
		return nil, fmt.Errorf("no containers found for pod %s/%s", pod.Namespace, pod.Name)
	}

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	err = d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("bulk status fetch failed: %w", err)
	}

	appOperDataMap := make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App)

	for _, appID := range discoveredContainers {
		if operData, ok := root.App[appID]; ok {
			appOperDataMap[appID] = operData
		} else {
			log.G(ctx).Warnf("App %s configured but no operational data found", appID)
		}
	}

	d.debugLogJson(ctx, root)
	statusPod := pod.DeepCopy()

	err = d.GetContainerStatus(ctx, statusPod, discoveredContainers, appOperDataMap)
	if err != nil {
		return nil, fmt.Errorf("failed to get container status: %w", err)
	}

	return statusPod, nil
}

// ListPods returns all pods currently running on the device
func (d *XEDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	res := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}

	err := d.client.Get(ctx, path, res, d.unmarshaller)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app oper data: %w", err)
	}

	pods := []*v1.Pod{}
	return pods, nil
}
