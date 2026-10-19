// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// Common acknowledged, metadata-only journal envelope. It grants observation,
// never a submission permit, physical admission or per-object ownership.
func definitionKnownTaskJournalSupported(op *cvk.NetworkControllerConfigurationOperation, section string) bool {
	return op != nil && op.Section == section && op.Phase == "Unknown" &&
		configurationOperationID.MatchString(op.ID) &&
		op.ConfigName != "" && op.ConfigUID != "" && op.ConfigGeneration > 0 && op.ControllerGeneration > 0 &&
		validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() &&
		op.ParentID == "" && op.TargetSerial == "" && op.CreatedResourceID == "" &&
		op.PhysicalGroup == nil && op.DependencyVersions == nil && op.CancellationRequestedAt == nil &&
		op.PreparedAt == nil && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
}

func definitionUpdateKnownTaskRecoverySupported(op *cvk.NetworkControllerConfigurationOperation, section string) bool {
	return definitionKnownTaskJournalSupported(op, section) && op.Action == "Update" && validAPIID(op.TargetID)
}

// A successful original task plus current desired-state readback can complete
// an ordinary Retain-only Create without learning its created UUID. This is
// the existing normal Create convergence contract, not creator ownership or
// permission to adopt/prune a same-name object. Taskless outcomes stay fenced.
func definitionCreateKnownTaskRecoverySupported(op *cvk.NetworkControllerConfigurationOperation, section string) bool {
	return definitionKnownTaskJournalSupported(op, section) && op.Action == "Create" && op.TargetID == ""
}

func wirelessDefinitionTaskSelection(executor configurationExecutor) func(*cvk.NetworkControllerConfigurationOperation) bool {
	wrapper, ok := executor.(*wirelessConfiguration)
	if !ok {
		return nil
	}
	resources := map[string]bool{}
	// Match the actual compiled executor and its group namespace. A resource
	// prefix alone cannot qualify another collection or an undeclared object.
	add := func(executor configurationExecutor, group string) {
		namespace, prefix := "", ""
		names := []string{}
		switch e := executor.(type) {
		case *wirelessAPExecutor:
			namespace, prefix = "ap_profiles", "ap-profile:"
			for _, v := range e.desired {
				if len(v.credentials) == 0 {
					names = append(names, v.name)
				}
			}
		case *wirelessAnchorExecutor:
			namespace, prefix = "anchor_groups", "anchor-group:"
			for _, v := range e.desired {
				names = append(names, v.Name)
			}
		case *wirelessInterfaceExecutor:
			namespace, prefix = "interfaces", "interface:"
			for _, v := range e.desired {
				names = append(names, v.Name)
			}
		case *wirelessPowerExecutor:
			namespace, prefix = "power_profiles", "power-profile:"
			for _, v := range e.desired {
				names = append(names, v.Name)
			}
		case *wirelessDot11beExecutor:
			namespace, prefix = "dot11be_profiles", "dot11be-profile:"
			for _, v := range e.desired {
				names = append(names, v.Name)
			}
		case *wirelessACLExecutor:
			namespace, prefix = "security_settings", "pre-auth-acl:"
			for _, v := range e.desired {
				names = append(names, v.Name)
			}
		case *wirelessRFExecutor:
			namespace, prefix = "rf_profiles", "rf-profile:"
			for _, v := range e.desired {
				names = append(names, v.name)
			}
		default:
			return
		}
		if group != "" {
			if group != namespace {
				return
			}
			prefix = group + "/" + prefix
		}
		for _, name := range names {
			resources[prefix+name] = true
		}
	}
	if group, ok := wrapper.configurationExecutor.(*configurationGroup); ok {
		for _, part := range group.parts {
			add(part.executor, part.name)
		}
	} else {
		add(wrapper.configurationExecutor, "")
	}
	// AP profiles are attached directly to the wireless wrapper, even when
	// prerequisite definitions use a grouped namespace. Credential-bearing APs
	// retain their existing version-bound completion/recovery path.
	if wrapper.ap != nil {
		add(wrapper.ap, "")
	}
	if len(resources) == 0 {
		return nil
	}
	return func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return (definitionUpdateKnownTaskRecoverySupported(op, "wireless") || definitionCreateKnownTaskRecoverySupported(op, "wireless")) && resources[op.Resource]
	}
}

func wirelessDefinitionKnownTaskRecovery(executor configurationExecutor) func(*cvk.NetworkControllerConfigurationOperation) bool {
	selected := wirelessDefinitionTaskSelection(executor)
	if selected == nil {
		return nil
	}
	return func(op *cvk.NetworkControllerConfigurationOperation) bool {
		// Preserve credential-specific recovery in mixed manifests. Credential
		// source/history verification remains in the common reconciler.
		return selected(op) || ssidKnownTaskRecoverySupported(op)
	}
}

func (c *client) wirelessDefinitionTaskCompletion(executor configurationExecutor) func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
	selected := wirelessDefinitionTaskSelection(executor)
	if selected == nil {
		return nil
	}
	return func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if c == nil || op == nil || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || !validAPIID(op.TaskID) {
			return false, errConfigurationTransition
		}
		observation := op.DeepCopy()
		if observation.Phase == "Accepted" {
			observation.Phase = "Unknown"
		}
		if selected(observation) {
			return c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		// Credential-bearing AP/SSID and other protocols retain their completion
		// contract. Full task-tree success gives no creator UUID ownership.
		return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
