package iosxe

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// CreatePodApps creates IOS-XE AppHosting configurations for all containers in a pod
func (d *XEDriver) CreatePodApps(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).Infof("Configuring AppHosting apps for pod: %s/%s", pod.Namespace, pod.Name)

	containerAppIDs := common.GenerateContainerAppIDs(pod)

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	for _, container := range pod.Spec.Containers {
		appName := containerAppIDs[container.Name]
		log.G(ctx).Infof("Configuring AppHosting app: %s for container: %s", appName, container.Name)

		apps := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{}

		gapp, err := apps.NewApp(appName)
		if err != nil {
			return fmt.Errorf("failed to create app struct for container %s: %w", container.Name, err)
		}

		netConfig := d.getNetworkConfig(pod, &container)
		if netConfig.useDHCP {
			// DHCP mode: only set interface name, omit static IP/gateway configuration
			gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
				VnicGateway_0:                        ygot.String("0"),
				VirtualportgroupGuestInterfaceName_1: ygot.String(netConfig.virtualPortgroupInterface),
			}
		} else {
			// Static IP mode: configure IP address, netmask, and gateway
			gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
				VnicGateway_0:                                  ygot.String("0"),
				VirtualportgroupGuestInterfaceName_1:           ygot.String(netConfig.virtualPortgroupInterface),
				VirtualportgroupGuestIpAddress_1:               ygot.String(netConfig.virtualPortgroupIP),
				VirtualportgroupGuestIpNetmask_1:               ygot.String(netConfig.virtualPortgroupNetmask),
				VirtualportgroupApplicationDefaultGateway_1:    ygot.String(netConfig.defaultGateway),
				VirtualportgroupGuestInterfaceDefaultGateway_1: ygot.Uint8(netConfig.gatewayInterface),
			}
		}

		gapp.RunOptss = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{
			RunOpts: map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
				1: {
					LineIndex: ygot.Uint16(1),
					LineRunOpts: ygot.String(fmt.Sprintf(
						"--label %s=%s "+
							"--label %s=%s "+
							"--label %s=%s "+
							"--label %s=%s",
						common.LabelPodName, pod.Name,
						common.LabelPodNamespace, pod.Namespace,
						common.LabelPodUID, pod.UID,
						common.LabelContainerName, container.Name,
					)),
				},
			},
		}

		resConfig := d.getResourceConfig(&container)
		gapp.ApplicationResourceProfile = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationResourceProfile{
			ProfileName:      ygot.String("custom"),
			CpuUnits:         ygot.Uint16(resConfig.cpuUnits),
			MemoryCapacityMb: ygot.Uint16(resConfig.memoryMB),
			DiskSizeMb:       ygot.Uint16(resConfig.diskMB),
			Vcpu:             ygot.Uint16(resConfig.vcpu),
		}

		gapp.Start = ygot.Bool(true)

		err = d.client.Post(ctx, path, apps, d.marshaller)
		if err != nil {
			return fmt.Errorf("AppHosting config failed for container %s: %w", container.Name, err)
		}

		log.G(ctx).Infof("AppHosting app %s successfully configured for container %s", appName, container.Name)

		err = d.InstallApp(ctx, appName, container.Image)
		if err != nil {
			return fmt.Errorf("failed to install app for container %s: %w", container.Name, err)
		}
	}

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

// DiscoverPodContainersOnDevice queries the device for configured apps matching the pod UID,
// then maps them back to container names using RunOpts labels.
// Returns a map of containerName -> appID.
func (d *XEDriver) DiscoverPodContainersOnDevice(ctx context.Context, pod *v1.Pod) (map[string]string, error) {
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data"

	appsContainer := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData{}

	err := d.client.Get(ctx, path, appsContainer, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app configs: %w", err)
	}

	// Clean the pod UID (remove hyphens) as that's how it appears in app names
	cleanUID := strings.ReplaceAll(string(pod.UID), "-", "")

	containerToAppID := make(map[string]string)

	for _, app := range appsContainer.Apps.App {
		if app.ApplicationName == nil {
			continue
		}

		appName := *app.ApplicationName

		// Check if app name contains the cleaned pod UID
		if !strings.Contains(appName, cleanUID) {
			continue
		}

		log.G(ctx).Debugf("Found app %s with matching pod UID", appName)

		// Extract container name from RunOpts labels
		var containerName string
		var runOptsLine string

		if app.RunOptss != nil {
			for _, opt := range app.RunOptss.RunOpts {
				if opt.LineRunOpts != nil {
					line := *opt.LineRunOpts
					runOptsLine = line

					log.G(ctx).Debugf("App %s RunOpts: %s", appName, line)

					// Verify this app belongs to our pod by checking all pod labels
					if strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodName, pod.Name)) &&
						strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodNamespace, pod.Namespace)) &&
						strings.Contains(line, fmt.Sprintf("%s=%s", common.LabelPodUID, pod.UID)) {

						// Extract the container name from the label
						containerName = common.ExtractContainerNameFromLabels(line)

						if containerName != "" {
							log.G(ctx).Debugf("Extracted container name: %s from app %s", containerName, appName)
						} else {
							log.G(ctx).Warnf("App %s has pod labels but no container name label in line: %s", appName, line)
						}
						break
					}
				}
			}
		}

		if containerName != "" {
			containerToAppID[containerName] = appName
			log.G(ctx).Infof("Discovered container %s -> app %s", containerName, appName)
		} else {
			log.G(ctx).Errorf("Found app %s with pod UID but couldn't extract container name from labels. RunOpts: %s",
				appName, runOptsLine)
		}
	}

	return containerToAppID, nil
}

// DiscoverAppDHCPIP queries the device for the app's IP address from app-hosting-oper-data.
// The NetworkInterface struct contains the IPv4 address directly, so no ARP lookup is needed.
// Returns the discovered IP address, or an error if not found.
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
