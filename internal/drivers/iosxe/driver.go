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

package iosxe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// UnmarshalFunc defines a function signature for unmarshalling data
type UnmarshalFunc func([]byte, any) error

// XEDriver implements the device driver for Cisco IOS-XE AppHosting
type XEDriver struct {
	config       *v1alpha1.DeviceSpec
	client       common.NetworkClient
	marshaller   func(any) ([]byte, error)
	unmarshaller UnmarshalFunc
	deviceInfo   *common.DeviceInfo
}

// NewAppHostingDriver creates a new IOS-XE AppHosting driver instance
func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*XEDriver, error) {
	u := &url.URL{
		Host: fmt.Sprintf("%s:%d", spec.Address, spec.Port),
	}

	if spec.TLS != nil && spec.TLS.Enabled {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}

	if spec.TLS != nil {
		tlsConfig.InsecureSkipVerify = spec.TLS.InsecureSkipVerify

		if spec.TLS.CertFile != "" && spec.TLS.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(spec.TLS.CertFile, spec.TLS.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load client certificate: %v", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		if spec.TLS.CAFile != "" {
			caCert, err := os.ReadFile(spec.TLS.CAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %v", err)
			}
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate")
			}
			tlsConfig.RootCAs = caCertPool
		}
	}

	BaseUrl := u.String()
	Timeout := 30 * time.Second
	Client, err := common.NewNetworkClient(
		BaseUrl,
		&common.ClientAuth{
			Method:   "BasicAuth",
			Username: spec.Username,
			Password: spec.Password,
		},
		tlsConfig,
		Timeout,
	)

	d := &XEDriver{
		config: spec,
		client: Client,
	}

	protocol := "restconf"
	if protocol == "restconf" {
		d.marshaller = d.getRestconfMarshaller()
		d.unmarshaller = d.getRestconfUnmarshaller()
	}

	err = d.CheckConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate device connection: %v", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"url":      BaseUrl,
		"platform": "IOS-XE",
	}).Info("Connected to IOSXE device")

	return d, nil
}

// gethostMetaUnmarshaller returns an unmarshaller for host-meta XML responses
func (d *XEDriver) gethostMetaUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		decoder := xml.NewDecoder(bytes.NewReader(data))
		decoder.Strict = false
		return decoder.Decode(v)
	}
}

// getRestconfMarshaller returns a marshaller for RESTCONF JSON payloads using ygot
func (d *XEDriver) getRestconfMarshaller() func(any) ([]byte, error) {
	return func(v any) ([]byte, error) {
		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return nil, fmt.Errorf("value is not a ygot.GoStruct")
		}
		jsonStr, err := ygot.EmitJSON(gs, &ygot.EmitJSONConfig{
			Format: ygot.RFC7951,
			RFC7951Config: &ygot.RFC7951JSONConfig{
				AppendModuleName: true,
			},
			SkipValidation: true,
		})
		return []byte(jsonStr), err
	}
}

// getRestconfUnmarshaller returns an unmarshaller for RESTCONF JSON responses using ygot
func (d *XEDriver) getRestconfUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse JSON wrapper: %w", err)
		}

		var innerData []byte
		if len(wrapper) == 1 {
			for _, val := range wrapper {
				innerData = val
			}
		} else {
			innerData = data
		}

		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return fmt.Errorf("target is not a ygot.GoStruct")
		}

		return Unmarshal(innerData, gs)
	}
}

// CheckConnection validates connectivity to the device and fetches device info
func (d *XEDriver) CheckConnection(ctx context.Context) error {
	res := &common.HostMeta{}

	err := d.client.Get(ctx, "/.well-known/host-meta", res, d.gethostMetaUnmarshaller())
	if err != nil {
		return fmt.Errorf("connectivity check failed: %w", err)
	}

	log.G(ctx).Debugf("Restconf Root: %s\n", res.Links[0].Href)

	d.deviceInfo = d.fetchDeviceInfo(ctx)
	return nil
}

func (d *XEDriver) fetchDeviceInfo(ctx context.Context) *common.DeviceInfo {
	info := &common.DeviceInfo{}

	resp := &Cisco_IOS_XEDeviceHardwareOper_DeviceHardwareData{}
	err := d.client.Get(ctx, "/restconf/data/Cisco-IOS-XE-device-hardware-oper:device-hardware-data", resp, d.unmarshaller)
	if err != nil {
		log.G(ctx).WithError(err).Debug("Failed to fetch device hardware info")
		return info
	}

	// Get software version from device-system-data and extract just the version number
	if resp.DeviceHardware != nil && resp.DeviceHardware.DeviceSystemData != nil {
		if resp.DeviceHardware.DeviceSystemData.SoftwareVersion != nil {
			info.SoftwareVersion = parseVersionNumber(*resp.DeviceHardware.DeviceSystemData.SoftwareVersion)
		}
	}

	// Find the chassis inventory entry for serial and part number
	if resp.DeviceHardware != nil && resp.DeviceHardware.DeviceInventory != nil {
		for _, inv := range resp.DeviceHardware.DeviceInventory {
			if inv.HwType == Cisco_IOS_XEDeviceHardwareOper_HwType_hw_type_chassis && inv.SerialNumber != nil && *inv.SerialNumber != "" {
				info.SerialNumber = *inv.SerialNumber
				if inv.PartNumber != nil {
					info.ProductID = *inv.PartNumber
				}
				break
			}
		}
	}

	if info.SerialNumber != "" {
		log.G(ctx).Infof("Device info: Serial=%s, Version=%s, Product=%s",
			info.SerialNumber, info.SoftwareVersion, info.ProductID)
	}

	return info
}

