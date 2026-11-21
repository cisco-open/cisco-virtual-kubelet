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
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type uidInventoryDriver struct {
	notifierDriver
	updates int
}

func (*uidInventoryDriver) PodInventoryUsesUID() bool { return true }
func (*uidInventoryDriver) PodNeedsReconciliation(pod *corev1.Pod) bool {
	return pod.Status.Phase != corev1.PodRunning
}
func (d *uidInventoryDriver) UpdatePod(context.Context, *corev1.Pod) error {
	d.updates++
	d.status.Phase = corev1.PodRunning
	return nil
}

func TestUIDInventoryRestoresPodIdentityAfterDriverRestart(t *testing.T) {
	pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
	pod.Namespace = "applications"
	orphan := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "456e4567-e89b-12d3-a456-426614174000", Namespace: "default", UID: "456e4567-e89b-12d3-a456-426614174000",
	}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "container-0000"}}}}
	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: string(pod.UID), Namespace: "default", UID: pod.UID}}
	d := &uidInventoryDriver{notifierDriver: notifierDriver{
		listPods: []*corev1.Pod{observed, orphan}, status: corev1.PodStatus{Phase: corev1.PodRunning},
	}}
	p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.GetPod(context.Background(), pod.Namespace, pod.Name)
	if err != nil || got.Name != pod.Name || got.Namespace != pod.Namespace || got.UID != pod.UID || got.Status.Phase != corev1.PodRunning {
		t.Fatalf("GetPod=%#v err=%v", got, err)
	}
	pods, err := p.GetPods(context.Background())
	if err != nil || len(pods) != 2 {
		t.Fatalf("GetPods=%#v err=%v", pods, err)
	}
	if pods[0].Name != pod.Name || pods[0].Namespace != pod.Namespace || pods[0].UID != pod.UID {
		t.Fatalf("known UID lost Kubernetes identity: %#v", pods[0])
	}
	if pods[1].Name != orphan.Name || pods[1].UID != orphan.UID || len(pods[1].Spec.Containers) != 1 {
		t.Fatalf("orphan lost cleanup identity: %#v", pods[1])
	}
	if observed.Name != string(pod.UID) {
		t.Fatal("rewrote driver-owned inventory")
	}
}

func TestUIDInventoryNeverAdoptsAnOldPodUID(t *testing.T) {
	pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
	old := pod.DeepCopy()
	old.UID = "456e4567-e89b-12d3-a456-426614174000"
	d := &uidInventoryDriver{notifierDriver: notifierDriver{listPods: []*corev1.Pod{old}}}
	p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetPod(context.Background(), pod.Namespace, pod.Name); !errdefs.IsNotFound(err) {
		t.Fatalf("old UID satisfied GetPod: %v", err)
	}
	pods, err := p.GetPods(context.Background())
	if err != nil || len(pods) != 1 || pods[0].UID != old.UID {
		t.Fatalf("old UID must remain an orphan: %#v, %v", pods, err)
	}
	if d.calls != 0 {
		t.Fatal("queried status for a different Pod UID")
	}
}

func TestGuardedStatusReconciliationRestartsStoppedApp(t *testing.T) {
	pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
	d := &uidInventoryDriver{notifierDriver: notifierDriver{status: corev1.PodStatus{Phase: corev1.PodPending}}}
	p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := p.statusOrReconcilePod(context.Background(), pod)
	if err != nil || status.Status.Phase != corev1.PodRunning || d.updates != 1 || d.calls != 2 {
		t.Fatalf("status=%#v err=%v updates=%d reads=%d", status, err, d.updates, d.calls)
	}
	if _, err := p.statusOrReconcilePod(context.Background(), pod); err != nil || d.updates != 1 {
		t.Fatalf("healthy app triggered another update: %v, updates=%d", err, d.updates)
	}
}

func TestGuardedStatusReconciliationHonorsMaintenanceAndDeletion(t *testing.T) {
	for _, terminating := range []bool{false, true} {
		t.Run(map[bool]string{false: "maintenance barrier", true: "terminating pod"}[terminating], func(t *testing.T) {
			pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
			d := &uidInventoryDriver{notifierDriver: notifierDriver{status: corev1.PodStatus{Phase: corev1.PodPending}}}
			p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, d, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if terminating {
				now := metav1.Now()
				pod.DeletionTimestamp = &now
			} else {
				// An incomplete coordinator rejects all writes. Recovery must
				// pass through it, just like explicit Deploy/Update operations.
				p.maintenance = &maintenance.Coordinator{}
			}
			_, err = p.statusOrReconcilePod(context.Background(), pod)
			if !terminating && err == nil {
				t.Fatal("maintenance write barrier was bypassed")
			}
			if d.updates != 0 || d.deployCalls != 0 {
				t.Fatalf("unexpected mutation: update=%d deploy=%d", d.updates, d.deployCalls)
			}
		})
	}
}
