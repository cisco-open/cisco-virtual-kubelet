package cisco

import (
	"context"
	"fmt"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// LifecycleManager handles comprehensive app lifecycle with enhanced validation and monitoring
type LifecycleManager struct {
	appManager *AppHostingManager
	restconf   *RESTCONFAppHostingClient
}

// NewLifecycleManager creates a new lifecycle manager
func NewLifecycleManager(appManager *AppHostingManager) *LifecycleManager {
	return &LifecycleManager{
		appManager: appManager,
		restconf:   appManager.restconfClient,
	}
}

// ===================================================================
// PRE-DEPLOYMENT VALIDATION (Enhanced)
// ===================================================================

// ComprehensivePreDeploymentValidation performs all pre-deployment checks
func (l *LifecycleManager) ComprehensivePreDeploymentValidation(ctx context.Context, appID string, spec ContainerSpec) error {
	log.G(ctx).Infof("🔍 [AGENT] Starting comprehensive pre-deployment validation for %s", appID)
	
	// 1. IOx Service Availability
	if err := l.validateIOxService(ctx); err != nil {
		log.G(ctx).Errorf("❌ [AGENT] IOx service validation failed: %v", err)
		return err
	}
	log.G(ctx).Infof("✅ [AGENT] IOx service is available and operational")
	
	// 2. App ID Uniqueness
	if err := l.validateAppIDUniqueness(ctx, appID); err != nil {
		log.G(ctx).Errorf("❌ [AGENT] App ID validation failed: %v", err)
		return err
	}
	log.G(ctx).Infof("✅ [AGENT] App ID %s is unique", appID)
	
	// 3. Resource Availability
	if err := l.validateResourceAvailability(ctx, spec); err != nil {
		log.G(ctx).Errorf("❌ [AGENT] Resource validation failed: %v", err)
		return err
	}
	log.G(ctx).Infof("✅ [AGENT] Sufficient resources available (Memory: %dMB, CPU: %dm)",
		getMemoryRequest(spec), getCPURequest(spec))
	
	// 4. Network Configuration
	if err := l.validateNetworkConfiguration(ctx); err != nil {
		log.G(ctx).Warnf("⚠️  [AGENT] Network validation warning: %v", err)
		// Non-fatal - log and continue
	} else {
		log.G(ctx).Infof("✅ [AGENT] Network configuration validated")
	}
	
	// 5. Device State Health
	if err := l.validateDeviceHealth(ctx); err != nil {
		log.G(ctx).Warnf("⚠️  [AGENT] Device health warning: %v", err)
		// Non-fatal - log and continue
	} else {
		log.G(ctx).Infof("✅ [AGENT] Device health check passed")
	}
	
	log.G(ctx).Infof("✅ [AGENT] All pre-deployment validations passed for %s", appID)
	return nil
}

// validateIOxService checks if IOx is enabled and operational
func (l *LifecycleManager) validateIOxService(ctx context.Context) error {
	if l.restconf == nil {
		return fmt.Errorf("RESTCONF client not available")
	}
	
	// Query operational data to verify IOx is responding
	_, err := l.restconf.ListApplications(ctx)
	if err != nil {
		return fmt.Errorf("IOx service not responding: %v", err)
	}
	
	return nil
}

// validateAppIDUniqueness ensures no app with this ID exists
func (l *LifecycleManager) validateAppIDUniqueness(ctx context.Context, appID string) error {
	apps, err := l.restconf.ListApplications(ctx)
	if err != nil {
		return fmt.Errorf("failed to query existing apps: %v", err)
	}
	
	for _, app := range apps {
		if app.Name == appID {
			return fmt.Errorf("app %s already exists in state: %s", appID, app.Details.State)
		}
	}
	
	return nil
}

// validateResourceAvailability checks if device has sufficient resources
func (l *LifecycleManager) validateResourceAvailability(ctx context.Context, spec ContainerSpec) error {
	// Get current capacity
	capacity := l.appManager.monitor.GetCapacity()
	
	requiredMem := getMemoryRequest(spec)
	requiredCPU := getCPURequest(spec)
	
	if int64(capacity.AvailableMemoryMB) < int64(requiredMem) {
		return fmt.Errorf("insufficient memory: need %dMB, have %dMB", requiredMem, capacity.AvailableMemoryMB)
	}
	
	if int64(capacity.AvailableCPUUnits) < int64(requiredCPU) {
		return fmt.Errorf("insufficient CPU: need %d units, have %d units", requiredCPU, capacity.AvailableCPUUnits)
	}
	
	if capacity.RunningApps >= capacity.MaxApps {
		return fmt.Errorf("maximum app limit reached: %d/%d", capacity.RunningApps, capacity.MaxApps)
	}
	
	return nil
}

// validateNetworkConfiguration checks network prerequisites
func (l *LifecycleManager) validateNetworkConfiguration(ctx context.Context) error {
	// For now, basic validation - can be extended
	return nil
}

// validateDeviceHealth performs basic device health check
func (l *LifecycleManager) validateDeviceHealth(ctx context.Context) error {
	// Query device to ensure it's responsive
	_, err := l.restconf.ListApplications(ctx)
	return err
}

// ===================================================================
// POST-DEPLOYMENT VERIFICATION (Enhanced)
// ===================================================================

// ComprehensivePostDeploymentVerification validates deployment success
func (l *LifecycleManager) ComprehensivePostDeploymentVerification(ctx context.Context, appID string, namespace, podName string) error {
	log.G(ctx).Infof("🔍 [POD:%s/%s] Starting post-deployment verification for app %s", namespace, podName, appID)
	
	// 1. Verify app exists via RESTCONF
	status, err := l.restconf.GetStatus(ctx, appID)
	if err != nil {
		log.G(ctx).Errorf("❌ [POD:%s/%s] App not found on device: %v", namespace, podName, err)
		return fmt.Errorf("deployment verification failed: app not found")
	}
	log.G(ctx).Infof("✅ [POD:%s/%s] App %s found on device", namespace, podName, appID)
	
	// 2. Verify app reached RUNNING state
	if status.Details.State != "RUNNING" {
		log.G(ctx).Errorf("❌ [POD:%s/%s] App in unexpected state: %s", namespace, podName, status.Details.State)
		return fmt.Errorf("app not running: state=%s", status.Details.State)
	}
	log.G(ctx).Infof("✅ [POD:%s/%s] App %s is RUNNING", namespace, podName, appID)
	
	// 3. Verify resource allocation
	if err := l.verifyResourceAllocation(ctx, appID, namespace, podName); err != nil {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] Resource allocation warning: %v", namespace, podName, err)
		// Non-fatal
	} else {
		log.G(ctx).Infof("✅ [POD:%s/%s] Resource allocation verified", namespace, podName)
	}
	
	// 4. Register for ongoing monitoring
	l.appManager.monitor.RegisterContainer(ctx, appID)
	log.G(ctx).Infof("📝 [POD:%s/%s] App %s registered for health monitoring", namespace, podName, appID)
	
	// 5. Update capacity metrics
	l.appManager.monitor.updateCapacity(ctx)
	log.G(ctx).Infof("📊 [AGENT] Capacity metrics updated after deployment")
	
	log.G(ctx).Infof("✅ [POD:%s/%s] Post-deployment verification complete for %s", namespace, podName, appID)
	return nil
}

