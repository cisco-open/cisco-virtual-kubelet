package cisco

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeviceManager manages multiple Cisco devices and abstracts their operations
type DeviceManager struct {
	devices     map[string]*ManagedDevice
	config      *CiscoConfig
	mutex       sync.RWMutex
	scheduler   *DeviceScheduler
	networkMgr  *NetworkManager
	monitor     *ResourceMonitor
}

// ManagedDevice represents a managed Cisco device with its client and state
type ManagedDevice struct {
	Config          DeviceConfig
	Client          DeviceClient
	State           *DeviceState
	Containers      map[string]*Container
	Networks        map[string]*NetworkNamespace
	AppHostingMgr   *AppHostingManager // Added for full app-hosting lifecycle management
	LastHealthCheck time.Time
	mutex           sync.RWMutex
}

// NewDeviceManager creates a new device manager
func NewDeviceManager(config *CiscoConfig) (*DeviceManager, error) {
	dm := &DeviceManager{
		devices:    make(map[string]*ManagedDevice),
		config:     config,
		scheduler:  NewDeviceScheduler(),
		networkMgr: NewNetworkManager(config),
		monitor:    NewResourceMonitor(),
	}

	// Initialize devices
	for _, deviceConfig := range config.Devices {
		if err := dm.AddDevice(deviceConfig); err != nil {
			return nil, fmt.Errorf("failed to add device %s: %v", deviceConfig.Name, err)
		}
	}

	return dm, nil
}

// AddDevice adds a new device to the manager
func (dm *DeviceManager) AddDevice(config DeviceConfig) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	if _, exists := dm.devices[config.Name]; exists {
		return fmt.Errorf("device %s already exists", config.Name)
	}

	// Create device client based on type
	var client DeviceClient
	switch config.Type {
	case DeviceTypeC9K, DeviceTypeC8K, DeviceTypeC8Kv:
		client = NewIOSXEClient(config)
	default:
		return fmt.Errorf("unsupported device type: %s", config.Type)
	}

	// Initialize AppHostingManager for IOS XE devices
	var appHostingMgr *AppHostingManager
	if iosxeClient, ok := client.(*IOSXEClient); ok {
		appHostingMgr = NewAppHostingManager(iosxeClient)
		// Start monitoring for this device
		appHostingMgr.StartMonitoring(context.Background())
	}

	managedDevice := &ManagedDevice{
		Config:        config,
		Client:        client,
		Containers:    make(map[string]*Container),
		Networks:      make(map[string]*NetworkNamespace),
		AppHostingMgr: appHostingMgr,
		State: &DeviceState{
			Name:       config.Name,
			Status:     DeviceStatusUnknown,
			Capacity:   config.Capabilities,
			Containers: []Container{},
			Conditions: []DeviceCondition{},
		},
	}

	dm.devices[config.Name] = managedDevice
	dm.scheduler.AddDevice(config.Name, &config.Capabilities)

	return nil
}

// RemoveDevice removes a device from the manager
func (dm *DeviceManager) RemoveDevice(name string) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	device, exists := dm.devices[name]
	if !exists {
		return fmt.Errorf("device %s not found", name)
	}

	// Check if device has running containers
	if len(device.Containers) > 0 {
		return fmt.Errorf("device %s has %d running containers", name, len(device.Containers))
	}

	// Disconnect from device
	if device.Client.IsConnected() {
		device.Client.Disconnect()
	}

	delete(dm.devices, name)
	dm.scheduler.RemoveDevice(name)

	return nil
}

// GetDevice returns a managed device by name
func (dm *DeviceManager) GetDevice(name string) (*ManagedDevice, error) {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	device, exists := dm.devices[name]
	if !exists {
		return nil, fmt.Errorf("device %s not found", name)
	}

	return device, nil
}

// ListDevices returns all managed devices
func (dm *DeviceManager) ListDevices() []*ManagedDevice {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	devices := make([]*ManagedDevice, 0, len(dm.devices))
	for _, device := range dm.devices {
		devices = append(devices, device)
	}

	return devices
}

