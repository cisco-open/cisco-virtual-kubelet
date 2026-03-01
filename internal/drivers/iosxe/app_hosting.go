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

package iosxe

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// CreateAppHostingApp creates a single IOS-XE AppHosting app from an AppHostingConfig.
// This function configures the app on the device and initiates the installation process.
func (d *XEDriver) CreateAppHostingApp(ctx context.Context, appConfig *AppHostingConfig) error {
	log.G(ctx).Infof("Creating AppHosting app: %s for container: %s", appConfig.AppName(), appConfig.ContainerName())

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	// Post the app configuration to the device
	err := d.client.Post(ctx, path, appConfig.Spec.DeviceConfig, d.marshaller)
	if err != nil {
		return fmt.Errorf("AppHosting config failed for app %s: %w", appConfig.AppName(), err)
	}

	log.G(ctx).Infof("AppHosting app %s successfully configured", appConfig.AppName())

	// Install the app package
	err = d.InstallApp(ctx, appConfig.AppName(), appConfig.ImagePath())
	if err != nil {
		return fmt.Errorf("failed to install app %s: %w", appConfig.AppName(), err)
	}

	log.G(ctx).Infof("Successfully created and installed app %s", appConfig.AppName())
	return nil
}

// appHostingRPC executes an app-hosting RPC operation on the device
func (d *XEDriver) appHostingRPC(ctx context.Context, operation string, appID string, extraParams map[string]string) error {
	payload := map[string]interface{}{
		operation: map[string]string{"appid": appID},
	}

	maps.Copy(payload[operation].(map[string]string), extraParams)

	path := "/restconf/operations/Cisco-IOS-XE-rpc:app-hosting"

	jsonMarshaller := func(v any) ([]byte, error) {
		return json.Marshal(v)
	}

	err := d.client.Post(ctx, path, payload, jsonMarshaller)
	if err != nil {
		return fmt.Errorf("%s operation failed for app %s: %w", operation, appID, err)
	}

	return nil
}

// InstallApp installs an app package on the device
func (d *XEDriver) InstallApp(ctx context.Context, appID string, packagePath string) error {
	log.G(ctx).Infof("Installing app %s from package: %s", appID, packagePath)

	err := d.appHostingRPC(ctx, "install", appID, map[string]string{"package": packagePath})
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully installed app %s", appID)
	return nil
}

// ActivateApp activates an installed app
func (d *XEDriver) ActivateApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Activating app %s", appID)

	err := d.appHostingRPC(ctx, "activate", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully activated app %s", appID)
	return nil
}

// StartApp starts an activated app
func (d *XEDriver) StartApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Starting app %s", appID)

	err := d.appHostingRPC(ctx, "start", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully started app %s", appID)
	return nil
}

// StopApp stops a running app
func (d *XEDriver) StopApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Stopping app %s", appID)

	err := d.appHostingRPC(ctx, "stop", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully stopped app %s", appID)
	return nil
}

// DeactivateApp deactivates an activated app
func (d *XEDriver) DeactivateApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Deactivating app %s", appID)

	err := d.appHostingRPC(ctx, "deactivate", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully deactivated app %s", appID)
	return nil
}

// UninstallApp uninstalls an app from the device
func (d *XEDriver) UninstallApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Uninstalling app %s", appID)

	err := d.appHostingRPC(ctx, "uninstall", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully uninstalled app %s", appID)
	return nil
}

