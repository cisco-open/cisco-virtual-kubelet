package cisco

import (
	"fmt"
	"sort"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// DeviceScheduler handles scheduling of containers to devices
type DeviceScheduler struct {
	devices      map[string]*DeviceResources
	mutex        sync.RWMutex
	strategy     SchedulingStrategy
}

// DeviceResources tracks resource allocation for a device
type DeviceResources struct {
	Name         string
	Capacity     *DeviceCapability
	Allocated    v1.ResourceList
	Available    v1.ResourceList
	Score        float64
	Affinity     map[string]float64 // Node affinity scores
}

// SchedulingStrategy defines different scheduling algorithms
type SchedulingStrategy string

const (
	StrategyLeastAllocated SchedulingStrategy = "least-allocated"
	StrategyMostAllocated  SchedulingStrategy = "most-allocated" 
	StrategyBalanced       SchedulingStrategy = "balanced"
	StrategyResourceBased  SchedulingStrategy = "resource-based"
)

// NewDeviceScheduler creates a new device scheduler
func NewDeviceScheduler() *DeviceScheduler {
	return &DeviceScheduler{
		devices:  make(map[string]*DeviceResources),
		strategy: StrategyBalanced,
	}
}

// AddDevice adds a device to the scheduler
func (ds *DeviceScheduler) AddDevice(name string, capacity *DeviceCapability) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	available := v1.ResourceList{
		v1.ResourceCPU:    capacity.CPU.DeepCopy(),
		v1.ResourceMemory: capacity.Memory.DeepCopy(),
		"storage":         capacity.Storage.DeepCopy(),
		v1.ResourcePods:   capacity.Pods.DeepCopy(),
	}

	ds.devices[name] = &DeviceResources{
		Name:      name,
		Capacity:  capacity,
		Allocated: make(v1.ResourceList),
		Available: available,
		Affinity:  make(map[string]float64),
	}
}

// RemoveDevice removes a device from the scheduler
func (ds *DeviceScheduler) RemoveDevice(name string) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	delete(ds.devices, name)
}

// UpdateResourceUsage updates resource allocation for a device
func (ds *DeviceScheduler) UpdateResourceUsage(deviceName string, resources v1.ResourceRequirements) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	device, exists := ds.devices[deviceName]
	if !exists {
		return
	}

	// Add to allocated resources
	for resourceName, quantity := range resources.Requests {
		if allocated, exists := device.Allocated[resourceName]; exists {
			allocated.Add(quantity)
			device.Allocated[resourceName] = allocated
		} else {
			device.Allocated[resourceName] = quantity.DeepCopy()
		}
	}

	// Update available resources
	ds.updateAvailableResources(device)
}

// FreeResources frees up resources when a container is deleted
func (ds *DeviceScheduler) FreeResources(deviceName string, resources v1.ResourceRequirements) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	device, exists := ds.devices[deviceName]
	if !exists {
		return
	}

	// Subtract from allocated resources
	for resourceName, quantity := range resources.Requests {
		if allocated, exists := device.Allocated[resourceName]; exists {
			allocated.Sub(quantity)
			device.Allocated[resourceName] = allocated
		}
	}

	// Update available resources
	ds.updateAvailableResources(device)
}

// ScheduleContainer selects the best device for a container
func (ds *DeviceScheduler) ScheduleContainer(spec ContainerSpec) (string, error) {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	if len(ds.devices) == 0 {
		return "", fmt.Errorf("no devices available for scheduling")
	}

	// Get resource requirements
	requirements := spec.Resources.Requests
	if requirements == nil {
		requirements = make(v1.ResourceList)
	}

	// Find suitable devices
	suitableDevices := ds.findSuitableDevices(requirements, spec)
	if len(suitableDevices) == 0 {
		return "", fmt.Errorf("no suitable devices found for container requirements")
	}

	// Score devices based on strategy
	ds.scoreDevices(suitableDevices, requirements, spec)

	// Sort by score (highest first)
	sort.Slice(suitableDevices, func(i, j int) bool {
		return suitableDevices[i].Score > suitableDevices[j].Score
	})

	return suitableDevices[0].Name, nil
}

// findSuitableDevices finds devices that can accommodate the container
func (ds *DeviceScheduler) findSuitableDevices(requirements v1.ResourceList, spec ContainerSpec) []*DeviceResources {
	var suitable []*DeviceResources

	for _, device := range ds.devices {
		if ds.canScheduleOnDevice(device, requirements, spec) {
			suitable = append(suitable, device)
		}
	}

	return suitable
}

