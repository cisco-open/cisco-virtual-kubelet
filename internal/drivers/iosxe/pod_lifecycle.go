package iosxe

import (
	"context"
	"fmt"

	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

func (d *XEDriver) ConfigureAppContainer(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).Infof("Configuring AppHosting app: %s", pod.Name)

	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	apps := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{}

	// 2. Create the new list entry (corresponds to app-id)
	gapp, err := apps.NewApp(pod.Name)
	if err != nil {
		return fmt.Errorf("failed to create app struct: %w", err)
	}

	gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
		ManagementInterfaceName:                        ygot.String("0"),
		ManagementGuestIpAddress:                       ygot.String("1.1.1.10"),
		ManagementGuestIpNetmask:                       ygot.String("255.255.255.0"),
		VirtualportgroupApplicationDefaultGateway_1:    ygot.String("1.1.1.1"),
		VirtualportgroupGuestInterfaceDefaultGateway_1: ygot.Uint8(0),
	}

	gapp.ApplicationResourceProfile = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationResourceProfile{
		ProfileName:      ygot.String("custom"),
		CpuUnits:         ygot.Uint16(1000),
		MemoryCapacityMb: ygot.Uint16(512),
		DiskSizeMb:       ygot.Uint16(1024),
		Vcpu:             ygot.Uint16(2),
	}

	err = d.client.Post(ctx, path, apps, d.marshaller)
	if err != nil {
		return fmt.Errorf("AppHosting config failed: %w", err)
	}

	log.G(ctx).Infof("AppHosting app %s successfully configured", pod.Name)

	return nil
}
