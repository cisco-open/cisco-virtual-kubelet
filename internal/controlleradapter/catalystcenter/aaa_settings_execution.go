// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// The replacement API requires both groups. Do not reconstruct an omitted
// group's write-only secret from a masked GET, or interpret omission as reset.
func parseAAASettingsApply(raw []byte) ([]networkBinding, error) {
	bindings, err := parseNetworkSettingsWithAAA(raw, func(p map[string]any, index int) ([]networkSetting, error) {
		leaves := []configurationCredentialIntent{}
		for _, group := range aaaGroups {
			object, ok := p[group.model].(map[string]any)
			if !ok || object["shared_secret"] != nil {
				return nil, errConfigurationSecret
			}
			encoded, err := json.Marshal(object["shared_secret_version"])
			var version int64
			if err != nil || json.Unmarshal(encoded, &version) != nil || version < 1 {
				return nil, errConfigurationSecret
			}
			delete(object, "shared_secret")
			delete(object, "shared_secret_version")
			// Match the pinned module's ISE PAN fallback to its primary IP.
			if object["server_type"] == "ISE" {
				if _, present := object["pan"]; !present {
					object["pan"] = object["primary_ip"]
				}
			}
			leaves = append(leaves, configurationCredentialIntent{path: fmt.Sprintf("/aaa_servers/%d/%s/shared_secret", index, group.model), field: group.api, version: version})
		}
		settings, err := parseAAAProfile(p)
		if err != nil {
			return nil, err
		}
		settings[0].aaaCredentials = leaves
		return settings, nil
	})
	if err != nil {
		return nil, err
	}
	selected := false
	for _, binding := range bindings {
		for _, setting := range binding.settings {
			if len(networkSettingIdentity(binding.path, setting.kind)) > 512 {
				return nil, errConfigurationSecret
			}
			if setting.kind == "aaa" {
				selected = true
			} else if _, err := networkSettingPayload(setting); err != nil {
				return nil, err
			}
		}
	}
	if !selected {
		return nil, errConfigurationSecret
	}
	return bindings, nil
}

func aaaCredentialKind(path string) string {
	p, err := configurationSecretPointer(path)
	if err != nil || len(p) != 4 || p[0] != "aaa_servers" || p[3] != "shared_secret" {
		return ""
	}
	i, err := strconv.Atoi(p[1])
	if err != nil || i < 0 || strconv.Itoa(i) != p[1] {
		return ""
	}
	for _, group := range aaaGroups {
		if p[2] == group.model {
			return group.api
		}
	}
	return ""
}

func (a *adapter) compileAAASettingsApply(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "network_settings" {
		return plan, errConfigurationSecret
	}
	raw := source.raw
	var credentials []globalCredentialIntent
	for _, ref := range config.Spec.SecretRefs {
		if strings.HasPrefix(ref.Path, "/device_credentials/") {
			var err error
			credentials, raw, err = splitNetworkCredentialConfiguration(raw)
			if err != nil {
				return plan, err
			}
			break
		}
	}
	intent, err := parseNetworkIntentWithSettings(raw, parseAAASettingsApply)
	if err != nil {
		return plan, err
	}
	aaaBindings, otherBindings := splitAAASettingsBindings(intent.bindings)
	if len(aaaBindings) == 0 {
		return plan, errConfigurationSecret
	}
	intent.bindings = otherBindings
	network, err := a.client.compileNetworkSettingsIntent(intent)
	if err != nil {
		return plan, err
	}
	destinations := []configurationSecretDestination{}
	seen := map[string]bool{}
	for _, credential := range credentials {
		if len(credential.credentials) == 0 {
			return plan, errConfigurationSecret
		}
		for _, leaf := range credential.credentials {
			destinations = append(destinations, configurationSecretDestination{"network_settings", leaf.path})
		}
	}
	for _, binding := range aaaBindings {
		for _, leaf := range binding.settings[0].aaaCredentials {
			if !seen[leaf.path] {
				seen[leaf.path] = true
				destinations = append(destinations, configurationSecretDestination{"network_settings", leaf.path})
			}
		}
	}
	if len(config.Spec.SecretRefs) != len(destinations) {
		return plan, errConfigurationSecret
	}
	secret, err := resolveConfigurationSecrets(nc, config, a.intentSecretPath, destinations)
	if err != nil {
		return plan, err
	}
	source.secrets = secret
	e := &aaaSettingsExecutor{networkConfigurationExecutor: networkConfigurationExecutor{client: a.client, bindings: aaaBindings}, secrets: secret, owner: string(config.UID)}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
	}
	plan.executor, plan.section = e, "network_settings"
	var upstream configurationExecutor = network
	if len(credentials) > 0 {
		upstream = &networkCredentialConfigurationExecutor{credentials: &globalCredentialExecutor{client: a.client, desired: credentials, secrets: secret, owner: string(config.UID), receipts: e.receipts}, network: network}
	}
	if upstream.count() > 0 {
		plan.executor = &aaaNetworkConfigurationExecutor{aaa: e, settings: upstream}
	}
	plan.sections = []configapi.NetworkControllerSectionStatus{{Name: "network_settings", ResourceCount: int64(plan.executor.count())}}
	return plan, nil
}

