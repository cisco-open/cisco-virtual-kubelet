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

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	statsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

type AppHostingProvider struct {
	ctx             context.Context
	deviceSpec      *v1alpha1.DeviceSpec
	driver          drivers.CiscoKubernetesDeviceDriver
	podsLister      corev1listers.PodLister
	configMapLister corev1listers.ConfigMapLister
	secretLister    corev1listers.SecretLister
	serviceLister   corev1listers.ServiceLister
}

func NewAppHostingProvider(
	ctx context.Context,
	deviceSpec *v1alpha1.DeviceSpec,
	vkCfg nodeutil.ProviderConfig,
) (*AppHostingProvider, error) {

	d, err := drivers.NewDriver(ctx, deviceSpec)
	if err != nil {
		return nil, fmt.Errorf("driver assignment failed: %v", err)
	}
	return &AppHostingProvider{
		ctx:             ctx,
		deviceSpec:      deviceSpec,
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
// Returns node and pod resource usage statistics for observability tools like Splunk.
func (p *AppHostingProvider) GetStatsSummary(ctx context.Context) (*statsv1alpha1.Summary, error) {
	log.G(ctx).Debug("GetStatsSummary called")

	nodeStats, err := p.buildNodeStats(ctx)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Failed to build node stats")
	}

	podStatsList, err := p.buildPodStats(ctx)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Failed to build pod stats")
	}

	return &statsv1alpha1.Summary{
		Node: nodeStats,
		Pods: podStatsList,
	}, nil
}

// buildNodeStats creates NodeStats from driver data
func (p *AppHostingProvider) buildNodeStats(ctx context.Context) (statsv1alpha1.NodeStats, error) {
	nodeName := GetNodeName("", p.deviceSpec.Address)
	now := metav1.Now()

	nodeStats := statsv1alpha1.NodeStats{
		NodeName:  nodeName,
		StartTime: now,
	}

	driverStats, err := p.driver.GetNodeStats(ctx)
	if err != nil {
		return nodeStats, err
	}

	if driverStats != nil {
		nodeStats.StartTime = metav1.NewTime(driverStats.Timestamp)

		if driverStats.CPUUsageNanoCores > 0 {
			cpuNano := driverStats.CPUUsageNanoCores
			nodeStats.CPU = &statsv1alpha1.CPUStats{
				Time:           now,
				UsageNanoCores: &cpuNano,
			}
		}

		if driverStats.MemoryUsageBytes > 0 || driverStats.MemoryAvailableBytes > 0 {
			nodeStats.Memory = &statsv1alpha1.MemoryStats{
				Time:            now,
				UsageBytes:      &driverStats.MemoryUsageBytes,
				AvailableBytes:  &driverStats.MemoryAvailableBytes,
				WorkingSetBytes: &driverStats.MemoryWorkingSetBytes,
			}
		}

		if driverStats.FsCapacityBytes > 0 {
			nodeStats.Fs = &statsv1alpha1.FsStats{
				Time:           now,
				CapacityBytes:  &driverStats.FsCapacityBytes,
				UsedBytes:      &driverStats.FsUsedBytes,
				AvailableBytes: &driverStats.FsAvailableBytes,
			}
		}

		if driverStats.NetworkRxBytes > 0 || driverStats.NetworkTxBytes > 0 {
			nodeStats.Network = &statsv1alpha1.NetworkStats{
				Time: now,
				InterfaceStats: statsv1alpha1.InterfaceStats{
					Name:    "eth0",
					RxBytes: &driverStats.NetworkRxBytes,
					TxBytes: &driverStats.NetworkTxBytes,
				},
			}
		}
	}

	return nodeStats, nil
}

// buildPodStats creates PodStats for all pods managed by this provider
func (p *AppHostingProvider) buildPodStats(ctx context.Context) ([]statsv1alpha1.PodStats, error) {
	pods, err := p.GetPods(ctx)
	if err != nil {
		return nil, err
	}

	podStatsList := make([]statsv1alpha1.PodStats, 0, len(pods))

	for _, pod := range pods {
		podStats, err := p.buildSinglePodStats(ctx, pod)
		if err != nil {
			log.G(ctx).WithError(err).WithField("pod", pod.Name).Warn("Failed to build pod stats")
			continue
		}
		podStatsList = append(podStatsList, podStats)
	}

	return podStatsList, nil
}

