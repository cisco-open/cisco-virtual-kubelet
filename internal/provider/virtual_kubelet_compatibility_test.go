// Copyright 2026 Cisco Systems Inc.
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
	"reflect"
	"sync"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
)

// Keep the device snapshot immutable: both the VK controller and the Cisco
// notifier query it concurrently, just as they do in the worker process.
type compatibilityPodDriver struct {
	nodeStatusTestDriver
	pod *v1.Pod
}

func (d compatibilityPodDriver) ListPods(context.Context) ([]*v1.Pod, error) {
	return []*v1.Pod{d.pod.DeepCopy()}, nil
}

func (d compatibilityPodDriver) GetPodStatus(_ context.Context, pod *v1.Pod) (*v1.Pod, error) {
	result := pod.DeepCopy()
	result.Status = *d.pod.Status.DeepCopy()
	return result, nil
}

func TestVirtualKubeletPodControllerRetriesStatusWithCurrentResourceVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pod := notifierPod("status-conflict", "77777777-7777-4777-8777-777777777777")
	pod.ResourceVersion = "41"
	pod.Spec.NodeName = "cisco-vk-test"
	pod.Spec.EnableServiceLinks = new(bool)
	pod.Status.Phase = v1.PodPending
	devicePod := pod.DeepCopy()
	devicePod.Status = notifierPodStatus(v1.PodRunning, true)
	client := fake.NewSimpleClientset(pod)
	factory := informers.NewSharedInformerFactory(client, 0)
	core := factory.Core().V1()
	pods := core.Pods()
	secrets := core.Secrets()
	configMaps := core.ConfigMaps()
	services := core.Services()
	provider, err := NewAppHostingProvider(ctx, &ciskov1.DeviceSpec{}, nodeutil.ProviderConfig{
		Pods: pods.Lister(), Secrets: secrets.Lister(), ConfigMaps: configMaps.Lister(), Services: services.Lister(),
	}, compatibilityPodDriver{pod: devicePod}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual NotifyPods registration and polling bridge. Unit tests
	// that call a stored callback directly cannot catch VK's UpdateStatus logic.
	provider.SetPodPollIntervalForTest(10 * time.Millisecond)
	controller, err := node.NewPodController(node.PodControllerConfig{
		PodClient: client.CoreV1(), PodInformer: pods,
		SecretInformer: secrets, ConfigMapInformer: configMaps, ServiceInformer: services,
		EventRecorder: record.NewFakeRecorder(100), Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}

	var attemptsMu sync.Mutex
	var attemptedVersions []string
	currentVersion := "41"
	conflictInjected := false
	client.PrependReactor("update", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "status" {
			return false, nil, nil
		}
		update := action.(clienttesting.UpdateAction).GetObject().(*v1.Pod)
		attemptsMu.Lock()
		defer attemptsMu.Unlock()
		attemptedVersions = append(attemptedVersions, update.ResourceVersion)
		// Fake clients do not enforce optimistic concurrency. Model it here so
		// the old VK ResourceVersion="0" behavior cannot pass this integration test.
		if update.ResourceVersion != currentVersion {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, pod.Name,
				fmt.Errorf("resourceVersion %q does not match %q", update.ResourceVersion, currentVersion))
		}
		if !conflictInjected {
			conflictInjected = true
			currentVersion = "42"
			newer := pod.DeepCopy()
			newer.ResourceVersion = currentVersion
			if err := client.Tracker().Update(v1.SchemeGroupVersion.WithResource("pods"), newer, pod.Namespace); err != nil {
				return true, nil, err
			}
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, pod.Name,
				fmt.Errorf("concurrent API update"))
		}
		return false, nil, nil
	})

	factory.Start(ctx.Done())
	go func() { _ = controller.Run(ctx, 1) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-controller.Done():
		case <-time.After(5 * time.Second):
			t.Error("pod controller did not stop")
		}
		factory.Shutdown()
	})
	select {
	case <-controller.Ready():
	case <-controller.Done():
		t.Fatalf("pod controller exited before ready: %v", controller.Err())
	case <-time.After(10 * time.Second):
		t.Fatal("pod controller did not become ready")
	}
	err = wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		current, err := client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		return err == nil && current.ResourceVersion == "42" && current.Status.Phase == v1.PodRunning, err
	})
	attemptsMu.Lock()
	versions := append([]string(nil), attemptedVersions...)
	injected := conflictInjected
	attemptsMu.Unlock()
	if err != nil {
		t.Fatalf("provider status did not recover after conflict: %v; attempted versions=%v", err, versions)
	}
	if !injected || len(versions) < 2 || versions[0] != "41" || versions[len(versions)-1] != "42" {
		t.Fatalf("status retry did not use refreshed informer version: %v", versions)
	}
	for _, version := range versions {
		if version != "41" && version != "42" {
			t.Fatalf("status update bypassed optimistic concurrency: %v", versions)
		}
	}
}

