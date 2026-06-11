// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package aci

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
	defaultAPICPUCores               int64 = 4
	defaultAPICMemoryMB              int64 = 16384
	defaultAPICStorageMB             int64 = 100000
	defaultAPICDeviceInfoCachePeriod       = 30 * time.Second
)

type healthClient interface {
	Check(context.Context) error
	Info(context.Context) (*APICInfo, error)
	FabricNodes(context.Context) ([]FabricNode, error)
	TopSystem(context.Context) (*TopSystem, error)
	Close() error
}

// ACIDriver presents an APIC controller as a CVK health, telemetry, and
// declarative-configuration node.
type ACIDriver struct {
	config *v1alpha1.DeviceSpec
	client healthClient

	mu                      sync.Mutex
	deviceInfo              *common.DeviceInfo
	deviceInfoLastRefreshed time.Time
	lastSnapshot            *HealthSnapshot
}

// HealthSnapshot is a compact troubleshooting bundle collected from APIC REST APIs.
type HealthSnapshot struct {
	CapturedAt metav1.Time
	DeviceInfo common.DeviceInfo
	System     TopSystem
	Nodes      []FabricNode
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*ACIDriver, error) {
	client, err := NewAPIClientFromSpec(spec, spec.Password)
	if err != nil {
		return nil, err
	}
	driver := &ACIDriver{config: spec, client: client}
	if err := driver.CheckConnection(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return driver, nil
}

func (d *ACIDriver) CheckConnection(ctx context.Context) error {
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return fmt.Errorf("aci/apic connectivity check failed: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": info.Hostname,
		"version":  info.SoftwareVersion,
		"product":  info.ProductID,
		"serial":   info.SerialNumber,
	}).Info("APIC controller connected")
	return nil
}

func (d *ACIDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	d.mu.Lock()
	cached := d.deviceInfo
	fresh := cached != nil && time.Since(d.deviceInfoLastRefreshed) < defaultAPICDeviceInfoCachePeriod
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

func (d *ACIDriver) refreshDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d == nil || d.client == nil {
		return nil, fmt.Errorf("aci driver: nil client")
	}
	snapshot, err := d.CollectHealthSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	info := &snapshot.DeviceInfo
	d.mu.Lock()
	d.deviceInfo = copyDeviceInfo(info)
	d.deviceInfoLastRefreshed = time.Now()
	d.lastSnapshot = snapshot
	d.mu.Unlock()
	return info, nil
}

func (d *ACIDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	res := apicResources(d.config)
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

func (d *ACIDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	res := apicResources(d.config)
	snapshot, err := d.CollectHealthSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	message := "Cisco APIC is reachable; app-hosting is not supported on this platform"
	if snapshot.System.Version != "" {
		message += "; version " + snapshot.System.Version
	}
	if len(snapshot.Nodes) > 0 {
		message += fmt.Sprintf("; fabric nodes %d", len(snapshot.Nodes))
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

// RunOperationalCommand exposes a small read-only APIC troubleshooting surface.
func (d *ACIDriver) RunOperationalCommand(ctx context.Context, command string) (string, error) {
	command = strings.ToLower(strings.TrimSpace(command))
	switch command {
	case "", "health", "show health", "show version":
		snapshot, err := d.CollectHealthSnapshot(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("hostname=%s\nproduct=%s\nversion=%s\nserial=%s\nstate=%s\nfabricNodes=%d\n",
			snapshot.DeviceInfo.Hostname,
			snapshot.DeviceInfo.ProductID,
			snapshot.DeviceInfo.SoftwareVersion,
			snapshot.DeviceInfo.SerialNumber,
			snapshot.System.State,
			len(snapshot.Nodes)), nil
	case "show nodes", "nodes", "show fabric":
		nodes, err := d.client.FabricNodes(ctx)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, node := range nodes {
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", node.ID, node.Name, node.Role, node.Version, node.FabricSt)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unsupported APIC operational command %q", command)
	}
}

func (d *ACIDriver) CollectHealthSnapshot(ctx context.Context) (*HealthSnapshot, error) {
	info, err := d.client.Info(ctx)
	if err != nil {
		return nil, err
	}
	product := "Cisco APIC"
	if info.System.Role != "" {
		product += " " + info.System.Role
	}
	device := common.DeviceInfo{
		Hostname:        firstNonEmpty(info.System.Name, "apic"),
		SoftwareVersion: firstNonEmpty(info.System.Version, info.Login.Version),
		ProductID:       product,
		SerialNumber:    firstNonEmpty(info.System.Serial, info.System.DN),
	}
	return &HealthSnapshot{CapturedAt: metav1.Now(), DeviceInfo: device, System: info.System, Nodes: info.Nodes}, nil
}

type applianceResources struct {
	cpuCores  int64
	memoryMB  int64
	storageMB int64
}

func apicResources(spec *v1alpha1.DeviceSpec) applianceResources {
	res := applianceResources{cpuCores: defaultAPICPUCores, memoryMB: defaultAPICMemoryMB, storageMB: defaultAPICStorageMB}
	if spec == nil || spec.APIC == nil || spec.APIC.Resources == nil {
		return res
	}
	if spec.APIC.Resources.CPUCores > 0 {
		res.cpuCores = spec.APIC.Resources.CPUCores
	}
	if spec.APIC.Resources.MemoryMB > 0 {
		res.memoryMB = spec.APIC.Resources.MemoryMB
	}
	if spec.APIC.Resources.StorageMB >= 0 {
		res.storageMB = spec.APIC.Resources.StorageMB
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
