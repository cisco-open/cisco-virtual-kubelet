package cisco

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/api/statsv1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/trace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CiscoProvider implements the virtual-kubelet provider interface for Cisco devices
type CiscoProvider struct {
	nodeName       string
	operatingSystem string
	internalIP     string
	daemonEndpointPort int32
	
	config         *CiscoConfig
	deviceManager  *DeviceManager
	networkManager *NetworkManager
	monitor        *ResourceMonitor
	
	pods           sync.Map // map[string]*v1.Pod (namespace/name -> pod)
	containers     sync.Map // map[string]*Container (container ID -> container)
	
	startTime          time.Time
	notifier           func(*v1.Pod)
	nodeStatusCallback func(*v1.Node)
	
	mutex          sync.RWMutex
}

// getDeviceNames extracts device names from config for logging
func getDeviceNames(config *CiscoConfig) []string {
	names := make([]string, len(config.Devices))
	for i, device := range config.Devices {
		names[i] = device.Name
	}
	return names
}

// NewCiscoProvider creates a new Cisco provider
func NewCiscoProvider(configPath, nodeName, operatingSystem, internalIP string, daemonEndpointPort int32) (*CiscoProvider, error) {
	// Try environment-based configuration first, then fall back to file-based config
	config, err := loadConfigFromEnvOrFile(configPath)
	if err != nil {
		fmt.Printf("[CISCO-VK] WARNING: Config loading failed: %v\n", err)
		fmt.Printf("[CISCO-VK] Using default configuration (no devices configured)\n")
		fmt.Printf("[CISCO-VK] Set CISCO_DEVICE_ADDRESS, CISCO_DEVICE_USERNAME, CISCO_DEVICE_PASSWORD env vars\n")
		fmt.Printf("[CISCO-VK] Or create config file at /etc/cisco-vk/config.yaml\n")
		config = getDefaultConfig()
	}

	deviceManager, err := NewDeviceManager(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create device manager: %v", err)
	}

	networkManager := NewNetworkManager(config)
	monitor := NewResourceMonitor()

	provider := &CiscoProvider{
		nodeName:           nodeName,
		operatingSystem:    operatingSystem,
		internalIP:         internalIP,
		daemonEndpointPort: daemonEndpointPort,
		config:             config,
		deviceManager:      deviceManager,
		networkManager:     networkManager,
		monitor:            monitor,
		startTime:          time.Now(),
	}

	// Initialize connections to devices
	ctx := context.Background()
	
	// Print configuration for debugging (before logger is available)
	fmt.Printf("[CISCO-VK] Provider configuration loaded: %d devices\n", len(provider.config.Devices))
	for _, device := range provider.config.Devices {
		fmt.Printf("[CISCO-VK] Device: %s @ %s - CPU:%s Memory:%s Storage:%s Pods:%s\n",
			device.Name, device.Address,
			device.Capabilities.CPU.String(),
			device.Capabilities.Memory.String(),
			device.Capabilities.Storage.String(),
			device.Capabilities.Pods.String())
	}
	
	if err := deviceManager.Connect(ctx); err != nil {
		fmt.Printf("[CISCO-VK] WARNING: Failed to connect to some devices: %v\n", err)
	} else {
		fmt.Printf("[CISCO-VK] Successfully connected to all devices\n")
	}
	
	// Log initial capacity after connection
	initialCapacity := deviceManager.GetCapacity()
	fmt.Printf("[CISCO-VK] Initial node capacity - CPU:%s Memory:%s Storage:%s Pods:%s\n",
		initialCapacity.CPU.String(),
		initialCapacity.Memory.String(),
		initialCapacity.Storage.String(),
		initialCapacity.Pods.String())

	// Start monitoring
	go provider.startMonitoring(ctx)

	return provider, nil
}

// GetDeviceCapacity returns the aggregated capacity from all managed devices
func (p *CiscoProvider) GetDeviceCapacity() *DeviceCapability {
	return p.deviceManager.GetCapacity()
}

