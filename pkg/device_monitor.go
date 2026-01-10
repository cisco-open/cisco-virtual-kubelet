package cisco

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// AppHostingCapacity represents available resources on the device
type AppHostingCapacity struct {
	TotalApps         int
	RunningApps       int
	MaxApps           int
	TotalMemoryMB     int64
	UsedMemoryMB      int64
	AvailableMemoryMB int64
	TotalCPUUnits     int
	UsedCPUUnits      int
	AvailableCPUUnits int
	LastUpdated       time.Time
}

// ContainerHealthInfo represents health of individual containers
type ContainerHealthInfo struct {
	AppID            string
	State            string
	Healthy          bool
	LastCheckTime    time.Time
	ConsecutiveFails int
	LastError        error
}

// DeviceMonitor handles ongoing health and capacity monitoring
type DeviceMonitor struct {
	client         *IOSXEClient
	restconfClient *RESTCONFAppHostingClient

	capacity        AppHostingCapacity
	containerHealth map[string]*ContainerHealthInfo

	mu     sync.RWMutex
	stopCh chan struct{}
}

// NewDeviceMonitor creates a new device monitoring instance
func NewDeviceMonitor(client *IOSXEClient, restconfClient *RESTCONFAppHostingClient) *DeviceMonitor {
	monitor := &DeviceMonitor{
		client:          client,
		restconfClient:  restconfClient,
		containerHealth: make(map[string]*ContainerHealthInfo),
		stopCh:          make(chan struct{}),
	}
	
	// Initialize capacity with defaults to avoid race conditions
	monitor.capacity = AppHostingCapacity{
		MaxApps:           10,
		TotalMemoryMB:     4096,
		TotalCPUUnits:     7400,
		AvailableMemoryMB: 4096,
		AvailableCPUUnits: 7400,
	}
	
	return monitor
}

// Start begins the monitoring routines
func (m *DeviceMonitor) Start(ctx context.Context) {
	log.G(ctx).Info("🔍 Starting device monitoring...")

	// Initial capacity check
	m.updateCapacity(ctx)

	// Start periodic monitoring
	go m.monitorCapacity(ctx)
	go m.monitorContainerHealth(ctx)
}

// Stop halts all monitoring routines
func (m *DeviceMonitor) Stop() {
	close(m.stopCh)
}

// monitorCapacity runs periodic capacity checks
func (m *DeviceMonitor) monitorCapacity(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.updateCapacity(ctx)
		case <-m.stopCh:
			log.G(ctx).Info("🛑 Stopping capacity monitoring")
			return
		}
	}
}

// updateCapacity fetches current resource utilization
func (m *DeviceMonitor) updateCapacity(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Fetch all apps to calculate capacity
	apps, err := m.getAllApps(ctx)
	if err != nil {
		log.G(ctx).Warnf("⚠️  Failed to fetch capacity info: %v", err)
		return
	}

	m.capacity.LastUpdated = time.Now()
	m.capacity.TotalApps = len(apps)
	m.capacity.RunningApps = 0
	m.capacity.UsedMemoryMB = 0
	m.capacity.UsedCPUUnits = 0

	// Calculate used resources from running apps
	for _, app := range apps {
		if app.Details.State == "RUNNING" || app.Details.State == "ACTIVATED" {
			m.capacity.RunningApps++
		}

		// Resource usage estimation (if not directly available)
		// Assuming 256MB per app if not specified
		m.capacity.UsedMemoryMB += 256
		m.capacity.UsedCPUUnits += 1000 // Rough estimate
	}

	// Platform limits for C9300 series (conservative estimates)
	m.capacity.MaxApps = 10
	m.capacity.TotalMemoryMB = 4096
	m.capacity.TotalCPUUnits = 7400

	// Calculate available resources
	m.capacity.AvailableMemoryMB = m.capacity.TotalMemoryMB - m.capacity.UsedMemoryMB
	m.capacity.AvailableCPUUnits = m.capacity.TotalCPUUnits - m.capacity.UsedCPUUnits

	log.G(ctx).Infof("📊 Capacity: %d/%d apps, Memory: %dMB/%dMB available, CPU: %d/%d units available",
		m.capacity.TotalApps, m.capacity.MaxApps,
		m.capacity.AvailableMemoryMB, m.capacity.TotalMemoryMB,
		m.capacity.AvailableCPUUnits, m.capacity.TotalCPUUnits)
}

// monitorContainerHealth runs periodic health checks on containers
func (m *DeviceMonitor) monitorContainerHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkAllContainerHealth(ctx)
		case <-m.stopCh:
			log.G(ctx).Info("🛑 Stopping container health monitoring")
			return
		}
	}
}

// checkAllContainerHealth verifies health of all tracked containers
func (m *DeviceMonitor) checkAllContainerHealth(ctx context.Context) {
	m.mu.RLock()
	appIDs := make([]string, 0, len(m.containerHealth))
	for appID := range m.containerHealth {
		appIDs = append(appIDs, appID)
	}
	m.mu.RUnlock()

	for _, appID := range appIDs {
		m.checkContainerHealth(ctx, appID)
	}
}

