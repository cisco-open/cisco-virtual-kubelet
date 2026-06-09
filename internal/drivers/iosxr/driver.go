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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultXRCPUCores              int64 = 4
	defaultXRMemoryMB              int64 = 16384
	defaultXRStorageMB             int64 = 1500
	defaultXRDeviceInfoCachePeriod       = 30 * time.Second
)

type commandClient interface {
	Run(ctx context.Context, command string) (string, error)
	Configure(ctx context.Context, commands ...string) (string, error)
}

// IOSXRDriver drives IOS XR app-hosting through appmgr over SSH CLI.
type IOSXRDriver struct {
	config *v1alpha1.DeviceSpec
	client commandClient

	mu                      sync.Mutex
	deviceInfo              *common.DeviceInfo
	deviceInfoLastRefreshed time.Time
}

// HealthSnapshot is a compact IOS XR troubleshooting bundle.
type HealthSnapshot struct {
	CapturedAt   metav1.Time
	DeviceInfo   common.DeviceInfo
	Platform     string
	SourceTable  string
	Applications string
	Interfaces   []common.InterfaceIP
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*IOSXRDriver, error) {
	client, err := newSSHClient(spec)
	if err != nil {
		return nil, err
	}
	driver := &IOSXRDriver{
		config: spec,
		client: client,
	}
	if err := driver.CheckConnection(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

func (d *IOSXRDriver) CheckConnection(ctx context.Context) error {
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return fmt.Errorf("iosxr connectivity check failed: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": info.Hostname,
		"version":  info.SoftwareVersion,
		"product":  info.ProductID,
		"serial":   info.SerialNumber,
	}).Info("IOS XR device connected")
	return nil
}

func (d *IOSXRDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	d.mu.Lock()
	cached := d.deviceInfo
	fresh := cached != nil && time.Since(d.deviceInfoLastRefreshed) < defaultXRDeviceInfoCachePeriod
	d.mu.Unlock()
	if fresh {
		return copyDeviceInfo(cached), nil
	}
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	return copyDeviceInfo(info), nil
}

func (d *IOSXRDriver) refreshDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	out, err := d.client.Run(ctx, "show version")
	if err != nil {
		return nil, err
	}
	info := parseShowVersion(out)
	if info.Hostname == "" {
		if hostOut, hostErr := d.client.Run(ctx, "show running-config hostname"); hostErr == nil {
			info.Hostname = parseHostname(hostOut)
		}
	}
	if platform, platformErr := d.client.Run(ctx, "show platform"); platformErr == nil {
		enrichPlatform(info, platform)
	}
	d.mu.Lock()
	d.deviceInfo = info
	d.deviceInfoLastRefreshed = time.Now()
	d.mu.Unlock()
	return info, nil
}

func (d *IOSXRDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	res := xrResources(d.config)
	var pods int32 = 16
	if d.config != nil && d.config.MaxPods > 0 {
		pods = d.config.MaxPods
	}
	out := v1.ResourceList{
		v1.ResourceCPU:     *resource.NewQuantity(res.cpuCores, resource.DecimalSI),
		v1.ResourceMemory:  *resource.NewQuantity(res.memoryMB*1024*1024, resource.BinarySI),
		v1.ResourceStorage: *resource.NewQuantity(res.storageMB*1024*1024, resource.BinarySI),
		v1.ResourcePods:    *resource.NewQuantity(int64(pods), resource.DecimalSI),
	}
	return &out, nil
}

func (d *IOSXRDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	if err := d.ensureDocker(ctx); err != nil {
		log.G(ctx).WithError(err).Warn("IOS XR docker daemon start check failed")
	}
	if _, err := d.client.Run(ctx, "show appmgr source-table"); err != nil {
		return nil, err
	}
	res := xrResources(d.config)
	return &common.AppHostingOperData{
		IoxEnabled: true,
		SystemCPU:  common.AppResource{Quota: res.cpuCores, Available: res.cpuCores, Unit: "cores"},
		Memory:     common.AppResource{Quota: res.memoryMB, Available: res.memoryMB, Unit: "MB"},
		Storage:    common.AppResource{Quota: res.storageMB, Available: res.storageMB, Unit: "MB"},
	}, nil
}

// RunOperationalCommand executes an IOS XR CLI command over the management
// channel. This is intentionally exported for future operations/diagnostics.
func (d *IOSXRDriver) RunOperationalCommand(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("iosxr operational command cannot be empty")
	}
	return d.client.Run(ctx, command)
}

func (d *IOSXRDriver) CollectHealthSnapshot(ctx context.Context) (*HealthSnapshot, error) {
	info, err := d.GetDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	platform, err := d.client.Run(ctx, "show platform")
	if err != nil {
		return nil, err
	}
	sourceTable, err := d.client.Run(ctx, "show appmgr source-table")
	if err != nil {
		return nil, err
	}
	apps, err := d.client.Run(ctx, "show appmgr application-table")
	if err != nil {
		return nil, err
	}
	ifaces, err := d.GetInterfaceIPs(ctx)
	if err != nil {
		return nil, err
	}
	return &HealthSnapshot{
		CapturedAt:   metav1.Now(),
		DeviceInfo:   *info,
		Platform:     platform,
		SourceTable:  sourceTable,
		Applications: apps,
		Interfaces:   ifaces,
	}, nil
}

func (d *IOSXRDriver) ensureDocker(ctx context.Context) error {
	command := xrEnableDockerCommand(d.config)
	if command == "" {
		return nil
	}
	_, err := d.client.Run(ctx, command)
	return err
}

type applianceResources struct {
	cpuCores  int64
	memoryMB  int64
	storageMB int64
}

func xrResources(spec *v1alpha1.DeviceSpec) applianceResources {
	res := applianceResources{
		cpuCores:  defaultXRCPUCores,
		memoryMB:  defaultXRMemoryMB,
		storageMB: defaultXRStorageMB,
	}
	if spec == nil || spec.XR == nil || spec.XR.Resources == nil {
		return res
	}
	if spec.XR.Resources.CPUCores > 0 {
		res.cpuCores = spec.XR.Resources.CPUCores
	}
	if spec.XR.Resources.MemoryMB > 0 {
		res.memoryMB = spec.XR.Resources.MemoryMB
	}
	if spec.XR.Resources.StorageMB >= 0 {
		res.storageMB = spec.XR.Resources.StorageMB
	}
	return res
}

func copyDeviceInfo(info *common.DeviceInfo) *common.DeviceInfo {
	if info == nil {
		return nil
	}
	out := *info
	return &out
}
