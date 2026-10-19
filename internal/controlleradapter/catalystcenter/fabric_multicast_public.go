// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"time"
)

// The common recovery route has already validated source/journal identity and
// all-member authorization. Select the exact logical declaration from the
// resource hash; each operation-specific bridge rechecks native/managed scope.
func (r *configReconciler) recoverFabricMulticastVNGroup(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error) error {
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
	identity := strings.Split(op.Resource, ":")[1]
	path, name := "", ""
	for _, site := range multicast.desired {
		for _, want := range site.intent.networks {
			if strings.Split(multicastVNResource(site.path, want.name, ""), ":")[1] == identity {
				if name != "" {
					return errConfigurationStale
				}
				path, name = site.path, want.name
			}
		}
	}
	if name == "" {
		return errConfigurationStale
	}
	switch op.Phase {
	case "Prepared":
		if err := r.a.revalidateFabricMulticastVNOperation(ctx, config, source, path, name, op, "Prepared"); err != nil {
			return err
		}
		permit, err := r.a.claimConfigurationGroup(ctx)
		if err != nil {
			return err
		}
		_, err = r.a.dispatchFabricMulticastVN(ctx, config, source, path, name, permit)
		return err
	case "Verifying":
		return r.a.recordFabricMulticastVNApplication(ctx, config, op.ID, path, name, members, authorize, time.Now())
	default:
		return r.a.beginFabricMulticastVNVerification(ctx, config, source, path, name, members, authorize, time.Now())
	}
}
