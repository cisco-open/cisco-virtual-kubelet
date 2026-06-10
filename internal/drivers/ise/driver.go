// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

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
	defaultISECPUCores              int64 = 16
	defaultISEMemoryMB              int64 = 32768
	defaultISEStorageMB             int64 = 300000
	defaultISEDeviceInfoCachePeriod       = 30 * time.Second
)

type commandClient interface {
	Run(ctx context.Context, command string) (string, error)
}

// ISEDriver presents an ISE appliance as a CVK health, telemetry, and
// declarative-configuration node.
type ISEDriver struct {
	config *v1alpha1.DeviceSpec
	client commandClient

	mu                      sync.Mutex
	deviceInfo              *common.DeviceInfo
	deviceInfoLastRefreshed time.Time
}

// HealthSnapshot is a compact troubleshooting bundle collected from ISE CLI.
type HealthSnapshot struct {
	CapturedAt        metav1.Time
	DeviceInfo        common.DeviceInfo
	ApplicationStatus iseApplicationStatus
	RawStatus         string
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*ISEDriver, error) {
	client, err := newSSHClient(spec)
	if err != nil {
		return nil, err
	}
	driver := &ISEDriver{config: spec, client: client}
	if err := driver.CheckConnection(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

func (d *ISEDriver) CheckConnection(ctx context.Context) error {
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return fmt.Errorf("ise connectivity check failed: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": info.Hostname,
		"version":  info.SoftwareVersion,
		"product":  info.ProductID,
		"serial":   info.SerialNumber,
	}).Info("ISE device connected")
	return nil
}

func (d *ISEDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	d.mu.Lock()
	cached := d.deviceInfo
	fresh := cached != nil && time.Since(d.deviceInfoLastRefreshed) < defaultISEDeviceInfoCachePeriod
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

func (d *ISEDriver) refreshDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	out, err := d.client.Run(ctx, "show version")
	if err != nil {
		return nil, err
	}
	info := parseShowVersion(out)
	d.mu.Lock()
	d.deviceInfo = info
	d.deviceInfoLastRefreshed = time.Now()
	d.mu.Unlock()
	return info, nil
}

func (d *ISEDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	res := iseResources(d.config)
	out := v1.ResourceList{
		v1.ResourceCPU:    *resource.NewQuantity(res.cpuCores, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(res.memoryMB*1024*1024, resource.BinarySI),
		v1.ResourcePods:   *resource.NewQuantity(0, resource.DecimalSI),
	}
	if res.storageMB > 0 {
		out[v1.ResourceStorage] = *resource.NewQuantity(res.storageMB*1024*1024, resource.BinarySI)
	}
	return &out, nil
}

func (d *ISEDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	res := iseResources(d.config)
	out, err := d.client.Run(ctx, "show application status ise")
	if err != nil {
		return nil, err
	}
	status := parseApplicationStatus(out)
	message := "Cisco ISE is reachable; app-hosting is not supported on this platform"
	if len(status.Services) > 0 && !applicationStatusHealthy(status) {
		message = "Cisco ISE is reachable, but one or more ISE services are not running"
	}
	return &common.AppHostingOperData{
		IoxEnabled:            false,
		AppHostingUnsupported: true,
		AppHostingMessage:     message,
		SystemCPU:             common.AppResource{Quota: res.cpuCores, Available: res.cpuCores, Unit: "cores"},
		Memory:                common.AppResource{Quota: res.memoryMB, Available: res.memoryMB, Unit: "MB"},
		Storage:               common.AppResource{Quota: res.storageMB, Available: res.storageMB, Unit: "MB"},
	}, nil
}

func (d *ISEDriver) RunOperationalCommand(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("ise operational command cannot be empty")
	}
	return d.client.Run(ctx, command)
}

func (d *ISEDriver) CollectHealthSnapshot(ctx context.Context) (*HealthSnapshot, error) {
	info, err := d.GetDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := d.client.Run(ctx, "show application status ise")
	if err != nil {
		return nil, err
	}
	return &HealthSnapshot{
		CapturedAt:        metav1.Now(),
		DeviceInfo:        *info,
		ApplicationStatus: parseApplicationStatus(raw),
		RawStatus:         raw,
	}, nil
}

type applianceResources struct {
	cpuCores  int64
	memoryMB  int64
	storageMB int64
}

func iseResources(spec *v1alpha1.DeviceSpec) applianceResources {
	res := applianceResources{cpuCores: defaultISECPUCores, memoryMB: defaultISEMemoryMB, storageMB: defaultISEStorageMB}
	if spec == nil || spec.ISE == nil || spec.ISE.Resources == nil {
		return res
	}
	if spec.ISE.Resources.CPUCores > 0 {
		res.cpuCores = spec.ISE.Resources.CPUCores
	}
	if spec.ISE.Resources.MemoryMB > 0 {
		res.memoryMB = spec.ISE.Resources.MemoryMB
	}
	if spec.ISE.Resources.StorageMB >= 0 {
		res.storageMB = spec.ISE.Resources.StorageMB
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
