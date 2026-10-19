// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func siteSingularOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	resource := validSitePath(op.Resource) && op.Resource != "Global"
	if strings.HasPrefix(op.Resource, sitePrivateCreatePrefix) || siteRecreateSelected(op.Resource) || siteAppliedSelected(op) {
		parts, ok := sitePrivateOperationParts(op.Resource)
		resource = ok && ((strings.HasPrefix(op.Resource, sitePrivateCreatePrefix) || siteRecreateSelected(op.Resource)) && op.Action == "Create" || siteAppliedSelected(op) && op.Action == "Update" && canonicalTemplateUUID(op.TargetID))
		if resource && (op.Phase != "" || op.SubmittedAt != nil || op.SourceUID != "" || op.SourceVersion != "") {
			resource = parts[2] == "i" && op.SourceUID == "" && op.SourceVersion == "" || parts[2] == "c" && op.SourceUID != "" && op.SourceVersion != ""
		}
	}
	return op.Section == "sites" && resource && validAPIID(op.ParentID) && op.TargetID != op.ParentID &&
		(op.Action == "Create" && op.TargetID == "" || op.Action == "Update" && validAPIID(op.TargetID)) &&
		op.CreatedResourceID == "" && op.TargetSerial == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.CancellationRequestedAt == nil && op.PreparedAt == nil && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
}

func (a *adapter) configureSingularSiteDispatch(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, executor *siteConfigurationExecutor) {
	executor.submissionGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "sites", resource, action, identity, siteSingularOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if err := executor.creatorRecordsMatch(state); err != nil {
				return err
			}
			if op.Action == "Create" {
				return siteCreateCapacity(state, op)
			}
			if siteAppliedSelected(op) {
				return siteApplyCapacity(state, op)
			}
			return nil
		})
	}
}
