package iosxe

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

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
		gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
			VnicGateway_0:                                  ygot.String("1"),
			VirtualportgroupGuestInterfaceName_1:           ygot.String(netConfig.virtualPortgroupInterface),
			VirtualportgroupGuestIpAddress_1:               ygot.String(netConfig.virtualPortgroupIP),
			VirtualportgroupGuestIpNetmask_1:               ygot.String(netConfig.virtualPortgroupNetmask),
			VirtualportgroupApplicationDefaultGateway_1:    ygot.String(netConfig.defaultGateway),
			VirtualportgroupGuestInterfaceDefaultGateway_1: ygot.Uint8(netConfig.gatewayInterface),
		}

		gapp.RunOptss = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{
			RunOpts: map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
				1: {
					LineIndex: ygot.Uint16(1),
					LineRunOpts: ygot.String(fmt.Sprintf(
						"--label io.kubernetes.pod.name=%s "+
							"--label io.kubernetes.pod.namespace=%s "+
							"--label io.kubernetes.pod.uid=%s "+
							"--label io.kubernetes.container.name=%s",
						pod.Name,
						pod.Namespace,
						pod.UID,
						container.Name,
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

func (d *XEDriver) InstallApp(ctx context.Context, appID string, packagePath string) error {
	log.G(ctx).Infof("Installing app %s from package: %s", appID, packagePath)

	err := d.appHostingRPC(ctx, "install", appID, map[string]string{"package": packagePath})
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully installed app %s", appID)
	return nil
}

func (d *XEDriver) ActivateApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Activating app %s", appID)

	err := d.appHostingRPC(ctx, "activate", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully activated app %s", appID)
	return nil
}

func (d *XEDriver) StartApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Starting app %s", appID)

	err := d.appHostingRPC(ctx, "start", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully started app %s", appID)
	return nil
}

func (d *XEDriver) StopApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Stopping app %s", appID)

	err := d.appHostingRPC(ctx, "stop", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully stopped app %s", appID)
	return nil
}

func (d *XEDriver) DeactivateApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Deactivating app %s", appID)

	err := d.appHostingRPC(ctx, "deactivate", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully deactivated app %s", appID)
	return nil
}

func (d *XEDriver) UninstallApp(ctx context.Context, appID string) error {
	log.G(ctx).Infof("Uninstalling app %s", appID)

	err := d.appHostingRPC(ctx, "uninstall", appID, nil)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("Successfully uninstalled app %s", appID)
	return nil
}

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

type networkConfig struct {
	virtualPortgroupInterface string
	virtualPortgroupIP        string
	virtualPortgroupNetmask   string
	defaultGateway            string
	gatewayInterface          uint8
}

func (d *XEDriver) getNetworkConfig(pod *v1.Pod, container *v1.Container) *networkConfig {
	ip, netmask, gateway := d.allocateIPForContainer(pod, container)

	return &networkConfig{
		virtualPortgroupInterface: "0",
		virtualPortgroupIP:        ip,
		virtualPortgroupNetmask:   netmask,
		defaultGateway:            gateway,
		gatewayInterface:          0,
	}
}

func (d *XEDriver) allocateIPForContainer(pod *v1.Pod, container *v1.Container) (ip, netmask, gateway string) {
	if d.config.Networking.PodCIDR != "" {
		_, ipNet, err := net.ParseCIDR(d.config.Networking.PodCIDR)
		if err == nil {
			netmask = net.IP(ipNet.Mask).String()
			gateway = d.getGatewayFromCIDR(ipNet)
			containerIndex := d.getContainerIndex(pod, container)
			ip = d.getIPForContainer(ipNet, containerIndex)
			return
		}
	}

	return "1.1.1.10", "255.255.255.0", "1.1.1.1"
}

func (d *XEDriver) getContainerIndex(pod *v1.Pod, container *v1.Container) int {
	for i, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			return i
		}
	}
	return 0
}

func (d *XEDriver) getGatewayFromCIDR(ipNet *net.IPNet) string {
	ip := ipNet.IP.To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + 1
	return ip.String()
}

func (d *XEDriver) getIPForContainer(ipNet *net.IPNet, containerIndex int) string {
	ip := ipNet.IP.To4()
	if ip == nil {
		return ""
	}
	ip[3] = ip[3] + uint8(10+containerIndex)
	return ip.String()
}

type resourceConfig struct {
	cpuUnits uint16
	memoryMB uint16
	diskMB   uint16
	vcpu     uint16
}

func (d *XEDriver) getResourceConfig(container *v1.Container) *resourceConfig {
	config := &resourceConfig{
		cpuUnits: 1000,
		memoryMB: 512,
		diskMB:   1024,
		vcpu:     1,
	}

	if container.Resources.Requests != nil {
		if cpu := container.Resources.Requests.Cpu(); cpu != nil && !cpu.IsZero() {
			config.cpuUnits = uint16(cpu.MilliValue())
		}
		if mem := container.Resources.Requests.Memory(); mem != nil && !mem.IsZero() {
			config.memoryMB = uint16(mem.Value() / (1024 * 1024))
		}
		if storage := container.Resources.Requests.Storage(); storage != nil && !storage.IsZero() {
			config.diskMB = uint16(storage.Value() / (1024 * 1024))
		}
	}

	if container.Resources.Limits != nil {
		if cpu := container.Resources.Limits.Cpu(); cpu != nil && !cpu.IsZero() {
			milliCores := cpu.MilliValue()
			config.vcpu = uint16((milliCores + 999) / 1000)
			if config.vcpu < 1 {
				config.vcpu = 1
			}
		}
	}

	if d.config.ResourceLimits.DefaultCPU != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultCPU); err == nil {
			config.cpuUnits = uint16(q.MilliValue())
		}
	}
	if d.config.ResourceLimits.DefaultMemory != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultMemory); err == nil {
			config.memoryMB = uint16(q.Value() / (1024 * 1024))
		}
	}
	if d.config.ResourceLimits.DefaultStorage != "" {
		if q, err := resource.ParseQuantity(d.config.ResourceLimits.DefaultStorage); err == nil {
			config.diskMB = uint16(q.Value() / (1024 * 1024))
		}
	}

	return config
}
