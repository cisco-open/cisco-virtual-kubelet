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
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type nodeStatusTestDriver struct{}

func (nodeStatusTestDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	return &v1.ResourceList{}, nil
}

func (nodeStatusTestDriver) GetDeviceInfo(context.Context) (*common.DeviceInfo, error) {
	return &common.DeviceInfo{
		SerialNumber:    "FOC2416U0MV",
		SoftwareVersion: "26.01.1",
		ProductID:       "C9300-24P",
		Hostname:        "cat9000-4",
	}, nil
}

func (nodeStatusTestDriver) DeployPod(context.Context, *v1.Pod, corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) error {
	return nil
}

func (nodeStatusTestDriver) UpdatePod(context.Context, *v1.Pod) error { return nil }

func (nodeStatusTestDriver) DeletePod(context.Context, *v1.Pod) error { return nil }

func (nodeStatusTestDriver) GetPodStatus(_ context.Context, pod *v1.Pod) (*v1.Pod, error) {
	return pod, nil
}

func (nodeStatusTestDriver) ListPods(context.Context) ([]*v1.Pod, error) { return nil, nil }

func (nodeStatusTestDriver) GetGlobalOperationalData(context.Context) (*common.AppHostingOperData, error) {
	return &common.AppHostingOperData{IoxEnabled: true}, nil
}

func TestSyncNodeStatusAppliesDeviceLabelsAndTaints(t *testing.T) {
	ctx := context.Background()
	node := NewAppHostingNode(ctx, "cat9000-4", &v1alpha1.DeviceSpec{
		Address: "198.51.100.103",
		Region:  "lab",
		Zone:    "rack-1",
		Labels: map[string]string{
			"workload": "edge",
		},
		Taints: []v1.Taint{{
			Key:    "workload",
			Value:  "edge",
			Effect: v1.TaintEffectNoSchedule,
		}},
	}, nodeStatusTestDriver{})

	var got *v1.Node
	node.syncNodeStatus(ctx, func(n *v1.Node) {
		got = n
	})

	if got == nil {
		t.Fatal("expected node status callback")
	}
	if got.Labels["workload"] != "edge" {
		t.Fatalf("expected device label to be published, got labels=%v", got.Labels)
	}
	if got.Labels["topology.kubernetes.io/region"] != "lab" || got.Labels["topology.kubernetes.io/zone"] != "rack-1" {
		t.Fatalf("expected device topology labels, got labels=%v", got.Labels)
	}
	if len(got.Spec.Taints) != 1 {
		t.Fatalf("expected one device taint, got %#v", got.Spec.Taints)
	}
	if got.Spec.Taints[0].Key != "workload" || got.Spec.Taints[0].Value != "edge" || got.Spec.Taints[0].Effect != v1.TaintEffectNoSchedule {
		t.Fatalf("unexpected taint: %#v", got.Spec.Taints[0])
	}
}

func TestSyncNodeStatusUsesDriverPlatformMetadata(t *testing.T) {
	ctx := context.Background()
	node := NewAppHostingNode(ctx, "nexus9300v-01", &v1alpha1.DeviceSpec{
		Driver:  v1alpha1.DeviceDriverNXOS,
		Address: "192.0.2.64",
	}, nodeStatusTestDriver{})

	var got *v1.Node
	node.syncNodeStatus(ctx, func(n *v1.Node) {
		got = n
	})

	if got == nil {
		t.Fatal("expected node status callback")
	}
	if got.Labels["platform"] != "cisco-nxos" {
		t.Fatalf("platform label=%q, want cisco-nxos", got.Labels["platform"])
	}
	if got.Labels["topology.kubernetes.io/region"] != "cisco-nxos" || got.Labels["topology.kubernetes.io/zone"] != "cisco-nxos" {
		t.Fatalf("expected NX-OS topology labels, got labels=%v", got.Labels)
	}
	if got.Status.NodeInfo.OSImage != "NX-OS" {
		t.Fatalf("OSImage=%q, want NX-OS", got.Status.NodeInfo.OSImage)
	}
}

func TestInitialNodeSpecUsesDriverPlatformMetadata(t *testing.T) {
	spec := &v1alpha1.DeviceSpec{
		Driver:  v1alpha1.DeviceDriverNXOS,
		Address: "192.0.2.64",
		Region:  "lab",
		Zone:    "rack-7",
		Labels: map[string]string{
			"workload": "edge",
		},
		Taints: []v1.Taint{{
			Key:    "cisco.vk/device",
			Value:  "nexus9300v-01",
			Effect: v1.TaintEffectNoExecute,
		}},
	}

	node := GetInitialNodeSpec("nexus9300v-01", spec)

	if node.Name != "nexus9300v-01" {
		t.Fatalf("node name=%q, want nexus9300v-01", node.Name)
	}
	if node.Labels["platform"] != "cisco-nxos" {
		t.Fatalf("platform label=%q, want cisco-nxos; labels=%v", node.Labels["platform"], node.Labels)
	}
	if node.Labels["kubernetes.io/hostname"] != "nexus9300v-01" {
		t.Fatalf("hostname label=%q, want nexus9300v-01", node.Labels["kubernetes.io/hostname"])
	}
	if node.Labels["topology.kubernetes.io/region"] != "lab" || node.Labels["topology.kubernetes.io/zone"] != "rack-7" {
		t.Fatalf("unexpected topology labels: %v", node.Labels)
	}
	if node.Labels["workload"] != "edge" {
		t.Fatalf("custom label not applied: %v", node.Labels)
	}
	if len(node.Spec.Taints) != 1 || node.Spec.Taints[0].Key != "cisco.vk/device" {
		t.Fatalf("device taints not applied: %#v", node.Spec.Taints)
	}
	if node.Status.NodeInfo.OSImage != "NX-OS" {
		t.Fatalf("OSImage=%q, want NX-OS", node.Status.NodeInfo.OSImage)
	}
}
