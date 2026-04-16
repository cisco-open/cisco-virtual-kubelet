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

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	statsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

// processStartTime is captured at init so buildStatsSummary can report
// an accurate node start time instead of a hardcoded approximation.
var processStartTime = time.Now()

// buildStatsSummary fetches device operational data and interface stats,
// then maps them to the Kubernetes stats/summary API format.
func (p *AppHostingProvider) buildStatsSummary(ctx context.Context) (*statsv1alpha1.Summary, error) {
	now := metav1.Now()

	// Fetch global operational data for CPU/memory/storage
	operData, err := p.driver.GetGlobalOperationalData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get operational data for stats summary: %w", err)
	}

	// Build node-level stats
	nodeStats := statsv1alpha1.NodeStats{
		NodeName:  p.nodeProvider.nodeName,
		StartTime: metav1.NewTime(processStartTime),
	}

	// CPU stats
	if operData != nil && operData.SystemCPU.Quota > 0 {
		usedCPU := operData.SystemCPU.Quota - operData.SystemCPU.Available
		usedNanoCores := uint64(usedCPU) * 1e9 / 100 // Convert percentage to nanocores approximation
		nodeStats.CPU = &statsv1alpha1.CPUStats{
			Time:           now,
			UsageNanoCores: &usedNanoCores,
		}
	}

	// Memory stats
	if operData != nil && operData.Memory.Quota > 0 {
		totalBytes := uint64(operData.Memory.Quota) * 1024 * 1024
		availableBytes := uint64(operData.Memory.Available) * 1024 * 1024
		usedBytes := totalBytes - availableBytes
		nodeStats.Memory = &statsv1alpha1.MemoryStats{
			Time:           now,
			UsageBytes:     &usedBytes,
			AvailableBytes: &availableBytes,
		}
	}

	// Filesystem (storage) stats
	if operData != nil && operData.Storage.Quota > 0 {
		totalBytes := uint64(operData.Storage.Quota) * 1024 * 1024
		availableBytes := uint64(operData.Storage.Available) * 1024 * 1024
		usedBytes := totalBytes - availableBytes
		nodeStats.Fs = &statsv1alpha1.FsStats{
			Time:           now,
			CapacityBytes:  &totalBytes,
			AvailableBytes: &availableBytes,
			UsedBytes:      &usedBytes,
		}
	}

	// Network stats from interface data (only if driver supports topology)
	if topo, ok := p.driver.(drivers.TopologyProvider); ok {
		ifStats, ifErr := topo.GetInterfaceStats(ctx)
		if ifErr != nil {
			log.G(ctx).WithError(ifErr).Debug("Failed to get interface stats for stats summary")
		} else if len(ifStats) > 0 {
			var interfaces []statsv1alpha1.InterfaceStats
			for _, intf := range ifStats {
				is := statsv1alpha1.InterfaceStats{
					Name:    intf.Name,
					RxBytes: &intf.InOctets,
					TxBytes: &intf.OutOctets,
				}
				interfaces = append(interfaces, is)
			}
			nodeStats.Network = &statsv1alpha1.NetworkStats{
				Time:       now,
				Interfaces: interfaces,
			}
			// Set the default interface to the first one
			if len(interfaces) > 0 {
				nodeStats.Network.InterfaceStats = interfaces[0]
			}
		}
	}

	return &statsv1alpha1.Summary{
		Node: nodeStats,
	}, nil
}

