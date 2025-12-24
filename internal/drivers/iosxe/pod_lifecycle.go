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

	// POST to app-hosting-cfg-data using correct YANG schema from Cisco-IOS-XE-app-hosting-cfg.yang
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps"

	// Use the correct YANG schema structure based on Cisco-IOS-XE-app-hosting-cfg.yang
	// Management interface fields discovered from working device config query
	// data := map[string]interface{}{
	// 	"Cisco-IOS-XE-app-hosting-cfg:app": []map[string]interface{}{
	// 		{
	// 			"application-name": app.Name,
	// 			// Network resource configuration - MUST include management interface for activation
	// 			"application-network-resource": map[string]interface{}{
	// 				// Management interface configuration (app-vnic management guest-interface)
	// 				"management-interface-name":   "0",
	// 				"management-guest-ip-address": "1.1.1.10",
	// 				"management-guest-ip-netmask": "255.255.255.0",
	// 				// Default gateway configuration
	// 				"virtualportgroup-application-default-gateway-1":     "1.1.1.1",
	// 				"virtualportgroup-guest-interface-default-gateway-1": 0,
	// 			},
	// 			// Resource profile configuration
	// 			"application-resource-profile": map[string]interface{}{
	// 				"cpu-units":          1000,
	// 				"memory-capacity-mb": 512,
	// 				"disk-size-mb":       1024,
	// 				"vcpu":               2,
	// 				"profile-name":       "custom",
	// 			},
	// 		},
	// 	},
	// }

	apps := &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{}

	// 2. Create the new list entry (corresponds to app-id)
	gapp, err := apps.NewApp(pod.Name)
	if err != nil {
		return fmt.Errorf("failed to create app struct: %w", err)
	}

	// 3. Populate Network Resources
	// Use ygot.String/Uint8/etc helpers to handle pointer types in generated structs
	gapp.ApplicationNetworkResource = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationNetworkResource{
		ManagementInterfaceName:                        ygot.String("0"),
		ManagementGuestIpAddress:                       ygot.String("1.1.1.10"),
		ManagementGuestIpNetmask:                       ygot.String("255.255.255.0"),
		VirtualportgroupApplicationDefaultGateway_1:    ygot.String("1.1.1.1"),
		VirtualportgroupGuestInterfaceDefaultGateway_1: ygot.Uint8(0),
	}

	// 4. Populate Resource Profile
	gapp.ApplicationResourceProfile = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_ApplicationResourceProfile{
		ProfileName:      ygot.String("custom"),
		CpuUnits:         ygot.Uint16(1000),
		MemoryCapacityMb: ygot.Uint16(512),
		DiskSizeMb:       ygot.Uint16(1024),
		Vcpu:             ygot.Uint16(2),
	}

	jsonPayload, err := ygot.EmitJSON(apps, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
		RFC7951Config: &ygot.RFC7951JSONConfig{
			AppendModuleName: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to serialize YANG to JSON: %w", err)
	}

	err = d.Client.Post(ctx, path, jsonPayload)
	if err != nil {
		return fmt.Errorf("AppHosting config failed: %w", err)
	}

	log.G(ctx).Infof("AppHosting app %s successfully configured", app.Name)

	return nil
}