// Connect connects to all devices
func (dm *DeviceManager) Connect(ctx context.Context) error {
	dm.mutex.RLock()
	devices := make([]*ManagedDevice, 0, len(dm.devices))
	for _, device := range dm.devices {
		devices = append(devices, device)
	}
	dm.mutex.RUnlock()

	var lastError error
	connectedCount := 0

	for _, device := range devices {
		if err := device.Client.Connect(); err != nil {
			log.G(ctx).Errorf("Failed to connect to device %s: %v", device.Config.Name, err)
			lastError = err
			device.updateStatus(DeviceStatusNotReady)
		} else {
			log.G(ctx).Infof("Successfully connected to device %s", device.Config.Name)
			device.updateStatus(DeviceStatusReady)
			connectedCount++
			
			// Query actual resources from device and update capabilities
			if err := device.updateCapacityFromDevice(ctx); err != nil {
				log.G(ctx).Warnf("Failed to query device resources for %s: %v", device.Config.Name, err)
				// Continue with configured capacity
			}
		}
	}

	if connectedCount == 0 {
		return fmt.Errorf("failed to connect to any devices: %v", lastError)
	}

	log.G(ctx).Infof("Connected to %d/%d devices", connectedCount, len(devices))
	return nil
}

// Disconnect disconnects from all devices
func (dm *DeviceManager) Disconnect() error {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	for _, device := range dm.devices {
		if device.Client.IsConnected() {
			device.Client.Disconnect()
			device.updateStatus(DeviceStatusUnknown)
		}
	}
	
	return nil
}

// CreateContainer creates a container on the best available device
func (dm *DeviceManager) CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error) {
	fmt.Printf("[CISCO-VK] 🎯 DeviceManager.CreateContainer called for: %s\n", spec.Name)
	
	// Select the best device for this container
	fmt.Printf("[CISCO-VK] 📋 Scheduling container on best device...\n")
	deviceName, err := dm.scheduler.ScheduleContainer(spec)
	if err != nil {
		fmt.Printf("[CISCO-VK] ❌ Scheduling failed: %v\n", err)
		return nil, fmt.Errorf("failed to schedule container: %v", err)
	}
	fmt.Printf("[CISCO-VK] ✅ Scheduled to device: %s\n", deviceName)

	device, err := dm.GetDevice(deviceName)
	if err != nil {
		fmt.Printf("[CISCO-VK] ❌ Failed to get device: %v\n", err)
		return nil, err
	}
	fmt.Printf("[CISCO-VK] ✅ Got device: %s\n", deviceName)

	// Create network namespace if needed
	fmt.Printf("[CISCO-VK] 🌐 Allocating network resources...\n")
	networkID, err := dm.networkMgr.CreateNetworkNamespace(ctx, device, spec)
	if err != nil {
		fmt.Printf("[CISCO-VK] ❌ Network allocation failed: %v\n", err)
		return nil, fmt.Errorf("failed to create network namespace: %v", err)
	}
	fmt.Printf("[CISCO-VK] ✅ Network allocated: %s\n", networkID)

	// Update container spec with network information
	spec.Annotations["cisco.com/network-id"] = networkID

	// Create container on device
	fmt.Printf("[CISCO-VK] 🚀 Calling device.Client.CreateContainer()...\n")
	container, err := device.Client.CreateContainer(ctx, spec)
	if err != nil {
		fmt.Printf("[CISCO-VK] ❌ device.Client.CreateContainer failed: %v\n", err)
		return nil, fmt.Errorf("failed to create container on device %s: %v", deviceName, err)
	}
	fmt.Printf("[CISCO-VK] ✅ device.Client.CreateContainer succeeded\n")

	// Update device state
	device.mutex.Lock()
	device.Containers[container.ID] = container
	device.State.Containers = append(device.State.Containers, *container)
	device.mutex.Unlock()

	// Update scheduler with resource usage
	dm.scheduler.UpdateResourceUsage(deviceName, spec.Resources)

	log.G(ctx).Infof("Created container %s on device %s", container.Name, deviceName)
	return container, nil
}

