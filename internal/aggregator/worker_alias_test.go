// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package aggregator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWorkerPhysicalAliasCannotBypassRetirement(t *testing.T) {
	for _, target := range []string{"serial-a", "SERIAL-B"} {
		t.Run(target, func(t *testing.T) {
			r := &AggregatedReconciler{}
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "original", Namespace: "one"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "SERIAL-A"}, Status: cvk.DeviceStatus{NodeIdentity: &cvk.DeviceNodeIdentityStatus{PhysicalIdentity: "serial-b"}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closeStarted, releaseClose := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
			t.Cleanup(release)
			if err := r.startWorkerLoop(device, "old", ctx, cancel, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() error { close(closeStarted); <-releaseClose; return nil }); err != nil {
				t.Fatal(err)
			}
			old := r.managed[devKey(device)]
			// Later caller/source pruning must not erase the worker's original scope.
			device.Spec.PhysicalIdentity = "later-source"
			device.Status.NodeIdentity = nil
			alias := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "renamed", Namespace: "other"}, Spec: cvk.DeviceSpec{PhysicalIdentity: target}}
			attempt := func(want bool) *deviceWorker {
				t.Helper()
				nextCtx, nextCancel := context.WithCancel(context.Background())
				var ran atomic.Bool
				err := r.startWorkerLoop(alias, "new", nextCtx, nextCancel, func(ctx context.Context) error { ran.Store(true); <-ctx.Done(); return ctx.Err() }, func() error { return nil })
				if (err == nil) != want {
					t.Fatal("alias registration outcome", err)
				}
				if !want {
					if ran.Load() {
						t.Fatal("blocked alias ran")
					}
					return nil
				}
				return r.managed[devKey(alias)]
			}
			attempt(false) // Active owner covers both declared and retained serials.
			r.stopWorker(devKey(device))
			awaitWorkerSignal(t, closeStarted, "old cleanup started")
			attempt(false) // A different namespace/name cannot evade pending Close.
			// Unrelated serials stay independent of the pending worker.
			peer := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "peer", Namespace: "other"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "serial-c"}}
			peerCtx, peerCancel := context.WithCancel(context.Background())
			if err := r.startWorkerLoop(peer, "peer", peerCtx, peerCancel, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() error { return nil }); err != nil {
				t.Fatal("unrelated serial blocked", err)
			}
			peerWorker := r.managed[devKey(peer)]
			r.stopWorker(devKey(peer))
			awaitWorkerSignal(t, peerWorker.done, "peer cleanup")
			release()
			awaitWorkerSignal(t, old.done, "old cleanup completed")
			current := attempt(true)
			r.stopWorker(devKey(alias))
			awaitWorkerSignal(t, current.done, "alias cleanup")
		})
	}
}

func TestConcurrentPhysicalAliasesHaveOneWorker(t *testing.T) {
	r := &AggregatedReconciler{}
	const count = 8
	var accepted, rejected, ran atomic.Int32
	var wg sync.WaitGroup
	started := make(chan struct{}, count)
	for i := range count {
		wg.Go(func() {
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: string(rune('a' + i)), Namespace: "tenant"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "SERIAL-SHARED"}}
			ctx, cancel := context.WithCancel(context.Background())
			err := r.startWorkerLoop(device, "same-physical", ctx, cancel, func(ctx context.Context) error { ran.Add(1); started <- struct{}{}; <-ctx.Done(); return ctx.Err() }, func() error { return nil })
			if err == nil {
				accepted.Add(1)
			} else {
				rejected.Add(1)
			}
		})
	}
	wg.Wait()
	awaitWorkerSignal(t, started, "sole physical worker started")
	if accepted.Load() != 1 || rejected.Load() != count-1 || ran.Load() != 1 || len(r.managed) != 1 {
		t.Fatal("physical aliases overlapped", accepted.Load(), rejected.Load(), ran.Load())
	}
	for key, worker := range r.managed {
		r.stopWorker(key)
		awaitWorkerSignal(t, worker.done, "sole worker cleanup")
	}
}

func TestPhysicalAliasRetainsFailedOrUnknownCleanup(t *testing.T) {
	for _, mode := range []string{"failed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			r := &AggregatedReconciler{}
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "old", Namespace: "one"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "serial"}}
			if mode == "unknown" {
				r.managed = map[string]*deviceWorker{devKey(device): {cancel: func() {}, physicalIdentities: []string{"serial"}}}
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				if err := r.startWorkerLoop(device, "old", ctx, cancel, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() error { return errors.New("close not acknowledged") }); err != nil {
					t.Fatal(err)
				}
			}
			old := r.managed[devKey(device)]
			r.stopWorker(devKey(device))
			if mode == "failed" {
				awaitWorkerSignal(t, old.done, "failed cleanup")
			}
			alias := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: "two"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "SERIAL"}}
			ctx, cancel := context.WithCancel(context.Background())
			if err := r.startWorkerLoop(alias, "alias", ctx, cancel, func(context.Context) error { t.Error("unknown physical alias ran"); return nil }, func() error { return nil }); err == nil {
				t.Fatal("alias took over retained cleanup")
			}
		})
	}
}

func TestRejectedPhysicalAliasCleanupRemainsTracked(t *testing.T) {
	r := &AggregatedReconciler{}
	owner := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "one"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "serial"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.startWorkerLoop(owner, "owner", ctx, cancel, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	old := r.managed[devKey(owner)]
	alias := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "alias", Namespace: "two"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "SERIAL"}}
	cleanupStarted, releaseClose, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	t.Cleanup(release)
	closeErr := errors.New("candidate transport close failed")
	go func() {
		defer close(returned)
		ctx, cancel := context.WithCancel(context.Background())
		err := r.startWorkerLoop(alias, "rejected", ctx, cancel, func(context.Context) error { t.Error("rejected alias ran"); return nil }, func() error { close(cleanupStarted); <-releaseClose; return closeErr })
		if !errors.Is(err, closeErr) {
			t.Error("candidate cleanup failure was lost", err)
		}
	}()
	awaitWorkerSignal(t, cleanupStarted, "rejected candidate cleanup")
	r.stopWorker(devKey(owner))
	awaitWorkerSignal(t, old.done, "original owner complete")
	r.mu.Lock()
	allowed := r.workerStartAllowedLocked("third/new", []string{"serial"})
	r.mu.Unlock()
	if allowed {
		t.Fatal("candidate cleanup disappeared after owner exit")
	}
	release()
	awaitWorkerSignal(t, returned, "candidate failed cleanup returned")
	r.mu.Lock()
	allowed = r.workerStartAllowedLocked("third/new", []string{"serial"})
	r.mu.Unlock()
	if allowed {
		t.Fatal("failed candidate Close allowed alias takeover")
	}
}
