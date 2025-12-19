package cisco

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceMonitor monitors resource usage across devices
type ResourceMonitor struct {
	metrics    map[string]*ResourceMetrics
	alerts     []ResourceAlert
	thresholds ResourceThresholds
	mutex      sync.RWMutex
	stopCh     chan struct{}
	running    bool
}

// ResourceMetrics represents resource usage metrics for a device
type ResourceMetrics struct {
	DeviceID           string             `json:"deviceId"`
	Timestamp          metav1.Time        `json:"timestamp"`
	CPUUsage           resource.Quantity  `json:"cpuUsage"`
	MemoryUsage        resource.Quantity  `json:"memoryUsage"`
	StorageUsage       resource.Quantity  `json:"storageUsage"`
	NetworkRxBytes     int64              `json:"networkRxBytes"`
	NetworkTxBytes     int64              `json:"networkTxBytes"`
	ContainerCount     int                `json:"containerCount"`
	CPUUtilization     float64            `json:"cpuUtilization"`
	MemoryUtilization  float64            `json:"memoryUtilization"`
	StorageUtilization float64            `json:"storageUtilization"`
	HealthStatus       DeviceHealthStatus `json:"healthStatus"`
	Alerts             []string           `json:"alerts"`
}

// ResourceAlert represents a resource usage alert
type ResourceAlert struct {
	ID         string        `json:"id"`
	DeviceID   string        `json:"deviceId"`
	Type       AlertType     `json:"type"`
	Severity   AlertSeverity `json:"severity"`
	Message    string        `json:"message"`
	Timestamp  metav1.Time   `json:"timestamp"`
	Resolved   bool          `json:"resolved"`
	ResolvedAt *metav1.Time  `json:"resolvedAt,omitempty"`
}

// ResourceThresholds defines alert thresholds
type ResourceThresholds struct {
	CPUWarning      float64 `json:"cpuWarning"`
	CPUCritical     float64 `json:"cpuCritical"`
	MemoryWarning   float64 `json:"memoryWarning"`
	MemoryCritical  float64 `json:"memoryCritical"`
	StorageWarning  float64 `json:"storageWarning"`
	StorageCritical float64 `json:"storageCritical"`
	ContainerLimit  int     `json:"containerLimit"`
}

// AlertType represents different types of alerts
type AlertType string

const (
	AlertTypeCPU        AlertType = "cpu"
	AlertTypeMemory     AlertType = "memory"
	AlertTypeStorage    AlertType = "storage"
	AlertTypeHealth     AlertType = "health"
	AlertTypeContainers AlertType = "containers"
	AlertTypeNetwork    AlertType = "network"
)

// AlertSeverity represents alert severity levels
type AlertSeverity string

const (
	AlertSeverityInfo     AlertSeverity = "info"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityCritical AlertSeverity = "critical"
)

// DeviceHealthStatus represents overall device health
type DeviceHealthStatus string

const (
	DeviceHealthHealthy  DeviceHealthStatus = "healthy"
	DeviceHealthWarning  DeviceHealthStatus = "warning"
	DeviceHealthCritical DeviceHealthStatus = "critical"
	DeviceHealthUnknown  DeviceHealthStatus = "unknown"
)

// NewResourceMonitor creates a new resource monitor
func NewResourceMonitor() *ResourceMonitor {
	return &ResourceMonitor{
		metrics: make(map[string]*ResourceMetrics),
		alerts:  make([]ResourceAlert, 0),
		thresholds: ResourceThresholds{
			CPUWarning:      70.0,
			CPUCritical:     90.0,
			MemoryWarning:   80.0,
			MemoryCritical:  95.0,
			StorageWarning:  75.0,
			StorageCritical: 90.0,
			ContainerLimit:  100,
		},
		stopCh: make(chan struct{}),
	}
}

// Start begins monitoring
func (rm *ResourceMonitor) Start(ctx context.Context, deviceManager *DeviceManager) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if rm.running {
		return nil
	}

	rm.running = true
	go rm.monitoringLoop(ctx, deviceManager)

	log.G(ctx).Info("Resource monitor started")
	return nil
}

