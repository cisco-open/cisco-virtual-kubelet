// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// The common no-dispatch writer descriptor has no config/source frame. Permit
// structural matching only for that empty descriptor; a persisted operation must
// still carry the exact real source binding and pass the ordinary validator.
func siteRetirementDispatchKind(op *cvk.NetworkControllerConfigurationOperation) bool {
	if siteRetirementOperation(op) {
		return true
	}
	if op == nil || op.Phase != "" || op.ConfigName != "" || op.ConfigUID != "" || op.ConfigGeneration != 0 || op.ControllerGeneration != 0 || op.SourceUID != "" || op.SourceVersion != "" || op.TaskID != "" || op.SubmittedAt != nil {
		return false
	}
	descriptor := op.DeepCopy()
	descriptor.SourceUID, descriptor.SourceVersion = "descriptor", "descriptor"
	return siteRetirementOperation(descriptor)
}

// Resource writer through the existing one-use guard and shared journal. The
// private site planner must establish the complete retained lifecycle first.
func (a *adapter) siteRetirementWrite(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, targetID string) (*configurationWrite, error) {
	candidate, err := a.bindSiteRetirement(ctx, nc, config, source, targetID)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(candidate.resource, siteRetirementPrefix), ":")
	frame := &cvk.NetworkControllerConfigurationOperation{Section: "sites", Resource: candidate.resource, Action: "Delete", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, TargetID: candidate.identity.target, ParentID: candidate.identity.parent}
	if err := siteRetirementReceiptCapacity(nc.Status.Configuration, frame); err != nil {
		return nil, err
	}
	c := a.client
	mutation := &siteMutation{desired: desiredSite{kind: parts[0]}, id: candidate.identity.target, parentID: candidate.identity.parent, action: "Delete", method: http.MethodDelete, endpoint: siteDesignMutationEndpoint(parts[0]) + "/" + candidate.identity.target}
	write := &configurationWrite{resource: candidate.resource, action: "Delete", identity: candidate.identity, submit: func(ctx context.Context) (Task, error) { return c.submitSiteMutation(ctx, mutation) }}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		if err := siteRetirementReceiptCapacity(current.Status.Configuration, current.Status.Configuration.Operation); err != nil {
			return err
		}
		return candidate.check(ctx)
	})
	return write, nil
}
