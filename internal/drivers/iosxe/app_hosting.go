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

	// Install the app package. If install fails and imagePullPolicy allows, attempt recovery
	// by pulling/copying the image and retrying install once.
	if err := d.installWithRecovery(ctx, appConfig); err != nil {
		return fmt.Errorf("failed to install app %s: %w", appConfig.AppName, err)
	}

	// Wait for the app to reach RUNNING state on the device.
	// The install RPC may return success before the device has actually pulled and started the app.
	// Without this wait, we can report success even though the image was never fetched,
	// resulting in pods that never come up and no retry being triggered.
	timeout := appConfig.PackageTimeout
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	log.G(ctx).Infof("Waiting for app %s to reach RUNNING state (timeout: %v)", appConfig.AppName, timeout)
	if err := d.WaitForAppStatus(ctx, appConfig.AppName, "RUNNING", timeout); err != nil {
		return fmt.Errorf("app %s did not reach RUNNING state after install: %w", appConfig.AppName, err)
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

type iosxeCopyRPCRequest struct {
	Source      string `json:"source-drop-node-name"`
	Destination string `json:"destination-drop-node-name"`
}

func (d *XEDriver) copyRPC(ctx context.Context, source string, destination string) error {
	payload := map[string]iosxeCopyRPCRequest{
		"Cisco-IOS-XE-rpc:copy": {
			Source:      source,
			Destination: destination,
		},
	}

	path := "/restconf/operations/Cisco-IOS-XE-rpc:copy"

	jsonMarshaller := func(v any) ([]byte, error) {
		return json.Marshal(v)
	}

	if err := d.client.Post(ctx, path, payload, jsonMarshaller); err != nil {
		return fmt.Errorf("copy operation failed (source=%s destination=%s): %w", source, destination, err)
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

func (d *XEDriver) installWithRecovery(ctx context.Context, appConfig AppHostingConfig) error {
	// First attempt: install using the image path as provided (current behavior).
	err := d.InstallApp(ctx, appConfig.AppName, appConfig.ImagePath)
	if err == nil {
		return nil
	}

	// Recovery path should be driven by imagePullPolicy.
	// Kubernetes defaulting: if empty, default is PullIfNotPresent.
	policy := appConfig.ImagePullPolicy
	if policy == "" {
		policy = "IfNotPresent"
	}

	// If user explicitly says never pull, do not attempt recovery.
	if policy == "Never" {
		return err
	}

	log.G(ctx).Warnf("Install failed for app %s, attempting image recovery (policy=%s): %v", appConfig.AppName, policy, err)

	// Attempt a best-effort recovery:
	// - determine on-device package destination (annotation value propagated into appConfig.PackageDest)
	// - if ImagePath is HTTP(S), try device-side fetch via IOS-XE copy RPC (optionally applying basic auth)
	// - retry app-hosting install once using the on-device destination file
	//
	// Known gaps:
	// - non-HTTP sources (e.g., local tar path) are not uploaded yet
	// - token-based auth is parsed but not applied to the device fetch currently
	//
	// Destination defaulting:
	// if PackageDest is not set, default to flash:/virtual-kubelet/<app>.tar
	dest := appConfig.PackageDest
	if dest == "" {
		dest = fmt.Sprintf("flash:/virtual-kubelet/%s.tar", appConfig.AppName)
	}
	log.G(ctx).Infof("Install recovery destination for app %s: %s", appConfig.AppName, dest)

	// If the image path is already something the device can fetch (URL), try a device-side copy to dest.
	// This matches the desired "device pulls" behavior via IOS-XE copy RPC.
	copySrc := appConfig.ImagePath
	if isHTTPURL(copySrc) {
		src := copySrc
		if d.secretLister != nil && len(appConfig.ImagePullSecrets) > 0 {
			if u, err := d.maybeAddAuthToURL(ctx, src, appConfig.ImagePullSecrets); err != nil {
				log.G(ctx).Warnf("Failed to apply imagePullSecrets auth for app %s: %v", appConfig.AppName, err)
			} else if u != "" {
				src = u
			}
		}

		if err := d.copyRPC(ctx, src, dest); err != nil {
			log.G(ctx).Warnf("Image recovery copy failed for app %s (src=%s dest=%s): %v", appConfig.AppName, src, dest, err)
		} else {
			log.G(ctx).Infof("Image recovery copy succeeded for app %s (dest=%s)", appConfig.AppName, dest)
			// Retry install using the on-device destination file.
			return d.InstallApp(ctx, appConfig.AppName, dest)
		}
	}

	// Best-effort retry with original packagePath (current behavior).
	return d.InstallApp(ctx, appConfig.AppName, appConfig.ImagePath)
}

const (
	podAnnotationIOSXEAppHostPackageDest    = "virtual-kubelet.cisco.com/iosxe-apphost-package-dest"
	podAnnotationIOSXEAppHostPackageTimeout = "virtual-kubelet.cisco.com/iosxe-apphost-package-timeout"
)

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

type registryAuth struct {
	Username string
	Password string
	Token    string
}

func (d *XEDriver) maybeAddAuthToURL(ctx context.Context, srcURL string, pullSecrets []v1.LocalObjectReference) (string, error) {
	// NOTE: This is a best-effort approach. IOS-XE copy RPC supports http(s) sources, but
	// how it consumes credentials depends on platform capabilities.
	//
	// We currently implement the most transport-agnostic method: embed basic auth in the URL
	// when available. Token auth is extracted for future use but not applied (no standard URL form).

	auth, err := d.resolveAuthFromPullSecrets(ctx, pullSecrets)
	if err != nil {
		return "", err
	}
	if auth == nil {
		return "", nil
	}

	// Prefer basic auth if available.
	if auth.Username == "" {
		return "", nil
	}

	// Only apply to http(s).
	if !isHTTPURL(srcURL) {
		return "", nil
	}

	u, err := url.Parse(srcURL)
	if err != nil {
		return "", err
	}

	// Do not override if already present.
	if u.User != nil {
		return "", nil
	}

	u.User = url.UserPassword(auth.Username, auth.Password)
	return u.String(), nil
}

func (d *XEDriver) resolveAuthFromPullSecrets(ctx context.Context, pullSecrets []v1.LocalObjectReference) (*registryAuth, error) {
	if d.secretLister == nil {
		return nil, nil
	}

	for _, ref := range pullSecrets {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			continue
		}

		secret, err := d.secretLister.Get(name)
		if err != nil {
			log.G(ctx).Debugf("imagePullSecret %q not found: %v", name, err)
			continue
		}

		auth, err := authFromSecret(secret)
		if err != nil {
			log.G(ctx).Debugf("imagePullSecret %q could not be parsed: %v", name, err)
			continue
		}
		if auth != nil {
			return auth, nil
		}
	}

	return nil, nil
}

func authFromSecret(secret *v1.Secret) (*registryAuth, error) {
	if secret == nil {
		return nil, nil
	}

	// Prefer service-account style token if present.
	// This does not map cleanly to URL/basic auth, but we capture it for future use.
	if tok, ok := secret.Data["token"]; ok && len(tok) > 0 {
		return &registryAuth{Token: strings.TrimSpace(string(tok))}, nil
	}

	// Docker config json secret.
	// https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/
	const dockerCfgKey = ".dockerconfigjson"
	b, ok := secret.Data[dockerCfgKey]
	if !ok || len(b) == 0 {
		return nil, nil
	}

	var cfg struct {
		Auths map[string]struct {
			Auth     string `json:"auth"`
			Username string `json:"username"`
			Password string `json:"password"`
			IdentityToken string `json:"identitytoken"`
			RegistryToken string `json:"registrytoken"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}

	for _, a := range cfg.Auths {
		// If identity token exists, prefer it.
		if t := strings.TrimSpace(a.IdentityToken); t != "" {
			return &registryAuth{Token: t}, nil
		}
		if t := strings.TrimSpace(a.RegistryToken); t != "" {
			return &registryAuth{Token: t}, nil
		}

		user := strings.TrimSpace(a.Username)
		pass := strings.TrimSpace(a.Password)
		if user != "" {
			return &registryAuth{Username: user, Password: pass, Token: ""}, nil
		}

		// Fallback to auth field if username/password not given.
		if strings.TrimSpace(a.Auth) != "" {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.Auth))
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

// DeleteApp orchestrates the full app deletion lifecycle: stop → deactivate → uninstall → delete config
func (d *XEDriver) DeleteApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Stopping app %s", appID)
	if err := d.StopApp(ctx, appID); err != nil {
		return fmt.Errorf("failed to stop app: %w", err)
	}
	if err := d.WaitForAppStatus(ctx, appID, "ACTIVATED", 30*time.Second); err != nil {
		log.G(ctx).Warnf("App %s did not reach ACTIVATED status after stop: %v", appID, err)
	}

	log.G(ctx).Infof("Deactivating app %s", appID)
	if err := d.DeactivateApp(ctx, appID); err != nil {
		return fmt.Errorf("failed to deactivate app: %w", err)
	}
	if err := d.WaitForAppStatus(ctx, appID, "DEPLOYED", 30*time.Second); err != nil {
		log.G(ctx).Warnf("App %s did not reach DEPLOYED status after deactivate: %v", appID, err)
	}

	log.G(ctx).Infof("Uninstalling app %s", appID)
	if err := d.UninstallApp(ctx, appID); err != nil {
		return fmt.Errorf("failed to uninstall app: %w", err)
	}
	if err := d.WaitForAppNotPresent(ctx, appID, 60*time.Second); err != nil {
		log.G(ctx).Warnf("App %s still present in oper data after uninstall: %v", appID, err)
	}

	log.G(ctx).Infof("Removing app %s config", appID)
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
	if err := d.client.Delete(ctx, path); err != nil {
		return fmt.Errorf("failed to delete app config: %w", err)
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

		// Check if the app exists in operational data at all
		found := false
		for _, app := range root.App {
			if app.Name == nil || *app.Name != appID {
				continue
			}

			found = true
			if app.Details != nil && app.Details.State != nil {
				currentState := *app.Details.State
				log.G(ctx).Infof("App %s current state: %s (waiting for: %s)", appID, currentState, expectedStatus)

				if currentState == expectedStatus {
					log.G(ctx).Infof("App %s reached expected status: %s", appID, expectedStatus)
					return nil
				}
			} else {
				log.G(ctx).Warnf("App %s found in oper data but has no state details yet", appID)
			}
			break
		}

		if !found {
			log.G(ctx).Warnf("App %s not yet present in operational data (will retry until timeout)", appID)
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
