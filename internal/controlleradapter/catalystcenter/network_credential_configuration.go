// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Credential-free definitions need controller ownership just as projected
// credentials do. Route them through the adapter, not the client-only compiler.
func (a *adapter) compileNetworkSettingsApply(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	if config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return a.compileIPAMPruneApply(nc, config, source)
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return configurationApplyPlan{}, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return configurationApplyPlan{}, errConfigurationTransition
	}
	if settings, ok := root["network_settings"].(map[string]any); ok {
		if _, present := settings["device_credentials"]; present {
			return a.compileGlobalCredentialApply(nc, config, source)
		}
	}
	return a.client.compileConfigurationApply(source.raw, config.Spec.ManagedSections, source.templateFiles)
}

// Project secret definitions without changing their original JSON-pointer paths.
// The remaining network intent still passes the existing strict compiler.
func splitNetworkCredentialConfiguration(raw []byte) ([]globalCredentialIntent, []byte, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, nil, errors.New("invalid credential network model")
	}
	for key := range root {
		if key != "network_settings" && key != "sites" {
			return nil, nil, errors.New("unsupported credential network section")
		}
	}
	network, ok := root["network_settings"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("network settings are required")
	}
	definitions, ok := network["device_credentials"]
	if !ok {
		return nil, nil, errors.New("credential definitions are required")
	}
	projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": definitions}}})
	desired, err := parseGlobalCredentials(projection, true)
	if err != nil {
		return nil, nil, err
	}
	delete(network, "device_credentials")
	if len(network) == 0 && len(root) == 1 {
		return desired, nil, nil
	}
	remainder, err := json.Marshal(map[string]any{"catalyst_center": root})
	return desired, remainder, err
}

type networkCredentialConfigurationExecutor struct {
	credentials *globalCredentialExecutor
	network     *networkSectionExecutor
}

func (e *networkCredentialConfigurationExecutor) count() int {
	return e.credentials.count() + e.network.count()
}
func (e *networkCredentialConfigurationExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	return op != nil && isGlobalCredentialResource(op.Section, op.Resource)
}
func (e *networkCredentialConfigurationExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	return e.credentials.verifyCredential(ctx, op)
}
func (e *networkCredentialConfigurationExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if e.credentialOperation(op) || op != nil && isMetadataCredentialOperation(op.Section, op.Resource) {
		return e.credentials.verify(ctx, op)
	}
	return e.network.verify(ctx, op)
}
func (e *networkCredentialConfigurationExecutor) next(ctx context.Context) (*configurationWrite, error) {
	credential, err := e.credentials.next(ctx)
	if err != nil {
		return nil, err
	}
	// Preflight every downstream dependency before the first credential write.
	// Only credentials declared in this same model may still be absent. No
	// placeholder UUID is ever submitted or accepted as verified ownership.
	var pending map[string]bool
	if credential != nil {
		pending = map[string]bool{}
		for _, want := range e.credentials.desired {
			pending[want.kind.model+"/"+want.metadata.Name] = true
		}
	}
	network, err := e.network.nextWithCredentialDefinitions(ctx, pending)
	if err != nil {
		return nil, err
	}
	if credential != nil {
		return credential, nil
	}
	return network, nil
}
