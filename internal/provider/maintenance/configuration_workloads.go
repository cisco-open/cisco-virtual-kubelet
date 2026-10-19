// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package maintenance

import (
	"context"
	"fmt"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
	core "k8s.io/api/core/v1"
)

// PrepareConfigurationWorkloads establishes the existing maintenance guard and
// proves empty Kubernetes and device inventory before an initial grant. The
// scanner must be the driver's explicitly supported ListPodsForDrain capability,
// never its compatibility ListPods fallback. The caller supplies uncached reads.
// It does not drain, delete workloads, publish a grant or authorize dispatch.
func (c *Coordinator) PrepareConfigurationWorkloads(ctx context.Context, reservation *ops.ControllerConfigurationReservation, strictScan func(context.Context) ([]*core.Pod, error)) error {
	if strictScan == nil {
		return fmt.Errorf("configuration preparation requires strict device application inventory")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := c.PrepareConfigurationMaintenance(ctx, reservation); err != nil {
		return err
	}
	checkPods := func() error {
		pods, err := workloaddrain.BoundActiveWorkloads(ctx, c.Client, c.NodeName)
		if err != nil {
			return fmt.Errorf("configuration workload inventory: %w", err)
		}
		if len(pods) != 0 {
			return fmt.Errorf("configuration preparation blocked by %d active Kubernetes workload(s)", len(pods))
		}
		return nil
	}
	if err := workloaddrain.VerifyEmptyWorkloads(ctx, c.Client, c.NodeName, strictScan); err != nil {
		return fmt.Errorf("configuration workload preparation: %w", err)
	}
	if err := c.PrepareConfigurationMaintenance(ctx, reservation); err != nil {
		return fmt.Errorf("configuration authority changed during inventory: %w", err)
	}
	return checkPods()
}
