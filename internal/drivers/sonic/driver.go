// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

import (
	"context"
	"fmt"
	"regexp"
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
	defaultSONICCPUCores              int64 = 4
	defaultSONICMemoryMB              int64 = 20480
	defaultSONICStorageMB             int64 = 0
	defaultSONICDeviceInfoCachePeriod       = 30 * time.Second
)

// SONICDriver presents a SONiC switch as a CVK health, telemetry, operations,
// and OpenConfig configuration node.
type SONICDriver struct {
	config *v1alpha1.DeviceSpec
	gnmi   gnmiClient
	ssh    commandClient

	mu                      sync.Mutex
	deviceInfo              *common.DeviceInfo
	deviceInfoLastRefreshed time.Time
	gnmiVersion             string
	modelCount              int
}

// HealthSnapshot is a compact troubleshooting bundle collected from SONiC.
type HealthSnapshot struct {
	CapturedAt  metav1.Time
	DeviceInfo  common.DeviceInfo
	GNMIVersion string
	Models      []string
	Interfaces  []common.InterfaceStats
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*SONICDriver, error) {
	client, err := newGNMIClientFromSpec(spec, spec.Password)
	if err != nil {
		return nil, err
	}
	sshClient, sshErr := newSSHClient(spec, spec.Password)
	if sshErr != nil {
		log.G(ctx).WithError(sshErr).Debug("SONiC SSH client unavailable; continuing with gNMI-only health")
	}
	driver := &SONICDriver{config: spec, gnmi: client, ssh: sshClient}
	if err := driver.CheckConnection(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return driver, nil
}

func (d *SONICDriver) CheckConnection(ctx context.Context) error {
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return fmt.Errorf("sonic connectivity check failed: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": info.Hostname,
		"version":  info.SoftwareVersion,
		"product":  info.ProductID,
		"models":   d.modelCount,
	}).Info("SONiC device connected")
	return nil
}

func (d *SONICDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	d.mu.Lock()
	cached := d.deviceInfo
	fresh := cached != nil && time.Since(d.deviceInfoLastRefreshed) < defaultSONICDeviceInfoCachePeriod
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

func (d *SONICDriver) refreshDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d == nil || d.gnmi == nil {
		return nil, fmt.Errorf("sonic driver: nil gNMI client")
	}
	caps, err := d.gnmi.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(caps.GetSupportedModels()))
	hasInterfaces := false
	for _, model := range caps.GetSupportedModels() {
		name := strings.TrimSpace(model.GetName())
		if name == "" {
			continue
		}
		models = append(models, name)
		if name == "openconfig-interfaces" {
			hasInterfaces = true
		}
	}
	if !hasInterfaces {
		return nil, fmt.Errorf("sonic gNMI target does not advertise openconfig-interfaces")
	}
	if _, err := d.gnmi.GetJSON(ctx, "/interfaces"); err != nil {
		return nil, fmt.Errorf("sonic gNMI /interfaces health probe: %w", err)
	}
	info := &common.DeviceInfo{Hostname: "sonic", SoftwareVersion: caps.GetGNMIVersion(), ProductID: "Cisco SONiC"}
	if d.config != nil && d.config.Address != "" {
		info.RouterID = d.config.Address
	}
	if d.ssh != nil {
		if out, err := d.ssh.Run(ctx, "hostname"); err == nil && strings.TrimSpace(out) != "" {
			info.Hostname = strings.TrimSpace(out)
		}
		if out, err := d.ssh.Run(ctx, "show version"); err == nil {
			mergeSONICVersionInfo(info, out)
		}
	}
	d.mu.Lock()
	d.deviceInfo = info
	d.deviceInfoLastRefreshed = time.Now()
	d.gnmiVersion = caps.GetGNMIVersion()
	d.modelCount = len(models)
	d.mu.Unlock()
	return info, nil
}

