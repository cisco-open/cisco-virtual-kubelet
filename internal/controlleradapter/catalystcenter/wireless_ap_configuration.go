// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
)

func (w *wirelessConfiguration) count() int {
	n := w.configurationExecutor.count()
	if w.ap != nil {
		n += w.ap.count()
	}
	if w.ssid != nil {
		n += w.ssid.count()
	}
	return n
}
func (w *wirelessConfiguration) next(ctx context.Context) (*configurationWrite, error) {
	first, err := w.configurationExecutor.next(ctx)
	if err != nil {
		return nil, err
	}
	var power, acl map[string]bool
	if first != nil {
		power, acl = w.plannedPower, w.plannedACL
	}
	// Preflight every dependent before releasing a prerequisite write.
	var ap, ssid *configurationWrite
	if w.ap != nil {
		ap, err = w.ap.plan(ctx, power)
		if err != nil {
			return nil, err
		}
	}
	if w.ssid != nil {
		ssid, err = w.ssid.plan(ctx, acl)
		if err != nil {
			return nil, err
		}
	}
	if first != nil {
		return first, nil
	}
	if ap != nil {
		return ap, nil
	}
	return ssid, nil
}
func (w *wirelessConfiguration) credentialLeaves() []configurationCredentialIntent {
	var leaves []configurationCredentialIntent
	if w.ap != nil {
		for _, want := range w.ap.desired {
			leaves = append(leaves, want.credentials...)
		}
	}
	if w.ssid != nil {
		for _, want := range w.ssid.desired {
			leaves = append(leaves, want.credentials...)
		}
	}
	return leaves
}
func isWirelessSSIDCredentialResource(section, resource string) bool {
	return section == "wireless" && (strings.HasPrefix(resource, "ssid-credential:") || strings.HasPrefix(resource, ssidHistoryResourcePrefix))
}

func (w *wirelessConfiguration) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	return op != nil && ((w.ap != nil && isWirelessAPCredentialResource(op.Section, op.Resource)) || (w.ssid != nil && isWirelessSSIDCredentialResource(op.Section, op.Resource)))
}
func (w *wirelessConfiguration) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	if !w.credentialOperation(op) {
		return "", false, errConfigurationTransition
	}
	if isWirelessSSIDCredentialResource(op.Section, op.Resource) {
		return w.ssid.verifyCredential(ctx, op)
	}
	return w.ap.verifyCredential(ctx, op)
}
func (w *wirelessConfiguration) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if w.ap != nil && (strings.HasPrefix(op.Resource, "ap-profile:") || strings.HasPrefix(op.Resource, "ap-credential-profile:")) {
		return w.ap.verify(ctx, op)
	}
	if w.ssid != nil && (strings.HasPrefix(op.Resource, "ssid:") || isWirelessSSIDCredentialResource(op.Section, op.Resource)) {
		return w.ssid.verify(ctx, op)
	}
	return w.configurationExecutor.verify(ctx, op)
}
func (a *adapter) compileWirelessCredentialApply(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "wireless" {
		return plan, errConfigurationSecret
	}
	bound := false
	executor, err := a.client.compileWirelessConfiguration(source.raw, func(e *wirelessConfiguration) error {
		destinations := []configurationSecretDestination{}
		for _, leaf := range e.credentialLeaves() {
			destinations = append(destinations, configurationSecretDestination{"wireless", leaf.path})
		}
		if len(destinations) == 0 || len(destinations) != len(config.Spec.SecretRefs) {
			return errConfigurationSecret
		}
		snapshot, err := resolveConfigurationSecrets(nc, config, a.intentSecretPath, destinations)
		if err != nil {
			return err
		}
		source.secrets = snapshot
		if e.ap != nil {
			e.ap.secrets, e.ap.owner = snapshot, string(config.UID)
			if nc.Status.Configuration != nil {
				e.ap.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
			}
		}
		if e.ssid != nil {
			e.ssid.secrets, e.ssid.owner = snapshot, string(config.UID)
			if nc.Status.Configuration != nil {
				e.ssid.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
			}
		}
		bound = true
		return nil
	})
	if err != nil {
		return plan, err
	}
	if !bound {
		return plan, errConfigurationSecret
	}
	plan.executor, plan.section = executor, "wireless"
	plan.recoverKnownTask = wirelessDefinitionKnownTaskRecovery(executor)
	plan.observeCompletion = a.client.wirelessDefinitionTaskCompletion(executor)
	plan.sections = []configapi.NetworkControllerSectionStatus{{Name: "wireless", ResourceCount: int64(executor.count())}}
	return plan, nil
}
