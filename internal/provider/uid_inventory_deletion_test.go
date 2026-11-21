// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type uidDeletionDriver struct {
	fakeTopologyDriver
	mu        sync.Mutex
	inventory []*corev1.Pod
	listErr   error
	statusErr error
	writeErr  error
	deleted   []types.UID
}

func (*uidDeletionDriver) PodInventoryUsesUID() bool { return true }
func (d *uidDeletionDriver) ListPods(context.Context) ([]*corev1.Pod, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*corev1.Pod(nil), d.inventory...), d.listErr
}
func (d *uidDeletionDriver) GetPodStatus(_ context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	if d.statusErr != nil {
		return nil, d.statusErr
	}
	out := pod.DeepCopy()
	out.Status.Phase = corev1.PodRunning
	return out, nil
}
func (d *uidDeletionDriver) DeployPod(context.Context, *corev1.Pod, corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) error {
	return d.writeErr
}
func (d *uidDeletionDriver) UpdatePod(context.Context, *corev1.Pod) error { return d.writeErr }
func (d *uidDeletionDriver) DeletePod(_ context.Context, pod *corev1.Pod) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.writeErr != nil {
		return d.writeErr
	}
	d.deleted = append(d.deleted, pod.UID)
	remaining := make([]*corev1.Pod, 0, len(d.inventory))
	for _, observed := range d.inventory {
		if observed.UID != pod.UID {
			remaining = append(remaining, observed)
		}
	}
	d.inventory = remaining
	return nil
}

func TestUIDInventoryDeletionAfterInformerRemoval(t *testing.T) {
	for _, rememberThrough := range []string{"get", "list", "status", "partial-create", "partial-update"} {
		t.Run(rememberThrough, func(t *testing.T) {
			ctx := context.Background()
			pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
			pod.Namespace = "applications"
			lister, indexer := podListerWithIndexer(t, pod)
			driver := &uidDeletionDriver{inventory: []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{
				Name: string(pod.UID), Namespace: "default", UID: pod.UID,
			}}}}
			provider, err := NewAppHostingProvider(ctx, &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: lister}, driver, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch rememberThrough {
			case "get":
				_, err = provider.GetPod(ctx, pod.Namespace, pod.Name)
			case "list":
				_, err = provider.GetPods(ctx)
			case "status":
				_, err = provider.statusOrReconcilePod(ctx, pod)
			case "partial-create":
				driver.writeErr = errors.New("connection lost after install")
				if err := provider.CreatePod(ctx, pod); err == nil {
					t.Fatal("expected partial creation error")
				}
				driver.writeErr = nil
			case "partial-update":
				driver.writeErr = errors.New("connection lost after activation")
				if err := provider.UpdatePod(ctx, pod); err == nil {
					t.Fatal("expected partial update error")
				}
				driver.writeErr = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := indexer.Delete(pod); err != nil {
				t.Fatal(err)
			}
			// This is upstream VK's force-delete path: it knows only name and
			// namespace after the Pod disappears from its informer.
			found, err := provider.GetPod(ctx, pod.Namespace, pod.Name)
			if err != nil || found.UID != pod.UID || found.Namespace != pod.Namespace || found.Name != pod.Name || len(found.Spec.Containers) != 1 {
				t.Fatalf("lost deletion identity: %#v, %v", found, err)
			}
			if err := provider.DeletePod(ctx, found); err != nil {
				t.Fatal(err)
			}
			if len(driver.deleted) != 1 || driver.deleted[0] != pod.UID {
				t.Fatalf("deleted UIDs = %v", driver.deleted)
			}
			if _, err := provider.GetPod(ctx, pod.Namespace, pod.Name); !errdefs.IsNotFound(err) {
				t.Fatalf("completed deletion still discovered: %v", err)
			}
		})
	}
}

func TestUIDInventoryDeletionNeverConfusesReusedNames(t *testing.T) {
	ctx := context.Background()
	old := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
	current := old.DeepCopy()
	current.UID = "456e4567-e89b-12d3-a456-426614174000"
	lister, indexer := podListerWithIndexer(t, old)
	driver := &uidDeletionDriver{inventory: []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: string(old.UID), Namespace: "default", UID: old.UID}},
		{ObjectMeta: metav1.ObjectMeta{Name: string(current.UID), Namespace: "default", UID: current.UID}},
	}}
	provider, err := NewAppHostingProvider(ctx, &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: lister}, driver, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetPod(ctx, old.Namespace, old.Name); err != nil {
		t.Fatal(err)
	}
	if err := indexer.Update(current); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetPod(ctx, current.Namespace, current.Name); err != nil {
		t.Fatal(err)
	}
	provider.rememberUIDPod(old) // a late callback for the former generation
	if err := provider.DeletePod(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := indexer.Delete(current); err != nil {
		t.Fatal(err)
	}
	provider.rememberUIDPod(old) // and a late callback after informer removal
	found, err := provider.GetPod(ctx, current.Namespace, current.Name)
	if err != nil || found.UID != current.UID {
		t.Fatalf("old cleanup erased/replaced the new identity: %#v, %v", found, err)
	}
	// A remembered identity is insufficient without matching live inventory.
	driver.inventory = nil
	if _, err := provider.GetPod(ctx, current.Namespace, current.Name); !errdefs.IsNotFound(err) {
		t.Fatalf("returned unconfirmed cached workload: %v", err)
	}
}

