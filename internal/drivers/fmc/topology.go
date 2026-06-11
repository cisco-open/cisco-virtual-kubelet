// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func (d *FMCDriver) GetCDPNeighbors(context.Context) ([]common.CDPNeighbor, error)   { return nil, nil }
func (d *FMCDriver) GetOSPFNeighbors(context.Context) ([]common.OSPFNeighbor, error) { return nil, nil }
func (d *FMCDriver) GetHostedApps(context.Context) ([]common.HostedApp, error)       { return nil, nil }

func (d *FMCDriver) GetInterfaceIPs(context.Context) ([]common.InterfaceIP, error) {
	if d == nil || d.config == nil || d.config.Address == "" {
		return nil, nil
	}
	return []common.InterfaceIP{{Interface: "Management0", IPv4: d.config.Address, Status: "up"}}, nil
}

func (d *FMCDriver) GetInterfaceStats(context.Context) ([]common.InterfaceStats, error) {
	if d == nil || d.config == nil || d.config.Address == "" {
		return nil, nil
	}
	return []common.InterfaceStats{{Name: "Management0", OperStatus: "up", IPv4Address: d.config.Address}}, nil
}
