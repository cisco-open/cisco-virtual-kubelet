// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Preparation is opt-in through the existing group runtime. Native metadata
// prerequisites must already converge; no source component is discarded to
// select a policy. The shared manager/worker actors publish and claim members.
func (r *configReconciler) prepareConfigurationExtranetGroup(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, bool, error) {
	if !r.a.configurationGroupAdmissionReady() || !supportedExtranetPreparation(config) || source == nil {
		return false, false, nil
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return false, false, nil
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return false, false, nil
	}
	fabric, _ := root["fabric"].(map[string]any)
	if _, exists := fabric["extranet_policies"]; !exists {
		return false, false, nil
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	prepareCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	before, targets, err := r.a.planDeclaredExtranet(prepareCtx, config, source, "")
	if err != nil {
		return true, false, err
	}
	if err := r.a.claimConfigurationSection(prepareCtx, config, source); err != nil {
		return true, false, err
	}
	observed, err := r.a.currentController(prepareCtx)
	if err != nil {
		return true, false, err
	}
	if observed.Status.Configuration != nil && observed.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	next, freshTargets, err := r.a.planDeclaredExtranet(prepareCtx, config, source, "")
	if err != nil {
		return true, false, err
	}
	if (before == nil) != (next == nil) || !reflect.DeepEqual(targets, freshTargets) {
		return true, false, errConfigurationStale
	}
	if before != nil && (before.resource != next.resource || before.action != next.action || !reflect.DeepEqual(before.identity, next.identity)) {
		return true, false, errConfigurationStale
	}
	if err := r.a.checkConfigurationSnapshot(prepareCtx, config, source); err != nil {
		return true, false, err
	}
	fresh, err := r.a.currentController(prepareCtx)
	if err != nil {
		return true, false, err
	}
	if fresh.UID != observed.UID || fresh.ResourceVersion != observed.ResourceVersion {
		return true, false, errConfigurationStale
	}
	if next == nil {
		return true, true, nil
	}
	identity := next.identity
	identity.groupTargets = freshTargets
	_, err = r.a.prepareConfiguration(prepareCtx, config, source, "fabric", next.resource, next.action, identity)
	return true, false, err
}
