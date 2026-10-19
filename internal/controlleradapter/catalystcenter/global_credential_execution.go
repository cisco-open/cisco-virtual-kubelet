// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (a *adapter) compileCredentialConfigurationApply(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	if len(config.Spec.ManagedSections) != 1 {
		return configurationApplyPlan{}, errConfigurationSecret
	}
	switch config.Spec.ManagedSections[0] {
	case "wireless":
		return a.compileWirelessCredentialApply(nc, config, source)
	case "system_settings":
		return a.compilePolicyServerApply(nc, config, source)
	case "network_settings":
		for _, ref := range config.Spec.SecretRefs {
			if strings.HasPrefix(ref.Path, "/aaa_servers/") {
				return a.compileAAASettingsApply(nc, config, source)
			}
		}
		return a.compileGlobalCredentialApply(nc, config, source)
	default:
		return configurationApplyPlan{}, errConfigurationSecret
	}
}
func (a *adapter) compileGlobalCredentialApply(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "network_settings" {
		return plan, errConfigurationSecret
	}
	desired, remainder, err := splitNetworkCredentialConfiguration(source.raw)
	if err != nil {
		return plan, err
	}
	var network *networkSectionExecutor
	if remainder != nil {
		executor, err := a.client.compileConfigurationExecutor(remainder, "network_settings", nil)
		if err != nil {
			return plan, err
		}
		network = executor.(*networkSectionExecutor)
	}
	destinations := []configurationSecretDestination{}
	for _, want := range desired {
		if len(want.credentials) == 0 && !want.metadataOnly() {
			return plan, errConfigurationSecret
		}
		for _, leaf := range want.credentials {
			destinations = append(destinations, configurationSecretDestination{"network_settings", leaf.path})
		}
	}
	if len(destinations) != len(config.Spec.SecretRefs) {
		return plan, errConfigurationSecret
	}
	var snapshot *configurationSecretSnapshot
	if len(destinations) > 0 {
		snapshot, err = resolveConfigurationSecrets(nc, config, a.intentSecretPath, destinations)
		if err != nil {
			return plan, err
		}
	}
	source.secrets = snapshot
	e := &globalCredentialExecutor{client: a.client, desired: desired, secrets: snapshot, owner: string(config.UID), configName: config.Name}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
		e.metadataReceipts = nc.Status.Configuration.DeepCopy().ActionApplications
	}
	for _, want := range desired {
		root, _, err := e.metadataCredentialReceipt(want)
		if err != nil || root != nil && (root.Operation.ConfigGeneration > config.Generation || root.Operation.ControllerGeneration > nc.Generation) {
			return plan, errConfigurationOwned
		}
	}
	plan.executor, plan.section = e, "network_settings"
	if network != nil {
		plan.executor = &networkCredentialConfigurationExecutor{credentials: e, network: network}
	}
	plan.sections = []configapi.NetworkControllerSectionStatus{{Name: "network_settings", ResourceCount: int64(plan.executor.count())}}
	a.bindGlobalCredentialTaskRecovery(&plan, e)
	bindSiteCredentialTaskRecovery(&plan)
	return plan, nil
}
func globalCredentialVersionIdentity(path string) string {
	p, err := configurationSecretPointer(path)
	if err != nil || len(p) != 4 || p[0] != "device_credentials" {
		return ""
	}
	index, err := strconv.Atoi(p[2])
	if err != nil || index < 0 || strconv.Itoa(index) != p[2] {
		return ""
	}
	valid := false
	switch p[1] {
	case "cli_credentials":
		valid = p[3] == "password" || p[3] == "enable"
	case "https_read_credentials", "https_write_credentials":
		valid = p[3] == "password"
	case "snmpv2_read_credentials":
		valid = p[3] == "read_community"
	case "snmpv2_write_credentials":
		valid = p[3] == "write_community"
	case "snmpv3_credentials":
		valid = p[3] == "auth_password" || p[3] == "privacy_password"
	}
	if !valid {
		return ""
	}
	return p[1] + "/" + p[3]
}
func (want globalCredentialIntent) resource() string {
	return "device_credentials/" + want.kind.model + "/" + want.metadata.Name
}

