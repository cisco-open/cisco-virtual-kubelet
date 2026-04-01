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

// GetGlobalOperationalData queries the device for global AppHosting operational data.
// Returns a common.AppHostingOperData struct with resource usage and notifications.
func (d *XEDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data"

	// The root structure matches the YANG model
	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch global oper data: %w", err)
	}

	result := &common.AppHostingOperData{}

	// 1. Check IOx Enabled Status (from AppGlobals)
	if root.AppGlobals != nil && root.AppGlobals.IoxEnabled != nil {
		result.IoxEnabled = *root.AppGlobals.IoxEnabled
	}

	// 2. Parse Resources
	// The YANG model defines lists for cpu, memory, etc. which ygot maps to Go maps.
	// We need to iterate or look up specific keys ("system CPU", "memory", "IOx persist-disk").

	if root.AppResources != nil {
		// Iterate over the "global" resource entry if it exists, or verify structure
		// Based on the XML, <app-resources> has a name. The generated struct uses a map keyed by Name.
		if globalStats, ok := root.AppResources["global"]; ok {

			// CPU
			if globalStats.Cpu != nil {
				if cpu, ok := globalStats.Cpu["system CPU"]; ok {
					if cpu.Quota != nil {
						result.SystemCPU.Quota = int64(*cpu.Quota)
					}
					if cpu.Available != nil {
						result.SystemCPU.Available = int64(*cpu.Available)
					}
					if cpu.QuotaUnit != nil {
						result.SystemCPU.Unit = fmt.Sprintf("%d", *cpu.QuotaUnit)
					}
				}
			}

			// Memory
			if globalStats.Memory != nil {
				if mem, ok := globalStats.Memory["memory"]; ok {
					if mem.Quota != nil {
						result.Memory.Quota = int64(*mem.Quota)
					}
					if mem.Available != nil {
						result.Memory.Available = int64(*mem.Available)
					}
				}
			}

			// Storage
			if globalStats.StorageDevice != nil {
				if disk, ok := globalStats.StorageDevice["IOx persist-disk"]; ok {
					if disk.Quota != nil {
						result.Storage.Quota = int64(*disk.Quota)
					}
					if disk.Available != nil {
						result.Storage.Available = int64(*disk.Available)
					}
				}
			}
		}
	}

	// 3. Parse Notifications
	if root.AppNotifications != nil {
		for _, note := range root.AppNotifications {
			n := common.AppNotification{}
			if note.AppId != nil {
				n.AppID = *note.AppId
			}
			if note.Message != nil {
				n.Message = *note.Message
			}
			if note.Timestamp != nil {
				n.Timestamp = *note.Timestamp
			}
			// Severity is an enum, we might want the string representation
			// result.Notifications = append(result.Notifications, n)
			// (For now just capturing basics)
			result.Notifications = append(result.Notifications, n)
		}
	}

	return result, nil
}
