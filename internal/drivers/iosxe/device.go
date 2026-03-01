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
	"fmt"
	"regexp"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// CheckConnection validates connectivity to the device and fetches device info
func (d *XEDriver) CheckConnection(ctx context.Context) error {
	res := &common.HostMeta{}

	err := d.client.Get(ctx, "/.well-known/host-meta", res, d.gethostMetaUnmarshaller())
	if err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}

	log.G(ctx).Debugf("Restconf Root: %s\n", res.Links[0].Href)

	d.deviceInfo = d.fetchDeviceInfo(ctx)
	return nil
}

func (d *XEDriver) fetchDeviceInfo(ctx context.Context) *common.DeviceInfo {
	info := &common.DeviceInfo{}

	resp := &Cisco_IOS_XEDeviceHardwareOper_DeviceHardwareData{}
	err := d.client.Get(ctx, "/restconf/data/Cisco-IOS-XE-device-hardware-oper:device-hardware-data", resp, d.unmarshaller)
	if err != nil {
		log.G(ctx).WithError(err).Debug("Failed to fetch device hardware info")
		return info
	}

	// Get software version from device-system-data and extract just the version number
	if resp.DeviceHardware != nil && resp.DeviceHardware.DeviceSystemData != nil {
		if resp.DeviceHardware.DeviceSystemData.SoftwareVersion != nil {
			info.SoftwareVersion = parseVersionNumber(*resp.DeviceHardware.DeviceSystemData.SoftwareVersion)
		}
	}

	// Find the chassis inventory entry for serial and part number
	if resp.DeviceHardware != nil && resp.DeviceHardware.DeviceInventory != nil {
		for _, inv := range resp.DeviceHardware.DeviceInventory {
			if inv.HwType == Cisco_IOS_XEDeviceHardwareOper_HwType_hw_type_chassis && inv.SerialNumber != nil && *inv.SerialNumber != "" {
				info.SerialNumber = *inv.SerialNumber
				if inv.PartNumber != nil {
					info.ProductID = *inv.PartNumber
				}
				break
			}
		}
	}

	if info.SerialNumber != "" {
		log.G(ctx).Infof("Device info: Serial=%s, Version=%s, Product=%s",
			info.SerialNumber, info.SoftwareVersion, info.ProductID)
	}

	return info
}

// parseVersionNumber extracts the version number (e.g., "17.18.2") from the full software-version string
func parseVersionNumber(fullVersion string) string {
	re := regexp.MustCompile(`Version\s+(\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(fullVersion)
	if len(matches) > 1 {
		return matches[1]
	}
	return fullVersion
}

// GetDeviceInfo returns cached device information
func (d *XEDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d.deviceInfo == nil {
		return &common.DeviceInfo{}, nil
	}
	return d.deviceInfo, nil
}

// GetDeviceResources returns the available resources on the device
func (d *XEDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
		v1.ResourcePods:    resource.MustParse("16"),
	}

	return &resources, nil
}
