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
	"github.com/cisco/virtual-kubelet-cisco/internal/config"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetNodeName(config *config.Config) string {
	NodeName := "cisco-virtual-kubelet"
	if config.Kubelet.NodeName != "" {
		NodeName = config.Kubelet.NodeName
	}
	return NodeName
}

func GetInitialNodeSpec(config *config.Config, deviceInfo *common.DeviceInfo) v1.Node {

	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: GetNodeName(config),
			Labels: map[string]string{
				"platform": "cisco-ios-xe",
				"provider": "cisco-apphosting",
			},
		},
		Status: v1.NodeStatus{
			Phase:      v1.NodeRunning,
			Conditions: InitNodeConditions(),
			NodeInfo:   InitNodeSystemInfo(deviceInfo),
			Capacity:   initNodeCapacity(),
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeInternalIP,
					Address: config.Kubelet.NodeInternalIP,
				},
			},
			DaemonEndpoints: v1.NodeDaemonEndpoints{
				KubeletEndpoint: v1.DaemonEndpoint{
					Port: 10250,
				},
			},
		},
	}
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

func InitNodeSystemInfo(deviceInfo *common.DeviceInfo) v1.NodeSystemInfo {
	info := v1.NodeSystemInfo{
		Architecture:            "amd64",
		OperatingSystem:         "linux",
		KubeletVersion:          "v1.0.0",
		ContainerRuntimeVersion: "cisco.app.hosting://1.0",
		OSImage:                 "Cisco IOS-XE",
	}
	if deviceInfo != nil && deviceInfo.SerialNumber != "" {
		info.MachineID = deviceInfo.SerialNumber
		info.SystemUUID = deviceInfo.SerialNumber
	}
	return info
}

func initNodeCapacity() v1.ResourceList {
	defaultCapacity := v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("8"),
		v1.ResourceMemory: resource.MustParse("8Gi"),
		"storage":         resource.MustParse("100Gi"),
		v1.ResourcePods:   resource.MustParse("16"),
	}

	return defaultCapacity
}