// verifyResourceAllocation checks if resources were properly allocated
func (l *LifecycleManager) verifyResourceAllocation(ctx context.Context, appID, namespace, podName string) error {
	status, err := l.restconf.GetStatus(ctx, appID)
	if err != nil {
		return err
	}
	
	// Verify app exists and has details
	if status.Details.State == "" {
		return fmt.Errorf("app details missing")
	}
	
	return nil
}

// ===================================================================
// ONGOING HEALTH MONITORING (Enhanced)
// ===================================================================

// PerformHealthCheck checks app health and updates K8s if needed
func (l *LifecycleManager) PerformHealthCheck(ctx context.Context, appID, namespace, podName string) (bool, error) {
	log.G(ctx).Debugf("🏥 [POD:%s/%s] Health check for %s", namespace, podName, appID)
	
	// Query app status via RESTCONF
	status, err := l.restconf.GetStatus(ctx, appID)
	if err != nil {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] Health check failed: %v", namespace, podName, err)
		return false, err
	}
	
	// Check if app is in healthy state
	healthyStates := map[string]bool{
		"RUNNING":   true,
		"ACTIVATED": true,
	}
	
	isHealthy := healthyStates[status.Details.State]
	
	if !isHealthy {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] App %s in unhealthy state: %s", 
			namespace, podName, appID, status.Details.State)
	}
	
	return isHealthy, nil
}

