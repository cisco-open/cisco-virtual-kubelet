package drivers

import (
	"context"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/fake"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"

	v1 "k8s.io/api/core/v1"
)

func NewDriver(ctx context.Context, config *config.DeviceConfig) (CiscoKubernetesDeviceDriver, error) {

	switch config.Driver {
	case "FAKE":
		return fake.NewAppHostingDriver(ctx, config)
	case "XE":
		return iosxe.NewAppHostingDriver(ctx, config)
	case "XR":
		return nil, fmt.Errorf("unsupported device type")
	default:
		return nil, fmt.Errorf("unsupported device type")
	}
}

type CiscoKubernetesDeviceDriver interface {
	GetDeviceResources(ctx context.Context) (*v1.ResourceList, error)
	DeployPod(ctx context.Context, pod *v1.Pod) error
	UpdatePod(ctx context.Context, pod *v1.Pod) error
	StopAndRemovePod(ctx context.Context, pod *v1.Pod) error
	GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error)
	ListPods(ctx context.Context) ([]*v1.Pod, error)
}
