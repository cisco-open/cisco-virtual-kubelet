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
	"reflect"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

type nodeCapacityTestDriver struct {
	nodeStatusTestDriver
	operData       *common.AppHostingOperData
	driverCapacity *v1.ResourceList
	listPodsCalls  int
}

func (d *nodeCapacityTestDriver) GetGlobalOperationalData(context.Context) (*common.AppHostingOperData, error) {
	return d.operData, nil
}

func (d *nodeCapacityTestDriver) ListPods(context.Context) ([]*v1.Pod, error) {
	d.listPodsCalls++
	return []*v1.Pod{{}, {}, {}, {}, {}}, nil
}

func (d *nodeCapacityTestDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	if d.driverCapacity == nil {
		return &v1.ResourceList{}, nil
	}
	return d.driverCapacity, nil
}

type nodeTopologyObservationTestDriver struct {
	nodeStatusTestDriver
	cdpCalls  int
	ospfCalls int
}

type unavailableOperationalDataDriver struct {
	nodeStatusTestDriver
}

func (unavailableOperationalDataDriver) GetGlobalOperationalData(context.Context) (*common.AppHostingOperData, error) {
	return nil, errors.New("operational endpoint unavailable")
}

func (d *nodeTopologyObservationTestDriver) GetDeviceInfo(context.Context) (*common.DeviceInfo, error) {
	return &common.DeviceInfo{Hostname: "edge-router", RouterID: "192.0.2.1"}, nil
}

func (d *nodeTopologyObservationTestDriver) GetCDPNeighbors(context.Context) ([]common.CDPNeighbor, error) {
	d.cdpCalls++
	return []common.CDPNeighbor{{}}, nil
}

func (d *nodeTopologyObservationTestDriver) GetOSPFNeighbors(context.Context) ([]common.OSPFNeighbor, error) {
	d.ospfCalls++
	return []common.OSPFNeighbor{{}}, nil
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

func TestSyncNodeStatusManagedPublishesStatusOnly(t *testing.T) {
	ctx := context.Background()
	driver := &nodeTopologyObservationTestDriver{}
	node := NewAppHostingNodeWithTopologyMode(
		ctx,
		"nexus9300v-01",
		&v1alpha1.DeviceSpec{Driver: v1alpha1.DeviceDriverNXOS, MaxPods: 16},
		driver,
		topology.ProjectionModeManaged,
	)
	node.SetManagedWorkerRevision("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	var got *v1.Node
	node.syncNodeStatus(ctx, func(n *v1.Node) {
		got = n
	})

	if got == nil {
		t.Fatal("expected node status callback")
	}
	if len(got.Labels) != 0 || len(got.Annotations) != 1 ||
		got.Annotations[managedprotocol.AnnotationWorkerObservedRevision] == "" || len(got.Spec.Taints) != 0 {
		t.Fatalf("managed callback wrote manager-owned Node fields: labels=%v annotations=%v taints=%v",
			got.Labels, got.Annotations, got.Spec.Taints)
	}
	if driver.cdpCalls != 0 || driver.ospfCalls != 0 {
		t.Fatalf("managed status fetched suppressed topology observations: CDP=%d OSPF=%d",
			driver.cdpCalls, driver.ospfCalls)
	}
	var managedReady *v1.NodeCondition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == v1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition) {
			managedReady = &got.Status.Conditions[i]
			break
		}
	}
	if managedReady == nil {
		t.Fatalf("managed callback did not publish %s", managedprotocol.ManagedWorkerReadyCondition)
	}
	if managedReady.Status != v1.ConditionTrue || managedReady.Reason != managedprotocol.ManagedWorkerReadyReason || managedReady.LastHeartbeatTime.IsZero() {
		t.Fatalf("invalid managed writer-handoff condition: %#v", managedReady)
	}
}

func TestSyncNodeStatusManagedTreatsMissingHealthAsUnknown(t *testing.T) {
	node := NewAppHostingNodeWithTopologyMode(
		context.Background(),
		"edge-01",
		&v1alpha1.DeviceSpec{Driver: v1alpha1.DeviceDriverXE, MaxPods: 16},
		unavailableOperationalDataDriver{},
		topology.ProjectionModeManaged,
	)

	var got *v1.Node
	node.syncNodeStatus(context.Background(), func(update *v1.Node) { got = update })
	if got == nil {
		t.Fatal("managed status callback was not published")
	}
	for _, condition := range got.Status.Conditions {
		if condition.Type == v1.NodeReady {
			if condition.Status != v1.ConditionUnknown || condition.Reason != "OperationalDataUnavailable" {
				t.Fatalf("Node Ready condition = %#v, want fail-closed unknown health", condition)
			}
			return
		}
	}
	t.Fatal("Node Ready condition is absent")
}

