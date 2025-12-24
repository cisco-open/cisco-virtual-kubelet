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

func (d *FAKEDriver) DeployContainer(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"namespace": pod.Namespace,
		"pod":       pod.Name,
	}).Info("Pod DeployContainer request received")

	// Update pod status
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

func (d *FAKEDriver) UpdateContainer(ctx context.Context, pod *v1.Pod) error {
	// TODO
	log.G(ctx).Info("Pod UpdateContainer request received")
	return nil
}

func (d *FAKEDriver) StopAndRemoveContainer(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithFields(log.Fields{
		"pod": pod.Name,
	}).Info("Pod StopAndRemoveContainer request received")
	return nil
}

func (d *FAKEDriver) GetContainerStatus(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	// TODO
	log.G(ctx).WithFields(log.Fields{
		"namespace": namespace,
		"pod":       name,
	}).Info("Looking for pod")
	pod := common.FindPod(d.pods, name, namespace)
	if pod != nil {
		return pod, nil
	}

	log.G(ctx).Info("FAKEDriver couldn't fnd pod")
	return nil, fmt.Errorf("could not find pod: %s, %s", namespace, name)
}

func (d *FAKEDriver) ListContainers(ctx context.Context) ([]*v1.Pod, error) {
	// TODO
	log.G(ctx).Info("Pod ListContainers request received")
	return nil, nil
}