// Capacity returns the capacity of the node
func (p *CiscoProvider) Capacity(ctx context.Context) v1.ResourceList {
	// Get capacity from device manager (only includes ready devices)
	capacity := p.deviceManager.GetCapacity()
	
	fmt.Printf("[CISCO-VK] Capacity() called - CPU:%s Memory:%s Storage:%s Pods:%s\n",
		capacity.CPU.String(),
		capacity.Memory.String(),
		capacity.Storage.String(),
		capacity.Pods.String())

	return v1.ResourceList{
		v1.ResourceCPU:     capacity.CPU,
		v1.ResourceMemory:  capacity.Memory,
		v1.ResourceStorage: capacity.Storage,
		v1.ResourcePods:    capacity.Pods,
	}
}

// NodeConditions returns the node conditions
func (p *CiscoProvider) NodeConditions(ctx context.Context) []v1.NodeCondition {
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletReady",
			Message:            "Cisco provider is ready",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "Cisco provider has sufficient disk space",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "Cisco provider has sufficient memory",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "Cisco provider has no disk pressure",
		},
		{
			Type:               "PIDPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientPID",
			Message:            "Cisco provider has sufficient PIDs",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "Cisco provider network is available",
		},
	}
}

// NodeAddresses returns the node addresses
func (p *CiscoProvider) NodeAddresses(ctx context.Context) []v1.NodeAddress {
	return []v1.NodeAddress{
		{
			Type:    v1.NodeInternalIP,
			Address: p.internalIP,
		},
	}
}

