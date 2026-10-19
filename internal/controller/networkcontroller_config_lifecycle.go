// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"slices"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Adapter policy is metadata-only and never constructs an adapter or calls a
// product API. Direct reads and an optimistic patch prevent stale status from
// removing a finalizer on changed/replaced intent. Worker RBAC remains unchanged.
func (r *NetworkControllerReconciler) reconcileConfigLifecycles(ctx context.Context, expected *cvk.NetworkController) (changed, pending bool, err error) {
	policies := controlleradapter.ConfigurationLifecyclesFor(string(expected.Spec.Type))
	if len(policies) == 0 {
		return false, false, nil
	}
	configs, err := r.dependentNetworkControllerConfigs(ctx, expected)
	if err != nil {
		return false, false, err
	}
	reader := r.APIReader
	if reader == nil {
		return false, false, errors.New("configuration lifecycle requires uncached APIReader")
	}
	for _, candidate := range configs.Items {
		var config config.NetworkControllerConfig
		if err = reader.Get(ctx, client.ObjectKeyFromObject(&candidate), &config); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, pending, err
		}
		if config.Spec.ControllerRef.Name != expected.Name || config.Namespace != expected.Namespace {
			continue
		}
		heldCount := 0
		for _, policy := range policies {
			if slices.Contains(config.Finalizers, policy.Finalizer) {
				heldCount++
			}
		}
		// A changed family must never transfer an existing cleanup obligation.
		if heldCount > 1 {
			pending = pending || !config.DeletionTimestamp.IsZero()
			continue
		}
		for _, policy := range policies {
			held := slices.Contains(config.Finalizers, policy.Finalizer)
			if !config.DeletionTimestamp.IsZero() {
				if !held {
					continue
				}
				pending = true
				var current cvk.NetworkController
				if err = reader.Get(ctx, client.ObjectKeyFromObject(expected), &current); err != nil {
					return false, pending, err
				}
				if current.UID != expected.UID || current.Generation != expected.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || !policy.Completed(current.DeepCopy(), config.DeepCopy()) {
					continue
				}
				before := config.DeepCopy()
				config.Finalizers = slices.DeleteFunc(config.Finalizers, func(value string) bool { return value == policy.Finalizer })
				if err = r.Patch(ctx, &config, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
					return false, pending, err
				}
				return true, pending, nil
			}
			if !held && heldCount == 0 && policy.Eligible(expected.DeepCopy(), config.DeepCopy()) {
				before := config.DeepCopy()
				config.Finalizers = append(config.Finalizers, policy.Finalizer)
				if err = r.Patch(ctx, &config, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
					return false, pending, err
				}
				return true, pending, nil
			}
		}
	}
	return false, pending, nil
}

// Only deleting config status changes need to wake the manager; ordinary worker
// observations remain filtered by the existing spec/deletion predicate.
func configLifecycleStatusChanged(oldObject, newObject client.Object) bool {
	oldConfig, oldOK := oldObject.(*config.NetworkControllerConfig)
	newConfig, newOK := newObject.(*config.NetworkControllerConfig)
	if !oldOK || !newOK || newConfig.DeletionTimestamp.IsZero() {
		return false
	}
	// The manager polls while pending; this wakeup only shortens completion latency.
	return oldConfig.ResourceVersion != newConfig.ResourceVersion
}