// ReconcileApp performs a single reconciliation step, driving the app one state
// closer to its desired state and updating appConfig.Status in place.
//
// Forward (DesiredState = Running):
//
//	"" (no config)  → POST config + install RPC  → Converging
//	"" (config, no oper) → re-issue install RPC   → Converging
//	"DEPLOYED"      → activate RPC               → Converging
//	"ACTIVATED"     → start RPC                  → Converging
//	"RUNNING"       → no-op                      → Ready
//
// Reverse (DesiredState = Deleted):
//
//	"RUNNING"       → stop RPC                   → Deleting
//	"ACTIVATED"/"STOPPED" → deactivate RPC       → Deleting
//	"DEPLOYED"      → uninstall RPC              → Deleting
//	"" (no oper)    → delete config              → Deleted
func (d *XEDriver) ReconcileApp(ctx context.Context, appConfig *AppHostingConfig) {
	appID := appConfig.AppName()
	desired := appConfig.Spec.DesiredState

	// 1. Observe current device state.
	state := d.getAppState(ctx, appID)
	appConfig.Status.ObservedState = state
	appConfig.Status.LastTransition = time.Now()

	log.G(ctx).Infof("ReconcileApp %s: observed=%q desired=%s phase=%s",
		appID, state, desired, appConfig.Status.Phase)

	// ── Forward path: drive toward RUNNING ────────────────────────────
	if desired == AppDesiredStateRunning {
		switch state {
		case "RUNNING":
			appConfig.Status.Phase = AppPhaseReady
			appConfig.Status.Message = "App is running"
			return

		case "ACTIVATED":
			// ACTIVATED → start
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Starting app"
			if err := d.StartApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: start failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("start failed: %v", err)
			}
			return

		case "DEPLOYED":
			// DEPLOYED → activate
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Activating app"
			if err := d.ActivateApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: activate failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("activate failed: %v", err)
			}
			return

		default:
			// No oper data (or unexpected state) — the install likely hasn't
			// happened or failed silently. Re-issue install if we have an image.
			imagePath := appConfig.ImagePath()
			if imagePath == "" {
				log.G(ctx).Warnf("ReconcileApp %s: no oper data and no image path; cannot install", appID)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = "no image path available for install"
				return
			}
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Re-issuing install"
			log.G(ctx).Warnf("ReconcileApp %s: no oper data; re-issuing install (image: %s)", appID, imagePath)
			if err := d.InstallApp(ctx, appID, imagePath); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: install failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("install failed: %v", err)
			}
			return
		}
	}

	// ── Reverse path: drive toward deletion ───────────────────────────
	if desired == AppDesiredStateDeleted {
		switch state {
		case "RUNNING":
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Stopping app"
			if err := d.StopApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: stop failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("stop failed: %v", err)
			}
			return

		case "ACTIVATED", "STOPPED":
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Deactivating app"
			if err := d.DeactivateApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: deactivate failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("deactivate failed: %v", err)
			}
			return

		case "DEPLOYED":
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Uninstalling app"
			if err := d.UninstallApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: uninstall failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("uninstall failed: %v", err)
			}
			return

		default:
			// No operational data — safe to remove config.
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Removing config"
			path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
			if err := d.client.Delete(ctx, path); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: config delete failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("config delete failed: %v", err)
				return
			}
			appConfig.Status.Phase = AppPhaseDeleted
			appConfig.Status.Message = "App fully removed"
			log.G(ctx).Infof("ReconcileApp %s: fully deleted", appID)
			return
		}
	}
}

// containerImagePath returns the image path for a named container in a pod spec.
func containerImagePath(pod *v1.Pod, containerName string) string {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == containerName {
			return pod.Spec.Containers[i].Image
		}
	}
	return ""
}

// getAppState returns the current operational state string for appID, or ""
// if the app has no oper data or the state cannot be determined.
func (d *XEDriver) getAppState(ctx context.Context, appID string) string {
	if d.client == nil {
		return ""
	}
	allOper, err := d.GetAppOperationalData(ctx)
	if err != nil {
		log.G(ctx).Warnf("Could not fetch oper data to check state of app %s: %v", appID, err)
		return ""
	}
	operData, ok := allOper[appID]
	if !ok || operData == nil || operData.Details == nil || operData.Details.State == nil {
		return ""
	}
	return *operData.Details.State
}

// DeleteApp orchestrates a reconciler-driven teardown of the app lifecycle.
//
// It creates a transient AppHostingConfig with DesiredState=Deleted and
// repeatedly invokes ReconcileApp until the app reaches the Deleted phase
// or a timeout is exceeded.
//
//	RUNNING  → stop → ACTIVATED → deactivate → DEPLOYED → uninstall → (absent) → config delete
func (d *XEDriver) DeleteApp(ctx context.Context, appID string) error {
	appConfig := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: appID},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateDeleted},
		Status:   AppHostingStatus{Phase: AppPhaseDeleting},
	}

	const maxAttempts = 15
	const reconcileInterval = 4 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		d.ReconcileApp(ctx, appConfig)

		if appConfig.Status.Phase == AppPhaseDeleted {
			log.G(ctx).Infof("Successfully deleted app %s after %d reconcile pass(es)", appID, attempt)
			return nil
		}

		log.G(ctx).Debugf("DeleteApp %s: attempt %d/%d, phase=%s observed=%q msg=%s",
			appID, attempt, maxAttempts, appConfig.Status.Phase, appConfig.Status.ObservedState, appConfig.Status.Message)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while deleting app %s", appID)
		case <-time.After(reconcileInterval):
		}
	}

	return fmt.Errorf("app %s not fully deleted after %d reconcile attempts (last phase: %s, observed: %q)",
		appID, maxAttempts, appConfig.Status.Phase, appConfig.Status.ObservedState)
}

