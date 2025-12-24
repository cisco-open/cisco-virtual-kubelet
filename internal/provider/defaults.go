package provider

import (
	"github.com/cisco/virtual-kubelet-cisco/internal/config"
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

func GetInitialNodeSpec(config *config.Config) v1.Node {

	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: GetNodeName(config),
			Labels: map[string]string{
				"platform": "cisco-ios-xe",
				"provider": "cisco-apphosting",
			},
		},
		Status: v1.NodeStatus{
			Conditions: InitNodeConditions(),
			NodeInfo:   InitNodeSystemInfo(config),
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

func InitNodeSystemInfo(config *config.Config) v1.NodeSystemInfo {
	// TODO Update this from driver information
	return v1.NodeSystemInfo{
		KubeletVersion:          "v2.00",
		OSImage:                 "Cisco IOSXE",
		KernelVersion:           "__cisco_ios__",
		ContainerRuntimeVersion: "cisco.app.hosting",
	}
}

func initNodeCapacity() v1.ResourceList {
	defaultCapacity := v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("8"),
		v1.ResourceMemory: resource.MustParse("8Gi"),
		"storage":         resource.MustParse("100Gi"),
		v1.ResourcePods:   resource.MustParse("16"),
	}
	// defaultCapacity := v1.ResourceList{
	// 	v1.ResourceCPU:    resource.MustParse("0"),
	// 	v1.ResourceMemory: resource.MustParse("0"),
	// 	"storage":         resource.MustParse("0"),
	// 	v1.ResourcePods:   resource.MustParse("0"),
	// }

	return defaultCapacity
}
