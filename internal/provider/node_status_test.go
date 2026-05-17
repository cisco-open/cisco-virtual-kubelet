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
