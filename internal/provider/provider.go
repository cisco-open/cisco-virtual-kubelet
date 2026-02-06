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

package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type AppHostingProvider struct {
	ctx             context.Context
	appCfg          *config.Config
	driver          drivers.CiscoKubernetesDeviceDriver
	podsLister      corev1listers.PodLister
	configMapLister corev1listers.ConfigMapLister
	secretLister    corev1listers.SecretLister
	serviceLister   corev1listers.ServiceLister
}

func NewAppHostingProvider(
	ctx context.Context,
	appCfg *config.Config,
	vkCfg nodeutil.ProviderConfig,
) (*AppHostingProvider, error) {

	d, err := drivers.NewDriver(ctx, &appCfg.Device)
	if err != nil {
		return nil, fmt.Errorf("driver assignment failed: %v", err)
	}
	return &AppHostingProvider{
		ctx:             ctx,
		appCfg:          appCfg,
		driver:          d,
		podsLister:      vkCfg.Pods,
		configMapLister: vkCfg.ConfigMaps,
		secretLister:    vkCfg.Secrets,
		serviceLister:   vkCfg.Services,
	}, nil
}

func (p *AppHostingProvider) GetCapacity(ctx context.Context) (v1.ResourceList, error) {
	resources, err := p.driver.GetDeviceResources(p.ctx)
	return *resources, err
}

func (p *AppHostingProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	// Deploy the container. This MUST be idempotent
	// In future we can range over the pod.spec.containers
	if err := p.driver.DeployPod(p.ctx, pod); err != nil {
		return errdefs.AsInvalidInput(err)
	}

	return nil
}

func (p *AppHostingProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	// IOS-XE/XR may have limited "Update" support (e.g., changing resources requires a restart)
	return p.driver.UpdatePod(p.ctx, pod)
}

func (p *AppHostingProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	return p.driver.DeletePod(p.ctx, pod)
}

func (p *AppHostingProvider) GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error) {

	log.G(p.ctx).WithFields(log.Fields{
		"name":      name,
		"namespace": namespace,
	}).Debug("Running GetPod:")

	// Fetch pod spec from informer cache (desired state)
	pod, err := p.podsLister.Pods(namespace).Get(name)
	if err != nil {
		return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found: %v", namespace, name, err))
	}

	// Get actual status from Cisco device
	return p.driver.GetPodStatus(p.ctx, pod)
}

func (p *AppHostingProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {

	log.G(p.ctx).WithFields(log.Fields{
		"name":      name,
		"namespace": namespace,
	}).Debug("Calling driver GetPodStatus:")

	// Fetch pod spec from informer cache (desired state)
	pod, err := p.podsLister.Pods(namespace).Get(name)
	if err != nil {
		return nil, errdefs.NotFound(fmt.Sprintf("pod %s/%s not found: %v", namespace, name, err))
	}

	// Get actual status from Cisco device
	statusPod, err := p.driver.GetPodStatus(p.ctx, pod)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}

	return &statusPod.Status, nil
}

func (p *AppHostingProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	pods, err := p.driver.ListPods(p.ctx)
	if err != nil {
		return nil, errdefs.AsNotFound(err)
	}

	return pods, nil
}

func (p *AppHostingProvider) AttachToContainer(ctx context.Context, namespace, podName, containerName string, attach api.AttachIO) error {
	// log.G(ctx).Infof("Attaching to container %s in pod %s/%s", containerName, namespace, podName)

	// For Cisco IOx containers, attachment is limited
	// We can simulate it by providing a shell prompt
	if attach.Stdout() != nil {
		attach.Stdout().Write([]byte("Cisco IOx container attachment not fully supported\n"))
	}

	return nil
}

// NOT YET IMPLEMENTED

// GetContainerLogs implements nodeutil.Provider.
func (p *AppHostingProvider) GetContainerLogs(ctx context.Context, namespace string, podName string, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	panic("unimplemented")
}

// GetMetricsResource implements nodeutil.Provider.
func (p *AppHostingProvider) GetMetricsResource(context.Context) ([]*io_prometheus_client.MetricFamily, error) {
	panic("unimplemented")
}

// GetStatsSummary implements nodeutil.Provider.
func (p *AppHostingProvider) GetStatsSummary(context.Context) (*statsv1alpha1.Summary, error) {
	panic("unimplemented")
}

// PortForward implements nodeutil.Provider.
func (p *AppHostingProvider) PortForward(ctx context.Context, namespace string, pod string, port int32, stream io.ReadWriteCloser) error {
	panic("unimplemented")
}

// RunInContainer implements nodeutil.Provider.
func (p *AppHostingProvider) RunInContainer(ctx context.Context, namespace string, podName string, containerName string, cmd []string, attach api.AttachIO) error {
	panic("unimplemented")
}

// AppHostingNode implements node.NodeProvider for proper heartbeat management.
// This follows the NaiveNodeProvider pattern from virtual-kubelet.
// The library's NodeController handles periodic heartbeat updates automatically.
type AppHostingNode struct {
	appCfg *config.Config
}

// NewAppHostingNode creates a new AppHostingNode
func NewAppHostingNode(
	ctx context.Context,
	appCfg *config.Config,
	vkCfg nodeutil.ProviderConfig,
) (*AppHostingNode, error) {
	return &AppHostingNode{
		appCfg: appCfg,
	}, nil
}

// Ping implements node.NodeProvider.
// Called periodically by the library's nodePingController.
// Returning nil indicates the node is healthy.
func (a *AppHostingNode) Ping(ctx context.Context) error {
	return nil
}

// NotifyNodeStatus implements node.NodeProvider.
// Called once at startup to allow async node status updates.
// We use this to update node info with device details after driver initialization.
func (a *AppHostingNode) NotifyNodeStatus(ctx context.Context, cb func(*v1.Node)) {
	if a.appCfg == nil {
		return
	}

	// Create a temporary driver to fetch device info
	// Note: NewDriver calls CheckConnection internally, which populates deviceInfo
	driver, err := drivers.NewDriver(ctx, &a.appCfg.Device)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Failed to create driver for node status update")
		return
	}

	deviceInfo, err := driver.GetDeviceInfo(ctx)
	if err != nil || deviceInfo == nil {
		return
	}

	// Only update if we have actual device info
	if deviceInfo.SerialNumber == "" {
		return
	}

	// Determine node internal IP: config value or device address
	nodeInternalIP := a.appCfg.Kubelet.NodeInternalIP
	if nodeInternalIP == "" {
		nodeInternalIP = a.appCfg.Device.Address
	}

	log.G(ctx).Infof("Updating node status with device info, InternalIP=%s", nodeInternalIP)

	// Create a node update with device info and addresses
	nodeUpdate := &v1.Node{
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				MachineID:       deviceInfo.SerialNumber,
				SystemUUID:      deviceInfo.SerialNumber,
				KernelVersion:   deviceInfo.SoftwareVersion,
				OSImage:         "IOS-XE",
				Architecture:    deviceInfo.ProductID,
				OperatingSystem: "Cisco",
			},
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeInternalIP,
					Address: nodeInternalIP,
				},
			},
		},
	}

	cb(nodeUpdate)
}