type globalCredentialExecutor struct {
	client           *client
	desired          []globalCredentialIntent
	secrets          *configurationSecretSnapshot
	owner            string
	receipts         []ciskov1.NetworkControllerConfigurationCredentialApplication
	configName       string
	metadataReceipts []ciskov1.NetworkControllerConfigurationActionApplication
}

func (e *globalCredentialExecutor) count() int { return len(e.desired) }
func (e *globalCredentialExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	return op != nil && isGlobalCredentialResource(op.Section, op.Resource)
}
func (e *globalCredentialExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.globalCredentials(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	for _, want := range e.desired {
		if want.metadataOnly() {
			write, err := e.nextMetadataCredential(want, actual)
			if err != nil {
				return nil, err
			}
			if first == nil {
				first = write
			}
			continue
		}
		got, exists := actual[want.kind.model][want.metadata.Name]
		prior, err := e.priorCredential(want)
		if err != nil {
			return nil, err
		}
		root, rootID, err := e.credentialPromotionRoot(want, prior)
		if err != nil {
			return nil, err
		}
		if root != nil {
			// A retained metadata receipt grants only a same-UUID promotion.
			// Before the first authenticated task, native metadata must still
			// match the exact NOAUTH payload proven by that original receipt.
			original := want
			original.metadata, original.credentials = got, nil
			if !exists || got.ID != rootID || prior == nil && (!original.metadataOnly() || original.metadataResource() != root.Operation.Resource) {
				return nil, errConfigurationOwned
			}
		}
		id := ""
		if exists {
			if prior == nil && root == nil || prior != nil && prior.TargetID != got.ID {
				return nil, errConfigurationOwned
			}
			id = got.ID
		}
		body, versions, err := want.payload(e.secrets, e.owner, id)
		if err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			return nil, errConfigurationSecret
		}
		rotate := true
		if prior != nil {
			rotate, err = configurationCredentialRotation(prior.Versions, versions, globalCredentialVersionIdentity)
			if err != nil {
				return nil, err
			}
		}
		if exists && !rotate && globalCredentialMatches(want.metadata, got) {
			continue
		}
		method, action := http.MethodPost, "Create"
		if exists {
			method, action = http.MethodPut, "Update"
		}
		write := &configurationWrite{resource: want.resource(), action: action, identity: configurationResourceIdentity{target: id, credentials: versions}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, method, globalCredentialsPath, body)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return Task{ID: receipt.Response.ID}, nil
		}}
		if first == nil {
			first = write
		}
	}
	return e.guardGlobalCredentialWrite(first, actual), nil
}
func (e *globalCredentialExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && isMetadataCredentialOperation(op.Section, op.Resource) {
		_, ok, err := e.verifyMetadataCredential(ctx, op, time.Now())
		return ok, err
	}
	_, ok, err := e.verifyCredential(ctx, op)
	return ok, err
}
func (e *globalCredentialExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	if op == nil || op.Section != "network_settings" || op.ConfigUID != e.owner || op.ParentID != "" || op.TargetSerial != "" || len(op.CredentialVersions) == 0 || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return "", false, errConfigurationTransition
	}
	for _, want := range e.desired {
		if op.Resource != want.resource() {
			continue
		}
		_, versions, err := want.payload(e.secrets, e.owner, op.TargetID)
		if err != nil || !slices.Equal(versions, op.CredentialVersions) {
			return "", false, errConfigurationSecret
		}
		actual, err := e.client.globalCredentials(ctx)
		if err != nil {
			return "", false, err
		}
		got, exists := actual[want.kind.model][want.metadata.Name]
		if !exists {
			return "", false, nil
		}
		if op.TargetID != "" && op.TargetID != got.ID {
			return "", false, errConfigurationStale
		}
		return got.ID, globalCredentialMatches(want.metadata, got), nil
	}
	return "", false, errConfigurationTransition
}
func isGlobalCredentialResource(section, resource string) bool {
	return section == "network_settings" && strings.HasPrefix(resource, "device_credentials/")
}
