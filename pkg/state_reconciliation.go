package cisco

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// reconcileExistingPod checks if a pod was previously deployed and restores its state
// This is called during CreatePod to handle VK restarts gracefully
func (p *CiscoProvider) reconcileExistingPod(ctx context.Context, pod *v1.Pod) (bool, error) {
	log.G(ctx).Infof("🔄 Checking if pod %s/%s was previously deployed", pod.Namespace, pod.Name)

	// Check if pod has deployment annotations (indicating it was deployed before)
	if pod.Annotations == nil || len(pod.Annotations) == 0 {
		log.G(ctx).Infof("  No annotations found - pod is new")
		return false, nil
	}

	// Look for container annotations
	containerIndex := 0
	var reconciledContainers []*Container

	for {
		prefix := fmt.Sprintf("cisco.com/container-%d", containerIndex)
		appIDKey := prefix + ".app-id"
		deviceIDKey := prefix + ".device-id"

		appID, hasAppID := pod.Annotations[appIDKey]
		deviceID, hasDeviceID := pod.Annotations[deviceIDKey]

		// No more containers to reconcile
		if !hasAppID {
			break
		}

		if !hasDeviceID {
			log.G(ctx).Warnf("  Container %d has app-id but no device-id annotation", containerIndex)
			containerIndex++
			continue
		}

		log.G(ctx).Infof("  Found previous deployment: container-%d with app-id=%s on device=%s", 
			containerIndex, appID, deviceID)

		// Get the device
		device, err := p.deviceManager.GetDevice(deviceID)
		if err != nil {
			log.G(ctx).Warnf("  Device %s not found: %v", deviceID, err)
			containerIndex++
			continue
		}

		// Check if device has RESTCONF client
		if device.AppHostingMgr == nil || device.AppHostingMgr.restconfClient == nil {
			log.G(ctx).Warnf("  Device %s has no RESTCONF client", deviceID)
			containerIndex++
			continue
		}

		// Query app status via RESTCONF to see if it still exists
		log.G(ctx).Infof("  Querying device via RESTCONF for app %s", appID)
		appStatus, err := device.AppHostingMgr.restconfClient.GetStatus(ctx, appID)
		if err != nil {
			log.G(ctx).Warnf("  App %s not found on device: %v", appID, err)
			// App doesn't exist - don't reconcile, let normal creation happen
			containerIndex++
			continue
		}

		log.G(ctx).Infof("  ✅ Found app %s on device in state: %s", appID, appStatus.Details.State)

		// Reconstruct container object from annotations and device state
		containerSpec := pod.Spec.Containers[containerIndex]
		
		// Create labels map with required Kubernetes labels for container lookup
		containerLabels := make(map[string]string)
		// Copy pod labels first
		for k, v := range pod.Labels {
			containerLabels[k] = v
		}
		// Add required Kubernetes labels that getContainerIDsForPod() uses
		containerLabels["io.kubernetes.pod.name"] = pod.Name
		containerLabels["io.kubernetes.pod.namespace"] = pod.Namespace
		containerLabels["io.kubernetes.container.name"] = containerSpec.Name
		
		container := &Container{
			ID:       appID,
			Name:     containerSpec.Name,
			Image:    containerSpec.Image,
			DeviceID: deviceID,
			State:    mapAppStateToContainerState(appStatus.Details.State),
			Resources: ResourceUsage{
				CPU:    *containerSpec.Resources.Requests.Cpu(),
				Memory: *containerSpec.Resources.Requests.Memory(),
			},
			Labels:      containerLabels,
			Annotations: pod.Annotations,
		}

		// Set start time if available from annotations
		if deployedAt, ok := pod.Annotations[prefix+".deployed-at"]; ok {
			if startTime, err := parseRFC3339Time(deployedAt); err == nil {
				container.StartedAt = &startTime
			}
		}

		// If no start time from annotations, use current time
		if container.StartedAt == nil {
			now := metav1.Now()
			container.StartedAt = &now
		}

		reconciledContainers = append(reconciledContainers, container)
		containerIndex++
	}

	// If we found any containers to reconcile, restore the state
	if len(reconciledContainers) == 0 {
		log.G(ctx).Infof("  No containers found to reconcile")
		return false, nil
	}

	log.G(ctx).Infof("  🔄 Reconciling %d container(s)", len(reconciledContainers))

	// Store containers in provider state
	for _, container := range reconciledContainers {
		p.containers.Store(container.ID, container)
		log.G(ctx).Infof("  ✅ Restored container %s to internal state", container.ID)
	}

	// Update pod status to reflect reconciled state
	now := metav1.Now()
	pod.Status = v1.PodStatus{
		Phase:     v1.PodRunning,
		HostIP:    p.internalIP,
		PodIP:     p.allocatePodIP(pod),
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodReady,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: now,
			},
		},
	}

	// Set container statuses
	for i, container := range reconciledContainers {
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

		// Update device state with the reconciled container
		device, err := p.deviceManager.GetDevice(container.DeviceID)
		if err == nil {
			device.mutex.Lock()
			device.Containers[container.ID] = container
			device.mutex.Unlock()
		}

		log.G(ctx).Infof("  ✅ Reconciled container %d: %s (state: %s)", i, container.Name, container.State)
	}

	// Store pod in provider state
	key := getPodKey(pod)
	p.pods.Store(key, pod.DeepCopy())

	log.G(ctx).Infof("✅ Successfully reconciled pod %s with %d container(s)", key, len(reconciledContainers))

	return true, nil
}