// buildSinglePodStats creates PodStats for a single pod
func (p *AppHostingProvider) buildSinglePodStats(ctx context.Context, pod *v1.Pod) (statsv1alpha1.PodStats, error) {
	now := metav1.Now()

	podStats := statsv1alpha1.PodStats{
		PodRef: statsv1alpha1.PodReference{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       string(pod.UID),
		},
		StartTime: now,
	}

	driverStats, err := p.driver.GetPodStats(ctx, pod)
	if err != nil {
		return podStats, err
	}

	if driverStats != nil {
		podStats.StartTime = metav1.NewTime(driverStats.Timestamp)

		if driverStats.CPUUsageNanoCores > 0 {
			cpuNano := driverStats.CPUUsageNanoCores
			podStats.CPU = &statsv1alpha1.CPUStats{
				Time:           now,
				UsageNanoCores: &cpuNano,
			}
		}

		if driverStats.MemoryUsageBytes > 0 {
			podStats.Memory = &statsv1alpha1.MemoryStats{
				Time:            now,
				UsageBytes:      &driverStats.MemoryUsageBytes,
				WorkingSetBytes: &driverStats.MemoryWorkingSetBytes,
			}
		}

		if driverStats.NetworkRxBytes > 0 || driverStats.NetworkTxBytes > 0 {
			podStats.Network = &statsv1alpha1.NetworkStats{
				Time: now,
				InterfaceStats: statsv1alpha1.InterfaceStats{
					Name:    "eth0",
					RxBytes: &driverStats.NetworkRxBytes,
					TxBytes: &driverStats.NetworkTxBytes,
				},
			}
		}

		containerStatsList := make([]statsv1alpha1.ContainerStats, 0, len(driverStats.Containers))
		for _, cs := range driverStats.Containers {
			containerStats := statsv1alpha1.ContainerStats{
				Name:      cs.Name,
				StartTime: metav1.NewTime(cs.Timestamp),
			}

			if cs.CPUUsageNanoCores > 0 {
				cpuNano := cs.CPUUsageNanoCores
				containerStats.CPU = &statsv1alpha1.CPUStats{
					Time:           now,
					UsageNanoCores: &cpuNano,
				}
			}

			if cs.MemoryUsageBytes > 0 {
				containerStats.Memory = &statsv1alpha1.MemoryStats{
					Time:            now,
					UsageBytes:      &cs.MemoryUsageBytes,
					WorkingSetBytes: &cs.MemoryWorkingSetBytes,
				}
			}

			if cs.FsCapacityBytes > 0 || cs.FsUsedBytes > 0 {
				containerStats.Rootfs = &statsv1alpha1.FsStats{
					Time:          now,
					CapacityBytes: &cs.FsCapacityBytes,
					UsedBytes:     &cs.FsUsedBytes,
				}
			}

			containerStatsList = append(containerStatsList, containerStats)
		}
		podStats.Containers = containerStatsList
	}

	return podStats, nil
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
	deviceSpec *v1alpha1.DeviceSpec
}

// NewAppHostingNode creates a new AppHostingNode
func NewAppHostingNode(
	ctx context.Context,
	deviceSpec *v1alpha1.DeviceSpec,
	vkCfg nodeutil.ProviderConfig,
) (*AppHostingNode, error) {
	return &AppHostingNode{
		deviceSpec: deviceSpec,
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
	if a.deviceSpec == nil {
		return
	}

	// Create a temporary driver to fetch device info
	// Note: NewDriver calls CheckConnection internally, which populates deviceInfo
	driver, err := drivers.NewDriver(ctx, a.deviceSpec)
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

	// Determine node internal IP from device address
	nodeInternalIP := a.deviceSpec.Address

	log.G(ctx).Infof("Updating node status with device info, InternalIP=%s", nodeInternalIP)

	// Create a node update with device info and addresses
	nodeUpdate := &v1.Node{
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				MachineID:       deviceInfo.SerialNumber,
				SystemUUID:      deviceInfo.SerialNumber,
				KernelVersion:   deviceInfo.SoftwareVersion,
				KubeletVersion:  getVirtualKubeletVersion(),
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
