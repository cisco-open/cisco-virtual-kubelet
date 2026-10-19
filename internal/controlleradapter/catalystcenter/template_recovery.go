// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Resume observation only for metadata resources in the actual compiled
// selection. The common reconciler still binds owner/controller/source revisions.
// Successful Create observation retains the normal unowned Retain contract; a
// name match cannot become task-bound creator ownership or permission to prune.
func templateKnownTaskRecovery(executor configurationExecutor) func(*cvk.NetworkControllerConfigurationOperation) bool {
	type selection struct{ project, commit, composite bool }
	resources := map[string]selection{}
	add := func(executor configurationExecutor, prefix string) {
		projects := []templateProject{}
		switch e := executor.(type) {
		case *templateProjectExecutor:
			projects = e.desired
		case *templateExecutor:
			projects = e.intent.projects
			for _, d := range e.intent.definitions {
				resources[prefix+"draft:"+d.identity.Key()] = selection{composite: d.identity.Composite}
				resources[prefix+"commit:"+d.identity.Key()] = selection{commit: true, composite: d.identity.Composite}
			}
		default:
			return
		}
		for _, project := range projects {
			if project.Name != "Onboarding Configuration" {
				resources[prefix+project.Name] = selection{project: true}
			}
		}
	}
	if group, ok := executor.(*configurationGroup); ok {
		for _, part := range group.parts {
			if part.name == "objects" {
				add(part.executor, "objects/")
			}
		}
	} else {
		add(executor, "")
	}
	if len(resources) == 0 {
		return nil
	}
	return func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op == nil || op.Section != "templates" || op.Phase != "Unknown" || !configurationOperationID.MatchString(op.ID) ||
			op.ConfigName == "" || op.ConfigUID == "" || op.ConfigGeneration <= 0 || op.ControllerGeneration <= 0 ||
			!validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() ||
			op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil ||
			op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 ||
			!configurationMetadataPreparationSupported(op) {
			return false
		}
		selected, ok := resources[op.Resource]
		if !ok || (selected.project && op.ParentID != "") || (!selected.project && !validAPIID(op.ParentID)) || op.PreparedAt != nil && !selected.composite {
			return false
		}
		if selected.commit {
			return op.Action == "Update" && validAPIID(op.TargetID)
		}
		return op.Action == "Create" && op.TargetID == "" || op.Action == "Update" && validAPIID(op.TargetID)
	}
}

func (c *client) templateTaskCompletion(executor configurationExecutor) func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
	selected := templateKnownTaskRecovery(executor)
	if selected == nil {
		return nil
	}
	return func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if c == nil || op == nil || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || !validAPIID(op.TaskID) {
			return false, errConfigurationTransition
		}
		if op.Phase == "Unknown" {
			if !selected(op) {
				return false, errConfigurationTransition
			}
			return c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if op.Phase != "Accepted" {
			return false, errConfigurationTransition
		}
		// Preserve normal accepted metadata completion and unrelated protocols.
		return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