func TestVirtualKubeletNodeControllerPreservesManagedIdentityAndMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	const nodeName = "cisco-vk-managed-test"
	const revision = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	spec := &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, Address: "192.0.2.8", MaxPods: 16}
	initial, err := GetInitialNodeSpecWithTopologyMode(nodeName, spec, topology.ProjectionModeManaged)
	if err != nil {
		t.Fatal(err)
	}
	// Match cmd/cisco-vk's managedWorkerInitialNode: the manager owns metadata
	// and spec; the VK worker contributes only identity and its status seed.
	initial.Labels, initial.Annotations, initial.Spec = nil, nil, v1.NodeSpec{}
	registered := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName, UID: "manager-created-node", ResourceVersion: "7",
			Labels:      map[string]string{"topology.kubernetes.io/zone": "manager-zone"},
			Annotations: map[string]string{"topology.cisco.vk/manager-owned": "preserve"},
		},
		Spec: v1.NodeSpec{Unschedulable: true, Taints: []v1.Taint{{Key: "manager-taint", Effect: v1.TaintEffectNoSchedule}}},
	}
	client := fake.NewSimpleClientset(registered)
	provider := NewAppHostingNodeWithTopologyMode(ctx, nodeName, spec, nodeStatusTestDriver{}, topology.ProjectionModeManaged)
	provider.SetManagedWorkerRevision(revision)
	controller, err := node.NewNodeController(provider, &initial, client.CoreV1().Nodes(),
		node.WithNodePingInterval(20*time.Millisecond),
		node.WithNodeEnableLeaseV1WithRenewInterval(client.CoordinationV1().Leases(v1.NamespaceNodeLease), 40, 20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = controller.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-controller.Done():
			if err := controller.Err(); err != nil {
				t.Errorf("node controller failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("node controller did not stop")
		}
	})
	var observed *v1.Node
	err = wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		var err error
		observed, err = client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil || observed.Annotations[managedprotocol.AnnotationWorkerObservedRevision] != revision {
			return false, err
		}
		lease, err := client.CoordinationV1().Leases(v1.NamespaceNodeLease).Get(ctx, nodeName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == nodeName &&
			lease.Spec.RenewTime != nil && len(lease.OwnerReferences) == 1 &&
			lease.OwnerReferences[0].UID == registered.UID, nil
	})
	if err != nil {
		t.Fatalf("unnamed provider status did not reach the named Node and Lease: %v; controller error=%v", err, controller.Err())
	}
	if observed.UID != registered.UID || !reflect.DeepEqual(observed.Labels, registered.Labels) ||
		!reflect.DeepEqual(observed.Spec, registered.Spec) ||
		observed.Annotations["topology.cisco.vk/manager-owned"] != "preserve" {
		t.Fatalf("upstream status merge altered manager-owned node fields: %#v", observed)
	}
	for _, key := range []string{managedprotocol.VirtualKubeletLastAppliedNodeStatus, managedprotocol.VirtualKubeletLastAppliedObjectMeta} {
		if observed.Annotations[key] == "" {
			t.Errorf("status patch omitted required VK bookkeeping annotation %s", key)
		}
	}
	ready := false
	for _, condition := range observed.Status.Conditions {
		if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue && !condition.LastHeartbeatTime.IsZero() {
			ready = true
		}
	}
	if !ready {
		t.Fatalf("device health did not reach NodeReady: %#v", observed.Status.Conditions)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "nodes" || action.GetVerb() == "get" {
			continue
		}
		if action.GetVerb() != "patch" || action.GetSubresource() != "status" || action.(clienttesting.PatchAction).GetName() != nodeName {
			t.Fatalf("managed node controller made an unexpected write: %#v", action)
		}
	}
}
