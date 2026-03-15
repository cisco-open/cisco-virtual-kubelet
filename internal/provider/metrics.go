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
	"strconv"
	"strings"
	"time"

	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	statsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

// GetStatsSummary returns node- and pod-level resource usage stats by querying
// the Cisco device's operational data via RESTCONF. This is the primary endpoint
// that observability collectors (Splunk OTEL, Prometheus, metrics-server) scrape
// for CPU, memory, and filesystem usage.
func (p *AppHostingProvider) GetStatsSummary(ctx context.Context) (*statsv1alpha1.Summary, error) {
	now := metav1.Now()

	// Fetch global operational data for node-level stats
	operData, err := p.driver.GetGlobalOperationalData(p.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device operational data: %w", err)
	}

	// --- Node Stats ---
	nodeStats := statsv1alpha1.NodeStats{
		NodeName:  p.nodeProvider.nodeName,
		StartTime: metav1.NewTime(time.Now()), // Approximation; VK doesn't track actual boot time
	}

	// CPU: IOS-XE reports CPU as percentage quota and available.
	// Convert to nanoseconds usage: (quota - available) / quota gives utilisation fraction,
	// multiplied by 1e9 (nanoseconds per core-second) and the number of "cores" (quota percentage / 100).
	if operData.SystemCPU.Quota > 0 {
		usedPercent := operData.SystemCPU.Quota - operData.SystemCPU.Available
		if usedPercent < 0 {
			usedPercent = 0
		}
		// UsageNanoCores: used percentage * 1e7 (since 100% = 1 core = 1e9 nanocores)
		usageNanoCores := uint64(usedPercent) * 10_000_000
		nodeStats.CPU = &statsv1alpha1.CPUStats{
			Time:           now,
			UsageNanoCores: &usageNanoCores,
		}
	}

	// Memory: quota and available are in MB from the device
	if operData.Memory.Quota > 0 {
		usedMB := operData.Memory.Quota - operData.Memory.Available
		if usedMB < 0 {
			usedMB = 0
		}
		usageBytes := uint64(usedMB) * 1024 * 1024
		availableBytes := uint64(operData.Memory.Available) * 1024 * 1024
		workingSetBytes := usageBytes // Best approximation from device data
		nodeStats.Memory = &statsv1alpha1.MemoryStats{
			Time:            now,
			UsageBytes:      &usageBytes,
			AvailableBytes:  &availableBytes,
			WorkingSetBytes: &workingSetBytes,
		}
	}

	// Filesystem: storage quota and available are in MB from the device
	if operData.Storage.Quota > 0 {
		capacityBytes := uint64(operData.Storage.Quota) * 1024 * 1024
		availableBytes := uint64(operData.Storage.Available) * 1024 * 1024
		usedBytes := capacityBytes - availableBytes
		nodeStats.Fs = &statsv1alpha1.FsStats{
			Time:           now,
			CapacityBytes:  &capacityBytes,
			AvailableBytes: &availableBytes,
			UsedBytes:      &usedBytes,
		}
	}

	// --- Pod Stats ---
	var podStats []statsv1alpha1.PodStats

	pods, err := p.driver.ListPods(p.ctx)
	if err != nil {
		log.G(ctx).WithError(err).Warn("Failed to list pods for stats summary")
	} else {
		for _, pod := range pods {
			ps := statsv1alpha1.PodStats{
				PodRef: statsv1alpha1.PodReference{
					Name:      pod.Name,
					Namespace: pod.Namespace,
					UID:       string(pod.UID),
				},
				StartTime: metav1.NewTime(time.Now()),
			}

			if pod.Status.StartTime != nil {
				ps.StartTime = *pod.Status.StartTime
			}

			// Get per-pod status which includes per-app operational data
			statusPod, statusErr := p.driver.GetPodStatus(p.ctx, pod)
			if statusErr != nil {
				log.G(ctx).WithError(statusErr).Debugf("Failed to get pod status for stats: %s/%s", pod.Namespace, pod.Name)
				podStats = append(podStats, ps)
				continue
			}

			// Build container stats from status information
			for _, cs := range statusPod.Status.ContainerStatuses {
				containerStats := statsv1alpha1.ContainerStats{
					Name:      cs.Name,
					StartTime: metav1.NewTime(time.Now()),
				}
				if cs.State.Running != nil {
					containerStats.StartTime = cs.State.Running.StartedAt
				}
				ps.Containers = append(ps.Containers, containerStats)
			}

			podStats = append(podStats, ps)
		}
	}

	return &statsv1alpha1.Summary{
		Node: nodeStats,
		Pods: podStats,
	}, nil
}