// Stop stops monitoring
func (rm *ResourceMonitor) Stop() error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if !rm.running {
		return nil
	}

	rm.running = false
	close(rm.stopCh)

	return nil
}

// monitoringLoop runs the main monitoring loop
func (rm *ResourceMonitor) monitoringLoop(ctx context.Context, deviceManager *DeviceManager) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rm.stopCh:
			return
		case <-ticker.C:
			rm.collectMetrics(ctx, deviceManager)
		}
	}
}

// collectMetrics collects metrics from all devices
func (rm *ResourceMonitor) collectMetrics(ctx context.Context, deviceManager *DeviceManager) {
	devices := deviceManager.ListDevices()

	for _, device := range devices {
		go rm.collectDeviceMetrics(ctx, device)
	}
}

// collectDeviceMetrics collects metrics from a single device
func (rm *ResourceMonitor) collectDeviceMetrics(ctx context.Context, device *ManagedDevice) {
	if !device.Client.IsConnected() {
		rm.updateDeviceHealth(device.Config.Name, DeviceHealthUnknown)
		return
	}

	// Get resource usage from device
	usage, err := device.Client.GetResourceUsage()
	if err != nil {
		log.G(ctx).Errorf("Failed to get resource usage from device %s: %v", device.Config.Name, err)
		rm.updateDeviceHealth(device.Config.Name, DeviceHealthCritical)
		return
	}

	// Calculate utilization percentages
	cpuUtil := rm.calculateUtilization(usage.CPU, device.Config.Capabilities.CPU)
	memUtil := rm.calculateUtilization(usage.Memory, device.Config.Capabilities.Memory)
	storageUtil := rm.calculateUtilization(usage.Storage, device.Config.Capabilities.Storage)

	// Count containers on this device
	containerCount := len(device.Containers)

	// Create metrics
	metrics := &ResourceMetrics{
		DeviceID:           device.Config.Name,
		Timestamp:          usage.Timestamp,
		CPUUsage:           usage.CPU,
		MemoryUsage:        usage.Memory,
		StorageUsage:       usage.Storage,
		NetworkRxBytes:     usage.NetworkRx,
		NetworkTxBytes:     usage.NetworkTx,
		ContainerCount:     containerCount,
		CPUUtilization:     cpuUtil,
		MemoryUtilization:  memUtil,
		StorageUtilization: storageUtil,
		HealthStatus:       rm.calculateHealthStatus(cpuUtil, memUtil, storageUtil),
		Alerts:             []string{},
	}

	// Check for alerts
	rm.checkAlerts(ctx, metrics)

	// Store metrics
	rm.mutex.Lock()
	rm.metrics[device.Config.Name] = metrics
	rm.mutex.Unlock()
}

// calculateUtilization calculates resource utilization percentage
func (rm *ResourceMonitor) calculateUtilization(used, capacity resource.Quantity) float64 {
	if capacity.IsZero() {
		return 0.0
	}

	return float64(used.MilliValue()) / float64(capacity.MilliValue()) * 100.0
}

// calculateHealthStatus determines overall device health
func (rm *ResourceMonitor) calculateHealthStatus(cpuUtil, memUtil, storageUtil float64) DeviceHealthStatus {
	if cpuUtil >= rm.thresholds.CPUCritical ||
		memUtil >= rm.thresholds.MemoryCritical ||
		storageUtil >= rm.thresholds.StorageCritical {
		return DeviceHealthCritical
	}

	if cpuUtil >= rm.thresholds.CPUWarning ||
		memUtil >= rm.thresholds.MemoryWarning ||
		storageUtil >= rm.thresholds.StorageWarning {
		return DeviceHealthWarning
	}

	return DeviceHealthHealthy
}

