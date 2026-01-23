package iosxe

import (
	"context"
	"fmt"
	"net"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func (d *XEDriver) ConfigureAppContainer(ctx context.Context, pod *v1.Pod) error {
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
			ManagementInterfaceName:                        ygot.String(netConfig.managementInterface),
			ManagementGuestIpAddress:                       ygot.String(netConfig.managementIP),
			ManagementGuestIpNetmask:                       ygot.String(netConfig.managementNetmask),
			VirtualportgroupApplicationDefaultGateway_1:    ygot.String(netConfig.defaultGateway),
			VirtualportgroupGuestInterfaceDefaultGateway_1: ygot.Uint8(netConfig.gatewayInterface),
		}

		gapp.RunOptss = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{
			RunOpts: map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
				1: {
					LineIndex:   ygot.Uint16(1),
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
	}

	return nil
}

type networkConfig struct {
	managementInterface string
	managementIP        string
	managementNetmask   string
	defaultGateway      string
	gatewayInterface    uint8
}

func (d *XEDriver) getNetworkConfig(pod *v1.Pod, container *v1.Container) *networkConfig {
	ip, netmask, gateway := d.allocateIPForContainer(pod, container)
	
	return &networkConfig{
		managementInterface: "0",
		managementIP:        ip,
		managementNetmask:   netmask,
		defaultGateway:      gateway,
		gatewayInterface:    0,
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