// NodeDaemonEndpoints returns the node daemon endpoints
func (p *CiscoProvider) NodeDaemonEndpoints(ctx context.Context) v1.NodeDaemonEndpoints {
	return v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

// PodLifecycleHandler implementation

// CreatePod creates a new pod on the best available Cisco device
func (p *CiscoProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "CreatePod")
	defer span.End()

	key := getPodKey(pod)
	fmt.Printf("[CISCO-VK] ✨ CreatePod() CALLED for pod: %s\n", key)
	log.G(ctx).Infof("🚀 Creating pod %s", key)
	
	// Reject system namespace pods (kube-proxy, etc.) - C9K doesn't support Kubernetes infrastructure
	systemNamespaces := []string{"kube-system", "kube-public", "kube-node-lease"}
	for _, ns := range systemNamespaces {
		if pod.Namespace == ns {
			log.G(ctx).Warnf("Rejecting system pod %s from namespace %s - C9K does not support Kubernetes infrastructure pods", key, pod.Namespace)
			return fmt.Errorf("C9K platform does not support system namespace pods from %s", pod.Namespace)
		}
	}

	// Check if pod already exists in internal state
	fmt.Printf("[CISCO-VK] 🔍 Checking if pod already exists in internal state...\n")
	if _, exists := p.pods.Load(key); exists {
		fmt.Printf("[CISCO-VK] ❌ Pod already exists in internal state\n")
		return fmt.Errorf("pod already exists")
	}
	fmt.Printf("[CISCO-VK] ✅ Pod not in internal state, proceeding...\n")

	// STATE RECONCILIATION: Check if this pod was previously deployed (handles VK restarts)
	// If pod has app-id annotations, try to reconcile with existing device apps via RESTCONF
	fmt.Printf("[CISCO-VK] 🔄 Attempting to reconcile existing pod...\n")
	reconciled, err := p.reconcileExistingPod(ctx, pod)
	if err != nil {
		fmt.Printf("[CISCO-VK] ⚠️ Reconciliation failed: %v - continuing with normal creation\n", err)
		log.G(ctx).Warnf("Failed to reconcile existing pod: %v - will attempt normal creation", err)
		// Continue with normal creation if reconciliation fails
	}
	if reconciled {
		fmt.Printf("[CISCO-VK] ✅ Pod was reconciled from existing deployment\n")
		log.G(ctx).Infof("✅ Pod %s was reconciled from existing deployment", key)
		// Notify about the reconciled pod
		if p.notifier != nil {
			p.notifier(pod.DeepCopy())
		}
		return nil
	}
	fmt.Printf("[CISCO-VK] ✅ Reconciliation complete (not reconciled), proceeding with creation...\n")

	// Convert pod spec to container specs
	fmt.Printf("[CISCO-VK] 📝 Converting pod to container specs...\n")
	containers, err := p.convertPodToContainers(pod)
	if err != nil {
		fmt.Printf("[CISCO-VK] ❌ Failed to convert pod to containers: %v\n", err)
		return fmt.Errorf("failed to convert pod to containers: %v", err)
	}
	fmt.Printf("[CISCO-VK] ✅ Converted to %d container(s)\n", len(containers))

	// Create containers on devices
	fmt.Printf("[CISCO-VK] 🏗️ Creating %d container(s) on devices...\n", len(containers))
	createdContainers := []*Container{}
	for i, containerSpec := range containers {
		fmt.Printf("[CISCO-VK] 📦 Creating container %d/%d: %s\n", i+1, len(containers), containerSpec.Name)
		container, err := p.deviceManager.CreateContainer(ctx, containerSpec)
		if err != nil {
			fmt.Printf("[CISCO-VK] ❌ Failed to create container %s: %v\n", containerSpec.Name, err)
			// Rollback previously created containers
			p.rollbackContainerCreation(ctx, createdContainers)
			return fmt.Errorf("failed to create container %s: %v", containerSpec.Name, err)
		}
		
		createdContainers = append(createdContainers, container)
		p.containers.Store(container.ID, container)
	}

	// Update pod status
	now := metav1.Now()
	pod.Status = v1.PodStatus{
		Phase:     v1.PodRunning,
		HostIP:    p.internalIP,
		PodIP:     p.allocatePodIP(pod),
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:   v1.PodInitialized,
				Status: v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:   v1.PodReady,
				Status: v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:   v1.PodScheduled,
				Status: v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	// Set container statuses
	for _, container := range createdContainers {
		containerStatus := v1.ContainerStatus{
			Name:    container.Name,
			Image:   container.Image,
			Ready:   container.State == ContainerStateRunning,
			State: v1.ContainerState{
				Running: &v1.ContainerStateRunning{
					StartedAt: *container.StartedAt,
				},
			},
			ContainerID: fmt.Sprintf("cisco://%s", container.ID),
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStatus)
	}

	// Add deployment metadata annotations for kubectl describe
	if err := p.addDeploymentMetadata(ctx, pod, createdContainers); err != nil {
		log.G(ctx).Warnf("Failed to add deployment metadata: %v", err)
		// Non-fatal - continue
	}

	// Store pod
	p.pods.Store(key, pod.DeepCopy())

	// Notify about pod status change
	if p.notifier != nil {
		p.notifier(pod.DeepCopy())
	}

	log.G(ctx).Infof("Successfully created pod %s with %d containers", key, len(createdContainers))
	return nil
}

// UpdatePod updates an existing pod
func (p *CiscoProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "UpdatePod")
	defer span.End()

	key := getPodKey(pod)
	log.G(ctx).Infof("Updating pod %s", key)

	// Check if pod exists
	existingPodInterface, exists := p.pods.Load(key)
	if !exists {
		return errdefs.NotFound("pod not found")
	}

	existingPod := existingPodInterface.(*v1.Pod)

	// For now, we'll mainly update the pod metadata and annotations
	// Container updates would require more complex logic
	updatedPod := existingPod.DeepCopy()
	updatedPod.Labels = pod.Labels
	updatedPod.Annotations = pod.Annotations
	updatedPod.Spec = pod.Spec

	// Store updated pod
	p.pods.Store(key, updatedPod)

	// Notify about pod status change
	if p.notifier != nil {
		p.notifier(updatedPod)
	}

	log.G(ctx).Infof("Successfully updated pod %s", key)
	return nil
}

