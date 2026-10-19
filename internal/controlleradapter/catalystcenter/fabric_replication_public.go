// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// The common recovery route has already validated source/journal identity and
// all-member authorization. Select the exact logical declaration from the
// resource hash; each operation-specific bridge rechecks native/managed scope.
func (r *configReconciler) recoverFabricReplicationGroup(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error) error {
	if source == nil || op == nil || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) {
		return errConfigurationTransition
	}
	plan, err := r.a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return err
	}
	multicast, ok := plan.configurationExecutor.(*fabricMulticastConfiguration)
	if !ok {
		return errConfigurationTransition
	}
	path := ""
	for _, site := range multicast.desired {
		if site.intent.replication == "" {
			continue
		}
		state, err := r.a.client.fabricReplicationState(ctx, site.path)
		if err != nil {
			return err
		}
		if state.fabricID == op.TargetID && state.siteID == op.ParentID {
			if path != "" {
				return errConfigurationStale
			}
			path = site.path
		}
	}
	if path == "" {
		return errConfigurationStale
	}
	switch op.Phase {
	case "Prepared":
		if err := r.a.revalidateFabricReplicationOperation(ctx, config, source, path, op, "Prepared"); err != nil {
			return err
		}
		permit, err := r.a.claimConfigurationGroup(ctx)
		if err != nil {
			return err
		}
		_, err = r.a.dispatchFabricReplication(ctx, config, source, path, permit)
		return err
	case "Verifying":
		return r.a.recordFabricReplicationApplication(ctx, config, op.ID, path, members, authorize, time.Now())
	default:
		return r.a.beginFabricReplicationVerification(ctx, config, source, path, members, authorize, time.Now())
	}
}