// DeleteContainer removes a container from its device
func (dm *DeviceManager) DeleteContainer(ctx context.Context, containerID string) error {
	log.G(ctx).Infof("🗑️ Deleting container %s", containerID)
	
	// Find the device that has this container
	var targetDevice *ManagedDevice
	for _, device := range dm.devices {
		device.mutex.RLock()
		if _, exists := device.Containers[containerID]; exists {
			targetDevice = device
			device.mutex.RUnlock()
			break
		}
		device.mutex.RUnlock()
	}

	if targetDevice == nil {
		log.G(ctx).Warnf("Container %s not found on any device", containerID)
		return fmt.Errorf("container %s not found on any device", containerID)
	}

	// Get container info before deletion
	container := targetDevice.Containers[containerID]
	log.G(ctx).Infof("  Found container on device %s", targetDevice.Config.Name)

	// RESTCONF-BASED CLEANUP: Use AppHostingMgr to undeploy via RESTCONF
	if targetDevice.AppHostingMgr != nil {
		log.G(ctx).Infof("  Using RESTCONF to undeploy app %s", containerID)
		
		// Undeploy application via RESTCONF (stop, deactivate, uninstall)
		if err := targetDevice.AppHostingMgr.UndeployApplication(ctx, containerID); err != nil {
			log.G(ctx).Errorf("  Failed to undeploy app via RESTCONF: %v", err)
			// Continue with cleanup even if RESTCONF fails
		} else {
			log.G(ctx).Infof("  ✅ Successfully undeployed app %s via RESTCONF", containerID)
		}
	} else {
		log.G(ctx).Warnf("  No AppHostingMgr available - cannot undeploy via RESTCONF")
		// Fall back to old SSH method if RESTCONF not available
		err := targetDevice.Client.DestroyContainer(ctx, containerID)
		if err != nil {
			log.G(ctx).Errorf("  Failed to delete container via SSH: %v", err)
			return fmt.Errorf("failed to delete container from device %s: %v", targetDevice.Config.Name, err)
		}
	}

	// Clean up network namespace
	if networkID, exists := container.Annotations["cisco.com/network-id"]; exists {
		dm.networkMgr.DeleteNetworkNamespace(ctx, targetDevice, networkID)
	}

	// Update device state
	targetDevice.mutex.Lock()
	delete(targetDevice.Containers, containerID)
	
	// Remove from containers slice
	for i, c := range targetDevice.State.Containers {
		if c.ID == containerID {
			targetDevice.State.Containers = append(
				targetDevice.State.Containers[:i],
				targetDevice.State.Containers[i+1:]...,
			)
			break
		}
	}
	targetDevice.mutex.Unlock()

	// Update scheduler with freed resources
	dm.scheduler.FreeResources(targetDevice.Config.Name, container.Resources.ToResourceRequirements())
	
	log.G(ctx).Infof("✅ Successfully deleted container %s from device %s", containerID, targetDevice.Config.Name)
	return nil
}

// GetContainer retrieves a container by ID
func (dm *DeviceManager) GetContainer(ctx context.Context, containerID string) (*Container, error) {
	for _, device := range dm.devices {
		device.mutex.RLock()
		if container, exists := device.Containers[containerID]; exists {
			device.mutex.RUnlock()
			
			// Get fresh status from device
			freshContainer, err := device.Client.GetContainer(ctx, containerID)
			if err != nil {
				return container, nil // Return cached version on error
			}
			
			// Update cached container
			device.mutex.Lock()
			device.Containers[containerID] = freshContainer
			device.mutex.Unlock()
			
			return freshContainer, nil
		}
		device.mutex.RUnlock()
	}

	return nil, fmt.Errorf("container %s not found", containerID)
}