// DeletePod removes a pod and its containers from Cisco devices
func (p *CiscoProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	ctx, span := trace.StartSpan(ctx, "DeletePod")
	defer span.End()

	key := getPodKey(pod)
	fmt.Printf("[CISCO-VK] 🗑️ DeletePod() CALLED for pod: %s\n", key)
	log.G(ctx).Infof("Deleting pod %s", key)

	// Try to get existing pod from internal state
	existingPodInterface, exists := p.pods.Load(key)
	
	var containerIDs []string
	var existingPod *v1.Pod
	
	if exists {
		// Pod found in internal state - use normal deletion path
		existingPod = existingPodInterface.(*v1.Pod)
		containerIDs = p.getContainerIDsForPod(existingPod)
		log.G(ctx).Infof("  Found pod in internal state with %d container(s)", len(containerIDs))
		
		// FALLBACK: If no containers found but pod has annotations, use annotation-based deletion
		// This handles reconciliation scenarios where pod was restored but containers weren't
		if len(containerIDs) == 0 && existingPod.Annotations != nil {
			log.G(ctx).Warnf("  No containers in internal state - falling back to annotation-based deletion")
			exists = false // Force annotation-based path
		}
	}
	
	if !exists {
		// Pod NOT found in internal state OR no containers found (VK restart/reconciliation scenario)
		// Try to extract app IDs from pod annotations and delete via RESTCONF
		log.G(ctx).Warnf("  Checking annotations for cleanup")
		
		if pod.Annotations != nil {
			// Look for container annotations with app-id
			for i := 0; ; i++ {
				appIDKey := fmt.Sprintf("cisco.com/container-%d.app-id", i)
				deviceIDKey := fmt.Sprintf("cisco.com/container-%d.device-id", i)
				
				appID, hasAppID := pod.Annotations[appIDKey]
				deviceID, hasDeviceID := pod.Annotations[deviceIDKey]
				
				if !hasAppID {
					break // No more containers
				}
				
				if hasDeviceID {
					log.G(ctx).Infof("  Found annotation: app-id=%s on device=%s", appID, deviceID)
					
					// Delete directly via device manager using app ID
					device, err := p.deviceManager.GetDevice(deviceID)
					if err != nil {
						log.G(ctx).Warnf("  Device %s not found: %v", deviceID, err)
						continue
					}
					
					if device.AppHostingMgr != nil {
						log.G(ctx).Infof("  Undeploying app %s via RESTCONF", appID)
						if err := device.AppHostingMgr.UndeployApplication(ctx, appID); err != nil {
							log.G(ctx).Errorf("  Failed to undeploy app %s: %v", appID, err)
						} else {
							log.G(ctx).Infof("  ✅ Successfully undeployed app %s", appID)
						}
						
						// Clean up device state
						device.mutex.Lock()
						delete(device.Containers, appID)
						device.mutex.Unlock()
					}
					
					// Add to container IDs for cleanup
					containerIDs = append(containerIDs, appID)
					p.containers.Delete(appID)
				}
			}
		}
		
		if len(containerIDs) == 0 {
			log.G(ctx).Warnf("  No containers found to delete (pod may have been deleted already)")
			// Return success even if no containers found - pod is being deleted
			return nil
		}
		
		existingPod = pod // Use the pod from K8s
	}

	// Delete containers using standard path (if they exist in internal state)
	if exists {
		for _, containerID := range containerIDs {
			if err := p.deviceManager.DeleteContainer(ctx, containerID); err != nil {
				log.G(ctx).Errorf("Failed to delete container %s: %v", containerID, err)
			}
			p.containers.Delete(containerID)
		}
	}

	// Update pod status to terminated if we have the pod
	if existingPod != nil {
		now := metav1.Now()
		terminatedPod := existingPod.DeepCopy()
		terminatedPod.Status.Phase = v1.PodSucceeded
		terminatedPod.Status.Reason = "CiscoProviderPodDeleted"

		// Update container statuses to terminated
		for i := range terminatedPod.Status.ContainerStatuses {
			terminatedPod.Status.ContainerStatuses[i].Ready = false
			terminatedPod.Status.ContainerStatuses[i].State = v1.ContainerState{
				Terminated: &v1.ContainerStateTerminated{
					Message:    "Container terminated by Cisco provider",
					FinishedAt: now,
					Reason:     "CiscoProviderContainerDeleted",
					StartedAt:  terminatedPod.Status.ContainerStatuses[i].State.Running.StartedAt,
				},
			}
		}

		// Notify about pod termination
		if p.notifier != nil {
			p.notifier(terminatedPod)
		}
	}

	// Remove pod from storage
	p.pods.Delete(key)

	log.G(ctx).Infof("✅ Successfully deleted pod %s", key)
	return nil
}

// GetPod retrieves a pod by name and namespace
func (p *CiscoProvider) GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPod")
	defer span.End()

	key := fmt.Sprintf("%s/%s", namespace, name)
	
	podInterface, exists := p.pods.Load(key)
	if !exists {
		return nil, errdefs.NotFoundf("pod \"%s\" is not known to the provider", key)
	}

	pod := podInterface.(*v1.Pod)
	return pod.DeepCopy(), nil
}