func TestUIDInventoryDeletionDoesNotMatchSyntheticNames(t *testing.T) {
	uid := types.UID("123e4567-e89b-12d3-a456-426614174000")
	driver := &uidDeletionDriver{inventory: []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{
		Name: string(uid), Namespace: "default", UID: uid,
	}}}}
	provider, err := NewAppHostingProvider(context.Background(), &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t)}, driver, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A deleted, unrelated Kubernetes Pod can have this UUID-shaped name.
	// Only the cached original UID, never name equality, proves ownership.
	if _, err := provider.GetPod(context.Background(), "default", string(uid)); !errdefs.IsNotFound(err) {
		t.Fatalf("synthetic inventory name authorized unrelated deletion: %v", err)
	}
}

func TestUIDInventoryPreservesTransportErrors(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
	lister, indexer := podListerWithIndexer(t, pod)
	transportErr := errors.New("AP inventory connection unavailable")
	driver := &uidDeletionDriver{listErr: transportErr}
	provider, err := NewAppHostingProvider(ctx, &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: lister}, driver, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []bool{false, true} {
		if absent {
			if err := indexer.Delete(pod); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := provider.GetPod(ctx, pod.Namespace, pod.Name); !errors.Is(err, transportErr) || errdefs.IsNotFound(err) {
			t.Fatalf("GetPod (absent=%t) suppressed a retryable inventory error: %v", absent, err)
		}
	}
	if _, err := provider.GetPods(ctx); !errors.Is(err, transportErr) || errdefs.IsNotFound(err) {
		t.Fatalf("GetPods changed an inventory error into NotFound: %v", err)
	}
}

func TestUIDInventoryPreservesStatusErrors(t *testing.T) {
	ctx := context.Background()
	pod := notifierPod("hello", "123e4567-e89b-12d3-a456-426614174000")
	transportErr := errors.New("AP status connection unavailable")
	driver := &uidDeletionDriver{statusErr: transportErr, inventory: []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{
		Name: string(pod.UID), Namespace: "default", UID: pod.UID,
	}}}}
	provider, err := NewAppHostingProvider(ctx, &v1alpha1.DeviceSpec{}, nodeutil.ProviderConfig{Pods: podLister(t, pod)}, driver, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetPodStatus(ctx, pod.Namespace, pod.Name); !errors.Is(err, transportErr) || errdefs.IsNotFound(err) {
		t.Fatalf("GetPodStatus changed a transport error into NotFound: %v", err)
	}
	if _, err := provider.GetPod(ctx, pod.Namespace, pod.Name); !errors.Is(err, transportErr) || errdefs.IsNotFound(err) {
		t.Fatalf("GetPod changed a detail error into NotFound: %v", err)
	}
	// When the application actually disappears between list and detail reads,
	// normalize the Kubernetes NotFound error for VK's provider contract.
	driver.statusErr = apierrors.NewNotFound(corev1.Resource("pods"), pod.Name)
	if _, err := provider.GetPod(ctx, pod.Namespace, pod.Name); !errdefs.IsNotFound(err) {
		t.Fatalf("GetPod did not preserve genuine missing workload: %v", err)
	}
}

func TestUIDInventoryMemoryBound(t *testing.T) {
	provider := &AppHostingProvider{driver: &uidDeletionDriver{}}
	for index := 0; index <= maxUIDInventoryPods; index++ {
		provider.rememberUIDPod(notifierPod(fmt.Sprintf("pod-%d", index), fmt.Sprintf("uid-%d", index)))
	}
	if len(provider.uidInventoryPods) > maxUIDInventoryPods {
		t.Fatal("UID inventory memory grows without a bound")
	}
	if provider.rememberedUIDPod("default", fmt.Sprintf("pod-%d", maxUIDInventoryPods)) == nil {
		t.Fatal("the most recent identity was evicted")
	}
}
