// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

const anycastBulkAnnotation = "cisco.vk/catalyst-center-bulk-anycast-gateways"

// Reuse the existing logical fabric journal/ownership contract. This binds only
// gateway creates; physical device actions keep their existing admission path.
func (a *adapter) bindAnycastBulk(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, executor configurationExecutor) error {
	flag := config.Annotations[anycastBulkAnnotation]
	if flag != "" && flag != "false" && flag != "true" {
		return errConfigurationTransition
	}
	if flag != "true" {
		return nil
	}
	if source == nil || config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return errConfigurationTransition
	}
	bound := 0
	visitAnycastGateways(executor, "", func(gateway *anycastGatewayExecutor, prefix string) {
		bound++
		gateway.bulkGuard = func(ctx context.Context, resource, parent string) error {
			if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
				return err
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
				return errConfigurationStale
			}
			op := nc.Status.Configuration.Operation
			if op.Phase != "Submitting" || op.Section != "fabric" || op.Resource != prefix+resource || op.Action != "Create" || op.ParentID != parent || op.TargetID != "" || op.TargetSerial != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || op.TaskID != "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
				return errConfigurationStale
			}
			owners := 0
			for _, owner := range nc.Status.Configuration.Owners {
				if owner.Section == "fabric" {
					if owner.ConfigUID != op.ConfigUID || owner.ConfigName != op.ConfigName {
						return errConfigurationOwned
					}
					owners++
				}
			}
			if owners != 1 {
				return errConfigurationOwned
			}
			return nil
		}
	})
	if bound == 0 {
		return errConfigurationTransition
	}
	return nil
}

// One structural visitor binds both submission authority and retained-task
// observation to exactly the same compiled gateway namespace. It does not walk
// unknown or physical-device executors.
func visitAnycastGateways(executor configurationExecutor, prefix string, visit func(*anycastGatewayExecutor, string)) {
	switch e := executor.(type) {
	case *configurationGroup:
		for _, part := range e.parts {
			visitAnycastGateways(part.executor, prefix+part.name+"/", visit)
		}
	case *fabricConfiguration:
		visitAnycastGateways(e.configurationExecutor, prefix, visit)
	case *fabricAnchorAdmissionGate:
		visitAnycastGateways(e.configurationExecutor, prefix, visit)
	case *fabricExtranetAdmissionGate:
		visitAnycastGateways(e.configurationExecutor, prefix, visit)
	case *fabricMulticastConfiguration:
		visitAnycastGateways(e.foundation, prefix, visit)
	case *fabricVLANSSIDConfiguration:
		visitAnycastGateways(e.foundation, prefix, visit)
	case *fabricGatewayDeployment:
		if e.gateways != nil {
			visit(e.gateways, prefix)
		}
	}
}
