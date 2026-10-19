// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type templateDeploymentVersionBinding struct {
	baseID  string
	version templateVersion
}

func parseTemplateDeploymentResource(resource string) (*templateDeploymentVersionBinding, error) {
	base, _, err := splitTemplateComparisonResource(resource)
	if err != nil {
		return nil, err
	}
	resource = base
	suffix, ok := strings.CutPrefix(resource, "template-deploy:")
	if !ok || len(resource) > 512 {
		return nil, errConfigurationTransition
	}
	parts := strings.Split(suffix, "/")
	if len(parts) != 4 || !validAPIID(parts[0]) || !validAPIID(parts[1]) || parts[0] == parts[1] || len(parts[3]) != 64 || strings.ToLower(parts[3]) != parts[3] {
		return nil, errConfigurationTransition
	}
	if _, err := hex.DecodeString(parts[3]); err != nil {
		return nil, errConfigurationTransition
	}
	number, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != parts[2] {
		return nil, errConfigurationTransition
	}
	return &templateDeploymentVersionBinding{baseID: parts[0], version: templateVersion{ID: parts[1], Number: number}}, nil
}

// Native receipt observation, not owner/source authorization or journal
// advancement. Recovery never POSTs and never substitutes the latest version.
func (c *client) observeRegularTemplateDeployment(ctx context.Context, device inventoryDeviceIntent, target regularTemplateTarget, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if op == nil || (op.Phase != "Accepted" && op.Phase != "Verifying") || op.Section != "inventory" || op.Action != "Update" || op.SubmittedAt == nil || op.PhysicalGroup != nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	if _, err := canonicalDeploymentID(op.TaskID); err != nil || op.SubmittedAt.IsZero() || now.Before(op.SubmittedAt.Time) {
		return false, errConfigurationTransition
	}
	pinned, err := parseTemplateDeploymentResource(op.Resource)
	if err != nil {
		return false, err
	}
	snapshot, err := c.regularTemplateDependenciesAtVersion(ctx, device, target.template, pinned)
	if err != nil {
		return false, err
	}
	resource, err := regularTemplateDeploymentResource(snapshot, target)
	if err != nil {
		return false, err
	}
	if op.Resource != resource || op.TargetID != snapshot.device.target.ID || op.ParentID != snapshot.device.site.ID || op.TargetSerial != snapshot.device.target.Serial {
		return false, errConfigurationStale
	}
	if _, err := snapshot.payload(target); err != nil {
		return false, err
	}
	return c.templateDeploymentComplete(ctx, templateDeploymentIdentity{deploymentID: op.TaskID, deviceID: op.TargetID, project: target.template.Project, name: target.template.Name, version: pinned.version.Number}, op.SubmittedAt.Time, now)
}