type aaaSettingsExecutor struct {
	networkConfigurationExecutor
	secrets  *configurationSecretSnapshot
	owner    string
	receipts []ciskov1.NetworkControllerConfigurationCredentialApplication
}

func (e *aaaSettingsExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	return op != nil && op.Section == "network_settings" && strings.HasSuffix(op.Resource, "#aaa")
}

func (e *aaaSettingsExecutor) next(ctx context.Context) (*configurationWrite, error) {
	dependencies, err := e.client.aaaRegistrationDependencies(ctx, e.bindings)
	if err != nil {
		return nil, err
	}
	sites, err := e.sites(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	var used atomic.Bool
	for _, binding := range e.bindings {
		want, site := binding.settings[0], sites[binding.path]
		resource := networkSettingIdentity(binding.path, "aaa")
		secrets, versions, err := resolveVersionedConfigurationCredentials(e.secrets, e.owner, "network_settings", want.aaaCredentials)
		if err != nil {
			return nil, err
		}
		got, err := e.client.effectiveNetworkSetting(ctx, sites, binding.path, "aaa")
		if err != nil {
			return nil, err
		}
		var prior *ciskov1.NetworkControllerConfigurationCredentialApplication
		for i := range e.receipts {
			r := &e.receipts[i]
			if r.Section == "network_settings" && r.Resource == resource {
				if prior != nil || r.ConfigUID != e.owner || r.TargetID != site.ID || !configurationOperationID.MatchString(r.OperationID) || len(r.Versions) != 2 {
					return nil, errConfigurationOwned
				}
				prior = r
			}
		}
		if prior == nil && (len(got.aaa["aaaNetwork"]) != 0 || len(got.aaa["aaaClient"]) != 0) {
			return nil, errConfigurationOwned
		}
		rotate := true
		if prior != nil {
			rotate, err = configurationCredentialRotation(prior.Versions, versions, aaaCredentialKind)
			if err != nil {
				return nil, err
			}
		}
		if !rotate && reflect.DeepEqual(want.aaa, got.aaa) {
			continue
		}
		payload := map[string]any{}
		for _, group := range aaaGroups {
			fields := map[string]string{}
			for key, value := range want.aaa[group.api] {
				fields[key] = value
			}
			fields["sharedSecret"] = secrets[group.api]
			payload[group.api] = fields
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, errConfigurationSecret
		}
		endpoint := sitesPath + "/" + site.ID + "/aaaSettings"
		write := &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: site.ID, credentials: versions}, submit: func(ctx context.Context) (Task, error) {
			if !used.CompareAndSwap(false, true) {
				return Task{}, errConfigurationTransition
			}
			fresh, err := e.client.aaaRegistrationDependencies(ctx, e.bindings)
			if err != nil {
				return Task{}, err
			}
			if !reflect.DeepEqual(fresh, dependencies) {
				return Task{}, errConfigurationStale
			}
			raw, err := e.client.mutate(ctx, http.MethodPut, endpoint, body)
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
	return first, nil
}

func (e *aaaSettingsExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	_, ok, err := e.verifyCredential(ctx, op)
	return ok, err
}

func (e *aaaSettingsExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	if !e.credentialOperation(op) || op.ConfigUID != e.owner || op.Action != "Update" || !validAPIID(op.TargetID) || op.ParentID != "" || op.TargetSerial != "" {
		return "", false, errConfigurationTransition
	}
	if _, err := e.client.aaaRegistrationDependencies(ctx, e.bindings); err != nil {
		return "", false, err
	}
	sites, err := e.sites(ctx)
	if err != nil {
		return "", false, err
	}
	for _, binding := range e.bindings {
		if networkSettingIdentity(binding.path, "aaa") != op.Resource {
			continue
		}
		want := binding.settings[0]
		_, versions, err := resolveVersionedConfigurationCredentials(e.secrets, e.owner, "network_settings", want.aaaCredentials)
		if err != nil || !slices.Equal(versions, op.CredentialVersions) {
			return "", false, errConfigurationSecret
		}
		if sites[binding.path].ID != op.TargetID {
			return "", false, errConfigurationStale
		}
		got, err := e.client.effectiveNetworkSetting(ctx, sites, binding.path, "aaa")
		if err != nil {
			return "", false, err
		}
		return op.TargetID, reflect.DeepEqual(want.aaa, got.aaa), nil
	}
	return "", false, errConfigurationTransition
}
