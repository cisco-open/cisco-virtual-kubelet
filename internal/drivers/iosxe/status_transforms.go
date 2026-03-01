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

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetContainerStatus maps IOS-XE app operational data to Kubernetes container statuses
func (d *XEDriver) GetContainerStatus(ctx context.Context, pod *v1.Pod,
	discoveredContainers map[string]string,
	appOperData map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App) error {

	now := metav1.Now()

	// Try to discover Pod IP from the first container's network interface
	podIP := d.discoverPodIP(ctx, discoveredContainers, appOperData)

	pod.Status = v1.PodStatus{
		Phase:     v1.PodPending,
		HostIP:    d.config.Address,
		PodIP:     podIP,
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionFalse,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	allReady := true
	anyRunning := false

	for containerName, appID := range discoveredContainers {
		var containerSpec *v1.Container
		for i := range pod.Spec.Containers {
			if pod.Spec.Containers[i].Name == containerName {
				containerSpec = &pod.Spec.Containers[i]
				break
			}
		}

		if containerSpec == nil {
			log.G(ctx).Warnf("Container spec not found for %s (appID: %s)", containerName, appID)
			continue
		}

		operData := appOperData[appID]

		containerStatus := v1.ContainerStatus{
			Name:        containerName,
			Image:       containerSpec.Image,
			ImageID:     containerSpec.Image,
			ContainerID: fmt.Sprintf("cisco://%s", appID),
			Ready:       false,
		}

		if operData != nil && operData.Details != nil && operData.Details.State != nil {
			state := *operData.Details.State

			switch state {
			case "RUNNING":
				containerStatus.State = v1.ContainerState{
					Running: &v1.ContainerStateRunning{
						StartedAt: now,
					},
				}
				containerStatus.Ready = true
				anyRunning = true
			case "DEPLOYED", "ACTIVATED":
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "ContainerCreating",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			case "STOPPED", "Uninstalled":
				containerStatus.State = v1.ContainerState{
					Terminated: &v1.ContainerStateTerminated{
						ExitCode:   0,
						Reason:     "Completed",
						FinishedAt: now,
					},
				}
				allReady = false
			default:
				containerStatus.State = v1.ContainerState{
					Waiting: &v1.ContainerStateWaiting{
						Reason:  "Unknown",
						Message: fmt.Sprintf("App state: %s", state),
					},
				}
				allReady = false
			}

			log.G(ctx).Infof("Container %s (app: %s) state: %s, ready: %v",
				containerName, appID, state, containerStatus.Ready)
		} else {
			containerStatus.State = v1.ContainerState{
				Waiting: &v1.ContainerStateWaiting{
					Reason:  "ContainerCreating",
					Message: "No operational data available",
				},
			}
			allReady = false
			log.G(ctx).Warnf("No operational data for container %s (app: %s)", containerName, appID)
		}

		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}

	if anyRunning && allReady {
		pod.Status.Phase = v1.PodRunning
		for i := range pod.Status.Conditions {
			if pod.Status.Conditions[i].Type == v1.PodReady ||
				pod.Status.Conditions[i].Type == v1.PodInitialized {
				pod.Status.Conditions[i].Status = v1.ConditionTrue
			}
		}
	} else if anyRunning {
		pod.Status.Phase = v1.PodRunning
	}

	log.G(ctx).Infof("Pod %s/%s status: Phase=%s, Containers=%d/%d ready",
		pod.Namespace, pod.Name, pod.Status.Phase,
		len(pod.Status.ContainerStatuses), len(pod.Spec.Containers))

	return nil
}