// WaitForAppStatus polls the device until the app reaches the expected status or times out
func (d *XEDriver) WaitForAppStatus(ctx context.Context, appID string, expectedStatus string, maxWaitTime time.Duration) error {
	log.G(ctx).Infof("Waiting for app %s to reach status: %s", appID, expectedStatus)

	pollInterval := 2 * time.Second
	deadline := time.Now().Add(maxWaitTime)

	for time.Now().Before(deadline) {
		path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"

		root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
		err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
		if err != nil {
			log.G(ctx).Warnf("Failed to fetch oper data: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		for _, app := range root.App {
			if app.Name == nil || *app.Name != appID {
				continue
			}

			if app.Details != nil && app.Details.State != nil {
				currentState := *app.Details.State
				log.G(ctx).Debugf("App %s current state: %s (waiting for: %s)", appID, currentState, expectedStatus)

				if currentState == expectedStatus {
					log.G(ctx).Infof("App %s reached expected status: %s", appID, expectedStatus)
					return nil
				}
			}
			break
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for app %s status", appID)
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timeout waiting for app %s to reach status %s after %v", appID, expectedStatus, maxWaitTime)
}

// WaitForAppNotPresent polls the device until the app is no longer in operational data
func (d *XEDriver) WaitForAppNotPresent(ctx context.Context, appID string, maxWaitTime time.Duration) error {
	log.G(ctx).Infof("Waiting for app %s to be removed from oper data", appID)

	pollInterval := 2 * time.Second
	deadline := time.Now().Add(maxWaitTime)

	for time.Now().Before(deadline) {
		path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"

		root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
		err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
		if err != nil {
			log.G(ctx).Warnf("Failed to fetch oper data: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		found := false
		for _, app := range root.App {
			if app.Name != nil && *app.Name == appID {
				found = true
				break
			}
		}

		if !found {
			log.G(ctx).Infof("App %s no longer present in oper data", appID)
			return nil
		}

		log.G(ctx).Debugf("App %s still present in oper data, waiting...", appID)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for app %s to be removed", appID)
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timeout waiting for app %s to be removed from oper data after %v", appID, maxWaitTime)
}

// ListAppHostingApps queries the device for all configured AppHosting apps.
// Returns a slice of all app configurations found on the device.
func (d *XEDriver) ListAppHostingApps(ctx context.Context) ([]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App, error) {
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data"

	appsContainer := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData{}

	err := d.client.Get(ctx, path, appsContainer, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app configs: %w", err)
	}

	if appsContainer.Apps == nil || len(appsContainer.Apps.App) == 0 {
		log.G(ctx).Debug("No apps found on device")
		return []*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App{}, nil
	}

	// Convert map to slice for easier iteration
	appsList := make([]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App, 0, len(appsContainer.Apps.App))
	for _, app := range appsContainer.Apps.App {
		appsList = append(appsList, app)
	}

	log.G(ctx).Debugf("Found %d apps on device", len(appsList))
	return appsList, nil
}

// GetAppOperationalData queries the device for operational data of all AppHosting apps.
// Returns a map of appName -> operational data.
func (d *XEDriver) GetAppOperationalData(ctx context.Context) (map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App, error) {
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app"

	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app operational data: %w", err)
	}

	if root.App == nil {
		log.G(ctx).Debug("No operational data found on device")
		return make(map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App), nil
	}

	log.G(ctx).Debugf("Fetched operational data for %d apps", len(root.App))
	d.debugLogJson(ctx, root)

	return root.App, nil
}

// DiscoverAppDHCPIP queries the device for the app's IP address from app-hosting-oper-data.
// The NetworkInterface struct contains the IPv4 address directly, so no ARP lookup is needed.
// Returns the discovered IP address, or an error if not found.
// --- REQUIRES VERIFICATION IN NEW CODE, not working in current c8kv router code - "c9300 running 26.01 dev image seems to work for ipv4" ---
func (d *XEDriver) DiscoverAppDHCPIP(ctx context.Context, appName string) (string, error) {
	log.G(ctx).Debugf("Discovering DHCP IP for app: %s", appName)

	// Query app-hosting-oper-data for the app's network interfaces
	appOperPath := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"

	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	err := d.client.Get(ctx, appOperPath, root, d.getRestconfUnmarshaller())
	if err != nil {
		return "", fmt.Errorf("failed to fetch app oper data: %w", err)
	}

	// Find the specific app in the operational data
	var appOperData *Cisco_IOS_XEAppHostingOper_AppHostingOperData_App
	for _, app := range root.App {
		if app.Name != nil && *app.Name == appName {
			appOperData = app
			break
		}
	}

	if appOperData == nil {
		return "", fmt.Errorf("app %s not found in operational data", appName)
	}

	// Extract IPv4 address from network interfaces
	if appOperData.NetworkInterfaces != nil {
		for macAddr, netIf := range appOperData.NetworkInterfaces.NetworkInterface {
			if netIf.Ipv4Address != nil && *netIf.Ipv4Address != "" {
				ipAddress := *netIf.Ipv4Address
				log.G(ctx).Infof("Discovered DHCP IP for app %s (MAC: %s): %s", appName, macAddr, ipAddress)
				return ipAddress, nil
			}
		}
	}

	return "", fmt.Errorf("no IPv4 address found for app %s", appName)
}
