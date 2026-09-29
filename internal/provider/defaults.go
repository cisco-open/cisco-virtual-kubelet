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
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultNodeName is used when no --nodename flag or VKUBELET_NODE_NAME env is set.
const DefaultNodeName = "cisco-virtual-kubelet"

// GetNodeName returns the supplied nodeName, or derives one from the device address.
// If neither is available, it falls back to DefaultNodeName.
func GetNodeName(nodeName, deviceAddress string) string {
	if nodeName != "" {
		return nodeName
	}
	if deviceAddress != "" {
		sanitized := sanitizeNodeName(deviceAddress)
		return fmt.Sprintf("cisco-vk-%s", sanitized)
	}
	return DefaultNodeName
}

func sanitizeNodeName(value string) string {
	replacer := strings.NewReplacer(
		":", "-",
		".", "-",
		"/", "-",
		" ", "-",
	)
	return replacer.Replace(strings.TrimSpace(value))
}

// GetInitialNodeSpec builds the initial v1.Node using runtime parameters.
// Standalone registration intentionally retains the legacy platform-as-topology
// fallback for this compatibility entry point.
func GetInitialNodeSpec(nodeName string, deviceSpec *ciskov1.DeviceSpec) v1.Node {
	node, err := GetInitialNodeSpecWithTopologyMode(nodeName, deviceSpec, topology.ProjectionModeStandaloneCompatibility)
	if err != nil {
		// Keep this long-standing, no-error API for standalone callers. Managed
		// callers use the error-returning mode-aware entry point below and fail
		// closed; the compatibility path preserves its original merge semantics.
		log.G(context.Background()).WithError(err).Warn("Invalid standalone Node label projection")
	}
	return node
}

// GetInitialNodeSpecWithTopologyMode builds an initial Node using an explicit
// topology compatibility mode. Callers must not publish the returned Node when
// err is non-nil.
func GetInitialNodeSpecWithTopologyMode(
	nodeName string,
	deviceSpec *ciskov1.DeviceSpec,
	mode topology.ProjectionMode,
) (v1.Node, error) {
	deviceAddress := ""
	driver := ciskov1.DeviceDriver("")
	var taints []v1.Taint
	if deviceSpec != nil {
		deviceAddress = deviceSpec.Address
		driver = deviceSpec.Driver
		taints = append([]v1.Taint(nil), deviceSpec.Taints...)
	}
	resolvedNodeName := GetNodeName(nodeName, deviceAddress)
	nodeInfo := InitNodeSystemInfo()
	nodePlatform := nodePlatformMetadata(driver)
	nodeInfo.OSImage = nodePlatform.OSImage
	nodeInfo.OperatingSystem = "Cisco"
	conditions := InitNodeConditions()
	maxPods := effectiveMaxPods(deviceSpec)
	capacity := initNodeCapacity(maxPods)
	var capacityErr error
	if mode == topology.ProjectionModeManaged {
		maxPods = rawMaxPods(deviceSpec)
		capacityErr = topology.ValidateManagedMaxPods(maxPods)
		capacity = initManagedNodeCapacity(maxPods, capacityErr == nil)
		for i := range conditions {
			if conditions[i].Type != v1.NodeReady {
				continue
			}
			conditions[i].Status = v1.ConditionUnknown
			conditions[i].Reason = "ManagedHealthUnobserved"
			conditions[i].Message = "managed device health has not yet been observed"
		}
	}
	labels, labelErr := normalizedNodeLabels(resolvedNodeName, deviceSpec, mode)

	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   resolvedNodeName,
			Labels: labels,
		},
		Spec: v1.NodeSpec{
			Taints: taints,
		},
		Status: v1.NodeStatus{
			Phase:      v1.NodeRunning,
			Conditions: conditions,
			NodeInfo:   nodeInfo,
			Capacity:   capacity,
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeInternalIP,
					Address: deviceAddress,
				},
			},
			DaemonEndpoints: v1.NodeDaemonEndpoints{
				KubeletEndpoint: v1.DaemonEndpoint{
					Port: 10250,
				},
			},
		},
	}, errors.Join(labelErr, capacityErr)
}