// ListContainers returns all containers across all devices
func (dm *DeviceManager) ListContainers(ctx context.Context) ([]*Container, error) {
	var allContainers []*Container

	for _, device := range dm.devices {
		containers, err := device.Client.ListContainers(ctx)
		if err != nil {
			log.G(ctx).Errorf("Failed to list containers from device %s: %v", device.Config.Name, err)
			continue
		}

		// Update device state with fresh container list
		device.mutex.Lock()
		device.Containers = make(map[string]*Container)
		device.State.Containers = make([]Container, len(containers))
		
		for i, container := range containers {
			device.Containers[container.ID] = container
			device.State.Containers[i] = *container
		}
		device.mutex.Unlock()

		allContainers = append(allContainers, containers...)
	}

	return allContainers, nil
}

// ExecuteCommand executes a command in a container
func (dm *DeviceManager) ExecuteCommand(ctx context.Context, containerID string, cmd []string) (*ExecResult, error) {
	container, err := dm.GetContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}

	device, err := dm.GetDevice(container.DeviceID)
	if err != nil {
		return nil, err
	}

	// For IOx containers, we need to execute commands via the device client
	cmdStr := fmt.Sprintf("app-hosting appid %s exec cmd \"%s\"", container.Name, strings.Join(cmd, " "))
	return device.Client.ExecuteCommand(cmdStr)
}

// GetContainerLogs retrieves logs from a container
func (dm *DeviceManager) GetContainerLogs(ctx context.Context, containerID string, opts LogOptions) (io.ReadCloser, error) {
	container, err := dm.GetContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}

	device, err := dm.GetDevice(container.DeviceID)
	if err != nil {
		return nil, err
	}

	// For IOx containers, logs are typically retrieved via CLI commands
	var cmdStr string
	if opts.Follow {
		cmdStr = fmt.Sprintf("show app-hosting list appid %s | include Log", container.Name)
	} else {
		cmdStr = fmt.Sprintf("show app-hosting log appid %s", container.Name)
	}

	result, err := device.Client.ExecuteCommand(cmdStr)
	if err != nil {
		return nil, err
	}

	return io.NopCloser(strings.NewReader(result.Stdout)), nil
}

// GetResourceUsage returns aggregated resource usage across all devices
func (dm *DeviceManager) GetResourceUsage(ctx context.Context) (*ResourceUsage, error) {
	totalUsage := &ResourceUsage{
		CPU:       resource.Quantity{},
		Memory:    resource.Quantity{},
		Storage:   resource.Quantity{},
		Timestamp: metav1.Now(),
	}

	for _, device := range dm.devices {
		if !device.Client.IsConnected() {
			continue
		}

		usage, err := device.Client.GetResourceUsage()
		if err != nil {
			log.G(ctx).Errorf("Failed to get resource usage from device %s: %v", device.Config.Name, err)
			continue
		}

		totalUsage.CPU.Add(usage.CPU)
		totalUsage.Memory.Add(usage.Memory)
		totalUsage.Storage.Add(usage.Storage)
		totalUsage.NetworkRx += usage.NetworkRx
		totalUsage.NetworkTx += usage.NetworkTx
	}

	return totalUsage, nil
}

// HealthCheck performs health checks on all devices
func (dm *DeviceManager) HealthCheck(ctx context.Context) error {
	for _, device := range dm.devices {
		go func(d *ManagedDevice) {
			if err := d.healthCheck(ctx); err != nil {
				log.G(ctx).Errorf("Health check failed for device %s: %v", d.Config.Name, err)
			}
		}(device)
	}

	return nil
}

