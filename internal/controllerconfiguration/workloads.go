// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"fmt"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The configuration protocol currently requires empty workloads. The bound
// device worker independently checks strict native inventory before granting;
// this controller-side read catches newly bound Pods before claim and dispatch.
// It cannot replace that native check or authorize eviction.
func readEmptyConfigurationWorkloads(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation) error {
	if r == nil {
		return fmt.Errorf("configuration workload reservation is unavailable")
	}
	pods, err := workloaddrain.BoundActiveWorkloads(ctx, reader, r.Spec.Node.Name)
	if err != nil {
		return err
	}
	if len(pods) != 0 {
		return fmt.Errorf("configuration dispatch blocked by %d active Kubernetes workload(s)", len(pods))
	}
	return nil
}