// canScheduleOnDevice checks if a container can be scheduled on a device
func (ds *DeviceScheduler) canScheduleOnDevice(device *DeviceResources, requirements v1.ResourceList, spec ContainerSpec) bool {
	// Check resource availability
	for resourceName, required := range requirements {
		available := device.Available[resourceName]
		if available.Cmp(required) < 0 {
			return false
		}
	}

	// Check architecture compatibility
	if len(device.Capacity.SupportedArch) > 0 {
		archCompatible := false
		if arch, exists := spec.Annotations["kubernetes.io/arch"]; exists {
			for _, supportedArch := range device.Capacity.SupportedArch {
				if arch == supportedArch {
					archCompatible = true
					break
				}
			}
		} else {
			// Default to first supported architecture
			archCompatible = true
		}
		
		if !archCompatible {
			return false
		}
	}

	// Check node selectors
	if nodeSelector := spec.Labels["nodeSelector"]; nodeSelector != "" {
		// Parse and validate node selector constraints
		// Implementation would check device labels against selectors
	}

	// Check taints and tolerations
	// Implementation would validate tolerations against device taints

	return true
}

// scoreDevices calculates scheduling scores for devices
func (ds *DeviceScheduler) scoreDevices(devices []*DeviceResources, requirements v1.ResourceList, spec ContainerSpec) {
	for _, device := range devices {
		switch ds.strategy {
		case StrategyLeastAllocated:
			device.Score = ds.calculateLeastAllocatedScore(device, requirements)
		case StrategyMostAllocated:
			device.Score = ds.calculateMostAllocatedScore(device, requirements)
		case StrategyBalanced:
			device.Score = ds.calculateBalancedScore(device, requirements)
		case StrategyResourceBased:
			device.Score = ds.calculateResourceBasedScore(device, requirements, spec)
		default:
			device.Score = ds.calculateBalancedScore(device, requirements)
		}

		// Apply affinity bonuses
		device.Score += ds.calculateAffinityScore(device, spec)
	}
}

// calculateLeastAllocatedScore prioritizes devices with the most available resources
func (ds *DeviceScheduler) calculateLeastAllocatedScore(device *DeviceResources, requirements v1.ResourceList) float64 {
	cpuScore := ds.resourceUtilizationScore(device.Available[v1.ResourceCPU], device.Capacity.CPU)
	memoryScore := ds.resourceUtilizationScore(device.Available[v1.ResourceMemory], device.Capacity.Memory)
	
	return (cpuScore + memoryScore) / 2.0
}

// calculateMostAllocatedScore prioritizes devices with the least available resources
func (ds *DeviceScheduler) calculateMostAllocatedScore(device *DeviceResources, requirements v1.ResourceList) float64 {
	return 100.0 - ds.calculateLeastAllocatedScore(device, requirements)
}

// calculateBalancedScore balances between resource utilization and availability
func (ds *DeviceScheduler) calculateBalancedScore(device *DeviceResources, requirements v1.ResourceList) float64 {
	leastScore := ds.calculateLeastAllocatedScore(device, requirements)
	mostScore := ds.calculateMostAllocatedScore(device, requirements)
	
	// Weighted average favoring least allocated
	return (leastScore * 0.7) + (mostScore * 0.3)
}

// calculateResourceBasedScore scores based on specific resource requirements
func (ds *DeviceScheduler) calculateResourceBasedScore(device *DeviceResources, requirements v1.ResourceList, spec ContainerSpec) float64 {
	score := 0.0
	weights := map[v1.ResourceName]float64{
		v1.ResourceCPU:    1.0,
		v1.ResourceMemory: 1.0,
		"storage":         0.5,
	}

	totalWeight := 0.0
	for resourceName, weight := range weights {
		if required, exists := requirements[resourceName]; exists && !required.IsZero() {
			available := device.Available[resourceName]
			capacity := getDeviceCapacityForResource(device.Capacity, resourceName)
			
			if !capacity.IsZero() {
				utilization := float64(available.MilliValue()) / float64(capacity.MilliValue())
				resourceScore := utilization * 100.0
				score += resourceScore * weight
				totalWeight += weight
			}
		}
	}

	if totalWeight > 0 {
		return score / totalWeight
	}
	return 50.0 // Default score
}

// calculateAffinityScore calculates bonus scores based on affinity rules
func (ds *DeviceScheduler) calculateAffinityScore(device *DeviceResources, spec ContainerSpec) float64 {
	score := 0.0

	// Check for device type preferences
	if preferredType, exists := spec.Annotations["cisco.com/preferred-device-type"]; exists {
		// Implementation would check device type and add bonus
		_ = preferredType
		score += 10.0
	}

	// Check for zone/region preferences
	if preferredZone, exists := spec.Annotations["cisco.com/preferred-zone"]; exists {
		// Implementation would check device zone and add bonus
		_ = preferredZone
		score += 5.0
	}

	return score
}

