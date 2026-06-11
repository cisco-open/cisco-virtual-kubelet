// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

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
	defaultFMCCPUCores              int64 = 4
	defaultFMCMemoryMB              int64 = 32768
	defaultFMCStorageMB             int64 = 250000
	defaultFMCDeviceInfoCachePeriod       = 30 * time.Second
)

type healthClient interface {
	Check(context.Context) error
	ServerVersion(context.Context) (*ServerVersion, error)
	SmartLicense(context.Context) (*SmartLicense, error)
	ManagedDevices(context.Context) ([]ManagedDevice, error)
	Close() error
}

// FMCDriver presents an FMC appliance as a CVK health, telemetry, and
// declarative-configuration node.
type FMCDriver struct {
	config *v1alpha1.DeviceSpec
	client healthClient

	mu                      sync.Mutex
	deviceInfo              *common.DeviceInfo
	deviceInfoLastRefreshed time.Time
}

// HealthSnapshot is a compact troubleshooting bundle collected from FMC REST APIs.
type HealthSnapshot struct {
	CapturedAt     metav1.Time
	DeviceInfo     common.DeviceInfo
	License        SmartLicense
	ManagedDevices []ManagedDevice
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*FMCDriver, error) {
	client, err := NewAPIClientFromSpec(spec, spec.Password)
	if err != nil {
		return nil, err
	}
	driver := &FMCDriver{config: spec, client: client}
	if err := driver.CheckConnection(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return driver, nil
}

func (d *FMCDriver) CheckConnection(ctx context.Context) error {
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return fmt.Errorf("fmc connectivity check failed: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": info.Hostname,
		"version":  info.SoftwareVersion,
		"product":  info.ProductID,
		"uuid":     info.SerialNumber,
	}).Info("FMC device connected")
	return nil
}

func (d *FMCDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	d.mu.Lock()
	cached := d.deviceInfo
	fresh := cached != nil && time.Since(d.deviceInfoLastRefreshed) < defaultFMCDeviceInfoCachePeriod
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

func (d *FMCDriver) refreshDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d == nil || d.client == nil {
		return nil, fmt.Errorf("fmc driver: nil client")
	}
	version, err := d.client.ServerVersion(ctx)
	if err != nil {
		return nil, err
	}
	info := &common.DeviceInfo{
		Hostname:        version.Hostname,
		SoftwareVersion: version.ServerVersion,
		ProductID:       version.Model,
		SerialNumber:    version.UUID,
	}
	if info.ProductID == "" {
		info.ProductID = "Cisco Secure Firewall Management Center"
	}
	if info.SerialNumber == "" {
		info.SerialNumber = version.SerialNumber
	}
	d.mu.Lock()
	d.deviceInfo = info
	d.deviceInfoLastRefreshed = time.Now()
	d.mu.Unlock()
	return info, nil
}

func (d *FMCDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	res := fmcResources(d.config)
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

func (d *FMCDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	res := fmcResources(d.config)
	license, licErr := d.client.SmartLicense(ctx)
	devices, devErr := d.client.ManagedDevices(ctx)
	if licErr != nil {
		return nil, licErr
	}
	if devErr != nil {
		return nil, devErr
	}
	message := "Cisco FMC is reachable; app-hosting is not supported on this platform"
	if license != nil && strings.TrimSpace(license.RegStatus) != "" {
		message += "; smart licensing is " + strings.TrimSpace(license.RegStatus)
	}
	if len(devices) > 0 {
		message += fmt.Sprintf("; managing %d device(s)", len(devices))
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

// RunOperationalCommand exposes a small read-only FMC troubleshooting command surface.
// It accepts friendly commands instead of raw shell because FMC operations are API-backed.
func (d *FMCDriver) RunOperationalCommand(ctx context.Context, command string) (string, error) {
	command = strings.ToLower(strings.TrimSpace(command))
	switch command {
	case "", "health", "show health", "show version":
		info, err := d.GetDeviceInfo(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("hostname=%s\nproduct=%s\nversion=%s\nuuid=%s\n", info.Hostname, info.ProductID, info.SoftwareVersion, info.SerialNumber), nil
	case "show license", "license":
		lic, err := d.client.SmartLicense(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("smartLicense=%s\nmetadata=%v\n", lic.RegStatus, lic.Metadata), nil
	case "show devices", "devices":
		devices, err := d.client.ManagedDevices(ctx)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, dev := range devices {
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", dev.Name, dev.HostName, dev.SoftwareVersion, dev.HealthStatus, dev.DeploymentStatus)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unsupported FMC operational command %q", command)
	}
}

func (d *FMCDriver) CollectHealthSnapshot(ctx context.Context) (*HealthSnapshot, error) {
	info, err := d.GetDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	license, err := d.client.SmartLicense(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := d.client.ManagedDevices(ctx)
	if err != nil {
		return nil, err
	}
	return &HealthSnapshot{CapturedAt: metav1.Now(), DeviceInfo: *info, License: *license, ManagedDevices: devices}, nil
}

type applianceResources struct {
	cpuCores  int64
	memoryMB  int64
	storageMB int64
}

func fmcResources(spec *v1alpha1.DeviceSpec) applianceResources {
	res := applianceResources{cpuCores: defaultFMCCPUCores, memoryMB: defaultFMCMemoryMB, storageMB: defaultFMCStorageMB}
	if spec == nil || spec.FMC == nil || spec.FMC.Resources == nil {
		return res
	}
	if spec.FMC.Resources.CPUCores > 0 {
		res.cpuCores = spec.FMC.Resources.CPUCores
	}
	if spec.FMC.Resources.MemoryMB > 0 {
		res.memoryMB = spec.FMC.Resources.MemoryMB
	}
	if spec.FMC.Resources.StorageMB >= 0 {
		res.storageMB = spec.FMC.Resources.StorageMB
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
