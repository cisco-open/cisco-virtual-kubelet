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
)

// createAppWaitTimeout is the maximum time to wait for each state transition
// during app creation. IOS-XE devices (especially C8000V) can take significant
// time to transition between states during install, activate, and start.
const createAppWaitTimeout = 120 * time.Second

// CreateAppHostingApp creates a single IOS-XE AppHosting app from an AppHostingConfig.
// This function configures the app on the device and drives it through the full
// lifecycle: configure → install → activate → start, waiting for each state
// transition to complete before proceeding.
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

	// Wait for the app to reach DEPLOYED status after install
	if err := d.WaitForAppStatus(ctx, appConfig.AppName, "DEPLOYED", createAppWaitTimeout); err != nil {
		return fmt.Errorf("app %s failed to reach DEPLOYED status after install: %w", appConfig.AppName, err)
	}

	// Activate the app
	if err := d.ActivateApp(ctx, appConfig.AppName); err != nil {
		return fmt.Errorf("failed to activate app %s: %w", appConfig.AppName, err)
	}

	// Wait for the app to reach ACTIVATED status
	if err := d.WaitForAppStatus(ctx, appConfig.AppName, "ACTIVATED", createAppWaitTimeout); err != nil {
		return fmt.Errorf("app %s failed to reach ACTIVATED status after activate: %w", appConfig.AppName, err)
	}

	// Start the app
	if err := d.StartApp(ctx, appConfig.AppName); err != nil {
		return fmt.Errorf("failed to start app %s: %w", appConfig.AppName, err)
	}

	// Wait for the app to reach RUNNING status
	if err := d.WaitForAppStatus(ctx, appConfig.AppName, "RUNNING", createAppWaitTimeout); err != nil {
		return fmt.Errorf("app %s failed to reach RUNNING status after start: %w", appConfig.AppName, err)
	}

	log.G(ctx).Infof("Successfully created and started app %s", appConfig.AppName)
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

// DeleteApp orchestrates the full app deletion lifecycle: stop → deactivate → uninstall → delete config.
// It is state-aware so that retries work correctly: if a previous attempt was interrupted
// mid-lifecycle (e.g., app left in STOPPED state), the function skips already-completed
// steps instead of failing on an inapplicable RPC.
func (d *XEDriver) DeleteApp(ctx context.Context, appID string) error {
	// Determine the app's current state so we can resume from the right point.
	currentState, err := d.getAppState(ctx, appID)
	if err != nil {
		return fmt.Errorf("failed to query app state for %s: %w", appID, err)
	}

	log.G(ctx).Infof("DeleteApp %s: current state is %s", appID, currentState)

	// State machine for teardown (reverse of create):
	//   RUNNING  → stop  → ACTIVATED
	//   STOPPED  →       → (treat as ACTIVATED, skip stop)
	//   ACTIVATED→ deactivate → DEPLOYED
	//   DEPLOYED → uninstall  → (removed)
	//
	// Fall through each phase so we always reach config deletion.

	if currentState == "RUNNING" {
		log.G(ctx).Infof("Stopping app %s", appID)
		if err := d.StopApp(ctx, appID); err != nil {
			return fmt.Errorf("failed to stop app: %w", err)
		}
		if err := d.WaitForAppStatus(ctx, appID, "ACTIVATED", 30*time.Second); err != nil {
			log.G(ctx).Warnf("App %s did not reach ACTIVATED status after stop: %v", appID, err)
		}
	}

	if currentState == "RUNNING" || currentState == "STOPPED" || currentState == "ACTIVATED" {
		log.G(ctx).Infof("Deactivating app %s", appID)
		if err := d.DeactivateApp(ctx, appID); err != nil {
			return fmt.Errorf("failed to deactivate app: %w", err)
		}
		if err := d.WaitForAppStatus(ctx, appID, "DEPLOYED", 30*time.Second); err != nil {
			log.G(ctx).Warnf("App %s did not reach DEPLOYED status after deactivate: %v", appID, err)
		}
	}

	if currentState == "RUNNING" || currentState == "STOPPED" || currentState == "ACTIVATED" || currentState == "DEPLOYED" {
		log.G(ctx).Infof("Uninstalling app %s", appID)
		if err := d.UninstallApp(ctx, appID); err != nil {
			return fmt.Errorf("failed to uninstall app: %w", err)
		}
		if err := d.WaitForAppNotPresent(ctx, appID, 60*time.Second); err != nil {
			log.G(ctx).Warnf("App %s still present in oper data after uninstall: %v", appID, err)
		}
	}

	log.G(ctx).Infof("Removing app %s config", appID)
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
	if err := d.client.Delete(ctx, path); err != nil {
		return fmt.Errorf("failed to delete app config: %w", err)
	}

	log.G(ctx).Infof("Successfully deleted app %s", appID)
	return nil
}

// getAppState returns the current operational state of an app (e.g., "RUNNING",
// "STOPPED", "ACTIVATED", "DEPLOYED"), or empty string if the app has no
// operational data.
func (d *XEDriver) getAppState(ctx context.Context, appID string) (string, error) {
	allAppOperData, err := d.GetAppOperationalData(ctx)
	if err != nil {
		return "", err
	}

	app, ok := allAppOperData[appID]
	if !ok || app.Details == nil || app.Details.State == nil {
		return "", nil
	}

	return *app.Details.State, nil
}

// deleteAppConfig removes only the RESTCONF configuration entry for an app,
// without attempting stop/deactivate/uninstall. This is used for orphaned configs
// where the app was never installed (no operational data exists).
func (d *XEDriver) deleteAppConfig(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Removing orphaned config for app %s", appID)
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
	if err := d.client.Delete(ctx, path); err != nil {
		return fmt.Errorf("failed to delete app config for %s: %w", appID, err)
	}
	log.G(ctx).Infof("Successfully removed orphaned config for app %s", appID)
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