func TestSyncNodeStatusManagedIgnoresConfiguredMetadata(t *testing.T) {
	ctx := context.Background()
	node := NewAppHostingNodeWithTopologyMode(
		ctx,
		"edge-01",
		&v1alpha1.DeviceSpec{MaxPods: 16, Labels: map[string]string{
			topology.LabelProvider: "foreign-provider",
		}},
		nodeStatusTestDriver{},
		topology.ProjectionModeManaged,
	)

	var got *v1.Node
	node.syncNodeStatus(ctx, func(update *v1.Node) {
		got = update
	})
	if got == nil {
		t.Fatal("managed status callback was suppressed by manager-owned metadata")
	}
	if len(got.Labels) != 0 || len(got.Spec.Taints) != 0 {
		t.Fatalf("managed callback published configured metadata: labels=%v taints=%v", got.Labels, got.Spec.Taints)
	}
}

func TestSyncNodeStatusStandaloneRetainsLegacyLabelOverrideSemantics(t *testing.T) {
	ctx := context.Background()
	node := NewAppHostingNode(ctx, "edge-01", &v1alpha1.DeviceSpec{
		Labels: map[string]string{
			topology.LabelProvider: "foreign-provider",
		},
	}, nodeStatusTestDriver{})

	var got *v1.Node
	node.syncNodeStatus(ctx, func(n *v1.Node) {
		got = n
	})
	if got == nil {
		t.Fatal("standalone compatibility status did not publish its safe projection")
	}
	if got.Labels[topology.LabelProvider] != "foreign-provider" {
		t.Fatalf("provider label=%q, want legacy spec.labels override", got.Labels[topology.LabelProvider])
	}
}

func TestSyncNodeStatusUsesTotalQuotaAndConfiguredMaxPods(t *testing.T) {
	ctx := context.Background()
	driver := &nodeCapacityTestDriver{operData: &common.AppHostingOperData{
		IoxEnabled: true,
		SystemCPU:  common.AppResource{Quota: 8, Available: 0, Unit: "cores"},
		Memory:     common.AppResource{Quota: 4096, Available: 512, Unit: "MB"},
		Storage:    common.AppResource{Quota: 11012, Available: 0, Unit: "MB"},
	}}
	node := NewAppHostingNode(ctx, "edge-01", &v1alpha1.DeviceSpec{MaxPods: 23}, driver)

	var got *v1.Node
	node.syncNodeStatus(ctx, func(n *v1.Node) {
		got = n
	})
	if got == nil {
		t.Fatal("expected node status callback")
	}

	want := map[v1.ResourceName]resource.Quantity{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("4096Mi"),
		v1.ResourceStorage: resource.MustParse("11012Mi"),
		v1.ResourcePods:    resource.MustParse("23"),
	}
	for name, wantQuantity := range want {
		capacity, ok := got.Status.Capacity[name]
		if !ok || capacity.Cmp(wantQuantity) != 0 {
			t.Errorf("Capacity[%s] = %v, want %s", name, capacity.String(), wantQuantity.String())
		}
		allocatable, ok := got.Status.Allocatable[name]
		if !ok || allocatable.Cmp(wantQuantity) != 0 {
			t.Errorf("Allocatable[%s] = %v, want total quota %s", name, allocatable.String(), wantQuantity.String())
		}
	}
	if driver.listPodsCalls != 0 {
		t.Fatalf("ListPods called %d times; scheduler accounts for bound Pod requests", driver.listPodsCalls)
	}
}

func TestAvailableResourceDataRemainsInStatsSummary(t *testing.T) {
	ctx := context.Background()
	driver := &nodeCapacityTestDriver{operData: &common.AppHostingOperData{
		IoxEnabled: true,
		SystemCPU:  common.AppResource{Quota: 100, Available: 25, Unit: "percentage"},
		Memory:     common.AppResource{Quota: 4096, Available: 512, Unit: "MB"},
		Storage:    common.AppResource{Quota: 11012, Available: 9025, Unit: "MB"},
	}}
	node := NewAppHostingNode(ctx, "edge-01", &v1alpha1.DeviceSpec{}, driver)
	provider := &AppHostingProvider{driver: driver, nodeProvider: node}

	summary, err := provider.buildStatsSummary(ctx)
	if err != nil {
		t.Fatalf("buildStatsSummary() error = %v", err)
	}
	if summary.Node.Memory == nil || summary.Node.Memory.AvailableBytes == nil {
		t.Fatalf("memory availability metric missing: %#v", summary.Node.Memory)
	}
	if got, want := *summary.Node.Memory.AvailableBytes, uint64(512*1024*1024); got != want {
		t.Errorf("memory AvailableBytes = %d, want %d", got, want)
	}
	if summary.Node.Fs == nil || summary.Node.Fs.AvailableBytes == nil {
		t.Fatalf("storage availability metric missing: %#v", summary.Node.Fs)
	}
	if got, want := *summary.Node.Fs.AvailableBytes, uint64(9025*1024*1024); got != want {
		t.Errorf("filesystem AvailableBytes = %d, want %d", got, want)
	}
}

