// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// Select actual compiled tag leaves, preserving their journal group paths.
// This grants observation of an acknowledged task only. The common reconciler
// still verifies the owner/controller/source revision before recovery.
func tagTaskSelection(executor configurationExecutor, section string) func(*cvk.NetworkControllerConfigurationOperation) bool {
	resources := map[string]bool{}
	var visit func(configurationExecutor, string)
	visit = func(executor configurationExecutor, prefix string) {
		switch e := executor.(type) {
		case *tagExecutor:
			for _, tag := range e.desired {
				resources[prefix+tag.name] = true
			}
		case *configurationGroup:
			for _, part := range e.parts {
				visit(part.executor, prefix+part.name+"/")
			}
		}
	}
	visit(executor, "")
	if len(resources) == 0 || section != "inventory" && section != "templates" {
		return nil
	}
	return func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && op.SubmittedAt != nil && !op.SubmittedAt.After(time.Now()) && resources[op.Resource] && (definitionCreateKnownTaskRecoverySupported(op, section) || definitionUpdateKnownTaskRecoverySupported(op, section))
	}
}

func (c *client) bindTagTaskRecovery(plan *configurationApplyPlan) {
	if plan == nil {
		return
	}
	selected := tagTaskSelection(plan.executor, plan.section)
	if selected == nil {
		return
	}
	priorRecovery, priorCompletion := plan.recoverKnownTask, plan.observeCompletion
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return selected(op) || priorRecovery != nil && priorRecovery(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if op == nil {
			return false, errConfigurationTransition
		}
		local := op.DeepCopy()
		if local.Phase == "Accepted" {
			local.Phase = "Unknown"
		}
		if selected(local) {
			if op.Action == "Create" {
				_, done, err := c.tagCreateTaskResult(ctx, op, now)
				return done, err
			}
			return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if priorCompletion != nil {
			return priorCompletion(ctx, op, now)
		}
		if op.SubmittedAt == nil {
			return false, errConfigurationTransition
		}
		return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
