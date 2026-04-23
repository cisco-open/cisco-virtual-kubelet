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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// CreateAppHostingApp creates a single IOS-XE AppHosting app from an AppHostingConfig.
// Primary path: installs the image URL directly and waits for RUNNING.
// Fallback path: if the device does not reach RUNNING (platform cannot pull from HTTP),
// downloads the image to device flash via the copy RPC and reinstalls from the local path.
func (d *XEDriver) CreateAppHostingApp(ctx context.Context, appConfig *AppHostingConfig) error {
	log.G(ctx).Infof("Creating AppHosting app: %s for container: %s", appConfig.AppName(), appConfig.ContainerName())

	cfgPath := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	if err := d.client.Post(ctx, cfgPath, appConfig.Spec.DeviceConfig, d.marshaller); err != nil {
		return fmt.Errorf("AppHosting config failed for app %s: %w", appConfig.AppName(), err)
	}

	timeout := appConfig.PackageTimeout()
	if timeout == 0 {
		timeout = defaultPackageTimeout
	}
	policy := appConfig.ImagePullPolicy()

	if err := d.InstallApp(ctx, appConfig.AppName(), appConfig.ImagePath()); err != nil {
		return fmt.Errorf("failed to install app %s: %w", appConfig.AppName(), err)
	}

	// For non-HTTP image paths (flash, bootflash, etc.) the device auto-advances
	// from DEPLOYED to RUNNING because Start=true is already set in the posted config.
	// Wait for RUNNING directly; no explicit activate/start RPCs are needed.
	if !isHTTPURL(appConfig.ImagePath()) {
		if err := d.WaitForAppStatus(ctx, appConfig.AppName(), "RUNNING", timeout); err != nil {
			return fmt.Errorf("app %s did not reach RUNNING after flash install: %w", appConfig.AppName(), err)
		}
		log.G(ctx).Infof("Successfully installed app %s (local path)", appConfig.AppName())
		return nil
	}

	// ── PRIMARY PATH (HTTP image) ─────────────────────────────────────────────
	// The device can pull and activate the image itself. Wait for RUNNING.
	waitErr := d.WaitForAppStatus(ctx, appConfig.AppName(), "RUNNING", timeout)
	if waitErr == nil {
		log.G(ctx).Infof("Successfully created and installed app %s", appConfig.AppName())
		return nil
	}

	log.G(ctx).Warnf("App %s did not reach RUNNING state after install: %v", appConfig.AppName(), waitErr)

	// ── FALLBACK PATH (copy-then-install) ─────────────────────────────────────
	if policy == v1.PullNever {
		return fmt.Errorf("app %s did not reach RUNNING and imagePullPolicy is Never: %w", appConfig.AppName(), waitErr)
	}
	if !isHTTPURL(appConfig.ImagePath()) {
		return fmt.Errorf("app %s did not reach RUNNING and image path is not an HTTP URL (no copy fallback): %w", appConfig.AppName(), waitErr)
	}

	dest := appConfig.PackageDest()
	if dest == "" {
		dest = fmt.Sprintf("flash:/virtual-kubelet/%s.tar", appConfig.AppName())
	}
	log.G(ctx).Infof("Falling back to copy-then-install for app %s (dest: %s)", appConfig.AppName(), dest)

	d.markPodRecovering(appConfig.PodUID())

	src, authErr := d.maybeAddAuthToURL(ctx, appConfig.ImagePath(), appConfig.ImagePullSecrets())
	if authErr != nil {
		log.G(ctx).Warnf("Failed to resolve auth for app %s: %v (continuing without credentials)", appConfig.AppName(), authErr)
		src = appConfig.ImagePath()
	}

	if delErr := d.DeleteApp(ctx, appConfig.AppName()); delErr != nil {
		log.G(ctx).Warnf("Failed to delete app %s before copy recovery (continuing): %v", appConfig.AppName(), delErr)
	}

	// Re-POST config with Start=false to prevent premature auto-start during copy.
	gapp := appConfig.Spec.DeviceConfig.App[appConfig.AppName()]
	origStart := gapp.Start
	falseVal := false
	gapp.Start = &falseVal
	postErr := d.client.Post(ctx, cfgPath, appConfig.Spec.DeviceConfig, d.marshaller)
	gapp.Start = origStart
	if postErr != nil {
		d.clearPodRecovering(appConfig.PodUID())
		return fmt.Errorf("failed to re-post config (Start=false) for app %s: %w", appConfig.AppName(), postErr)
	}

	if err := d.copyRPC(ctx, src, dest); err != nil {
		d.clearPodRecovering(appConfig.PodUID())
		return fmt.Errorf("copy failed for app %s: %w", appConfig.AppName(), err)
	}

	if err := d.InstallApp(ctx, appConfig.AppName(), dest); err != nil {
		d.clearPodRecovering(appConfig.PodUID())
		return fmt.Errorf("failed to reinstall app %s from flash: %w", appConfig.AppName(), err)
	}

	if err := d.WaitForAppStatus(ctx, appConfig.AppName(), "DEPLOYED", 30*time.Second); err != nil {
		d.clearPodRecovering(appConfig.PodUID())
		return fmt.Errorf("app %s did not reach DEPLOYED after flash install: %w", appConfig.AppName(), err)
	}

	// Set Start=true and re-POST to trigger device native auto-start.
	trueVal := true
	gapp.Start = &trueVal
	postErr = d.client.Post(ctx, cfgPath, appConfig.Spec.DeviceConfig, d.marshaller)
	gapp.Start = origStart
	if postErr != nil {
		d.clearPodRecovering(appConfig.PodUID())
		return fmt.Errorf("failed to re-post config (Start=true) for app %s: %w", appConfig.AppName(), postErr)
	}

	if err := d.WaitForAppStatus(ctx, appConfig.AppName(), "RUNNING", timeout); err != nil {
		d.clearPodRecovering(appConfig.PodUID())
		return fmt.Errorf("app %s did not reach RUNNING after copy fallback: %w", appConfig.AppName(), err)
	}

	d.clearPodRecovering(appConfig.PodUID())
	log.G(ctx).Infof("Successfully installed app %s via copy fallback", appConfig.AppName())
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

// GetHostedApps returns a lightweight summary of all app-hosting containers
// on the device, suitable for topology emission.  It merges config (RunOpts
// labels) and oper data (state, network interfaces) into common.HostedApp
// structs.
func (d *XEDriver) GetHostedApps(ctx context.Context) ([]common.HostedApp, error) {
	operMap, err := d.GetAppOperationalData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch oper data for hosted apps: %w", err)
	}

	var apps []common.HostedApp
	for appName, oper := range operMap {
		ha := common.HostedApp{AppID: appName}

		// State
		if oper.Details != nil && oper.Details.State != nil {
			ha.State = *oper.Details.State
		}

		// K8s metadata from RunOpts labels (oper data mirrors the configured RunOpts)
		if oper.Details != nil && oper.Details.DockerRunOpts != nil {
			line := *oper.Details.DockerRunOpts
			ha.PodName = common.ExtractLabelValue(line, common.LabelPodName)
			ha.PodNamespace = common.ExtractLabelValue(line, common.LabelPodNamespace)
			ha.PodUID = common.ExtractLabelValue(line, common.LabelPodUID)
			ha.ContainerName = common.ExtractContainerNameFromLabels(line)
		}

		// Fallback: derive metadata from CVK naming convention
		if ha.PodUID == "" || ha.ContainerName == "" {
			if idx, uid, ok := common.ParseCVKAppName(appName); ok {
				if ha.PodUID == "" {
					ha.PodUID = uid
				}
				if ha.ContainerName == "" {
					ha.ContainerName = fmt.Sprintf("container-%d", idx)
				}
				if ha.PodName == "" {
					ha.PodName = appName
				}
				if ha.PodNamespace == "" {
					ha.PodNamespace = "default"
				}
			}
		}

		// Network info from the first interface with an IP
		if oper.NetworkInterfaces != nil {
			for mac, netIf := range oper.NetworkInterfaces.NetworkInterface {
				if netIf == nil {
					continue
				}
				ha.MACAddress = mac
				if netIf.AttachedInterface != nil {
					ha.AttachedInterface = *netIf.AttachedInterface
				}
				if netIf.Ipv4Address != nil && *netIf.Ipv4Address != "" {
					ha.IPv4Address = *netIf.Ipv4Address
					break // prefer the interface that has an IP
				}
			}
		}

		apps = append(apps, ha)
	}

	log.G(ctx).Debugf("GetHostedApps: found %d hosted apps", len(apps))
	return apps, nil
}