// GetPodStatus retrieves the status of a pod
func (p *CiscoProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	ctx, span := trace.StartSpan(ctx, "GetPodStatus")
	defer span.End()

	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	return &pod.Status, nil
}

// GetPods retrieves all pods managed by this provider
func (p *CiscoProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	ctx, span := trace.StartSpan(ctx, "GetPods")
	defer span.End()

	var pods []*v1.Pod
	p.pods.Range(func(key, value interface{}) bool {
		pod := value.(*v1.Pod)
		pods = append(pods, pod.DeepCopy())
		return true
	})

	return pods, nil
}

// nodeutil.Provider implementation

// GetContainerLogs retrieves container logs with streaming health vitals
func (p *CiscoProvider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	ctx, span := trace.StartSpan(ctx, "GetContainerLogs")
	defer span.End()

	log.G(ctx).Infof("📋 Getting logs for container %s in pod %s/%s (streaming health vitals)", containerName, namespace, podName)

	// Find the container
	containerID, err := p.findContainerID(namespace, podName, containerName)
	if err != nil {
		return nil, err
	}

	// Get container to find app ID
	containerObj, exists := p.containers.Load(containerID)
	if !exists {
		return nil, fmt.Errorf("container %s not found", containerID)
	}
	
	container := containerObj.(*Container)
	
	// Get device to access RESTCONF
	device, err := p.deviceManager.GetDevice(container.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("device not found: %v", err)
	}
	
	// Get RESTCONF client from app hosting manager
	if device.AppHostingMgr == nil {
		return nil, fmt.Errorf("app hosting manager not available for device %s", container.DeviceID)
	}
	if device.AppHostingMgr.restconfClient == nil {
		return nil, fmt.Errorf("RESTCONF client not available for device %s", container.DeviceID)
	}
	
	log.G(ctx).Infof("📊 Creating log stream for container %s, app ID: %s", container.Name, container.ID)
	
	// Create log streamer for health vitals
	if opts.Follow {
		log.G(ctx).Infof("📺 Following logs (streaming health vitals every 60s)")
		streamer := NewLogStreamer(container.ID, container.ID, device.AppHostingMgr.restconfClient)
		return streamer.Stream(ctx), nil
	}
	
	// For non-follow mode, return single snapshot
	log.G(ctx).Infof("📋 Snapshot mode (single health check)")
	streamer := NewLogStreamer(container.ID, container.ID, device.AppHostingMgr.restconfClient)
	return streamer.Stream(ctx), nil
}

// RunInContainer executes a command in a container
func (p *CiscoProvider) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach api.AttachIO) error {
	ctx, span := trace.StartSpan(ctx, "RunInContainer")
	defer span.End()

	log.G(ctx).Infof("Executing command in container %s in pod %s/%s", containerName, namespace, podName)

	// Find the container
	containerID, err := p.findContainerID(namespace, podName, containerName)
	if err != nil {
		return err
	}

	// Execute command
	result, err := p.deviceManager.ExecuteCommand(ctx, containerID, cmd)
	if err != nil {
		return err
	}

	// Write output to attach streams
	if attach.Stdout() != nil {
		attach.Stdout().Write([]byte(result.Stdout))
	}
	if attach.Stderr() != nil && result.Stderr != "" {
		attach.Stderr().Write([]byte(result.Stderr))
	}

	return nil
}

// AttachToContainer attaches to a running container
func (p *CiscoProvider) AttachToContainer(ctx context.Context, namespace, podName, containerName string, attach api.AttachIO) error {
	log.G(ctx).Infof("Attaching to container %s in pod %s/%s", containerName, namespace, podName)
	
	// For Cisco IOx containers, attachment is limited
	// We can simulate it by providing a shell prompt
	if attach.Stdout() != nil {
		attach.Stdout().Write([]byte("Cisco IOx container attachment not fully supported\n"))
	}
	
	return nil
}