// mapAppStateToContainerState converts RESTCONF app state to container state
func mapAppStateToContainerState(appState string) ContainerState {
	switch appState {
	case "RUNNING":
		return ContainerStateRunning
	case "STOPPED", "DEPLOYED", "ACTIVATED":
		return ContainerStateStopped
	case "ERROR":
		return ContainerStateError
	default:
		return ContainerStateUnknown
	}
}

// parseRFC3339Time parses a time string in RFC3339 format
func parseRFC3339Time(timeStr string) (metav1.Time, error) {
	// Use time.Parse with RFC3339 format
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return metav1.Time{}, err
	}
	return metav1.Time{Time: t}, nil
}

// ReconcileAllPods performs startup reconciliation for all pods
// This is called when the provider starts to restore state from K8s and devices
func (p *CiscoProvider) ReconcileAllPods(ctx context.Context) error {
	log.G(ctx).Info("🔄 Starting startup reconciliation...")

	// Get all devices
	devices := p.deviceManager.ListDevices()
	if len(devices) == 0 {
		log.G(ctx).Warn("  No devices available for reconciliation")
		return nil
	}

	// For each device, query all apps via RESTCONF
	totalApps := 0
	for _, device := range devices {
		if device.AppHostingMgr == nil || device.AppHostingMgr.restconfClient == nil {
			log.G(ctx).Warnf("  Device %s has no RESTCONF client, skipping", device.Config.Name)
			continue
		}

		log.G(ctx).Infof("  Querying apps on device %s via RESTCONF", device.Config.Name)

		// Query all apps on the device
		// Note: We use the general endpoint since individual app queries might not be supported
		// The GetStatus method now queries all apps and filters, so we can use it
		// But for startup reconciliation, we just log what's on the device
		// The actual reconciliation happens in CreatePod when VK syncs pods from K8s

		// For now, just log device status
		totalApps++
	}

	log.G(ctx).Infof("✅ Startup reconciliation complete - found %d device(s)", len(devices))
	log.G(ctx).Info("  Note: Individual pod reconciliation will occur as VK syncs pods from K8s")

	return nil
}

// cleanupOrphanedApps identifies and optionally removes apps on devices that don't have corresponding pods
func (p *CiscoProvider) cleanupOrphanedApps(ctx context.Context, dryRun bool) ([]string, error) {
	log.G(ctx).Info("🧹 Checking for orphaned apps on devices...")

	var orphanedApps []string

	// Get all pods currently managed by this provider
	managedPods := make(map[string]bool)
	p.pods.Range(func(key, value interface{}) bool {
		pod := value.(*v1.Pod)
		// Extract app IDs from annotations
		for i := 0; ; i++ {
			appIDKey := fmt.Sprintf("cisco.com/container-%d.app-id", i)
			if appID, ok := pod.Annotations[appIDKey]; ok {
				managedPods[appID] = true
			} else {
				break
			}
		}
		return true
	})

	log.G(ctx).Infof("  Currently managing %d app(s)", len(managedPods))

	// Check each device for orphaned apps
	devices := p.deviceManager.ListDevices()
	for _, device := range devices {
		if device.AppHostingMgr == nil {
			continue
		}

		// Get all apps on device
		device.mutex.RLock()
		deviceApps := device.Containers
		device.mutex.RUnlock()

		for appID := range deviceApps {
			// Check if this app is managed by a pod
			if !managedPods[appID] && strings.HasPrefix(appID, "vk_default_") {
				log.G(ctx).Warnf("  Found orphaned app: %s on device %s", appID, device.Config.Name)
				orphanedApps = append(orphanedApps, appID)

				if !dryRun {
					log.G(ctx).Infof("  Cleaning up orphaned app: %s", appID)
					if err := device.AppHostingMgr.UndeployApplication(ctx, appID); err != nil {
						log.G(ctx).Errorf("  Failed to cleanup orphaned app %s: %v", appID, err)
					}
				}
			}
		}
	}

	if len(orphanedApps) > 0 {
		if dryRun {
			log.G(ctx).Warnf("⚠️  Found %d orphaned app(s) (dry-run, not cleaning up)", len(orphanedApps))
		} else {
			log.G(ctx).Infof("✅ Cleaned up %d orphaned app(s)", len(orphanedApps))
		}
	} else {
		log.G(ctx).Info("✅ No orphaned apps found")
	}

	return orphanedApps, nil
}

// Helper function to parse integer from string annotation
func parseIntAnnotation(annotations map[string]string, key string, defaultValue int) int {
	if val, ok := annotations[key]; ok {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultValue
}