// parseVersionNumber extracts the version number (e.g., "17.18.2") from the full software-version string
func parseVersionNumber(fullVersion string) string {
	re := regexp.MustCompile(`Version\s+(\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(fullVersion)
	if len(matches) > 1 {
		return matches[1]
	}
	return fullVersion
}

// GetDeviceInfo returns cached device information
func (d *XEDriver) GetDeviceInfo(ctx context.Context) (*common.DeviceInfo, error) {
	if d.deviceInfo == nil {
		return &common.DeviceInfo{}, nil
	}
	return d.deviceInfo, nil
}

// GetDeviceResources returns the available resources on the device
func (d *XEDriver) GetDeviceResources(ctx context.Context) (*v1.ResourceList, error) {
	resources := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
		v1.ResourcePods:    resource.MustParse("16"),
	}

	return &resources, nil
}

// debugLogJson logs a ygot struct as formatted JSON for debugging
func (d *XEDriver) debugLogJson(ctx context.Context, obj ygot.GoStruct) error {
	jsonStr, err := ygot.EmitJSON(obj, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
		Indent: "  ",
		RFC7951Config: &ygot.RFC7951JSONConfig{
			AppendModuleName: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to serialize ygot object: %w", err)
	}

	log.G(ctx).Debug(jsonStr)
	return nil
}

// GetNodeStats returns resource usage statistics for the device node
func (d *XEDriver) GetNodeStats(ctx context.Context) (*common.NodeResourceStats, error) {
	log.G(ctx).Debug("Fetching node stats from device")
	now := time.Now()

	stats := &common.NodeResourceStats{
		Timestamp: now,
	}

	return stats, nil
}

// GetPodStats returns resource usage statistics for a specific pod/container
func (d *XEDriver) GetPodStats(ctx context.Context, pod *v1.Pod) (*common.PodResourceStats, error) {
	log.G(ctx).WithFields(log.Fields{
		"namespace": pod.Namespace,
		"pod":       pod.Name,
	}).Debug("Fetching pod stats from device")

	now := time.Now()
	containerAppIDs := common.GenerateContainerAppIDs(pod)

	containerStats := make([]common.ContainerResourceStats, 0, len(pod.Spec.Containers))
	var totalCPU, totalMem, totalMemWS uint64

	for _, container := range pod.Spec.Containers {
		appID := containerAppIDs[container.Name]
		cStats, err := d.fetchAppStats(ctx, appID)
		if err != nil {
			log.G(ctx).WithError(err).WithField("app", appID).Warn("Failed to fetch app stats")
			containerStats = append(containerStats, common.ContainerResourceStats{
				Name:      container.Name,
				Timestamp: now,
			})
			continue
		}

		containerStats = append(containerStats, common.ContainerResourceStats{
			Name:                  container.Name,
			Timestamp:             now,
			CPUUsageNanoCores:     cStats.CPUUsageNanoCores,
			MemoryUsageBytes:      cStats.MemoryUsageBytes,
			MemoryWorkingSetBytes: cStats.MemoryUsageBytes,
		})

		totalCPU += cStats.CPUUsageNanoCores
		totalMem += cStats.MemoryUsageBytes
		totalMemWS += cStats.MemoryUsageBytes
	}

	return &common.PodResourceStats{
		Namespace:             pod.Namespace,
		Name:                  pod.Name,
		UID:                   string(pod.UID),
		Timestamp:             now,
		Containers:            containerStats,
		CPUUsageNanoCores:     totalCPU,
		MemoryUsageBytes:      totalMem,
		MemoryWorkingSetBytes: totalMemWS,
	}, nil
}

type appStats struct {
	CPUUsageNanoCores uint64
	MemoryUsageBytes  uint64
}

// fetchAppStats retrieves stats for a specific AppHosting application
func (d *XEDriver) fetchAppStats(ctx context.Context, appID string) (*appStats, error) {
	path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app=%s", url.PathEscape(appID))
	resp := &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{}
	err := d.client.Get(ctx, path, resp, d.unmarshaller)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app stats for %s: %w", appID, err)
	}

	stats := &appStats{}

	if resp.Utilization != nil {
		if resp.Utilization.CpuUtil != nil && resp.Utilization.CpuUtil.ActualApplicationUtil != nil {
			cpuPercent := *resp.Utilization.CpuUtil.ActualApplicationUtil
			stats.CPUUsageNanoCores = cpuPercent * 10000000
		}
		if resp.Utilization.MemoryUtil != nil && resp.Utilization.MemoryUtil.MemoryUsed != nil {
			memStr := *resp.Utilization.MemoryUtil.MemoryUsed
			stats.MemoryUsageBytes = parseMemoryString(memStr)
		}
	}

	return stats, nil
}

// parseMemoryString parses memory strings like "256MB" or "1GB" to bytes
func parseMemoryString(memStr string) uint64 {
	var value uint64
	var unit string
	_, err := fmt.Sscanf(memStr, "%d%s", &value, &unit)
	if err != nil {
		return 0
	}

	switch unit {
	case "KB", "K":
		return value * 1024
	case "MB", "M":
		return value * 1024 * 1024
	case "GB", "G":
		return value * 1024 * 1024 * 1024
	default:
		return value
	}
}