// GetCapacity returns total capacity across all devices
func (dm *DeviceManager) GetCapacity() *DeviceCapability {
	totalCapacity := &DeviceCapability{
		CPU:       resource.Quantity{},
		Memory:    resource.Quantity{},
		Storage:   resource.Quantity{},
		Pods:      resource.Quantity{},
	}

	dm.mutex.RLock()
	defer dm.mutex.RUnlock()

	for _, device := range dm.devices {
		if device.State.Status == DeviceStatusReady {
			totalCapacity.CPU.Add(device.Config.Capabilities.CPU)
			totalCapacity.Memory.Add(device.Config.Capabilities.Memory)
			totalCapacity.Storage.Add(device.Config.Capabilities.Storage)
			totalCapacity.Pods.Add(device.Config.Capabilities.Pods)
		}
	}

	return totalCapacity
}

// ManagedDevice methods

func (md *ManagedDevice) updateStatus(status DeviceStatus) {
	md.mutex.Lock()
	defer md.mutex.Unlock()

	if md.State.Status != status {
		condition := DeviceCondition{
			Type:               string(status),
			Status:             v1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "StatusChanged",
			Message:            fmt.Sprintf("Device status changed to %s", status),
		}

		md.State.Status = status
		md.State.Conditions = append(md.State.Conditions, condition)
		md.State.LastSeen = metav1.Now()
	}
}

func (md *ManagedDevice) healthCheck(ctx context.Context) error {
	if !md.Client.IsConnected() {
		md.updateStatus(DeviceStatusNotReady)
		return fmt.Errorf("device not connected")
	}

	// Perform ping/health check
	systemInfo, err := md.Client.GetSystemInfo()
	if err != nil {
		md.updateStatus(DeviceStatusNotReady)
		return err
	}

	// Update device information
	md.mutex.Lock()
	md.State.Version = systemInfo.Version
	md.LastHealthCheck = time.Now()
	md.mutex.Unlock()

	md.updateStatus(DeviceStatusReady)
	return nil
}

// updateCapacityFromDevice queries the device for actual resource availability
// and updates the device capability configuration with real-time data
func (md *ManagedDevice) updateCapacityFromDevice(ctx context.Context) error {
	// Only works with IOS XE clients
	iosxeClient, ok := md.Client.(*IOSXEClient)
	if !ok {
		return fmt.Errorf("device client does not support resource querying")
	}

	// Query app-hosting resources from device
	resources, err := iosxeClient.GetAppHostingResources(ctx)
	if err != nil {
		return err
	}

	md.mutex.Lock()
	defer md.mutex.Unlock()

	// Update capabilities with actual device resources
	// Convert from device units to Kubernetes resource quantities
	md.Config.Capabilities.CPU = resource.MustParse(fmt.Sprintf("%d", resources.VCPU.Count))
	md.Config.Capabilities.Memory = resource.MustParse(fmt.Sprintf("%dMi", resources.Memory.Available))
	md.Config.Capabilities.Storage = resource.MustParse(fmt.Sprintf("%dMi", resources.Storage.Available))
	
	// Conservative pod limit based on memory (assume ~256MB per pod minimum)
	maxPods := resources.Memory.Available / 256
	if maxPods > 50 {
		maxPods = 50 // Cap at 50 pods
	}
	if maxPods < 5 {
		maxPods = 5 // Minimum 5 pods
	}
	md.Config.Capabilities.Pods = resource.MustParse(fmt.Sprintf("%d", maxPods))

	fmt.Printf("[CISCO-VK] Device %s real capacity: CPU:%s Memory:%s Storage:%s Pods:%s\n",
		md.Config.Name,
		md.Config.Capabilities.CPU.String(),
		md.Config.Capabilities.Memory.String(),
		md.Config.Capabilities.Storage.String(),
		md.Config.Capabilities.Pods.String())

	return nil
}

// Helper method to convert ResourceUsage to ResourceRequirements
func (ru *ResourceUsage) ToResourceRequirements() v1.ResourceRequirements {
	return v1.ResourceRequirements{
		Requests: v1.ResourceList{
			v1.ResourceCPU:    ru.CPU,
			v1.ResourceMemory: ru.Memory,
		},
	}
}