// isHTTPURL returns true if s begins with http:// or https://.
func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// copyRPC downloads a file from source to destination using the IOS-XE copy RESTCONF RPC.
// The call is synchronous and may block for several minutes while the device fetches the image.
func (d *XEDriver) copyRPC(ctx context.Context, source, destination string) error {
	log.G(ctx).Infof("Starting copy operation (may take several minutes): %s -> %s", source, destination)

	payload := map[string]interface{}{
		"Cisco-IOS-XE-rpc:copy": map[string]string{
			"source-drop-node-name":      source,
			"destination-drop-node-name": destination,
		},
	}

	path := "/restconf/operations/Cisco-IOS-XE-rpc:copy"
	jsonMarshaller := func(v any) ([]byte, error) { return json.Marshal(v) }

	if err := d.client.Post(ctx, path, payload, jsonMarshaller); err != nil {
		return fmt.Errorf("copy RPC failed (%s -> %s): %w", source, destination, err)
	}

	log.G(ctx).Infof("Copy operation completed successfully: %s -> %s", source, destination)
	return nil
}

// registryAuth holds registry credentials resolved from an imagePullSecret.
type registryAuth struct {
	Username string
	Password string
	Token    string
}

// authFromSecret extracts registry credentials from a Kubernetes Secret.
// Returns nil, nil when no usable credentials are found.
func authFromSecret(secret *v1.Secret) (*registryAuth, error) {
	if token, ok := secret.Data["token"]; ok && len(token) > 0 {
		return &registryAuth{Token: string(token)}, nil
	}

	dcj, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		return nil, nil
	}

	var cfg struct {
		Auths map[string]struct {
			Username      string `json:"username"`
			Password      string `json:"password"`
			Auth          string `json:"auth"`
			IdentityToken string `json:"identitytoken"`
			RegistryToken string `json:"registrytoken"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(dcj, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse .dockerconfigjson: %w", err)
	}

	for _, entry := range cfg.Auths {
		if entry.IdentityToken != "" {
			return &registryAuth{Token: entry.IdentityToken}, nil
		}
		if entry.RegistryToken != "" {
			return &registryAuth{Token: entry.RegistryToken}, nil
		}
		if entry.Username != "" {
			return &registryAuth{Username: entry.Username, Password: entry.Password}, nil
		}
		if entry.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				continue
			}
			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				return &registryAuth{Username: parts[0], Password: parts[1]}, nil
			}
		}
	}
	return nil, nil
}

// resolveAuthFromPullSecrets iterates pull secrets and returns the first usable auth.
func (d *XEDriver) resolveAuthFromPullSecrets(ctx context.Context, refs []v1.LocalObjectReference) (*registryAuth, error) {
	if d.secretLister == nil {
		return nil, nil
	}
	for _, ref := range refs {
		secret, err := d.secretLister.Get(ref.Name)
		if err != nil {
			log.G(ctx).Warnf("Failed to get imagePullSecret %q: %v", ref.Name, err)
			continue
		}
		auth, err := authFromSecret(secret)
		if err != nil {
			log.G(ctx).Warnf("Failed to parse imagePullSecret %q: %v", ref.Name, err)
			continue
		}
		if auth != nil {
			return auth, nil
		}
	}
	return nil, nil
}

// maybeAddAuthToURL embeds basic-auth credentials into srcURL if pull secrets provide them
// and the URL does not already carry credentials.
// Token auth is resolved but not applied (copy RPC payload does not support headers).
func (d *XEDriver) maybeAddAuthToURL(ctx context.Context, srcURL string, refs []v1.LocalObjectReference) (string, error) {
	auth, err := d.resolveAuthFromPullSecrets(ctx, refs)
	if err != nil || auth == nil {
		return srcURL, err
	}
	if auth.Username == "" {
		return srcURL, nil // token auth — cannot be embedded in URL
	}
	u, parseErr := url.Parse(srcURL)
	if parseErr != nil {
		return srcURL, parseErr
	}
	if u.User != nil {
		return srcURL, nil // URL already has credentials
	}
	u.User = url.UserPassword(auth.Username, auth.Password)
	return u.String(), nil
}
