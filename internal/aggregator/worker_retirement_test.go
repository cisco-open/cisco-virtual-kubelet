// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package aggregator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func awaitWorkerSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("missing worker observation:", what)
	}
}

func TestWorkerRetirementWaitsForRunAndTransport(t *testing.T) {
	device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "tenant"}}
	r := &AggregatedReconciler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runStarted, runCancelled, releaseRun := make(chan struct{}), make(chan struct{}), make(chan struct{})
	closeStarted, releaseClose := make(chan struct{}), make(chan struct{})
	var cancels atomic.Int32
	err := r.startWorkerLoop(device, "old", ctx, func() { cancels.Add(1); cancel() }, func(ctx context.Context) error {
		close(runStarted)
		<-ctx.Done()
		close(runCancelled)
		<-releaseRun
		return ctx.Err()
	}, func() error { close(closeStarted); <-releaseClose; return nil })
	if err != nil {
		t.Fatal(err)
	}
	awaitWorkerSignal(t, runStarted, "run started")
	key := devKey(device)
	old := r.managed[key]
	var stops sync.WaitGroup
	for range 8 {
		stops.Go(func() { r.stopWorker(key) })
	}
	stops.Wait()
	awaitWorkerSignal(t, runCancelled, "cancellation delivered")
	if r.retiredWorkersDrained(key) || len(r.managed) != 0 || cancels.Load() != 1 {
		t.Fatal("cancellation was mistaken for completion", cancels.Load())
	}
	tryReplacement := func() {
		t.Helper()
		nextCtx, nextCancel := context.WithCancel(context.Background())
		ran := false
		cleaned := false
		err := r.startWorkerLoop(device, "replacement", nextCtx, nextCancel, func(context.Context) error { ran = true; return nil }, func() error { cleaned = true; return nil })
		if err == nil || ran || !cleaned {
			t.Fatal("replacement ran before previous retirement", err)
		}
	}
	tryReplacement()
	close(releaseRun)
	awaitWorkerSignal(t, closeStarted, "transport cleanup started")
	if r.retiredWorkersDrained(key) {
		t.Fatal("Run return was mistaken for transport closure")
	}
	tryReplacement()
	close(releaseClose)
	awaitWorkerSignal(t, old.done, "transport cleanup completed")
	if !r.retiredWorkersDrained(key) {
		t.Fatal("completed retirement was not observed")
	}
	nextCtx, nextCancel := context.WithCancel(context.Background())
	nextStarted := make(chan struct{})
	if err := r.startWorkerLoop(device, "new", nextCtx, nextCancel, func(ctx context.Context) error { close(nextStarted); <-ctx.Done(); return ctx.Err() }, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	awaitWorkerSignal(t, nextStarted, "replacement starts after cleanup")
	next := r.managed[key]
	r.stopWorker(key)
	awaitWorkerSignal(t, next.done, "replacement retirement")
}

func TestWorkerRetirementRetainsCleanupFailure(t *testing.T) {
	device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "tenant"}}
	r := &AggregatedReconciler{}
	ctx, cancel := context.WithCancel(context.Background())
	if err := r.startWorkerLoop(device, "old", ctx, cancel, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() error { return errors.New("transport close not acknowledged") }); err != nil {
		t.Fatal(err)
	}
	key := devKey(device)
	old := r.managed[key]
	r.stopWorker(key)
	awaitWorkerSignal(t, old.done, "failed transport cleanup")
	for range 2 {
		if r.retiredWorkersDrained(key) {
			t.Fatal("failed close permitted automatic replacement")
		}
	}
	if len(r.retiring[key]) != 1 {
		t.Fatal("failed retirement evidence lost")
	}
}

func TestWorkerRetirementUnknownIsNotAbsent(t *testing.T) {
	r := &AggregatedReconciler{managed: map[string]*deviceWorker{"tenant/edge": {cancel: func() {}}}}
	r.stopWorker("tenant/edge")
	if r.retiredWorkersDrained("tenant/edge") {
		t.Fatal("missing completion handle was treated as finished")
	}
}

func TestAggregatorRebindsAfterObservedRetirement(t *testing.T) {
	registerStubFakeDriver(t)
	for _, mode := range []string{"generation", "device-uid", "node-identity"} {
		t.Run(mode, func(t *testing.T) {
			root, cancel := context.WithCancel(context.Background())
			defer cancel()
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "tenant", UID: "original", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverFAKE, Address: "198.51.100.101", Password: "test-only"}, Status: cvk.DeviceStatus{Conditions: []metav1.Condition{{Type: cvk.CiscoDeviceConditionAggregatorOwned, Status: metav1.ConditionTrue}}}}
			c := fake.NewClientBuilder().WithScheme(aggScheme(t)).WithStatusSubresource(device).WithObjects(device).Build()
			if err := c.Get(root, client.ObjectKeyFromObject(device), device); err != nil {
				t.Fatal(err)
			}
			r := &AggregatedReconciler{Client: c, rootCtx: root}
			closeStarted, releaseClose := make(chan struct{}), make(chan struct{})
			oldCtx, oldCancel := context.WithCancel(root)
			if err := r.startWorkerLoop(device, specHash(device, device.Spec.Password), oldCtx, oldCancel, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() error { close(closeStarted); <-releaseClose; return nil }); err != nil {
				t.Fatal(err)
			}
			key := devKey(device)
			old := r.managed[key]
			switch mode {
			case "generation":
				device.Generation++
			case "device-uid":
				if err := c.Delete(root, device); err != nil {
					t.Fatal(err)
				}
				device.ResourceVersion = ""
				device.UID = "replacement"
				if err := c.Create(root, device); err != nil {
					t.Fatal(err)
				}
			case "node-identity":
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{PhysicalIdentity: "bootstrap123", NodeUID: "new-node"}
			}
			if mode != "device-uid" {
				if mode == "node-identity" {
					if err := c.Status().Update(root, device); err != nil {
						t.Fatal(err)
					}
				} else if err := c.Update(root, device); err != nil {
					t.Fatal(err)
				}
			}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(device)}
			result, err := r.Reconcile(root, req)
			if err != nil || result.RequeueAfter <= 0 || len(r.managed) != 0 {
				t.Fatal("rebind did not wait for previous cleanup", err, result)
			}
			awaitWorkerSignal(t, closeStarted, "old transport cleanup")
			close(releaseClose)
			awaitWorkerSignal(t, old.done, "old worker complete")
			if _, err := r.Reconcile(root, req); err != nil {
				t.Fatal(err)
			}
			current := r.managed[key]
			if current == nil || current == old || current.bindingHash == old.bindingHash {
				t.Fatal("new binding reused old worker")
			}
			r.stopWorker(key)
			awaitWorkerSignal(t, current.done, "new worker cleanup")
		})
	}
}
