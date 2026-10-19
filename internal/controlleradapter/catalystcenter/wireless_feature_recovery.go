// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"

// An acknowledged update already pins its existing native UUID. Observation may
// resume through the shared Unknown -> Verifying transition; this grants no new
// submission permit, create ownership or task inference from converged metadata.
func wirelessFeatureUpdateKnownTaskRecoverySupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	return definitionUpdateKnownTaskRecoverySupported(op, "feature_templates")
}

// Bind only the namespaces actually produced by the feature compiler. An
// unrelated executor, guessed prefix or unselected name cannot gain recovery.
func wirelessFeatureKnownTaskRecovery(executor configurationExecutor) func(*cvk.NetworkControllerConfigurationOperation) bool {
	wrapper, ok := executor.(*wirelessConfiguration)
	if !ok {
		return nil
	}
	resources := map[string]bool{}
	addCleanAir := func(e *wirelessCleanAirExecutor, prefix string) {
		for _, want := range e.desired {
			resources[prefix+"cleanair:"+want.Name] = true
		}
	}
	addFRA := func(e *wirelessFRAExecutor, prefix string) {
		for _, want := range e.desired {
			resources[prefix+"rrm-fra:"+want.Name] = true
		}
	}
	executor = wrapper.configurationExecutor
	if group, ok := executor.(*wirelessFeatureGroup); ok {
		executor = group.configurationGroup
	}
	switch e := executor.(type) {
	case *wirelessCleanAirExecutor:
		addCleanAir(e, "")
	case *wirelessFRAExecutor:
		addFRA(e, "")
	case *configurationGroup:
		for _, part := range e.parts {
			switch part.name {
			case "cleanair":
				if e, ok := part.executor.(*wirelessCleanAirExecutor); ok {
					addCleanAir(e, "cleanair/")
				}
			case "rrm_fra":
				if e, ok := part.executor.(*wirelessFRAExecutor); ok {
					addFRA(e, "rrm_fra/")
				}
			}
		}
	}
	if len(resources) == 0 {
		return nil
	}
	return func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return wirelessFeatureUpdateKnownTaskRecoverySupported(op) && resources[op.Resource]
	}
}
