// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type staleUIDRecoveryDriver struct {
	notifierDriver
	updates  int
	mutated  *corev1.Pod
	onStatus func()
}

func (*staleUIDRecoveryDriver) PodInventoryUsesUID() bool { return true }
func (*staleUIDRecoveryDriver) PodNeedsReconciliation(pod *corev1.Pod) bool {
	return pod.Status.Phase != corev1.PodRunning
}
func (d *staleUIDRecoveryDriver) GetPodStatus(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	if d.onStatus != nil {
		hook := d.onStatus
		d.onStatus = nil
		hook()
	}
	return d.notifierDriver.GetPodStatus(ctx, pod)
}
func (d *staleUIDRecoveryDriver) DeployPod(ctx context.Context, pod *corev1.Pod, secrets corev1listers.SecretNamespaceLister, configs corev1listers.ConfigMapNamespaceLister) error {
	d.mutated = pod.DeepCopy()
	d.status.Phase = corev1.PodRunning
	return d.notifierDriver.DeployPod(ctx, pod, secrets, configs)
}
func (d *staleUIDRecoveryDriver) UpdatePod(_ context.Context, pod *corev1.Pod) error {
	d.updates++
	d.mutated = pod.DeepCopy()
	d.status.Phase = corev1.PodRunning
	return nil
}

func TestUIDRecoveryRejectsStaleDesiredSnapshots(t *testing.T) {
	for _, missing := range []bool{false, true} {
		path := map[bool]string{false: "stopped", true: "missing"}[missing]
		for _, changed := range []string{"absent", "replaced", "terminating", "no informer"} {
			t.Run(path+"/"+changed, func(t *testing.T) {
				stale := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
				current := stale.DeepCopy()
				var lister corev1listers.PodLister
				switch changed {
				case "absent":
					lister = podLister(t)
				case "replaced":
					current.UID = "456e4567-e89b-12d3-a456-426614174000"
					lister = podLister(t, current)
				case "terminating":
					now := metav1.Now()
					current.DeletionTimestamp = &now
					lister = podLister(t, current)
				}
				d := &staleUIDRecoveryDriver{notifierDriver: notifierDriver{status: corev1.PodStatus{Phase: corev1.PodPending}, missingUntilDeploy: missing}}
				p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: lister}, d, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.statusOrReconcilePod(context.Background(), stale)
				if d.updates != 0 || d.deployCalls != 0 {
					t.Fatalf("stale callback mutated workload: updates=%d deploys=%d", d.updates, d.deployCalls)
				}
			})
		}
	}
}

func TestUIDRecoveryRechecksDesiredStateAfterStatusRead(t *testing.T) {
	for _, missing := range []bool{false, true} {
		path := map[bool]string{false: "stopped", true: "missing"}[missing]
		for _, changed := range []string{"absent", "replaced", "terminating"} {
			t.Run(path+"/"+changed, func(t *testing.T) {
				stale := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
				lister, indexer := podListerWithIndexer(t, stale)
				d := &staleUIDRecoveryDriver{notifierDriver: notifierDriver{status: corev1.PodStatus{Phase: corev1.PodPending}, missingUntilDeploy: missing}}
				d.onStatus = func() {
					current := stale.DeepCopy()
					var err error
					switch changed {
					case "absent":
						err = indexer.Delete(stale)
					case "replaced":
						current.UID = "456e4567-e89b-12d3-a456-426614174000"
						err = indexer.Update(current)
					case "terminating":
						now := metav1.Now()
						current.DeletionTimestamp = &now
						err = indexer.Update(current)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: lister}, d, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.statusOrReconcilePod(context.Background(), stale)
				if d.updates != 0 || d.deployCalls != 0 {
					t.Fatalf("informer change during status read was ignored: updates=%d deploys=%d", d.updates, d.deployCalls)
				}
			})
		}
	}
}

func TestUIDRecoveryUsesCurrentDesiredSpec(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "missing"}[missing], func(t *testing.T) {
			stale := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
			stale.Spec.Containers[0].Image = "https://packages.example/stale.tar"
			current := stale.DeepCopy()
			current.Spec.Containers[0].Image = "https://packages.example/current.tar"
			current.Annotations = map[string]string{"desired-revision": "current"}
			d := &staleUIDRecoveryDriver{notifierDriver: notifierDriver{status: corev1.PodStatus{Phase: corev1.PodPending}, missingUntilDeploy: missing}}
			p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, current)}, d, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			status, err := p.statusOrReconcilePod(context.Background(), stale)
			if err != nil || status == nil || status.Status.Phase != corev1.PodRunning {
				t.Fatalf("fresh recovery status=%#v error=%v", status, err)
			}
			if d.mutated == nil || !reflect.DeepEqual(d.mutated.Spec, current.Spec) || !reflect.DeepEqual(d.mutated.Annotations, current.Annotations) {
				t.Fatalf("recovery did not use current desired object: %#v", d.mutated)
			}
			if missing && (d.deployCalls != 1 || d.updates != 0) || !missing && (d.updates != 1 || d.deployCalls != 0) {
				t.Fatalf("unexpected recovery actions: updates=%d deploys=%d", d.updates, d.deployCalls)
			}
		})
	}
}

