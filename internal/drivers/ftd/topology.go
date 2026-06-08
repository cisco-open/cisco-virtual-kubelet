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

package ftd

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func (d *FTDDriver) GetCDPNeighbors(ctx context.Context) ([]common.CDPNeighbor, error) {
	return nil, nil
}

func (d *FTDDriver) GetOSPFNeighbors(ctx context.Context) ([]common.OSPFNeighbor, error) {
	return nil, nil
}

func (d *FTDDriver) GetInterfaceStats(ctx context.Context) ([]common.InterfaceStats, error) {
	network, err := d.fetchNetworkInfo(ctx)
	if err != nil {
		return nil, err
	}
	stats := make([]common.InterfaceStats, 0, len(network.Interfaces))
	for _, intf := range network.Interfaces {
		stats = append(stats, common.InterfaceStats{
			Name:        intf.Name,
			OperStatus:  ftdInterfaceStatus(intf),
			IPv4Address: intf.IPv4,
		})
	}
	return stats, nil
}

func (d *FTDDriver) GetInterfaceIPs(ctx context.Context) ([]common.InterfaceIP, error) {
	network, err := d.fetchNetworkInfo(ctx)
	if err != nil {
		return nil, err
	}
	ips := make([]common.InterfaceIP, 0, len(network.Interfaces))
	for _, intf := range network.Interfaces {
		if intf.IPv4 == "" {
			continue
		}
		ips = append(ips, common.InterfaceIP{
			Interface: intf.Name,
			IPv4:      intf.IPv4,
			Status:    ftdInterfaceStatus(intf),
		})
	}
	return ips, nil
}

func (d *FTDDriver) GetHostedApps(ctx context.Context) ([]common.HostedApp, error) {
	return nil, nil
}
