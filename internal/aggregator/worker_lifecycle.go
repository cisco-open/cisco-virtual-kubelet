// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package aggregator

import (
	"context"
	"errors"
	"fmt"
	"slices"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
)

// Only closed completion channels with successful transport cleanup permit
// replacement. Nil channels and cleanup failures retain unknown local state.
// This is in-process evidence, never native completion or restart-safe authority.
func (r *AggregatedReconciler) retiredWorkersDrainedLocked(key string) bool {
	retained := r.retiring[key][:0]
	for _, worker := range r.retiring[key] {
		select {
		case <-worker.done:
			if worker.closeErr != nil {
				retained = append(retained, worker)
			}
		default:
			retained = append(retained, worker)
		}
	}
	if len(retained) == 0 {
		delete(r.retiring, key)
		return true
	}
	r.retiring[key] = retained
	return false
}

func (r *AggregatedReconciler) retiredWorkersDrained(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.retiredWorkersDrainedLocked(key)
}

// Physical footprint comparison is local to this manager. It is done only on
// startup/registration, without another independently maintained identity index.
func (r *AggregatedReconciler) workerStartAllowedLocked(key string, physical []string) bool {
	if r.managed[key] != nil || !r.retiredWorkersDrainedLocked(key) {
		return false
	}
	overlaps := func(worker *deviceWorker) bool {
		for _, identity := range physical {
			if slices.Contains(worker.physicalIdentities, identity) {
				return true
			}
		}
		return false
	}
	for _, worker := range r.managed {
		if overlaps(worker) {
			return false
		}
	}
	for other := range r.retiring {
		if r.retiredWorkersDrainedLocked(other) {
			continue
		}
		for _, worker := range r.retiring[other] {
			if overlaps(worker) {
				return false
			}
		}
	}
	return true
}

func (r *AggregatedReconciler) stopWorker(key string) {
	r.mu.Lock()
	w, ok := r.managed[key]
	if ok {
		delete(r.managed, key)
		r.retainWorkerLocked(key, w)
	}
	r.mu.Unlock()
	if ok && w.cancel != nil {
		w.stopOnce.Do(w.cancel)
	}
}

// startWorkerLoop serializes registration with local retirement. Both platform
// constructors supply their existing Run loop and transport cleanup. Completion
// is published only after Run returns, cancellation and cleanup finish.
func (r *AggregatedReconciler) startWorkerLoop(device *cvk.CiscoDevice, hash string, ctx context.Context, cancel context.CancelFunc, run func(context.Context) error, cleanup func() error) error {
	physical, err := contract.DevicePhysicalIdentities(device)
	if err != nil {
		cancel()
		return errors.Join(err, cleanup())
	}
	worker := &deviceWorker{cancel: cancel, specHash: hash, bindingHash: workerBindingHash(device), physicalIdentities: physical, done: make(chan struct{})}
	key := devKey(device)
	if err := ctx.Err(); err != nil {
		r.mu.Lock()
		r.retainWorkerLocked(key, worker)
		r.mu.Unlock()
		return finishUnstartedWorker(worker, cleanup, err)
	}
	r.mu.Lock()
	if !r.workerStartAllowedLocked(key, physical) {
		r.retainWorkerLocked(key, worker)
		r.mu.Unlock()
		return finishUnstartedWorker(worker, cleanup, fmt.Errorf("aggregator worker %s has an active or unresolved worker in its physical scope", key))
	}
	if r.managed == nil {
		r.managed = map[string]*deviceWorker{}
	}
	r.managed[key] = worker
	r.mu.Unlock()
	go func() {
		err := run(ctx)
		worker.stopOnce.Do(cancel)
		worker.closeErr = cleanup()
		close(worker.done) // Publishes closeErr; readers inspect it only after receive.
		if r.Recorder != nil {
			if err != nil && !errors.Is(err, context.Canceled) {
				r.Recorder.Eventf(device, core.EventTypeWarning, "AggregatorWorkerExit", "in-process reconciler exited: %v", err)
			}
			if worker.closeErr != nil {
				r.Recorder.Eventf(device, core.EventTypeWarning, "AggregatorWorkerCloseFailed", "retaining worker retirement after transport cleanup failure")
			}
		}
	}()
	return nil
}

// A candidate losing registration can still own a just-opened transport. Retain
// its footprint before cleanup, so a slow or failed Close cannot disappear into
// a concurrent ownership change even though the config loop never started.
func (r *AggregatedReconciler) retainWorkerLocked(key string, worker *deviceWorker) {
	if r.retiring == nil {
		r.retiring = map[string][]*deviceWorker{}
	}
	r.retiring[key] = append(r.retiring[key], worker)
}

func finishUnstartedWorker(worker *deviceWorker, cleanup func() error, reason error) error {
	worker.stopOnce.Do(worker.cancel)
	worker.closeErr = cleanup()
	close(worker.done)
	return errors.Join(reason, worker.closeErr)
}
