// Copyright (c) 2026 Cisco Systems Inc.
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

package iosxr

import (
	"context"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func (d *IOSXRDriver) GetHostedApps(ctx context.Context) ([]common.HostedApp, error) {
	apps, err := d.listApps(ctx)
	if err != nil {
		return nil, err
	}
	hosted := make([]common.HostedApp, 0, len(apps))
	for _, app := range apps {
		detail, err := d.appDetail(ctx, app.ID)
		if err == nil && detail.ID != "" {
			app = detail
		}
		hosted = append(hosted, hostedAppFromXR(app))
	}
	return hosted, nil
}

func (d *IOSXRDriver) GetInterfaceIPs(ctx context.Context) ([]common.InterfaceIP, error) {
	out, err := d.client.Run(ctx, "show ipv4 interface brief")
	if err != nil {
		return nil, err
	}
	return parseXRInterfaceIPs(out), nil
}

func (d *IOSXRDriver) GetInterfaceStats(ctx context.Context) ([]common.InterfaceStats, error) {
	ifaces, err := d.GetInterfaceIPs(ctx)
	if err != nil {
		return nil, err
	}
	stats := make([]common.InterfaceStats, 0, len(ifaces))
	for _, iface := range ifaces {
		stats = append(stats, common.InterfaceStats{
			Name:        iface.Interface,
			OperStatus:  iface.Status,
			IPv4Address: iface.IPv4,
		})
	}
	return stats, nil
}

func (d *IOSXRDriver) GetCDPNeighbors(ctx context.Context) ([]common.CDPNeighbor, error) {
	out, err := d.client.Run(ctx, "show cdp neighbors detail")
	if err != nil {
		return nil, err
	}
	return parseCDPNeighbors(out), nil
}

func (d *IOSXRDriver) GetOSPFNeighbors(ctx context.Context) ([]common.OSPFNeighbor, error) {
	out, err := d.client.Run(ctx, "show ospf neighbor")
	if err != nil {
		return nil, err
	}
	return parseOSPFNeighbors(out), nil
}

func parseCDPNeighbors(out string) []common.CDPNeighbor {
	var neighbors []common.CDPNeighbor
	var cur common.CDPNeighbor
	flush := func() {
		if cur.DeviceID != "" {
			neighbors = append(neighbors, cur)
			cur = common.CDPNeighbor{}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Device ID:") {
			flush()
			cur.DeviceID = strings.TrimSpace(strings.TrimPrefix(line, "Device ID:"))
		}
		if strings.HasPrefix(line, "IP address:") {
			cur.IP = strings.TrimSpace(strings.TrimPrefix(line, "IP address:"))
		}
		if strings.HasPrefix(line, "Platform:") {
			cur.Platform = strings.TrimSpace(strings.TrimPrefix(line, "Platform:"))
		}
		if strings.HasPrefix(line, "Interface:") {
			parts := strings.Split(line, ",")
			cur.LocalInterface = strings.TrimSpace(strings.TrimPrefix(parts[0], "Interface:"))
			if len(parts) > 1 {
				cur.RemoteInterface = strings.TrimSpace(strings.TrimPrefix(parts[1], "Port ID (outgoing port):"))
			}
		}
	}
	flush()
	return neighbors
}

func parseOSPFNeighbors(out string) []common.OSPFNeighbor {
	var neighbors []common.OSPFNeighbor
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 || strings.EqualFold(fields[0], "Neighbor") {
			continue
		}
		neighbors = append(neighbors, common.OSPFNeighbor{
			NeighborID: fields[0],
			State:      strings.ToLower(fields[2]),
			Address:    fields[3],
		})
	}
	return neighbors
}
