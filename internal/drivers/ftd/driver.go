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
	defaultFTDCPUCores              int64 = 4
	defaultFTDMemoryMB              int64 = 8192
	defaultFTDStorageMB             int64 = 0
	defaultFTDDeviceInfoCachePeriod       = 30 * time.Second
)

type commandClient interface {
	Run(ctx context.Context, command string) (string, error)
}

// FTDDriver presents an FTD appliance as a CVK health and telemetry node.
type FTDDriver struct {
	config *v1alpha1.DeviceSpec
	client commandClient

	mu                      sync.Mutex
	deviceInfo              *common.DeviceInfo
	deviceInfoLastRefreshed time.Time
}

// HealthSnapshot is a compact troubleshooting bundle collected from FTD CLI.
type HealthSnapshot struct {
	CapturedAt metav1.Time
	DeviceInfo common.DeviceInfo
	Network    ftdNetworkInfo
	Managers   string
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*FTDDriver, error) {
	client, err := newSSHClient(spec)
	if err != nil {
		return nil, err
	}
	driver := &FTDDriver{
		config: spec,
		client: client,
	}
	if err := driver.CheckConnection(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

func (d *FTDDriver) CheckConnection(ctx context.Context) error {
	info, err := d.refreshDeviceInfo(ctx)
	if err != nil {
		return fmt.Errorf("ftd connectivity check failed: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": info.Hostname,
		"version":  info.SoftwareVersion,
		"product":  info.ProductID,
		"uuid":     info.SerialNumber,
	}).Info("FTD device connected")
	return nil
}

func (d *FTDDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	d.mu.Lock()
	cached := d.deviceInfo
	fresh := cached != nil && time.Since(d.deviceInfoLastRefreshed) < defaultFTDDeviceInfoCachePeriod
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

func (d *FTDDriver) refreshDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	out, err := d.client.Run(ctx, "show version")
	if err != nil {
		return nil, err
	}
	info := parseShowVersion(out)
	if info.Hostname == "" {
		if network, networkErr := d.fetchNetworkInfo(ctx); networkErr == nil && network.Hostname != "" {
			info.Hostname = network.Hostname
		}
	}
	d.mu.Lock()
	d.deviceInfo = info
	d.deviceInfoLastRefreshed = time.Now()
	d.mu.Unlock()
	return info, nil
}

func (d *FTDDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	res := ftdResources(d.config)
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

func (d *FTDDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	res := ftdResources(d.config)
	if _, err := d.client.Run(ctx, "show managers"); err != nil {
		return nil, err
	}
	return &common.AppHostingOperData{
		IoxEnabled:            false,
		AppHostingUnsupported: true,
		AppHostingMessage:     "Cisco FTD is reachable; app-hosting is not supported on this platform",
		SystemCPU:             common.AppResource{Quota: res.cpuCores, Available: res.cpuCores, Unit: "cores"},
		Memory:                common.AppResource{Quota: res.memoryMB, Available: res.memoryMB, Unit: "MB"},
		Storage:               common.AppResource{Quota: res.storageMB, Available: res.storageMB, Unit: "MB"},
		Notifications:         nil,
	}, nil
}

// RunOperationalCommand executes an FTD CLI command over the management
// channel. It is intentionally exposed for future operations and diagnostics
// integration while the CVK app-hosting contract remains workload-free.
func (d *FTDDriver) RunOperationalCommand(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("ftd operational command cannot be empty")
	}
	return d.client.Run(ctx, command)
}

func (d *FTDDriver) CollectHealthSnapshot(ctx context.Context) (*HealthSnapshot, error) {
	info, err := d.GetDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	network, err := d.fetchNetworkInfo(ctx)
	if err != nil {
		return nil, err
	}
	managers, err := d.client.Run(ctx, "show managers")
	if err != nil {
		return nil, err
	}
	return &HealthSnapshot{
		CapturedAt: metav1.Now(),
		DeviceInfo: *info,
		Network:    network,
		Managers:   managers,
	}, nil
}

func (d *FTDDriver) fetchNetworkInfo(ctx context.Context) (ftdNetworkInfo, error) {
	out, err := d.client.Run(ctx, "show network")
	if err != nil {
		return ftdNetworkInfo{}, err
	}
	return parseShowNetwork(out), nil
}

type applianceResources struct {
	cpuCores  int64
	memoryMB  int64
	storageMB int64
}

func ftdResources(spec *v1alpha1.DeviceSpec) applianceResources {
	res := applianceResources{
		cpuCores:  defaultFTDCPUCores,
		memoryMB:  defaultFTDMemoryMB,
		storageMB: defaultFTDStorageMB,
	}
	if spec == nil || spec.FTD == nil || spec.FTD.Resources == nil {
		return res
	}
	if spec.FTD.Resources.CPUCores > 0 {
		res.cpuCores = spec.FTD.Resources.CPUCores
	}
	if spec.FTD.Resources.MemoryMB > 0 {
		res.memoryMB = spec.FTD.Resources.MemoryMB
	}
	if spec.FTD.Resources.StorageMB >= 0 {
		res.storageMB = spec.FTD.Resources.StorageMB
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