func (d *SONICDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	res := sonicResources(d.config)
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

func (d *SONICDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	res := sonicResources(d.config)
	caps, err := d.gnmi.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	return &common.AppHostingOperData{
		IoxEnabled:            false,
		AppHostingUnsupported: true,
		AppHostingMessage:     fmt.Sprintf("Cisco SONiC is reachable over gNMI/OpenConfig; app-hosting is not supported on this platform; gnmi=%s models=%d", caps.GetGNMIVersion(), len(caps.GetSupportedModels())),
		SystemCPU:             common.AppResource{Quota: res.cpuCores, Available: res.cpuCores, Unit: "cores"},
		Memory:                common.AppResource{Quota: res.memoryMB, Available: res.memoryMB, Unit: "MB"},
		Storage:               common.AppResource{Quota: res.storageMB, Available: res.storageMB, Unit: "MB"},
	}, nil
}

// RunOperationalCommand executes a read-only SONiC troubleshooting command.
func (d *SONICDriver) RunOperationalCommand(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("sonic operational command cannot be empty")
	}
	if !isReadOnlySONICCommand(command) {
		return "", fmt.Errorf("unsupported SONiC operational command %q", command)
	}
	if d.ssh == nil {
		return "", fmt.Errorf("sonic SSH channel is not configured")
	}
	return d.ssh.Run(ctx, command)
}

func (d *SONICDriver) CollectHealthSnapshot(ctx context.Context) (*HealthSnapshot, error) {
	info, err := d.GetDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	caps, err := d.gnmi.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(caps.GetSupportedModels()))
	for _, model := range caps.GetSupportedModels() {
		if model.GetName() != "" {
			models = append(models, model.GetName())
		}
	}
	interfaces, err := d.GetInterfaceStats(ctx)
	if err != nil {
		return nil, err
	}
	return &HealthSnapshot{CapturedAt: metav1.Now(), DeviceInfo: *info, GNMIVersion: caps.GetGNMIVersion(), Models: models, Interfaces: interfaces}, nil
}

type sonicResourceShape struct {
	cpuCores  int64
	memoryMB  int64
	storageMB int64
}

func sonicResources(spec *v1alpha1.DeviceSpec) sonicResourceShape {
	res := sonicResourceShape{cpuCores: defaultSONICCPUCores, memoryMB: defaultSONICMemoryMB, storageMB: defaultSONICStorageMB}
	if spec == nil || spec.SONIC == nil || spec.SONIC.Resources == nil {
		return res
	}
	if spec.SONIC.Resources.CPUCores > 0 {
		res.cpuCores = spec.SONIC.Resources.CPUCores
	}
	if spec.SONIC.Resources.MemoryMB > 0 {
		res.memoryMB = spec.SONIC.Resources.MemoryMB
	}
	if spec.SONIC.Resources.StorageMB >= 0 {
		res.storageMB = spec.SONIC.Resources.StorageMB
	}
	return res
}

func copyDeviceInfo(info *common.DeviceInfo) *common.DeviceInfo {
	if info == nil {
		return nil
	}
	copy := *info
	return &copy
}

var sonicVersionLineRe = regexp.MustCompile(`^([^:]+):\s*(.*)$`)

func mergeSONICVersionInfo(info *common.DeviceInfo, out string) {
	if info == nil {
		return
	}
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		m := sonicVersionLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) != 3 {
			continue
		}
		values[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(m[2])
	}
	if v := firstNonEmpty(values["sonic software version"], values["sonic os version"]); v != "" {
		info.SoftwareVersion = v
	}
	if v := firstNonEmpty(values["model number"], values["hwsku"], values["platform"]); v != "" {
		info.ProductID = v
	}
	if v := values["serial number"]; v != "" {
		info.SerialNumber = v
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isReadOnlySONICCommand(command string) bool {
	command = strings.TrimSpace(command)
	allowed := []string{
		"show ",
		"hostname",
		"ip ",
		"docker ps",
		"systemctl status ",
		"systemctl is-active ",
		"cat /etc/sonic/",
	}
	for _, prefix := range allowed {
		if command == strings.TrimSpace(prefix) || strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}