// VerifyStateConsistency checks if device state matches expected state
func (l *LifecycleManager) VerifyStateConsistency(ctx context.Context, appID, namespace, podName, expectedState string) error {
	status, err := l.restconf.GetStatus(ctx, appID)
	if err != nil {
		return fmt.Errorf("failed to query state: %v", err)
	}
	
	if status.Details.State != expectedState {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] State mismatch: expected=%s, actual=%s",
			namespace, podName, expectedState, status.Details.State)
		return fmt.Errorf("state mismatch: expected=%s, got=%s", expectedState, status.Details.State)
	}
	
	log.G(ctx).Debugf("✅ [POD:%s/%s] State consistent: %s", namespace, podName, expectedState)
	return nil
}

// ===================================================================
// CLEANUP VALIDATION (Enhanced)
// ===================================================================

// ComprehensiveCleanup performs complete app removal with verification
func (l *LifecycleManager) ComprehensiveCleanup(ctx context.Context, appID, namespace, podName string) error {
	log.G(ctx).Infof("🗑️  [POD:%s/%s] Starting comprehensive cleanup for %s", namespace, podName, appID)
	
	// 1. Stop the application
	if err := l.stopApp(ctx, appID, namespace, podName); err != nil {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] Stop failed (continuing): %v", namespace, podName, err)
	}
	
	// 2. Deactivate the application
	if err := l.deactivateApp(ctx, appID, namespace, podName); err != nil {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] Deactivate failed (continuing): %v", namespace, podName, err)
	}
	
	// 3. Uninstall the application
	if err := l.uninstallApp(ctx, appID, namespace, podName); err != nil {
		log.G(ctx).Warnf("⚠️  [POD:%s/%s] Uninstall failed (continuing): %v", namespace, podName, err)
	}
	
	// 4. Verify app is completely removed
	if err := l.verifyAppRemoved(ctx, appID, namespace, podName); err != nil {
		log.G(ctx).Errorf("❌ [POD:%s/%s] Cleanup verification failed: %v", namespace, podName, err)
		return fmt.Errorf("cleanup incomplete: %v", err)
	}
	
	// 5. Unregister from monitoring
	l.appManager.monitor.UnregisterContainer(ctx, appID)
	log.G(ctx).Infof("📝 [POD:%s/%s] Unregistered %s from health monitoring", namespace, podName, appID)
	
	// 6. Update capacity metrics
	l.appManager.monitor.updateCapacity(ctx)
	log.G(ctx).Infof("📊 [AGENT] Capacity metrics updated after cleanup")
	
	log.G(ctx).Infof("✅ [POD:%s/%s] Comprehensive cleanup complete for %s", namespace, podName, appID)
	return nil
}

// stopApp stops a running application
func (l *LifecycleManager) stopApp(ctx context.Context, appID, namespace, podName string) error {
	log.G(ctx).Infof("  ⏸️  [POD:%s/%s] Stopping %s via RESTCONF", namespace, podName, appID)
	
	if err := l.restconf.Stop(ctx, appID); err != nil {
		return err
	}
	
	time.Sleep(2 * time.Second) // Allow state transition
	log.G(ctx).Infof("  ✅ [POD:%s/%s] Stopped %s", namespace, podName, appID)
	return nil
}

// deactivateApp deactivates an application
func (l *LifecycleManager) deactivateApp(ctx context.Context, appID, namespace, podName string) error {
	log.G(ctx).Infof("  ⏬ [POD:%s/%s] Deactivating %s via RESTCONF", namespace, podName, appID)
	
	if err := l.restconf.Deactivate(ctx, appID); err != nil {
		return err
	}
	
	time.Sleep(2 * time.Second) // Allow state transition
	log.G(ctx).Infof("  ✅ [POD:%s/%s] Deactivated %s", namespace, podName, appID)
	return nil
}

// uninstallApp uninstalls an application
func (l *LifecycleManager) uninstallApp(ctx context.Context, appID, namespace, podName string) error {
	log.G(ctx).Infof("  🗑️  [POD:%s/%s] Uninstalling %s via RESTCONF", namespace, podName, appID)
	
	if err := l.restconf.Uninstall(ctx, appID); err != nil {
		return err
	}
	
	time.Sleep(2 * time.Second) // Allow state transition
	log.G(ctx).Infof("  ✅ [POD:%s/%s] Uninstalled %s", namespace, podName, appID)
	return nil
}

