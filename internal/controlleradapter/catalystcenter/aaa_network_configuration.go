// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Preserve site identities and original profile-index secret paths. Resource
// names remain unchanged so dedicated and combined models share receipt keys.
func splitAAASettingsBindings(bindings []networkBinding) (aaa, other []networkBinding) {
	for _, binding := range bindings {
		a, b := binding, binding
		a.settings, b.settings = nil, nil
		for _, setting := range binding.settings {
			if setting.kind == "aaa" {
				a.settings = append(a.settings, setting)
			} else {
				b.settings = append(b.settings, setting)
			}
		}
		if len(a.settings) > 0 {
			aaa = append(aaa, a)
		}
		if len(b.settings) > 0 {
			other = append(other, b)
		}
	}
	return
}

type aaaNetworkConfigurationExecutor struct {
	aaa      *aaaSettingsExecutor
	settings configurationExecutor
}

func (e *aaaNetworkConfigurationExecutor) count() int { return e.aaa.count() + e.settings.count() }
func (e *aaaNetworkConfigurationExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	if e.aaa.credentialOperation(op) {
		return true
	}
	if credentials, ok := e.settings.(interface {
		credentialOperation(*ciskov1.NetworkControllerConfigurationOperation) bool
	}); ok {
		return credentials.credentialOperation(op)
	}
	return false
}
func (e *aaaNetworkConfigurationExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	if e.aaa.credentialOperation(op) {
		return e.aaa.verifyCredential(ctx, op)
	}
	if credentials, ok := e.settings.(interface {
		verifyCredential(context.Context, *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error)
	}); ok {
		return credentials.verifyCredential(ctx, op)
	}
	return "", false, errConfigurationTransition
}
func (e *aaaNetworkConfigurationExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if e.aaa.credentialOperation(op) {
		return e.aaa.verify(ctx, op)
	}
	return e.settings.verify(ctx, op)
}
func (e *aaaNetworkConfigurationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	// Match NaC's network/telemetry-before-AAA ordering while observing both
	// components first: invalid AAA ownership must prevent even an NTP update.
	settings, err := e.settings.next(ctx)
	if err != nil {
		return nil, err
	}
	aaa, err := e.aaa.next(ctx)
	if err != nil {
		return nil, err
	}
	if settings != nil {
		return settings, nil
	}
	return aaa, nil
}
