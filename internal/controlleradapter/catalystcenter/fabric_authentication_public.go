// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (r *configReconciler) prepareFabricAuthenticationGroup(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, bool, error) {
	plan, err := r.a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return false, false, nil
	}
	auth, ok := plan.configurationExecutor.(*fabricAuthConfiguration)
	if !ok {
		return false, false, nil
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	prepareCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for _, w := range auth.desired {
		if _, err := r.a.declaredFabricAuthentication(prepareCtx, source, w.site.path); err != nil {
			return true, false, err
		}
	}
	if err := r.a.claimConfigurationSection(prepareCtx, config, source); err != nil {
		return true, false, err
	}
	observed, err := r.a.currentController(prepareCtx)
	if err != nil {
		return true, false, err
	}
	var next *configurationWrite
	for _, w := range auth.desired {
		write, targets, err := r.a.client.planManagedFabricAuthentication(prepareCtx, r.a.statusReader, config.Namespace, w)
		if err != nil {
			return true, false, err
		}
		if write != nil && next == nil {
			copy := *write
			copy.identity.groupTargets = targets
			next = &copy
		}
	}
	if err := r.a.checkConfigurationSnapshot(prepareCtx, config, source); err != nil {
		return true, false, err
	}
	fresh, err := r.a.currentController(prepareCtx)
	if err != nil {
		return true, false, err
	}
	if observed.UID != fresh.UID || observed.ResourceVersion != fresh.ResourceVersion {
		return true, false, errConfigurationStale
	}
	if next == nil {
		return true, true, nil
	}
	_, err = r.a.prepareConfiguration(prepareCtx, config, source, "fabric", next.resource, next.action, next.identity)
	return true, false, err
}

func (r *configReconciler) recoverFabricAuthenticationGroup(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error) error {
	parts, err := parseFabricAuthManagedResource(op.Resource)
	if err != nil {
		return err
	}
	plan, err := r.a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return err
	}
	auth, ok := plan.configurationExecutor.(*fabricAuthConfiguration)
	if !ok {
		return errConfigurationTransition
	}
	selected := ""
	for _, w := range auth.desired {
		if fabricAuthDigest([]string{w.site.path, w.profile.Name}) == parts[0] {
			if selected != "" {
				return errConfigurationStale
			}
			selected = w.site.path
		}
	}
	if selected == "" {
		return errConfigurationStale
	}
	switch op.Phase {
	case "Prepared":
		if err := r.a.revalidateFabricAuthenticationOperation(ctx, config, source, selected, op, "Prepared"); err != nil {
			return err
		}
		permit, err := r.a.claimConfigurationGroup(ctx)
		if err != nil {
			return err
		}
		_, err = r.a.dispatchFabricAuthentication(ctx, config, source, selected, permit)
		return err
	case "Verifying":
		return r.a.recordFabricAuthenticationApplication(ctx, config, op.ID, selected, members, authorize, time.Now())
	default:
		return r.a.beginFabricAuthenticationVerification(ctx, config, source, selected, members, authorize, time.Now())
	}
}
