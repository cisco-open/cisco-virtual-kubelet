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

package fake

import (
	"context"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

type FAKEDriver struct {
	config *config.DeviceConfig
	pods   []v1.Pod
}

func NewAppHostingDriver(ctx context.Context, config *config.DeviceConfig) (*FAKEDriver, error) {
	log.G(ctx).Info("Initialise new FAKE driver")
	return &FAKEDriver{
		config: config,
		pods:   []v1.Pod{},
	}, nil

}

func (d *FAKEDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {

	log.G(ctx).Info("Pod GetDeviceResources request received")
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
		v1.ResourcePods:    resource.MustParse("16"),
	}

	return &resources, nil
}

func (d *FAKEDriver) DeployPod(ctx context.Context, pod *v1.Pod) error {
	containerAppIDs := common.GenerateContainerAppIDs(pod)

	log.G(ctx).WithFields(log.Fields{
		"namespace":  pod.Namespace,
		"pod":        pod.Name,
		"containers": len(containerAppIDs),
	}).Info("Pod DeployContainer request received")

	for containerName, appID := range containerAppIDs {
		log.G(ctx).WithFields(log.Fields{
			"container":   containerName,
			"appHostName": appID,
		}).Info("Generated appID for container")
	}

	now := metav1.Now()
	pod.Status = v1.PodStatus{
		Phase:     v1.PodRunning,
		HostIP:    "1.1.1.2",
		PodIP:     "1.1.1.1",
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	for _, container := range pod.Spec.Containers {
		containerStatus := v1.ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
			Ready: true,
			State: v1.ContainerState{
				Running: &v1.ContainerStateRunning{
					StartedAt: metav1.Now(),
				},
			},
			ContainerID: string(uuid.NewUUID()),
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}
	d.pods = append(d.pods, *pod)
	log.G(ctx).WithFields(log.Fields{
		"pod": pod.Name,
	}).Info("Stored pod in FAKEDriver")
	return nil
}

func (d *FAKEDriver) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	// TODO
	log.G(ctx).Info("Pod UpdateContainer request received")
	return nil
}

func (d *FAKEDriver) DeletePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod.Name,
	}).Info("Pod DeletePod request received")
	return nil
}

func (d *FAKEDriver) GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	// TODO
	log.G(ctx).WithFields(log.Fields{
		"namespace": pod.Namespace,
		"pod":       pod.Name,
	}).Info("Looking for pod")
	statusPod := common.FindPod(d.pods, pod.Namespace, pod.Name)
	if statusPod != nil {
		return statusPod, nil
	}

	log.G(ctx).Info("FAKEDriver couldn't find pod")
	return nil, fmt.Errorf("could not find pod: %s, %s", pod.Namespace, pod.Name)
}

func (d *FAKEDriver) ListPods(ctx context.Context) ([]*v1.Pod, error) {
	// TODO
	log.G(ctx).Info("Pod ListContainers request received")
	return nil, nil
}