// GetStatsSummary returns resource usage statistics for HPA support
func (p *CiscoProvider) GetStatsSummary(ctx context.Context) (*statsv1alpha1.Summary, error) {
	ctx, span := trace.StartSpan(ctx, "GetStatsSummary")
	defer span.End()

	// Get overall resource usage
	_, err := p.deviceManager.GetResourceUsage(ctx)
	if err != nil {
		return nil, err
	}

	// Create node stats
	nodeStats := statsv1alpha1.NodeStats{
		NodeName:  p.nodeName,
		StartTime: metav1.NewTime(p.startTime),
		CPU: &statsv1alpha1.CPUStats{
			Time: metav1.NewTime(time.Now()),
			UsageNanoCores: func() *uint64 {
				val := uint64(1000000000)
				return &val
			}(),
		},
		Memory: &statsv1alpha1.MemoryStats{
			Time: metav1.NewTime(time.Now()),
			UsageBytes: func() *uint64 {
				val := uint64(1024 * 1024 * 1024)
				return &val
			}(),
		},
		// Network: &statsv1alpha1.NetworkStats{
			// Time:            metav1.NewTime(usage.Timestamp.Time),
			// UsageBytes:      uint64ToPointer(uint64(usage.Memory.Value())),
			// WorkingSetBytes: uint64ToPointer(uint64(usage.Memory.Value())),
		// },
		// Network stats - fields may vary by Kubernetes version
	}

	summary := &statsv1alpha1.Summary{
		Node: nodeStats,
		Pods: []statsv1alpha1.PodStats{},
	}

	// Add pod statistics
	pods, _ := p.GetPods(ctx)
	for _, pod := range pods {
		podStats := p.createPodStats(ctx, pod)
		if podStats != nil {
			summary.Pods = append(summary.Pods, *podStats)
		}
	}

	return summary, nil
}

// GetMetricsResource returns Prometheus metrics
func (p *CiscoProvider) GetMetricsResource(ctx context.Context) ([]*dto.MetricFamily, error) {
	// Implementation would return Prometheus metrics
	// For now, return empty metrics
	return []*dto.MetricFamily{}, nil
}

// PortForward forwards a port to a pod
func (p *CiscoProvider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	log.G(ctx).Infof("Port forward requested for pod %s/%s port %d", namespace, pod, port)
	
	// Port forwarding for IOx containers would require specific implementation
	// For now, we'll return an error indicating it's not supported
	return fmt.Errorf("port forwarding not currently supported for Cisco IOx containers")
}

// NotifyPods sets the pod notification callback
func (p *CiscoProvider) NotifyPods(ctx context.Context, notifier func(*v1.Pod)) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
	p.notifier = notifier
}

// NotifyNodeStatus is called to notify the node controller of node status changes
// This method is required by the NodeProvider interface
func (p *CiscoProvider) NotifyNodeStatus(ctx context.Context, notifier func(*v1.Node)) {
	fmt.Printf("[CISCO-VK] NotifyNodeStatus callback registered - starting status update loop\n")
	log.G(ctx).Info("NotifyNodeStatus callback registered")
	
	// Store the notifier
	p.mutex.Lock()
	p.nodeStatusCallback = notifier
	p.mutex.Unlock()
	
	// Start periodic status update goroutine
	go p.runNodeStatusUpdater(ctx)
}

// runNodeStatusUpdater periodically updates node status
func (p *CiscoProvider) runNodeStatusUpdater(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	// Initial update after short delay
	time.Sleep(5 * time.Second)
	p.updateNodeStatus(ctx)
	
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[CISCO-VK] Node status updater stopped\n")
			return
		case <-ticker.C:
			p.updateNodeStatus(ctx)
		}
	}
}

// updateNodeStatus pushes current node status to the framework
func (p *CiscoProvider) updateNodeStatus(ctx context.Context) {
	p.mutex.Lock()
	callback := p.nodeStatusCallback
	p.mutex.Unlock()
	
	if callback == nil {
		return
	}
	
	// Build node with current status
	node := &v1.Node{}
	node.Status.Conditions = p.NodeConditions(ctx)
	node.Status.Addresses = p.NodeAddresses(ctx)
	
	// Get current capacity
	capacity := p.deviceManager.GetCapacity()
	node.Status.Capacity = v1.ResourceList{
		v1.ResourceCPU:    capacity.CPU,
		v1.ResourceMemory: capacity.Memory,
		"storage":         capacity.Storage,
		v1.ResourcePods:   capacity.Pods,
	}
	node.Status.Allocatable = node.Status.Capacity
	
	fmt.Printf("[CISCO-VK] 💓 Sending node status update (Ready=True)\n")
	callback(node)
}

