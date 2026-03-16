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
	"net"
	"os"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type AppHostingProvider struct {
	ctx             context.Context
	deviceSpec      *v1alpha1.DeviceSpec
	driver          drivers.CiscoKubernetesDeviceDriver
	podsLister      corev1listers.PodLister
	configMapLister corev1listers.ConfigMapLister
	secretLister    corev1listers.SecretLister
	serviceLister   corev1listers.ServiceLister
	nodeProvider    *AppHostingNode
}

func NewAppHostingProvider(
	ctx context.Context,
	deviceSpec *v1alpha1.DeviceSpec,
	vkCfg nodeutil.ProviderConfig,
	driver drivers.CiscoKubernetesDeviceDriver,
	nodeProvider *AppHostingNode,
) (*AppHostingProvider, error) {
	return &AppHostingProvider{
		ctx:             ctx,
		deviceSpec:      deviceSpec,
		driver:          driver,
		podsLister:      vkCfg.Pods,
		configMapLister: vkCfg.ConfigMaps,
		secretLister:    vkCfg.Secrets,
		serviceLister:   vkCfg.Services,
		nodeProvider:    nodeProvider,
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

	// Trigger node status update to reflect potentially changed resources
	if p.nodeProvider != nil {
		p.nodeProvider.ForceStatusUpdate(p.ctx)
	}

	return nil
}

func (p *AppHostingProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	// IOS-XE/XR may have limited "Update" support (e.g., changing resources requires a restart)
	return p.driver.UpdatePod(p.ctx, pod)
}

func (p *AppHostingProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	err := p.driver.DeletePod(p.ctx, pod)

	// Trigger node status update to reflect potentially freed resources
	if p.nodeProvider != nil {
		p.nodeProvider.ForceStatusUpdate(p.ctx)
	}

	return err
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
	return nil, fmt.Errorf("GetContainerLogs is not supported by the Cisco Virtual Kubelet")
}

// GetMetricsResource and GetStatsSummary are implemented in metrics.go

// PortForward implements nodeutil.Provider.
func (p *AppHostingProvider) PortForward(ctx context.Context, namespace string, pod string, port int32, stream io.ReadWriteCloser) error {
	return fmt.Errorf("PortForward is not supported by the Cisco Virtual Kubelet")
}

// RunInContainer implements nodeutil.Provider.
func (p *AppHostingProvider) RunInContainer(ctx context.Context, namespace string, podName string, containerName string, cmd []string, attach api.AttachIO) error {
	return fmt.Errorf("RunInContainer is not supported by the Cisco Virtual Kubelet")
}

// AppHostingNode implements node.NodeProvider for proper heartbeat management.
// This follows the NaiveNodeProvider pattern from virtual-kubelet.
// The library's NodeController handles periodic heartbeat updates automatically.
type AppHostingNode struct {
	ctx             context.Context // long-lived app context for async operations
	nodeName        string
	deviceSpec      *v1alpha1.DeviceSpec
	driver          drivers.CiscoKubernetesDeviceDriver
	statusCallback  func(*v1.Node)
	lastStatusSync  time.Time
	syncInFlight    bool
	statusSyncMutex sync.Mutex
	// Track previous condition statuses for correct LastTransitionTime handling
	prevReadyStatus            v1.ConditionStatus
	prevDiskPressureStatus     v1.ConditionStatus
	readyTransitionTime        metav1.Time
	diskPressureTransitionTime metav1.Time
}

// NewAppHostingNode creates a new AppHostingNode.
// The provided ctx should be the long-lived application context, not a request-scoped one.
func NewAppHostingNode(
	ctx context.Context,
	nodeName string,
	deviceSpec *v1alpha1.DeviceSpec,
	driver drivers.CiscoKubernetesDeviceDriver,
) *AppHostingNode {
	return &AppHostingNode{
		ctx:        ctx,
		nodeName:   nodeName,
		deviceSpec: deviceSpec,
		driver:     driver,
	}
}

// Ping implements node.NodeProvider.
// Called periodically by the library's nodePingController.
// Returning nil indicates the node is healthy.
// Note: Ping reports process-level health, not device reachability.
// Device health is surfaced via NodeReady conditions in syncNodeStatus.
func (a *AppHostingNode) Ping(ctx context.Context) error {
	a.statusSyncMutex.Lock()
	defer a.statusSyncMutex.Unlock()

	// Throttle: only sync if >30s since last attempt and no sync already in-flight
	if !a.syncInFlight && time.Since(a.lastStatusSync) > 30*time.Second {
		if a.statusCallback != nil {
			a.syncInFlight = true
			go a.syncNodeStatus(a.ctx, a.statusCallback)
			a.lastStatusSync = time.Now()
		}
	}
	return nil
}

// NotifyNodeStatus implements node.NodeProvider.
// Called once at startup to allow async node status updates.
// We use this to update node info with device details and monitor operational status.
func (a *AppHostingNode) NotifyNodeStatus(ctx context.Context, cb func(*v1.Node)) {
	if a.deviceSpec == nil {
		return
	}

	a.statusSyncMutex.Lock()
	a.statusCallback = cb
	a.statusSyncMutex.Unlock()

	// Perform initial sync immediately using the long-lived app context,
	// not the NotifyNodeStatus ctx which may be short-lived.
	go a.syncNodeStatus(a.ctx, cb)
}

// ForceStatusUpdate triggers an immediate status update if a callback is registered.
// Skipped if a sync is already in-flight to avoid redundant device queries.
func (a *AppHostingNode) ForceStatusUpdate(ctx context.Context) {
	a.statusSyncMutex.Lock()
	cb := a.statusCallback
	inFlight := a.syncInFlight
	a.statusSyncMutex.Unlock()

	if cb != nil && !inFlight {
		log.G(a.ctx).Info("Forcing node status update due to pod lifecycle event")
		a.statusSyncMutex.Lock()
		a.syncInFlight = true
		a.statusSyncMutex.Unlock()
		go a.syncNodeStatus(a.ctx, cb)
	}
}

// syncNodeStatus fetches the latest device info and operational data, then calls the callback.
func (a *AppHostingNode) syncNodeStatus(ctx context.Context, cb func(*v1.Node)) {
	// Always clear syncInFlight when we're done
	defer func() {
		a.statusSyncMutex.Lock()
		a.syncInFlight = false
		a.statusSyncMutex.Unlock()
	}()

	// Record time of attempt
	a.statusSyncMutex.Lock()
	a.lastStatusSync = time.Now()
	a.statusSyncMutex.Unlock()

	deviceInfo, err := a.driver.GetDeviceInfo(ctx)
	if err != nil || deviceInfo == nil {
		log.G(ctx).Warn("Failed to get device info during node status sync")
		return // Skip update if we can't identify the device
	}

	// Fetch dynamic operational data (IOx status, resources)
	operData, err := a.driver.GetGlobalOperationalData(ctx)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Failed to get operational data during node status sync")
		// We continue with basic device info, but conditions may be incomplete
	}

	// Determine node internal IP: use the VK pod's own IP so the API server
	// can proxy kubelet requests (e.g. /stats/summary) to our :10250 listener.
	// The device address is used only for RESTCONF communication with the device.
	nodeInternalIP := getKubeletIP(a.deviceSpec.Address)

	log.G(ctx).Debugf("Updating node status with device info, InternalIP=%s", nodeInternalIP)

	now := metav1.Now()

	// --- Build Node Conditions with correct LastTransitionTime tracking ---
	conditions := []v1.NodeCondition{}

	// Condition: Ready
	newReadyStatus := v1.ConditionTrue
	readyReason := "KubeletReady"
	readyMessage := "Cisco IOx is enabled and reachable"

	if operData != nil && !operData.IoxEnabled {
		newReadyStatus = v1.ConditionFalse
		readyReason = "IOxDisabled"
		readyMessage = "IOx hosting is disabled on device"
	}

	a.statusSyncMutex.Lock()
	if a.prevReadyStatus == "" || a.prevReadyStatus != newReadyStatus {
		// First report or actual transition — update transition time
		a.readyTransitionTime = now
		a.prevReadyStatus = newReadyStatus
	}
	readyTransitionTime := a.readyTransitionTime
	a.statusSyncMutex.Unlock()

	conditions = append(conditions, v1.NodeCondition{
		Type:               v1.NodeReady,
		Status:             newReadyStatus,
		LastHeartbeatTime:  now,
		LastTransitionTime: readyTransitionTime,
		Reason:             readyReason,
		Message:            readyMessage,
	})

	// Condition: DiskPressure (IOx Storage)
	newDiskPressureStatus := v1.ConditionFalse
	diskReason := "StorageAvailable"
	diskMessage := "Sufficient storage available"

	if operData != nil && operData.Storage.Quota > 0 {
		if float64(operData.Storage.Available)/float64(operData.Storage.Quota) < 0.05 {
			newDiskPressureStatus = v1.ConditionTrue
			diskReason = "StorageLow"
			diskMessage = fmt.Sprintf("Available storage low: %d/%d %s", operData.Storage.Available, operData.Storage.Quota, operData.Storage.Unit)
		}
	}

	a.statusSyncMutex.Lock()
	if a.prevDiskPressureStatus == "" || a.prevDiskPressureStatus != newDiskPressureStatus {
		a.diskPressureTransitionTime = now
		a.prevDiskPressureStatus = newDiskPressureStatus
	}
	diskPressureTransitionTime := a.diskPressureTransitionTime
	a.statusSyncMutex.Unlock()

	conditions = append(conditions, v1.NodeCondition{
		Type:               v1.NodeDiskPressure,
		Status:             newDiskPressureStatus,
		LastHeartbeatTime:  now,
		LastTransitionTime: diskPressureTransitionTime,
		Reason:             diskReason,
		Message:            diskMessage,
	})

	// --- Build dynamic Capacity and Allocatable from operational data ---
	capacity := v1.ResourceList{}
	if operData != nil {
		if operData.SystemCPU.Quota > 0 {
			capacity[v1.ResourceCPU] = *resource.NewQuantity(operData.SystemCPU.Quota, resource.DecimalSI)
		}
		if operData.Memory.Quota > 0 {
			// Memory quota is in MB from the device; convert to bytes for Kubernetes
			capacity[v1.ResourceMemory] = *resource.NewQuantity(operData.Memory.Quota*1024*1024, resource.BinarySI)
		}
		if operData.Storage.Quota > 0 {
			capacity[v1.ResourceStorage] = *resource.NewQuantity(operData.Storage.Quota*1024*1024, resource.BinarySI)
		}
	}

	// Discover deployed pods to calculate available pod slots
	var maxPods int64 = 16
	var deployedPodCount int64
	pods, podErr := a.driver.ListPods(ctx)
	if podErr != nil {
		log.G(ctx).WithError(podErr).Warn("Failed to list pods during node status sync, using 0 for deployed count")
	} else {
		deployedPodCount = int64(len(pods))
	}
	capacity[v1.ResourcePods] = *resource.NewQuantity(maxPods, resource.DecimalSI)

	// Allocatable reflects currently available resources
	allocatable := v1.ResourceList{}
	if operData != nil {
		if operData.SystemCPU.Available > 0 {
			allocatable[v1.ResourceCPU] = *resource.NewQuantity(operData.SystemCPU.Available, resource.DecimalSI)
		}
		if operData.Memory.Available > 0 {
			allocatable[v1.ResourceMemory] = *resource.NewQuantity(operData.Memory.Available*1024*1024, resource.BinarySI)
		}
		if operData.Storage.Available > 0 {
			allocatable[v1.ResourceStorage] = *resource.NewQuantity(operData.Storage.Available*1024*1024, resource.BinarySI)
		}
	}
	availablePods := maxPods - deployedPodCount
	if availablePods < 0 {
		availablePods = 0
	}
	allocatable[v1.ResourcePods] = *resource.NewQuantity(availablePods, resource.DecimalSI)

	// Create a node update with device info and addresses
	nodeUpdate := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"kubernetes.io/hostname":        a.nodeName,
				"type":                          "virtual-kubelet",
				"topology.kubernetes.io/zone":   "cisco-iosxe",
				"topology.kubernetes.io/region": "cisco-iosxe",
			},
		},
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				MachineID:               deviceInfo.SerialNumber,
				SystemUUID:              deviceInfo.SerialNumber,
				BootID:                  deviceInfo.SerialNumber,
				KernelVersion:           deviceInfo.SoftwareVersion,
				KubeletVersion:          getVirtualKubeletVersion(),
				ContainerRuntimeVersion: fmt.Sprintf("iosxe-apphosting://%s", deviceInfo.SoftwareVersion),
				OSImage:                 "IOS-XE",
				Architecture:            deviceInfo.ProductID,
				OperatingSystem:         "Cisco",
			},
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeInternalIP,
					Address: nodeInternalIP,
				},
			},
			Conditions:  conditions,
			Capacity:    capacity,
			Allocatable: allocatable,
		},
	}

	cb(nodeUpdate)
}

// getKubeletIP returns the IP address where the VK's kubelet API server (:10250) is reachable.
// The Kubernetes API server proxies requests like /stats/summary to InternalIP:10250,
// so this must be the VK pod's IP, not the managed device's IP.
//
// Resolution order:
//  1. POD_IP env var (set via Kubernetes downward API)
//  2. Auto-detected outbound IP of the host
//  3. Fallback to deviceAddress (original behavior, may not be routable to :10250)
func getKubeletIP(deviceAddress string) string {
	// Prefer POD_IP from the downward API
	if podIP := os.Getenv("POD_IP"); podIP != "" {
		return podIP
	}

	// Auto-detect: open a UDP "connection" to determine which local IP the OS would use
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return addr.IP.String()
		}
	}

	return deviceAddress
}
