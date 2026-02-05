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

package drivers

import (
	"context"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
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
	GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error)
	DeployPod(ctx context.Context, pod *v1.Pod) error
	UpdatePod(ctx context.Context, pod *v1.Pod) error
	DeletePod(ctx context.Context, pod *v1.Pod) error
	GetPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error)
	ListPods(ctx context.Context) ([]*v1.Pod, error)
}
