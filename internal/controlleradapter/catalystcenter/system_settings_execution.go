// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Keep the server and external-auth parsers authoritative for their leaves.
// Splitting never discards an unknown field, and occurs before reading secrets
// or querying the appliance.
func splitSystemSettingsApply(raw []byte) ([]byte, *externalAuthIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, nil, errors.New("system settings require a section-specific model")
	}
	settings, ok := root["system_settings"].(map[string]any)
	if !ok || len(settings) == 0 {
		return nil, nil, errors.New("invalid system settings")
	}
	for key := range settings {
		if key != "authentication_and_policy_servers" && key != "external_authentication" {
			return nil, nil, errors.New("unsupported system settings field")
		}
	}
	var external *externalAuthIntent
	if value, exists := settings["external_authentication"]; exists {
		projection, err := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"system_settings": map[string]any{"external_authentication": value}}})
		if err != nil {
			return nil, nil, errors.New("invalid external authentication")
		}
		intent, err := parseExternalAuth(projection)
		if err != nil {
			return nil, nil, err
		}
		external = &intent
	}
	servers, err := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"system_settings": map[string]any{"authentication_and_policy_servers": settings["authentication_and_policy_servers"]}}})
	return servers, external, err
}

// The upstream dependency is registration before external-auth settings. All
// selected read surfaces are checked before returning any write. Server task
// completion and its durable credential receipt are required before next can
// return a dependent external-auth mutation. The shared journal executes one
// operation at a time; this wrapper does not submit or retry anything.
type systemSettingsExecutor struct {
	servers  *policyServerExecutor
	external *externalAuthExecutor
}

func (e *systemSettingsExecutor) count() int { return e.servers.count() + e.external.count() }
func (e *systemSettingsExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if _, _, err := e.external.observe(ctx); err != nil {
		return nil, err
	}
	write, err := e.servers.next(ctx)
	if err != nil || write != nil {
		return write, err
	}
	return e.external.next(ctx)
}
func externalAuthenticationResource(resource string) bool {
	return resource == "external_authentication" || resource == "external_authentication_aaa_attribute"
}
func (e *systemSettingsExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	return op != nil && !externalAuthenticationResource(op.Resource) && e.servers.credentialOperation(op)
}
func (e *systemSettingsExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	return e.servers.verifyCredential(ctx, op)
}
func (e *systemSettingsExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && !externalAuthenticationResource(op.Resource) {
		return e.servers.verify(ctx, op)
	}
	if op == nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	return e.external.verify(ctx, op)
}