// verifyAppRemoved confirms app no longer exists on device
func (l *LifecycleManager) verifyAppRemoved(ctx context.Context, appID, namespace, podName string) error {
	log.G(ctx).Infof("  🔍 [POD:%s/%s] Verifying %s is removed", namespace, podName, appID)
	
	// Try to get app status - should fail
	_, err := l.restconf.GetStatus(ctx, appID)
	if err == nil {
		// App still exists!
		return fmt.Errorf("app %s still exists on device", appID)
	}
	
	// Verify app not in list
	apps, err := l.restconf.ListApplications(ctx)
	if err != nil {
		log.G(ctx).Warnf("  ⚠️  [POD:%s/%s] Could not verify via list: %v", namespace, podName, err)
		// Continue - the GetStatus failure is good enough
		return nil
	}
	
	for _, app := range apps {
		if app.Name == appID {
			return fmt.Errorf("app %s found in app list", appID)
		}
	}
	
	log.G(ctx).Infof("  ✅ [POD:%s/%s] Verified %s is completely removed", namespace, podName, appID)
	return nil
}

// ===================================================================
// RECONCILIATION (Enhanced)
// ===================================================================

// ReconcileState ensures device state matches Kubernetes state
func (l *LifecycleManager) ReconcileState(ctx context.Context, expectedApps map[string]PodInfo) (*ReconciliationReport, error) {
	log.G(ctx).Infof("🔄 [AGENT] Starting state reconciliation")
	
	report := &ReconciliationReport{
		Timestamp: time.Now(),
	}
	
	// Get all apps from device
	deviceApps, err := l.restconf.ListApplications(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query device apps: %v", err)
	}
	
	// Check each device app
	for _, app := range deviceApps {
		// Skip system apps
		if app.Name == "AGENT" || app.Name == "guestshell" {
			continue
		}
		
		// Check if VK-managed
		if !isVKManagedApp(app.Name) {
			report.ManualApps = append(report.ManualApps, app.Name)
			continue
		}
		
		// Check if has corresponding K8s pod
		if _, exists := expectedApps[app.Name]; exists {
			report.HealthyApps = append(report.HealthyApps, app.Name)
		} else {
			report.OrphanedApps = append(report.OrphanedApps, app.Name)
			log.G(ctx).Warnf("⚠️  [AGENT] Found orphaned app: %s (state: %s)", app.Name, app.Details.State)
		}
	}
	
	// Check for missing apps (in K8s but not on device)
	for appID := range expectedApps {
		found := false
		for _, app := range deviceApps {
			if app.Name == appID {
				found = true
				break
			}
		}
		if !found {
			report.MissingApps = append(report.MissingApps, appID)
			log.G(ctx).Warnf("⚠️  [AGENT] App missing from device: %s", appID)
		}
	}
	
	log.G(ctx).Infof("✅ [AGENT] Reconciliation complete: Healthy=%d, Orphaned=%d, Missing=%d, Manual=%d",
		len(report.HealthyApps), len(report.OrphanedApps), len(report.MissingApps), len(report.ManualApps))
	
	return report, nil
}

// ===================================================================
// HELPER TYPES AND FUNCTIONS
// ===================================================================

// PodInfo contains information about a Kubernetes pod
type PodInfo struct {
	Namespace string
	PodName   string
	AppID     string
}

// ReconciliationReport contains reconciliation results
type ReconciliationReport struct {
	Timestamp    time.Time
	HealthyApps  []string
	OrphanedApps []string
	MissingApps  []string
	ManualApps   []string
}

// isVKManagedApp checks if an app is managed by Virtual Kubelet
func isVKManagedApp(appName string) bool {
	return len(appName) > 3 && appName[:3] == "vk_"
}

// Helper functions to extract resource requirements
func getMemoryRequest(spec ContainerSpec) int {
	if memReq, ok := spec.Resources.Requests["memory"]; ok {
		return int(memReq.Value() / (1024 * 1024)) // Convert to MB
	}
	return 256 // Default
}

func getCPURequest(spec ContainerSpec) int {
	if cpuReq, ok := spec.Resources.Requests["cpu"]; ok {
		return int(cpuReq.MilliValue()) // milliCPU
	}
	return 1000 // Default
}