// checkContainerHealth verifies a single container's health
func (m *DeviceMonitor) checkContainerHealth(ctx context.Context, appID string) {
	status, err := m.restconfClient.GetStatus(ctx, appID)

	m.mu.Lock()
	defer m.mu.Unlock()

	health, exists := m.containerHealth[appID]
	if !exists {
		health = &ContainerHealthInfo{AppID: appID}
		m.containerHealth[appID] = health
	}

	health.LastCheckTime = time.Now()

	if err != nil {
		health.Healthy = false
		health.ConsecutiveFails++
		health.LastError = err
		log.G(ctx).Warnf("⚠️  Container %s health check failed: %v (consecutive fails: %d)", appID, err, health.ConsecutiveFails)
		return
	}

	health.State = status.Details.State

	// Define healthy states
	healthyStates := map[string]bool{
		"RUNNING":   true,
		"ACTIVATED": true,
	}

	if healthyStates[status.Details.State] {
		if !health.Healthy || health.ConsecutiveFails > 0 {
			log.G(ctx).Infof("✅ Container %s recovered: state=%s", appID, status.Details.State)
		}
		health.Healthy = true
		health.ConsecutiveFails = 0
		health.LastError = nil
	} else {
		health.Healthy = false
		health.ConsecutiveFails++
		health.LastError = fmt.Errorf("unhealthy state: %s", status.Details.State)
		log.G(ctx).Warnf("⚠️  Container %s unhealthy: state=%s (consecutive fails: %d)", appID, status.Details.State, health.ConsecutiveFails)
	}
}

// RegisterContainer adds a container to health monitoring
func (m *DeviceMonitor) RegisterContainer(ctx context.Context, appID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.containerHealth[appID]; !exists {
		m.containerHealth[appID] = &ContainerHealthInfo{
			AppID:   appID,
			Healthy: true, // Assume healthy on registration
		}
		log.G(ctx).Infof("📝 Registered container %s for health monitoring", appID)
	}
}

// UnregisterContainer removes a container from health monitoring
func (m *DeviceMonitor) UnregisterContainer(ctx context.Context, appID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.containerHealth, appID)
	log.G(ctx).Infof("📝 Unregistered container %s from health monitoring", appID)
}

// GetCapacity returns current capacity information
func (m *DeviceMonitor) GetCapacity() AppHostingCapacity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.capacity
}

// GetContainerHealth returns health status for a specific container
func (m *DeviceMonitor) GetContainerHealth(appID string) (*ContainerHealthInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	health, exists := m.containerHealth[appID]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	healthCopy := *health
	return &healthCopy, true
}

// CanDeployApp checks if there's sufficient capacity for a new app
func (m *DeviceMonitor) CanDeployApp(requiredMemoryMB int, requiredCPUUnits int) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check app count limit
	if m.capacity.TotalApps >= m.capacity.MaxApps {
		return false, fmt.Sprintf("max apps reached (%d/%d)", m.capacity.TotalApps, m.capacity.MaxApps)
	}

	// Check memory
	if int64(requiredMemoryMB) > m.capacity.AvailableMemoryMB {
		return false, fmt.Sprintf("insufficient memory (need %dMB, available %dMB)", requiredMemoryMB, m.capacity.AvailableMemoryMB)
	}

	// Check CPU
	if requiredCPUUnits > m.capacity.AvailableCPUUnits {
		return false, fmt.Sprintf("insufficient CPU (need %d units, available %d)", requiredCPUUnits, m.capacity.AvailableCPUUnits)
	}

	return true, ""
}

// getAllApps fetches all applications from the device
func (m *DeviceMonitor) getAllApps(ctx context.Context) ([]AppStatus, error) {
	// Use the RESTCONF client's existing GetAllApps method if available,
	// otherwise query operational data directly
	apps, err := m.restconfClient.GetAllApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get apps: %v", err)
	}
	return apps, nil
}

// GetMonitoringReport generates a comprehensive monitoring report
func (m *DeviceMonitor) GetMonitoringReport() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	report := fmt.Sprintf(`
📊 Device Monitoring Report
═══════════════════════════════════════════════════

💾 Capacity:
   Apps:             %d / %d (max)
   Running Apps:     %d
   Memory:           %d MB available / %d MB total
   CPU Units:        %d available / %d total
   Last Updated:     %s

🐳 Container Health:
`,
		m.capacity.TotalApps, m.capacity.MaxApps,
		m.capacity.RunningApps,
		m.capacity.AvailableMemoryMB, m.capacity.TotalMemoryMB,
		m.capacity.AvailableCPUUnits, m.capacity.TotalCPUUnits,
		m.capacity.LastUpdated.Format("2006-01-02 15:04:05"),
	)

	if len(m.containerHealth) == 0 {
		report += "   No containers registered\n"
	} else {
		for appID, health := range m.containerHealth {
			status := "❌"
			if health.Healthy {
				status = "✅"
			}
			report += fmt.Sprintf("   %s %s: state=%s, fails=%d, last_check=%s\n",
				status, appID, health.State, health.ConsecutiveFails,
				health.LastCheckTime.Format("15:04:05"))
		}
	}

	report += "═══════════════════════════════════════════════════\n"

	return report
}