// checkAlerts checks for resource usage alerts
func (rm *ResourceMonitor) checkAlerts(ctx context.Context, metrics *ResourceMetrics) {
	// Check CPU alerts
	if metrics.CPUUtilization >= rm.thresholds.CPUCritical {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeCPU, AlertSeverityCritical,
			fmt.Sprintf("CPU utilization critical: %.1f%%", metrics.CPUUtilization))
	} else if metrics.CPUUtilization >= rm.thresholds.CPUWarning {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeCPU, AlertSeverityWarning,
			fmt.Sprintf("CPU utilization high: %.1f%%", metrics.CPUUtilization))
	}

	// Check memory alerts
	if metrics.MemoryUtilization >= rm.thresholds.MemoryCritical {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeMemory, AlertSeverityCritical,
			fmt.Sprintf("Memory utilization critical: %.1f%%", metrics.MemoryUtilization))
	} else if metrics.MemoryUtilization >= rm.thresholds.MemoryWarning {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeMemory, AlertSeverityWarning,
			fmt.Sprintf("Memory utilization high: %.1f%%", metrics.MemoryUtilization))
	}

	// Check storage alerts
	if metrics.StorageUtilization >= rm.thresholds.StorageCritical {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeStorage, AlertSeverityCritical,
			fmt.Sprintf("Storage utilization critical: %.1f%%", metrics.StorageUtilization))
	} else if metrics.StorageUtilization >= rm.thresholds.StorageWarning {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeStorage, AlertSeverityWarning,
			fmt.Sprintf("Storage utilization high: %.1f%%", metrics.StorageUtilization))
	}

	// Check container count alerts
	if metrics.ContainerCount >= rm.thresholds.ContainerLimit {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeContainers, AlertSeverityWarning,
			fmt.Sprintf("Container count approaching limit: %d", metrics.ContainerCount))
	}

	// Check health alerts
	if metrics.HealthStatus == DeviceHealthCritical {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeHealth, AlertSeverityCritical,
			"Device health is critical")
	} else if metrics.HealthStatus == DeviceHealthWarning {
		rm.createAlert(ctx, metrics.DeviceID, AlertTypeHealth, AlertSeverityWarning,
			"Device health is degraded")
	}
}

// createAlert creates a new alert if it doesn't already exist
func (rm *ResourceMonitor) createAlert(ctx context.Context, deviceID string, alertType AlertType, severity AlertSeverity, message string) {
	alertID := fmt.Sprintf("%s-%s-%s", deviceID, string(alertType), string(severity))

	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	// Check if alert already exists and is not resolved
	for _, alert := range rm.alerts {
		if alert.ID == alertID && !alert.Resolved {
			return // Alert already exists
		}
	}

	// Create new alert
	alert := ResourceAlert{
		ID:        alertID,
		DeviceID:  deviceID,
		Type:      alertType,
		Severity:  severity,
		Message:   message,
		Timestamp: metav1.Now(),
		Resolved:  false,
	}

	rm.alerts = append(rm.alerts, alert)
	log.G(ctx).Warnf("Alert created: %s - %s", alertID, message)
}

// resolveAlert marks an alert as resolved
func (rm *ResourceMonitor) resolveAlert(alertID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	for i := range rm.alerts {
		if rm.alerts[i].ID == alertID && !rm.alerts[i].Resolved {
			rm.alerts[i].Resolved = true
			now := metav1.Now()
			rm.alerts[i].ResolvedAt = &now
			break
		}
	}
}

// updateDeviceHealth updates the health status for a device
func (rm *ResourceMonitor) updateDeviceHealth(deviceID string, health DeviceHealthStatus) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if metrics, exists := rm.metrics[deviceID]; exists {
		metrics.HealthStatus = health
		metrics.Timestamp = metav1.Now()
	}
}

// GetDeviceMetrics returns metrics for a specific device
func (rm *ResourceMonitor) GetDeviceMetrics(deviceID string) (*ResourceMetrics, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	metrics, exists := rm.metrics[deviceID]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid concurrent modification
	metricsCopy := *metrics
	return &metricsCopy, true
}

// GetAllMetrics returns metrics for all devices
func (rm *ResourceMonitor) GetAllMetrics() map[string]*ResourceMetrics {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	result := make(map[string]*ResourceMetrics)
	for deviceID, metrics := range rm.metrics {
		metricsCopy := *metrics
		result[deviceID] = &metricsCopy
	}

	return result
}