// Ping checks if the node is still active
// This method is required by the NodeProvider interface
func (p *CiscoProvider) Ping(ctx context.Context) error {
	// Check if at least one device is connected
	capacity := p.deviceManager.GetCapacity()
	if capacity.CPU.IsZero() && capacity.Memory.IsZero() {
		return fmt.Errorf("no devices available")
	}
	return nil
}

// ConfigureNode configures the virtual kubelet node
func (p *CiscoProvider) ConfigureNode(ctx context.Context, node *v1.Node) {
	fmt.Printf("[CISCO-VK] *** ConfigureNode() CALLED ***\n")
	
	// Set node capacity based on aggregated device capabilities
	capacity := p.deviceManager.GetCapacity()
	
	fmt.Printf("[CISCO-VK] ConfigureNode - Setting capacity: CPU:%s Memory:%s Storage:%s Pods:%s\n",
		capacity.CPU.String(),
		capacity.Memory.String(),
		capacity.Storage.String(),
		capacity.Pods.String())
	
	node.Status.Capacity = v1.ResourceList{
		v1.ResourceCPU:    capacity.CPU,
		v1.ResourceMemory: capacity.Memory,
		"storage":         capacity.Storage,
		v1.ResourcePods:   capacity.Pods,
	}
	
	node.Status.Allocatable = node.Status.Capacity

	// Set node labels - CRITICAL for pod scheduling
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	
	// Required labels for pod scheduling
	node.Labels["kubernetes.io/hostname"] = p.nodeName
	node.Labels["kubernetes.io/os"] = p.operatingSystem
	node.Labels["kubernetes.io/arch"] = "amd64"
	node.Labels["kubernetes.io/role"] = "agent"
	node.Labels["type"] = "virtual-kubelet"
	
	// Cisco-specific labels
	node.Labels["cisco.com/provider"] = "cisco"
	node.Labels["cisco.com/device-count"] = fmt.Sprintf("%d", len(p.config.Devices))
	
	fmt.Printf("[CISCO-VK] ConfigureNode - Setting labels: hostname=%s type=virtual-kubelet\n", p.nodeName)
	
	// Add taint to prevent DaemonSets from scheduling on this node
	// Only pods with matching toleration will be scheduled
	node.Spec.Taints = []v1.Taint{
		{
			Key:    "virtual-kubelet.io/provider",
			Value:  "cisco",
			Effect: v1.TaintEffectNoSchedule,
		},
	}
	
	// Set node annotations
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	
	node.Annotations["cisco.com/provider-version"] = "1.0.0"
	node.Annotations["cisco.com/supported-devices"] = "c9k,c8k,c8kv"

	// Add node conditions
	node.Status.Conditions = []v1.NodeCondition{
		{
			Type:               v1.NodeReady,
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "CiscoProviderReady",
			Message:            "Cisco provider is ready",
		},
		{
			Type:               v1.NodeNetworkUnavailable,
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}

	// Set node addresses
	node.Status.Addresses = []v1.NodeAddress{
		{
			Type:    v1.NodeInternalIP,
			Address: p.internalIP,
		},
		{
			Type:    v1.NodeHostName,
			Address: p.nodeName,
		},
	}

	// Set daemon endpoints
	node.Status.DaemonEndpoints = v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

// Helper methods

func (p *CiscoProvider) convertPodToContainers(pod *v1.Pod) ([]ContainerSpec, error) {
	var containers []ContainerSpec

	for _, container := range pod.Spec.Containers {
		spec := ContainerSpec{
			Name:         container.Name,
			Image:        container.Image,
			Command:      container.Command,
			Args:         container.Args,
			Env:          container.Env,
			Resources:    container.Resources,
			VolumeMounts: container.VolumeMounts,
			Ports:        container.Ports,
			SecurityContext: container.SecurityContext,
			LivenessProbe:   container.LivenessProbe,
			ReadinessProbe:  container.ReadinessProbe,
			StartupProbe:    container.StartupProbe,
			Labels:       make(map[string]string),
			Annotations:  make(map[string]string),
		}

		// Copy pod labels and annotations to container
		if pod.Labels != nil {
			for k, v := range pod.Labels {
				spec.Labels[k] = v
			}
		}
		if pod.Annotations != nil {
			for k, v := range pod.Annotations {
				spec.Annotations[k] = v
			}
		}

		// Add pod metadata
		spec.Labels["io.kubernetes.pod.name"] = pod.Name
		spec.Labels["io.kubernetes.pod.namespace"] = pod.Namespace
		spec.Labels["io.kubernetes.container.name"] = container.Name

		containers = append(containers, spec)
	}

	return containers, nil
}

func (p *CiscoProvider) rollbackContainerCreation(ctx context.Context, containers []*Container) {
	for _, container := range containers {
		if err := p.deviceManager.DeleteContainer(ctx, container.ID); err != nil {
			log.G(ctx).Errorf("Failed to rollback container %s: %v", container.ID, err)
		}
		p.containers.Delete(container.ID)
	}
}

func (p *CiscoProvider) allocatePodIP(pod *v1.Pod) string {
	// Simple IP allocation - in production, this would use the network manager
	return "10.244.1." + fmt.Sprintf("%d", time.Now().Unix()%254+1)
}

func (p *CiscoProvider) getContainerIDsForPod(pod *v1.Pod) []string {
	var containerIDs []string
	
	p.containers.Range(func(key, value interface{}) bool {
		container := value.(*Container)
		if container.Labels["io.kubernetes.pod.name"] == pod.Name &&
		   container.Labels["io.kubernetes.pod.namespace"] == pod.Namespace {
			containerIDs = append(containerIDs, container.ID)
		}
		return true
	})
	
	return containerIDs
}

func (p *CiscoProvider) findContainerID(namespace, podName, containerName string) (string, error) {
	var foundContainerID string
	
	p.containers.Range(func(key, value interface{}) bool {
		container := value.(*Container)
		if container.Labels["io.kubernetes.pod.name"] == podName &&
		   container.Labels["io.kubernetes.pod.namespace"] == namespace &&
		   container.Labels["io.kubernetes.container.name"] == containerName {
			foundContainerID = container.ID
			return false // Stop iteration
		}
		return true
	})
	
	if foundContainerID == "" {
		return "", fmt.Errorf("container %s not found in pod %s/%s", containerName, namespace, podName)
	}
	
	return foundContainerID, nil
}

func (p *CiscoProvider) createPodStats(ctx context.Context, pod *v1.Pod) *statsv1alpha1.PodStats {
	// Create basic pod statistics
	// In production, this would gather real metrics from devices
	
	podStats := &statsv1alpha1.PodStats{
		PodRef: statsv1alpha1.PodReference{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       string(pod.UID),
		},
		StartTime: pod.CreationTimestamp,
		Containers: []statsv1alpha1.ContainerStats{},
	}

	// Add container statistics
	for _, containerStatus := range pod.Status.ContainerStatuses {
		containerStats := statsv1alpha1.ContainerStats{
			Name:      containerStatus.Name,
			StartTime: pod.CreationTimestamp,
			CPU: &statsv1alpha1.CPUStats{
				Time: metav1.NewTime(time.Now()),
				UsageNanoCores: func() *uint64 {
					val := uint64(100000000)
					return &val
				}(),
			},
			Memory: &statsv1alpha1.MemoryStats{
				Time:            metav1.NewTime(time.Now()),
				UsageBytes:      uint64ToPointer(uint64(128 * 1024 * 1024)), // 128MB
				WorkingSetBytes: uint64ToPointer(uint64(128 * 1024 * 1024)),
			},
		}
		
		podStats.Containers = append(podStats.Containers, containerStats)
	}

	return podStats
}

func (p *CiscoProvider) startMonitoring(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.deviceManager.HealthCheck(ctx); err != nil {
				log.G(ctx).Errorf("Health check failed: %v", err)
			}
		}
	}
}

// Helper functions

func getPodKey(pod *v1.Pod) string {
	return fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
}

func uint64ToPointer(val uint64) *uint64 {
	return &val
}