func TestEffectiveMaxPodsUsesCompatibilityDefault(t *testing.T) {
	tests := []struct {
		name string
		spec *v1alpha1.DeviceSpec
		want int64
	}{
		{name: "nil spec", want: 16},
		{name: "zero before API defaulting", spec: &v1alpha1.DeviceSpec{}, want: 16},
		{name: "invalid negative", spec: &v1alpha1.DeviceSpec{MaxPods: -1}, want: 16},
		{name: "configured", spec: &v1alpha1.DeviceSpec{MaxPods: 37}, want: 37},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveMaxPods(tt.spec); got != tt.want {
				t.Fatalf("effectiveMaxPods() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSchedulerCapacityUsesDriverNormalizedCPUForIOSXEQuotaUnits(t *testing.T) {
	// IOS XE quota-unit is not a count of cores. The driver-facing ResourceList
	// is the existing Kubernetes-normalized contract and must win for CPU.
	driverCapacity := v1.ResourceList{v1.ResourceCPU: resource.MustParse("8")}
	capacity, allocatable := schedulerCapacity(&common.AppHostingOperData{
		SystemCPU: common.AppResource{Quota: 75, Available: 20, Unit: "37500"},
	}, &driverCapacity, 16, true)
	want := resource.MustParse("8")
	if got := capacity[v1.ResourceCPU]; got.Cmp(want) != 0 {
		t.Fatalf("Capacity[cpu] = %s, want normalized driver value %s", got.String(), want.String())
	}
	if got := allocatable[v1.ResourceCPU]; got.Cmp(want) != 0 {
		t.Fatalf("Allocatable[cpu] = %s, want normalized driver value %s", got.String(), want.String())
	}
}

func TestSchedulerCapacityAcceptsExplicitCoreOperationalUnit(t *testing.T) {
	driverCapacity := v1.ResourceList{v1.ResourceCPU: resource.MustParse("4")}
	capacity, _ := schedulerCapacity(&common.AppHostingOperData{
		SystemCPU: common.AppResource{Quota: 12, Unit: "cores"},
	}, &driverCapacity, 16, true)
	want := resource.MustParse("12")
	if got := capacity[v1.ResourceCPU]; got.Cmp(want) != 0 {
		t.Fatalf("Capacity[cpu] = %s, want operational core count %s", got.String(), want.String())
	}
}

func TestManagedSchedulerCapacityNeverUsesDriverPlaceholders(t *testing.T) {
	driverCapacity := v1.ResourceList{
		v1.ResourceCPU:     resource.MustParse("8"),
		v1.ResourceMemory:  resource.MustParse("16Gi"),
		v1.ResourceStorage: resource.MustParse("100Gi"),
	}
	capacity, allocatable := schedulerCapacity(&common.AppHostingOperData{
		SystemCPU: common.AppResource{Quota: 75, Unit: "37500"},
		Memory:    common.AppResource{Quota: 4096, Unit: "MB"},
	}, &driverCapacity, 16, false)
	if _, ok := capacity[v1.ResourceCPU]; ok {
		t.Fatalf("managed capacity invented CPU from a placeholder: %v", capacity)
	}
	if got := capacity[v1.ResourceMemory]; got.Cmp(resource.MustParse("4096Mi")) != 0 {
		t.Fatalf("managed memory capacity = %s, want observed quota", got.String())
	}
	if _, ok := capacity[v1.ResourceStorage]; ok {
		t.Fatalf("managed capacity invented storage from a placeholder: %v", capacity)
	}
	if !reflect.DeepEqual(capacity, allocatable) {
		t.Fatalf("Capacity and Allocatable differ: %v / %v", capacity, allocatable)
	}
}

func TestInitialNodeSpecUsesDriverPlatformMetadata(t *testing.T) {
	spec := &v1alpha1.DeviceSpec{
		Driver:  v1alpha1.DeviceDriverNXOS,
		Address: "192.0.2.64",
		Region:  "lab",
		Zone:    "rack-7",
		MaxPods: 31,
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
	if pods := node.Status.Capacity[v1.ResourcePods]; pods.Cmp(resource.MustParse("31")) != 0 {
		t.Fatalf("pod capacity=%s, want 31", pods.String())
	}
}

func TestInitialNodeSpecWithTopologyModeReportsConflicts(t *testing.T) {
	node, err := GetInitialNodeSpecWithTopologyMode("edge-01", &v1alpha1.DeviceSpec{
		MaxPods: 16,
		Region:  "eu-central",
		Labels: map[string]string{
			v1.LabelTopologyRegion: "us-west",
		},
	}, topology.ProjectionModeManaged)
	if err == nil {
		t.Fatal("expected conflicting topology sources to fail")
	}
	if _, ok := node.Labels[v1.LabelTopologyRegion]; ok {
		t.Fatalf("conflicting topology was projected: %v", node.Labels)
	}
}

func TestManagedMaxPodsFailsClosedAtInitialAndRuntimeBoundaries(t *testing.T) {
	for _, value := range []int32{1, 110} {
		node, err := GetInitialNodeSpecWithTopologyMode("edge-01", &v1alpha1.DeviceSpec{
			Driver: v1alpha1.DeviceDriverXE, MaxPods: value,
		}, topology.ProjectionModeManaged)
		if err != nil {
			t.Fatalf("managed maxPods=%d initial projection: %v", value, err)
		}
		if got := node.Status.Capacity[v1.ResourcePods]; got.Cmp(*resource.NewQuantity(int64(value), resource.DecimalSI)) != 0 {
			t.Fatalf("managed maxPods=%d initial capacity=%s", value, got.String())
		}
	}

	for _, value := range []int32{-1, 0, 111} {
		node, err := GetInitialNodeSpecWithTopologyMode("edge-01", &v1alpha1.DeviceSpec{
			Driver: v1alpha1.DeviceDriverXE, MaxPods: value,
		}, topology.ProjectionModeManaged)
		if err == nil {
			t.Fatalf("managed maxPods=%d initial projection succeeded", value)
		}
		if _, exists := node.Status.Capacity[v1.ResourcePods]; exists {
			t.Fatalf("managed maxPods=%d published initial Pod capacity: %v", value, node.Status.Capacity)
		}

		provider := NewAppHostingNodeWithTopologyMode(
			context.Background(), "edge-01",
			&v1alpha1.DeviceSpec{Driver: v1alpha1.DeviceDriverXE, MaxPods: value},
			nodeStatusTestDriver{}, topology.ProjectionModeManaged,
		)
		var update *v1.Node
		provider.syncNodeStatus(context.Background(), func(node *v1.Node) { update = node })
		if update == nil {
			t.Fatalf("managed maxPods=%d runtime callback was absent", value)
		}
		if _, exists := update.Status.Allocatable[v1.ResourcePods]; exists {
			t.Fatalf("managed maxPods=%d published runtime Pod allocatable: %v", value, update.Status.Allocatable)
		}
		ready := conditionByType(update.Status.Conditions, v1.NodeReady)
		if ready == nil || ready.Status != v1.ConditionUnknown || ready.Reason != "ManagedCapacityInvalid" {
			t.Fatalf("managed maxPods=%d Ready=%#v", value, ready)
		}
	}

	// Standalone retains the legacy compatibility semantics: non-positive
	// values default to 16 and values above the managed ceiling are preserved.
	for _, value := range []int32{0, 111} {
		node := GetInitialNodeSpec("edge-01", &v1alpha1.DeviceSpec{MaxPods: value})
		want := int64(value)
		if value <= 0 {
			want = 16
		}
		if got := node.Status.Capacity[v1.ResourcePods]; got.Cmp(*resource.NewQuantity(want, resource.DecimalSI)) != 0 {
			t.Fatalf("standalone maxPods=%d capacity=%s, want %d", value, got.String(), want)
		}
	}
}

func conditionByType(conditions []v1.NodeCondition, conditionType v1.NodeConditionType) *v1.NodeCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func TestInitialManagedNodeSpecFailsClosedBeforeCapacityObservation(t *testing.T) {
	node, err := GetInitialNodeSpecWithTopologyMode("edge-01", &v1alpha1.DeviceSpec{
		Driver:  v1alpha1.DeviceDriverXE,
		MaxPods: 23,
	}, topology.ProjectionModeManaged)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []v1.ResourceName{v1.ResourceCPU, v1.ResourceMemory, v1.ResourceStorage} {
		if _, exists := node.Status.Capacity[name]; exists {
			t.Fatalf("managed initial Node invented %s capacity: %v", name, node.Status.Capacity)
		}
	}
	if got := node.Status.Capacity[v1.ResourcePods]; got.Cmp(resource.MustParse("23")) != 0 {
		t.Fatalf("managed initial pod capacity = %s, want 23", got.String())
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type != v1.NodeReady {
			continue
		}
		if condition.Status != v1.ConditionUnknown || condition.Reason != "ManagedHealthUnobserved" {
			t.Fatalf("managed initial Ready condition = %#v", condition)
		}
		return
	}
	t.Fatal("managed initial Ready condition is absent")
}