// GetActiveAlerts returns all active (unresolved) alerts
func (rm *ResourceMonitor) GetActiveAlerts() []ResourceAlert {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	var activeAlerts []ResourceAlert
	for _, alert := range rm.alerts {
		if !alert.Resolved {
			activeAlerts = append(activeAlerts, alert)
		}
	}

	return activeAlerts
}

// GetAllAlerts returns all alerts (resolved and unresolved)
func (rm *ResourceMonitor) GetAllAlerts() []ResourceAlert {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	// Return a copy to avoid concurrent modification
	alertsCopy := make([]ResourceAlert, len(rm.alerts))
	copy(alertsCopy, rm.alerts)
	return alertsCopy
}

// GetAggregatedMetrics returns aggregated metrics across all devices
func (rm *ResourceMonitor) GetAggregatedMetrics() *ResourceMetrics {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	if len(rm.metrics) == 0 {
		return nil
	}

	aggregated := &ResourceMetrics{
		DeviceID:     "aggregate",
		Timestamp:    metav1.Now(),
		CPUUsage:     resource.Quantity{},
		MemoryUsage:  resource.Quantity{},
		StorageUsage: resource.Quantity{},
		HealthStatus: DeviceHealthHealthy,
	}

	totalDevices := len(rm.metrics)
	healthyCount := 0
	warningCount := 0
	criticalCount := 0

	for _, metrics := range rm.metrics {
		aggregated.CPUUsage.Add(metrics.CPUUsage)
		aggregated.MemoryUsage.Add(metrics.MemoryUsage)
		aggregated.StorageUsage.Add(metrics.StorageUsage)
		aggregated.NetworkRxBytes += metrics.NetworkRxBytes
		aggregated.NetworkTxBytes += metrics.NetworkTxBytes
		aggregated.ContainerCount += metrics.ContainerCount

		switch metrics.HealthStatus {
		case DeviceHealthHealthy:
			healthyCount++
		case DeviceHealthWarning:
			warningCount++
		case DeviceHealthCritical:
			criticalCount++
		}
	}

	// Calculate average utilization
	aggregated.CPUUtilization = rm.calculateAverageUtilization("cpu")
	aggregated.MemoryUtilization = rm.calculateAverageUtilization("memory")
	aggregated.StorageUtilization = rm.calculateAverageUtilization("storage")

	// Determine overall health
	if criticalCount > 0 {
		aggregated.HealthStatus = DeviceHealthCritical
	} else if warningCount > 0 {
		aggregated.HealthStatus = DeviceHealthWarning
	} else if healthyCount == totalDevices {
		aggregated.HealthStatus = DeviceHealthHealthy
	} else {
		aggregated.HealthStatus = DeviceHealthUnknown
	}

	return aggregated
}

// calculateAverageUtilization calculates average utilization for a resource type
func (rm *ResourceMonitor) calculateAverageUtilization(resourceType string) float64 {
	if len(rm.metrics) == 0 {
		return 0.0
	}

	total := 0.0
	count := 0

	for _, metrics := range rm.metrics {
		switch resourceType {
		case "cpu":
			total += metrics.CPUUtilization
		case "memory":
			total += metrics.MemoryUtilization
		case "storage":
			total += metrics.StorageUtilization
		}
		count++
	}

	return total / float64(count)
}

// UpdateThresholds updates alert thresholds
func (rm *ResourceMonitor) UpdateThresholds(thresholds ResourceThresholds) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	rm.thresholds = thresholds
}

// GetThresholds returns current alert thresholds
func (rm *ResourceMonitor) GetThresholds() ResourceThresholds {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	return rm.thresholds
}

// GetMonitoringStatus returns the current monitoring status
func (rm *ResourceMonitor) GetMonitoringStatus() map[string]interface{} {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	activeAlerts := 0
	for _, alert := range rm.alerts {
		if !alert.Resolved {
			activeAlerts++
		}
	}

	return map[string]interface{}{
		"running":           rm.running,
		"monitored_devices": len(rm.metrics),
		"active_alerts":     activeAlerts,
		"total_alerts":      len(rm.alerts),
		"thresholds":        rm.thresholds,
	}
}