func TestUIDRecoveryRechecksAfterWaitingForDeletionGate(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "missing"}[missing], func(t *testing.T) {
			stale := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
			lister, indexer := podListerWithIndexer(t, stale)
			statusRead := make(chan struct{})
			d := &staleUIDRecoveryDriver{notifierDriver: notifierDriver{status: corev1.PodStatus{Phase: corev1.PodPending}, missingUntilDeploy: missing}, onStatus: func() { close(statusRead) }}
			p, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: lister}, d, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			releaseGate := sync.OnceFunc(func() { close(release) })
			defer releaseGate()
			holderDone := make(chan error, 1)
			go func() {
				holderDone <- p.withUIDMutation(context.Background(), func(context.Context) error {
					close(entered)
					<-release
					return nil
				})
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("deletion did not enter mutation gate")
			}
			recoveryDone := make(chan error, 1)
			go func() { _, err := p.statusOrReconcilePod(context.Background(), stale); recoveryDone <- err }()
			select {
			case <-statusRead:
			case <-time.After(2 * time.Second):
				t.Fatal("recovery did not read device status")
			}
			// Simulate deletion completing while an old detached status callback
			// waits to acquire the same gate. Its former desired state is gone.
			if err := indexer.Delete(stale); err != nil {
				t.Fatal(err)
			}
			releaseGate()
			select {
			case err := <-holderDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("deletion gate did not release")
			}
			select {
			case <-recoveryDone:
			case <-time.After(2 * time.Second):
				t.Fatal("recovery did not finish")
			}
			if d.updates != 0 || d.deployCalls != 0 {
				t.Fatalf("queued stale recovery resurrected the workload: updates=%d deploys=%d", d.updates, d.deployCalls)
			}
		})
	}
}

func TestUIDMutationGateCancellation(t *testing.T) {
	p := &AppHostingProvider{driver: &staleUIDRecoveryDriver{}}
	entered, release := make(chan struct{}), make(chan struct{})
	releaseGate := sync.OnceFunc(func() { close(release) })
	defer releaseGate()
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- p.withUIDMutation(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("mutation holder did not acquire gate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiterDone := make(chan error, 1)
	called := false
	go func() {
		waiterDone <- p.withUIDMutation(ctx, func(context.Context) error { called = true; return nil })
	}()
	cancel()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) || called {
			t.Fatalf("cancelled gate waiter: error=%v callback=%t", err, called)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled mutation waiter remained blocked")
	}
	releaseGate()
	select {
	case err := <-holderDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mutation gate holder did not release")
	}
}