func normalizedNodeLabels(
	nodeName string,
	deviceSpec *ciskov1.DeviceSpec,
	mode topology.ProjectionMode,
) (map[string]string, error) {
	driver := ciskov1.DeviceDriver("")
	region := ""
	zone := ""
	var sourceLabels map[string]string
	if deviceSpec != nil {
		driver = deviceSpec.Driver
		region = deviceSpec.Region
		zone = deviceSpec.Zone
		sourceLabels = deviceSpec.Labels
	}
	nodePlatform := nodePlatformMetadata(driver)
	return topology.NodeLabels(topology.NodeLabelsOptions{
		Mode:                   mode,
		NodeName:               nodeName,
		Platform:               nodePlatform.Label,
		Region:                 region,
		Zone:                   zone,
		LegacyPlatformTopology: nodePlatform.LegacyTopology,
		SourceLabels:           sourceLabels,
	})
}

func InitNodeConditions() []v1.NodeCondition {
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletReady",
			Message:            "Cisco provider is ready",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "Cisco provider has sufficient disk space",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "Cisco provider has sufficient memory",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "Cisco provider has no disk pressure",
		},
		{
			Type:               "PIDPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientPID",
			Message:            "Cisco provider has sufficient PIDs",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "Cisco provider network is available",
		},
	}
}

func InitNodeSystemInfo() v1.NodeSystemInfo {
	// TODO Update this from driver information
	return v1.NodeSystemInfo{
		Architecture:            "unknown",
		OperatingSystem:         "unknown",
		KubeletVersion:          getVirtualKubeletVersion(),
		ContainerRuntimeVersion: "unknown",
		OSImage:                 "unknown",
	}
}

type nodePlatformInfo struct {
	Label          string
	LegacyTopology string
	OSImage        string
}

func nodePlatformMetadata(driver ciskov1.DeviceDriver) nodePlatformInfo {
	switch driver {
	case ciskov1.DeviceDriverNXOS:
		return nodePlatformInfo{Label: "cisco-nxos", LegacyTopology: "cisco-nxos", OSImage: "NX-OS"}
	case ciskov1.DeviceDriverXR:
		return nodePlatformInfo{Label: "cisco-iosxr", LegacyTopology: "cisco-iosxr", OSImage: "IOS-XR"}
	case ciskov1.DeviceDriverOPENCONFIG:
		return nodePlatformInfo{Label: "openconfig", LegacyTopology: "openconfig", OSImage: "OpenConfig"}
	default:
		return nodePlatformInfo{Label: "cisco-ios-xe", LegacyTopology: "cisco-iosxe", OSImage: "IOS-XE"}
	}
}

func getVirtualKubeletVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/virtual-kubelet/virtual-kubelet" {
			return dep.Version
		}
	}
	return "unknown"
}

func initNodeCapacity(maxPods int64) v1.ResourceList {
	defaultCapacity := v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("8"),
		v1.ResourceMemory: resource.MustParse("8Gi"),
		"storage":         resource.MustParse("100Gi"),
		v1.ResourcePods:   *resource.NewQuantity(maxPods, resource.DecimalSI),
	}

	return defaultCapacity
}

func initManagedNodeCapacity(maxPods int64, valid bool) v1.ResourceList {
	// maxPods is explicit scheduler configuration with a compatibility default.
	// CPU, memory, and device storage remain absent until live app-hosting quota
	// proves them; model placeholders must never become schedulable capacity.
	if !valid {
		return v1.ResourceList{}
	}
	return v1.ResourceList{
		v1.ResourcePods: *resource.NewQuantity(maxPods, resource.DecimalSI),
	}
}

func effectiveMaxPods(deviceSpec *ciskov1.DeviceSpec) int64 {
	if deviceSpec != nil && deviceSpec.MaxPods > 0 {
		return int64(deviceSpec.MaxPods)
	}
	return 16
}

func rawMaxPods(deviceSpec *ciskov1.DeviceSpec) int64 {
	if deviceSpec == nil {
		return 0
	}
	return int64(deviceSpec.MaxPods)
}
