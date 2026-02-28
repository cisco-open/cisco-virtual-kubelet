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
func (d *XEDriver) CreateAppHostingApp(ctx context.Context, appConfig AppHostingConfig) error {
	log.G(ctx).Infof("Creating AppHosting app: %s for container: %s", appConfig.AppName, appConfig.ContainerName)

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	// Post the app configuration to the device
	err := d.client.Post(ctx, path, appConfig.Apps, d.marshaller)
	if err != nil {
		return fmt.Errorf("AppHosting config failed for app %s: %w", appConfig.AppName, err)
	}

	log.G(ctx).Infof("AppHosting app %s successfully configured", appConfig.AppName)

	// Install the app package
	err = d.InstallApp(ctx, appConfig.AppName, appConfig.ImagePath)
	if err != nil {
		return fmt.Errorf("failed to install app %s: %w", appConfig.AppName, err)
	}

	log.G(ctx).Infof("Successfully created and installed app %s", appConfig.AppName)
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

// ensureAppRunning checks whether an app has operational data after install.
// The device is expected to auto-advance the app to RUNNING via the config-level
// `start: true` flag, so this function only handles the silent install failure
// case where no operational data is present at all.
// This is a best-effort remediation: errors are logged but not propagated,
// because the next status poll will retry.
func (d *XEDriver) ensureAppRunning(ctx context.Context, appID string,
	operData *Cisco_IOS_XEAppHostingOper_AppHostingOperData_App, imagePath string) {

	// If there is any operational data, the device has accepted the install and
	// is driving the lifecycle via start:true — don't interfere.
	if operData != nil && operData.Details != nil && operData.Details.State != nil {
		return
	}

	// No operational data at all — the install likely failed silently.
	if imagePath == "" {
		log.G(ctx).Warnf("App %s has no oper data and no image path; cannot re-install", appID)
		return
	}
	log.G(ctx).Warnf("App %s has no oper data; re-issuing install (image: %s)", appID, imagePath)
	if err := d.InstallApp(ctx, appID, imagePath); err != nil {
		log.G(ctx).Warnf("Re-install of app %s failed: %v", appID, err)
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

// DeleteApp orchestrates a best-effort teardown of the app lifecycle before
// removing the config entry.
//
// State is re-read before each RPC so we only issue operations that are valid
// for the device's *actual* current state.  Each step waits for the expected
// intermediate state before proceeding to the next, ensuring we never send an
// RPC the device will reject (e.g. deactivate while still RUNNING).
//
// The config entry is NOT deleted until the app is confirmed absent from oper
// data, preventing orphaned apps (still running, no config, unmanageable).
//
//	RUNNING  → stop → ACTIVATED → deactivate → DEPLOYED → uninstall → (absent)
//	ACTIVATED/STOPPED            → deactivate → DEPLOYED → uninstall → (absent)
//	DEPLOYED                                             → uninstall → (absent)
//	"" / Uninstalled             → skip teardown, proceed to config delete
func (d *XEDriver) DeleteApp(ctx context.Context, appID string) error {
	state := d.getAppState(ctx, appID)
	log.G(ctx).Infof("Deleting app %s (current state: %q)", appID, state)

	// Step 1: RUNNING → stop → wait for ACTIVATED.
	if state == "RUNNING" {
		log.G(ctx).Infof("Stopping app %s", appID)
		if err := d.StopApp(ctx, appID); err != nil {
			log.G(ctx).Warnf("Stop app %s failed: %v", appID, err)
		} else if err := d.WaitForAppStatus(ctx, appID, "ACTIVATED", 30*time.Second); err != nil {
			log.G(ctx).Warnf("App %s did not reach ACTIVATED after stop: %v", appID, err)
		}
		// Re-read; only continue to deactivate if we actually reached ACTIVATED.
		state = d.getAppState(ctx, appID)
		log.G(ctx).Debugf("App %s state after stop: %q", appID, state)
	}

	// Step 2: ACTIVATED or STOPPED → deactivate → wait for DEPLOYED.
	if state == "ACTIVATED" || state == "STOPPED" {
		log.G(ctx).Infof("Deactivating app %s", appID)
		if err := d.DeactivateApp(ctx, appID); err != nil {
			log.G(ctx).Warnf("Deactivate app %s failed: %v", appID, err)
		} else if err := d.WaitForAppStatus(ctx, appID, "DEPLOYED", 30*time.Second); err != nil {
			log.G(ctx).Warnf("App %s did not reach DEPLOYED after deactivate: %v", appID, err)
		}
		// Re-read; only continue to uninstall if we actually reached DEPLOYED.
		state = d.getAppState(ctx, appID)
		log.G(ctx).Debugf("App %s state after deactivate: %q", appID, state)
	}

	// Step 3: DEPLOYED → uninstall → wait for absent from oper data.
	// Gate the config delete on oper data being cleared to prevent orphaning.
	// The device can take a long time, or the RPC may silently fail, so we
	// retry the uninstall RPC up to maxUninstallAttempts times, waiting
	// between each attempt.  Only if the app is still present after all
	// attempts do we return an error.
	if state == "DEPLOYED" {
		const maxUninstallAttempts = 3
		const uninstallWait = 60 * time.Second

		var uninstallErr error
		for attempt := 1; attempt <= maxUninstallAttempts; attempt++ {
			log.G(ctx).Infof("Uninstalling app %s (attempt %d/%d)", appID, attempt, maxUninstallAttempts)
			if err := d.UninstallApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("Uninstall app %s attempt %d failed: %v", appID, attempt, err)
			}
			if err := d.WaitForAppNotPresent(ctx, appID, uninstallWait); err != nil {
				log.G(ctx).Warnf("App %s still present after uninstall attempt %d: %v", appID, attempt, err)
				uninstallErr = err
				continue
			}
			// App is gone — proceed to config delete.
			uninstallErr = nil
			break
		}
		if uninstallErr != nil {
			return fmt.Errorf("app %s still present in oper data after %d uninstall attempts; deferring config delete to avoid orphan: %w",
				appID, maxUninstallAttempts, uninstallErr)
		}
	}

	// Config delete — only reached once oper data is clean (or was never present).
	log.G(ctx).Infof("Removing app %s config", appID)
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
	if err := d.client.Delete(ctx, path); err != nil {
		return fmt.Errorf("failed to delete app config for %s: %w", appID, err)
	}

	log.G(ctx).Infof("Successfully deleted app %s", appID)
	return nil
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
