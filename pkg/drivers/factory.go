package drivers

import (
	"context"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/pkg/config"
	"github.com/cisco/virtual-kubelet-cisco/pkg/drivers/fake"
	"github.com/cisco/virtual-kubelet-cisco/pkg/drivers/iosxe"

	v1 "k8s.io/api/core/v1"
)

func NewDriver(ctx context.Context, config *config.DeviceConfig) (CiscoDeviceDriver, error) {

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

type CiscoDeviceDriver interface {
	GetDeviceResources(ctx context.Context) (*v1.ResourceList, error)
	DeployContainer(ctx context.Context, pod *v1.Pod) error
	UpdateContainer(ctx context.Context, pod *v1.Pod) error
	StopAndRemoveContainer(ctx context.Context, pod *v1.Pod) error
	GetContainerStatus(ctx context.Context, namespace, name string) (*v1.Pod, error)
	ListContainers(ctx context.Context) ([]*v1.Pod, error)
}