// resourceUtilizationScore calculates a score based on resource utilization
func (ds *DeviceScheduler) resourceUtilizationScore(available, capacity resource.Quantity) float64 {
	if capacity.IsZero() {
		return 0.0
	}

	utilization := float64(available.MilliValue()) / float64(capacity.MilliValue())
	return utilization * 100.0
}

// getDeviceCapacityForResource gets capacity for a specific resource
func getDeviceCapacityForResource(capacity *DeviceCapability, resourceName v1.ResourceName) resource.Quantity {
	switch resourceName {
	case v1.ResourceCPU:
		return capacity.CPU
	case v1.ResourceMemory:
		return capacity.Memory
	case "storage":
		return capacity.Storage
	case v1.ResourcePods:
		return capacity.Pods
	default:
		return resource.Quantity{}
	}
}

// updateAvailableResources recalculates available resources for a device
func (ds *DeviceScheduler) updateAvailableResources(device *DeviceResources) {
	device.Available = v1.ResourceList{
		v1.ResourceCPU:    device.Capacity.CPU.DeepCopy(),
		v1.ResourceMemory: device.Capacity.Memory.DeepCopy(),
		"storage":         device.Capacity.Storage.DeepCopy(),
		v1.ResourcePods:   device.Capacity.Pods.DeepCopy(),
	}

	// Subtract allocated resources
	for resourceName, allocated := range device.Allocated {
		if available, exists := device.Available[resourceName]; exists {
			available.Sub(allocated)
			device.Available[resourceName] = available
		}
	}
}

// GetDeviceResources returns current resource state for a device
func (ds *DeviceScheduler) GetDeviceResources(deviceName string) (*DeviceResources, bool) {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	device, exists := ds.devices[deviceName]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid concurrent modification
	return &DeviceResources{
		Name:      device.Name,
		Capacity:  device.Capacity,
		Allocated: copyResourceList(device.Allocated),
		Available: copyResourceList(device.Available),
		Score:     device.Score,
		Affinity:  copyAffinityMap(device.Affinity),
	}, true
}

// GetAllDeviceResources returns resource state for all devices
func (ds *DeviceScheduler) GetAllDeviceResources() map[string]*DeviceResources {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	result := make(map[string]*DeviceResources)
	for name, device := range ds.devices {
		result[name] = &DeviceResources{
			Name:      device.Name,
			Capacity:  device.Capacity,
			Allocated: copyResourceList(device.Allocated),
			Available: copyResourceList(device.Available),
			Score:     device.Score,
			Affinity:  copyAffinityMap(device.Affinity),
		}
	}

	return result
}

// SetSchedulingStrategy changes the scheduling strategy
func (ds *DeviceScheduler) SetSchedulingStrategy(strategy SchedulingStrategy) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	ds.strategy = strategy
}

// GetSchedulingStrategy returns the current scheduling strategy
func (ds *DeviceScheduler) GetSchedulingStrategy() SchedulingStrategy {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	return ds.strategy
}

// Helper functions

func copyResourceList(original v1.ResourceList) v1.ResourceList {
	copy := make(v1.ResourceList)
	for name, quantity := range original {
		copy[name] = quantity.DeepCopy()
	}
	return copy
}

func copyAffinityMap(original map[string]float64) map[string]float64 {
	copy := make(map[string]float64)
	for key, value := range original {
		copy[key] = value
	}
	return copy
}

// UpdateDeviceAffinity updates affinity scores for a device
func (ds *DeviceScheduler) UpdateDeviceAffinity(deviceName string, affinityKey string, score float64) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if device, exists := ds.devices[deviceName]; exists {
		device.Affinity[affinityKey] = score
	}
}

// GetSchedulingMetrics returns metrics about the scheduler state
func (ds *DeviceScheduler) GetSchedulingMetrics() map[string]interface{} {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	metrics := map[string]interface{}{
		"total_devices":     len(ds.devices),
		"scheduling_strategy": string(ds.strategy),
		"devices": make(map[string]interface{}),
	}

	deviceMetrics := make(map[string]interface{})
	for name, device := range ds.devices {
		cpuUtil := 0.0
		memUtil := 0.0

		if !device.Capacity.CPU.IsZero() {
			allocatedCPU := device.Allocated[v1.ResourceCPU]
			cpuUtil = float64(allocatedCPU.MilliValue()) / float64(device.Capacity.CPU.MilliValue()) * 100
		}

		if !device.Capacity.Memory.IsZero() {
			allocatedMem := device.Allocated[v1.ResourceMemory]
			memUtil = float64(allocatedMem.Value()) / float64(device.Capacity.Memory.Value()) * 100
		}

		deviceMetrics[name] = map[string]interface{}{
			"cpu_utilization":    cpuUtil,
			"memory_utilization": memUtil,
			"score":              device.Score,
		}
	}

	metrics["devices"] = deviceMetrics
	return metrics
}
