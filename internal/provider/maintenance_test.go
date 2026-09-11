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
	"sync/atomic"
	"testing"
	"time"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type maintenancePodDriver struct {
	notifierDriver
	updates int
}

func (d *maintenancePodDriver) UpdatePod(context.Context, *v1.Pod) error { d.updates++; return nil }

func TestAppHostingMaintenanceBlocksWritesAndRecoveryButNotStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{coordv1.AddToScheme, opsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "leases", TTL: 26 * time.Hour}
	if _, err := leaser.Acquire(ctx, devicecoordination.DeviceKey("edge", "switch"), devicecoordination.MutationLeaseFamily, "software-upgrade/run"); err != nil {
		t.Fatal(err)
	}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "app", UID: "app-uid"}, Spec: v1.PodSpec{Containers: []v1.Container{{Name: "app"}}}}
	driver := &maintenancePodDriver{notifierDriver: notifierDriver{status: v1.PodStatus{Phase: v1.PodRunning}}}
	p, err := NewAppHostingProvider(ctx, &ciskov1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, driver, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.SetMaintenance(&maintenance.Coordinator{Client: c, Namespace: "edge", DeviceName: "switch", LeaseNamespace: "leases", MutationsEnabled: true})
	for _, mutate := range []func(context.Context, *v1.Pod) error{p.CreatePod, p.UpdatePod, p.DeletePod} {
		if err := mutate(ctx, pod); err == nil {
			t.Fatal("pod mutation bypassed upgrade lease")
		}
	}
	if status, err := p.GetPodStatus(ctx, pod.Namespace, pod.Name); err != nil || status.Phase != v1.PodRunning {
		t.Fatalf("status read blocked: %v", err)
	}
	driver.missingUntilDeploy = true
	if _, err := p.statusOrReconcilePod(ctx, pod); err == nil {
		t.Fatal("missing-pod redeploy bypassed upgrade lease")
	}
	if driver.deployCalls != 0 || driver.updates != 0 || atomic.LoadInt32(&driver.deleteCalls) != 0 {
		t.Fatal("blocked app path reached mutating driver")
	}
	deleting := pod.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	p.recoverDeletingPod(ctx, deleting)
	deadline := time.Now().Add(time.Second)
	for {
		p.deleteRecoveryMu.Lock()
		_, pending := p.deleteRecoveryInFlight[pod.UID]
		p.deleteRecoveryMu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delete recovery did not release local in-flight marker")
		}
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&driver.deleteCalls) != 0 {
		t.Fatal("delete recovery bypassed upgrade lease")
	}
}

type panickingMaintenanceDriver struct{ notifierDriver }

func (*panickingMaintenanceDriver) DeployPod(context.Context, *v1.Pod, corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) error {
	panic("device mutation panic")
}

func TestAppHostingMaintenancePanicRetainsLease(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprintf("create=%t", create), func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{coordv1.AddToScheme, opsv1alpha1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "app"}}
			p, err := NewAppHostingProvider(ctx, &ciskov1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, &panickingMaintenanceDriver{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			p.SetMaintenance(&maintenance.Coordinator{Client: c, Namespace: "edge", DeviceName: "switch", LeaseNamespace: "leases", MutationsEnabled: true})
			func() {
				defer func() {
					if got := recover(); got != "device mutation panic" {
						t.Fatalf("panic was suppressed or replaced: %v", got)
					}
				}()
				if create {
					_ = p.CreatePod(ctx, pod)
				} else {
					_ = p.withMutation(ctx, func(context.Context) error { panic("device mutation panic") })
				}
			}()
			var lease coordv1.Lease
			key := types.NamespacedName{Namespace: "leases", Name: engine.LeaseName(devicecoordination.DeviceKey("edge", "switch"), devicecoordination.MutationLeaseFamily)}
			if err := c.Get(ctx, key, &lease); err != nil {
				t.Fatalf("panic released uncertain mutation lease: %v", err)
			}
		})
	}
}