// buildMetricsResource fetches device data and returns Prometheus MetricFamily entries
// for device-level and topology metrics.
func (p *AppHostingProvider) buildMetricsResource(ctx context.Context) ([]*io_prometheus_client.MetricFamily, error) {
	var families []*io_prometheus_client.MetricFamily

	// Fetch operational data
	operData, err := p.driver.GetGlobalOperationalData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get operational data for metrics: %w", err)
	}

	gaugeType := io_prometheus_client.MetricType_GAUGE

	// CPU usage percentage
	if operData != nil && operData.SystemCPU.Quota > 0 {
		usedPct := float64(operData.SystemCPU.Quota-operData.SystemCPU.Available) / float64(operData.SystemCPU.Quota) * 100
		families = append(families, &io_prometheus_client.MetricFamily{
			Name: proto.String("cisco_device_cpu_usage_percent"),
			Help: proto.String("CPU usage percentage of the Cisco device IOx subsystem"),
			Type: &gaugeType,
			Metric: []*io_prometheus_client.Metric{
				{Gauge: &io_prometheus_client.Gauge{Value: &usedPct}},
			},
		})
	}

	// Memory
	if operData != nil && operData.Memory.Quota > 0 {
		usedBytes := float64((operData.Memory.Quota - operData.Memory.Available) * 1024 * 1024)
		totalBytes := float64(operData.Memory.Quota * 1024 * 1024)
		families = append(families,
			&io_prometheus_client.MetricFamily{
				Name: proto.String("cisco_device_memory_used_bytes"),
				Help: proto.String("Memory used in bytes on the Cisco device IOx subsystem"),
				Type: &gaugeType,
				Metric: []*io_prometheus_client.Metric{
					{Gauge: &io_prometheus_client.Gauge{Value: &usedBytes}},
				},
			},
			&io_prometheus_client.MetricFamily{
				Name: proto.String("cisco_device_memory_total_bytes"),
				Help: proto.String("Total memory in bytes on the Cisco device IOx subsystem"),
				Type: &gaugeType,
				Metric: []*io_prometheus_client.Metric{
					{Gauge: &io_prometheus_client.Gauge{Value: &totalBytes}},
				},
			},
		)
	}

	// Storage
	if operData != nil && operData.Storage.Quota > 0 {
		usedBytes := float64((operData.Storage.Quota - operData.Storage.Available) * 1024 * 1024)
		totalBytes := float64(operData.Storage.Quota * 1024 * 1024)
		families = append(families,
			&io_prometheus_client.MetricFamily{
				Name: proto.String("cisco_device_storage_used_bytes"),
				Help: proto.String("Storage used in bytes on the Cisco device IOx subsystem"),
				Type: &gaugeType,
				Metric: []*io_prometheus_client.Metric{
					{Gauge: &io_prometheus_client.Gauge{Value: &usedBytes}},
				},
			},
			&io_prometheus_client.MetricFamily{
				Name: proto.String("cisco_device_storage_total_bytes"),
				Help: proto.String("Total storage in bytes on the Cisco device IOx subsystem"),
				Type: &gaugeType,
				Metric: []*io_prometheus_client.Metric{
					{Gauge: &io_prometheus_client.Gauge{Value: &totalBytes}},
				},
			},
		)
	}

	// Topology metrics (only if driver supports TopologyProvider)
	topo, ok := p.driver.(drivers.TopologyProvider)
	if !ok {
		return families, nil
	}

	// Interface metrics
	ifStats, ifErr := topo.GetInterfaceStats(ctx)
	if ifErr != nil {
		log.G(ctx).WithError(ifErr).Debug("Failed to get interface stats for metrics")
	} else if len(ifStats) > 0 {
		var rxMetrics []*io_prometheus_client.Metric
		var txMetrics []*io_prometheus_client.Metric
		var stateMetrics []*io_prometheus_client.Metric

		for _, intf := range ifStats {
			intfName := intf.Name
			intfLabel := &io_prometheus_client.LabelPair{
				Name:  proto.String("interface"),
				Value: proto.String(intfName),
			}

			rxBps := float64(intf.InBitsPerSec)
			rxMetrics = append(rxMetrics, &io_prometheus_client.Metric{
				Label: []*io_prometheus_client.LabelPair{intfLabel},
				Gauge: &io_prometheus_client.Gauge{Value: &rxBps},
			})

			txBps := float64(intf.OutBitsPerSec)
			txMetrics = append(txMetrics, &io_prometheus_client.Metric{
				Label: []*io_prometheus_client.LabelPair{intfLabel},
				Gauge: &io_prometheus_client.Gauge{Value: &txBps},
			})

			stateVal := float64(0)
			if intf.OperStatus == "up" {
				stateVal = 1
			}
			stateLabel := &io_prometheus_client.LabelPair{
				Name:  proto.String("state"),
				Value: proto.String(intf.OperStatus),
			}
			stateMetrics = append(stateMetrics, &io_prometheus_client.Metric{
				Label: []*io_prometheus_client.LabelPair{intfLabel, stateLabel},
				Gauge: &io_prometheus_client.Gauge{Value: &stateVal},
			})
		}

		families = append(families,
			&io_prometheus_client.MetricFamily{
				Name:   proto.String("cisco_device_interface_rx_bits_per_sec"),
				Help:   proto.String("Interface receive rate in bits per second"),
				Type:   &gaugeType,
				Metric: rxMetrics,
			},
			&io_prometheus_client.MetricFamily{
				Name:   proto.String("cisco_device_interface_tx_bits_per_sec"),
				Help:   proto.String("Interface transmit rate in bits per second"),
				Type:   &gaugeType,
				Metric: txMetrics,
			},
			&io_prometheus_client.MetricFamily{
				Name:   proto.String("cisco_device_interface_state"),
				Help:   proto.String("Interface operational state (1=up, 0=down)"),
				Type:   &gaugeType,
				Metric: stateMetrics,
			},
		)
	}

	// Topology neighbor counts
	cdpNeighbors, cdpErr := topo.GetCDPNeighbors(ctx)
	if cdpErr == nil {
		cdpCount := float64(len(cdpNeighbors))
		families = append(families, &io_prometheus_client.MetricFamily{
			Name: proto.String("cisco_device_cdp_neighbor_count"),
			Help: proto.String("Number of CDP neighbors discovered"),
			Type: &gaugeType,
			Metric: []*io_prometheus_client.Metric{
				{Gauge: &io_prometheus_client.Gauge{Value: &cdpCount}},
			},
		})

		// Per-neighbor link metrics
		var linkMetrics []*io_prometheus_client.Metric
		for _, n := range cdpNeighbors {
			linkVal := float64(1)
			linkMetrics = append(linkMetrics, &io_prometheus_client.Metric{
				Label: []*io_prometheus_client.LabelPair{
					{Name: proto.String("target"), Value: proto.String(n.DeviceID)},
					{Name: proto.String("interface"), Value: proto.String(n.LocalInterface)},
					{Name: proto.String("protocol"), Value: proto.String("cdp")},
					{Name: proto.String("platform"), Value: proto.String(n.Platform)},
				},
				Gauge: &io_prometheus_client.Gauge{Value: &linkVal},
			})
		}
		if len(linkMetrics) > 0 {
			families = append(families, &io_prometheus_client.MetricFamily{
				Name:   proto.String("cisco_device_neighbor_link"),
				Help:   proto.String("Neighbor link state (1=discovered)"),
				Type:   &gaugeType,
				Metric: linkMetrics,
			})
		}
	}

	ospfNeighbors, ospfErr := topo.GetOSPFNeighbors(ctx)
	if ospfErr == nil {
		ospfCount := float64(len(ospfNeighbors))
		families = append(families, &io_prometheus_client.MetricFamily{
			Name: proto.String("cisco_device_ospf_neighbor_count"),
			Help: proto.String("Number of OSPF neighbors discovered"),
			Type: &gaugeType,
			Metric: []*io_prometheus_client.Metric{
				{Gauge: &io_prometheus_client.Gauge{Value: &ospfCount}},
			},
		})
	}

	return families, nil
}