// GetMetricsResource returns node-level resource metrics in Prometheus exposition format.
// This supplements GetStatsSummary for collectors that prefer the /metrics/resource endpoint.
func (p *AppHostingProvider) GetMetricsResource(ctx context.Context) ([]*io_prometheus_client.MetricFamily, error) {
	operData, err := p.driver.GetGlobalOperationalData(p.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device operational data: %w", err)
	}

	var families []*io_prometheus_client.MetricFamily

	nodeName := p.nodeProvider.nodeName

	// CPU usage metric
	if operData.SystemCPU.Quota > 0 {
		usedPercent := operData.SystemCPU.Quota - operData.SystemCPU.Available
		if usedPercent < 0 {
			usedPercent = 0
		}
		usageNanoCores := float64(usedPercent) * 10_000_000
		families = append(families, newGaugeMetricFamily(
			"node_cpu_usage_nanocores",
			"CPU usage in nanocores",
			nodeName,
			usageNanoCores,
		))
	}

	// Memory usage metrics
	if operData.Memory.Quota > 0 {
		usedMB := operData.Memory.Quota - operData.Memory.Available
		if usedMB < 0 {
			usedMB = 0
		}
		usageBytes := float64(usedMB) * 1024 * 1024
		availableBytes := float64(operData.Memory.Available) * 1024 * 1024

		families = append(families, newGaugeMetricFamily(
			"node_memory_working_set_bytes",
			"Memory working set in bytes",
			nodeName,
			usageBytes,
		))
		families = append(families, newGaugeMetricFamily(
			"node_memory_available_bytes",
			"Memory available in bytes",
			nodeName,
			availableBytes,
		))
	}

	// Filesystem usage metrics
	if operData.Storage.Quota > 0 {
		capacityBytes := float64(operData.Storage.Quota) * 1024 * 1024
		availableBytes := float64(operData.Storage.Available) * 1024 * 1024
		usedBytes := capacityBytes - availableBytes

		families = append(families, newGaugeMetricFamily(
			"node_fs_capacity_bytes",
			"Filesystem capacity in bytes",
			nodeName,
			capacityBytes,
		))
		families = append(families, newGaugeMetricFamily(
			"node_fs_available_bytes",
			"Filesystem available in bytes",
			nodeName,
			availableBytes,
		))
		families = append(families, newGaugeMetricFamily(
			"node_fs_used_bytes",
			"Filesystem used in bytes",
			nodeName,
			usedBytes,
		))
	}

	return families, nil
}

// newGaugeMetricFamily creates a Prometheus MetricFamily with a single gauge metric.
func newGaugeMetricFamily(name, help, nodeName string, value float64) *io_prometheus_client.MetricFamily {
	gaugeType := io_prometheus_client.MetricType_GAUGE
	labelName := "node"
	return &io_prometheus_client.MetricFamily{
		Name: &name,
		Help: &help,
		Type: &gaugeType,
		Metric: []*io_prometheus_client.Metric{
			{
				Label: []*io_prometheus_client.LabelPair{
					{Name: &labelName, Value: &nodeName},
				},
				Gauge: &io_prometheus_client.Gauge{Value: &value},
			},
		},
	}
}

// parseMemoryString parses memory strings from IOS-XE operational data (e.g. "256MB", "1024")
// and returns the value in bytes.
func parseMemoryString(memStr string) (uint64, error) {
	memStr = strings.TrimSpace(memStr)
	if memStr == "" {
		return 0, fmt.Errorf("empty memory string")
	}

	// Try parsing as plain number (assumed MB)
	memStr = strings.TrimSuffix(strings.ToUpper(memStr), "MB")
	memStr = strings.TrimSuffix(memStr, "KB")

	val, err := strconv.ParseUint(strings.TrimSpace(memStr), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse memory value %q: %w", memStr, err)
	}

	// Assume MB if no unit
	return val * 1024 * 1024, nil
}
