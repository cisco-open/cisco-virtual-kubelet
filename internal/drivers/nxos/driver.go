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

package nxos

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// NXOSDriver drives NX-OS app-hosting through NX-API CLI.
type NXOSDriver struct {
	config     *v1alpha1.DeviceSpec
	client     *nxapiClient
	deviceInfo *common.DeviceInfo
	mu         sync.Mutex
}

func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*NXOSDriver, error) {
	client, err := newNXAPIClient(spec)
	if err != nil {
		return nil, err
	}
	d := &NXOSDriver{
		config: spec,
		client: client,
	}
	if err := d.CheckConnection(ctx); err != nil {
		return nil, err
	}
	if spec.AllowUnsignedApps {
		if err := d.ConfigureSignVerification(ctx, false); err != nil {
			log.G(ctx).WithError(err).Warn("allowUnsignedApps=true but failed to disable NX-OS app-hosting sign-verification; unsigned installs may be blocked")
		}
	}
	return d, nil
}

func (d *NXOSDriver) CheckConnection(ctx context.Context) error {
	out, err := d.client.show(ctx, "show version")
	if err != nil {
		return fmt.Errorf("nxos connectivity check failed: %w", err)
	}
	d.deviceInfo = parseDeviceInfo(out)
	if d.deviceInfo.Hostname == "" {
		if host, hostErr := d.client.show(ctx, "show hostname"); hostErr == nil {
			d.deviceInfo.Hostname = strings.TrimSpace(host)
		}
	}
	if inv, invErr := d.client.show(ctx, "show inventory"); invErr == nil {
		enrichInventory(d.deviceInfo, inv)
	}
	log.G(ctx).WithFields(log.Fields{
		"hostname": d.deviceInfo.Hostname,
		"version":  d.deviceInfo.SoftwareVersion,
		"product":  d.deviceInfo.ProductID,
		"serial":   d.deviceInfo.SerialNumber,
	}).Info("NX-OS device connected")
	return nil
}

func (d *NXOSDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d.deviceInfo == nil {
		if err := d.CheckConnection(ctx); err != nil {
			return nil, err
		}
	}
	return d.deviceInfo, nil
}

func (d *NXOSDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	pods := d.config.MaxPods
	if pods <= 0 {
		pods = 16
	}
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("4"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("1500Mi"),
		v1.ResourcePods:    *resource.NewQuantity(int64(pods), resource.DecimalSI),
	}
	return &resources, nil
}

func (d *NXOSDriver) GetGlobalOperationalData(ctx context.Context) (*common.AppHostingOperData, error) {
	out, err := d.client.show(ctx, "show feature | include app-hosting")
	if err != nil {
		return nil, err
	}
	enabled := strings.Contains(strings.ToLower(out), "enabled")
	return &common.AppHostingOperData{
		IoxEnabled: enabled,
		SystemCPU:  common.AppResource{Quota: 4, Available: 4, Unit: "cores"},
		Memory:     common.AppResource{Quota: 16384, Available: 16384, Unit: "MB"},
		Storage:    common.AppResource{Quota: 1500, Available: 1500, Unit: "MB"},
	}, nil
}

func (d *NXOSDriver) ConfigureSignVerification(ctx context.Context, enabled bool) error {
	mode := "disable"
	if enabled {
		mode = "enable"
	}
	if _, err := d.client.conf(ctx, "configure terminal", fmt.Sprintf("app-hosting signed-verification %s", mode)); err != nil {
		return fmt.Errorf("nxos configure app-hosting signed-verification %s: %w", mode, err)
	}
	log.G(ctx).Infof("NX-OS app-hosting signed-verification set to %s", mode)
	return nil
}

func parseDeviceInfo(out string) *common.DeviceInfo {
	info := &common.DeviceInfo{}
	for _, line := range strings.Split(out, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "device name") || strings.HasPrefix(lower, "hostname") {
			if v := afterColon(line); v != "" {
				info.Hostname = v
			}
		}
		if strings.Contains(lower, "nxos: version") || strings.Contains(lower, "nx-os") && strings.Contains(lower, "version") {
			if m := regexp.MustCompile(`(?i)(?:nxos:|nx-os)?\s*version\s+([^\s,]+)`).FindStringSubmatch(line); len(m) > 1 {
				info.SoftwareVersion = m[1]
			}
		}
		if strings.Contains(lower, "system version") {
			if v := afterColon(line); v != "" {
				info.SoftwareVersion = strings.TrimPrefix(v, "version ")
			}
		}
	}
	return info
}

func enrichInventory(info *common.DeviceInfo, out string) {
	pidRe := regexp.MustCompile(`(?i)\bPID:\s*([^,\s]+)`)
	serialRe := regexp.MustCompile(`(?i)\bSN:\s*([^,\s]+)`)
	if m := pidRe.FindStringSubmatch(out); len(m) > 1 && info.ProductID == "" {
		info.ProductID = strings.TrimSpace(m[1])
	}
	if m := serialRe.FindStringSubmatch(out); len(m) > 1 && info.SerialNumber == "" {
		info.SerialNumber = strings.TrimSpace(m[1])
	}
}

func afterColon(line string) string {
	idx := strings.Index(line, ":")
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(line[idx+1:])
}

func nxosGuestInterface(spec *v1alpha1.DeviceSpec) uint8 {
	if spec == nil || spec.NXOS == nil || spec.NXOS.Networking == nil ||
		spec.NXOS.Networking.Interface == nil ||
		spec.NXOS.Networking.Interface.Management == nil {
		return 0
	}
	return spec.NXOS.Networking.Interface.Management.GuestInterface
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
