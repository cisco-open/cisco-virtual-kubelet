// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package aggregator

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider"
)

func (r *AggregatedReconciler) startFMCWorker(dev *ciskov1.CiscoDevice, password, hash string) error {
	applier, err := provider.NewFMCConfigApplier(&dev.Spec, password)
	if err != nil {
		return fmt.Errorf("fmc config applier: %w", err)
	}
	devCtx, cancel := context.WithCancel(r.rootCtx)
	rec := &provider.FMCConfigReconciler{
		Client:     r.Client,
		DeviceName: dev.Name,
		Applier:    applier,
		Recorder:   r.Recorder,
	}

	r.mu.Lock()
	r.managed[devKey(dev)] = &deviceWorker{cancel: cancel, specHash: hash}
	r.mu.Unlock()

	go func() {
		defer applier.Close()
		if err := rec.Run(devCtx); err != nil && err != context.Canceled {
			if r.Recorder != nil {
				r.Recorder.Eventf(dev, corev1.EventTypeWarning, "AggregatorFMCWorkerExit", "FMC reconciler exited: %v", err)
			}
		}
	}()
	return nil
}
