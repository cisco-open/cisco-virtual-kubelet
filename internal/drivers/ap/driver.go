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

package ap

import (
	"context"
	"fmt"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const defaultMaxPods int32 = 8

// Driver deploys prebuilt IOx app packages directly to a Catalyst Access Point.
// The address, credentials, and optional TLS settings identify the AP itself.
// A wireless LAN controller is a separate prerequisite and is not contacted.
type Driver struct {
	config *v1alpha1.DeviceSpec
	client *ioxClient
}

var _ drivers.CiscoKubernetesDeviceDriver = (*Driver)(nil)

func NewDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*Driver, error) {
	client, err := newIOxClient(spec)
	if err != nil {
		return nil, err
	}
	d := &Driver{config: spec, client: client}
	if _, err := client.listApps(ctx); err != nil {
		return nil, fmt.Errorf("AP IOx connectivity check failed: %w", err)
	}
	return d, nil
}

func (d *Driver) GetDeviceResources(context.Context) (*corev1.ResourceList, error) {
	maxPods := d.config.MaxPods
	if maxPods <= 0 {
		maxPods = defaultMaxPods
	}
	resources := corev1.ResourceList{
		corev1.ResourcePods: *resource.NewQuantity(int64(maxPods), resource.DecimalSI),
	}
	return &resources, nil
}

func (d *Driver) GetDeviceInfo(context.Context) (*common.DeviceInfo, error) {
	return &common.DeviceInfo{
		Hostname:        d.config.Address,
		ProductID:       "Catalyst Access Point (IOx)",
		SoftwareVersion: "unknown",
	}, nil
}

func (d *Driver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	if _, err := d.client.listApps(ctx); err != nil {
		return nil, err
	}
	return &common.AppHostingOperData{IoxEnabled: true}, nil
}

func (d *Driver) validatePod(pod *corev1.Pod) error {
	if pod == nil {
		return fmt.Errorf("AP app-hosting: nil pod")
	}
	if len(pod.Spec.Containers) == 0 {
		return fmt.Errorf("AP app-hosting: pod has no containers")
	}
	if len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 {
		return fmt.Errorf("AP app-hosting: init and ephemeral containers are not supported")
	}
	if len(pod.Spec.Volumes) != 0 {
		return fmt.Errorf("AP app-hosting: Kubernetes volumes are not supported; package required files into the IOx app")
	}
	if len(pod.Spec.ImagePullSecrets) != 0 {
		return fmt.Errorf("AP app-hosting: imagePullSecrets are not supported for IOx package artifacts")
	}
	if pod.Spec.HostNetwork || pod.Spec.HostPID || pod.Spec.HostIPC || (pod.Spec.ShareProcessNamespace != nil && *pod.Spec.ShareProcessNamespace) {
		return fmt.Errorf("AP app-hosting: Kubernetes host namespaces are not supported")
	}
	if pod.Spec.RestartPolicy != "" && pod.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		return fmt.Errorf("AP app-hosting: only Kubernetes Always restart policy is supported")
	}
	for _, container := range pod.Spec.Containers {
		if _, err := packageURL(container.Image); err != nil {
			return fmt.Errorf("container %s image: %w", container.Name, err)
		}
		if len(container.Command) != 0 || len(container.Args) != 0 || len(container.Env) != 0 || container.WorkingDir != "" ||
			len(container.Ports) != 0 || len(container.VolumeMounts) != 0 || len(container.Resources.Requests) != 0 ||
			len(container.Resources.Limits) != 0 || container.Lifecycle != nil || container.LivenessProbe != nil ||
			container.ReadinessProbe != nil || container.StartupProbe != nil || container.SecurityContext != nil ||
			container.Stdin || container.TTY {
			return fmt.Errorf("AP app-hosting: container %s uses Kubernetes runtime settings that IOx packages must define themselves", container.Name)
		}
	}
	return nil
}

func validateAppID(id string) error {
	if _, _, ok := common.ParseCVKAppName(id); !ok {
		return fmt.Errorf("AP app-hosting: invalid CVK IOx application ID")
	}
	return nil
}

func appState(state string) string { return strings.ToUpper(strings.TrimSpace(state)) }
