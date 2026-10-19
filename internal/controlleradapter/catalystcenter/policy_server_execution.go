// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func (a *adapter) compilePolicyServerApply(nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "system_settings" {
		return plan, errConfigurationSecret
	}
	serverRaw, external, err := splitSystemSettingsApply(source.raw)
	if err != nil {
		return plan, err
	}
	desired, err := parsePolicyServerApply(serverRaw)
	if err != nil {
		return plan, err
	}
	destinations := make([]configurationSecretDestination, 0, len(desired))
	for _, want := range desired {
		for _, leaf := range want.credentialIntents() {
			destinations = append(destinations, configurationSecretDestination{"system_settings", leaf.path})
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
	e := &policyServerExecutor{client: a.client, desired: desired, secrets: secret, owner: string(config.UID)}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
	}
	plan.executor, plan.section = e, "system_settings"
	if external != nil {
		plan.executor = &systemSettingsExecutor{servers: e, external: &externalAuthExecutor{client: a.client, desired: *external}}
	}
	plan.sections = []configapi.NetworkControllerSectionStatus{{Name: "system_settings", ResourceCount: int64(plan.executor.count())}}
	return plan, nil
}

type policyServerIntent struct {
	server  authenticationPolicyServer
	path    string
	version int64
	extra   []configurationCredentialIntent
}

// Parse before loading credentials: inline values are rejected, including
// empty strings. The only supported credential input is an authorized projected
// leaf with an explicit NetAsCode write-only version; no field is silently discarded.
func parsePolicyServerApply(raw []byte) ([]policyServerIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errConfigurationSecret
	}
	settings, ok := root["system_settings"].(map[string]any)
	if !ok || len(settings) != 1 {
		return nil, errors.New("AAA execution requires a server-only system_settings section")
	}
	servers, ok := settings["authentication_and_policy_servers"].(map[string]any)
	if !ok || len(servers) == 0 || len(servers) > 2 {
		return nil, errConfigurationSecret
	}
	for key := range servers {
		if key != "aaa" && key != "ise" {
			return nil, errConfigurationSecret
		}
	}
	list := []any{}
	if value, present := servers["aaa"]; present {
		var valid bool
		list, valid = value.([]any)
		if !valid || len(list) == 0 || len(list) > 128 {
			return nil, errors.New("invalid AAA execution collection")
		}
	}
	aaaCount := len(list)
	if value, present := servers["ise"]; present {
		list = append(list, value)
	}
	out := make([]policyServerIntent, 0, len(list))
	seen := map[string]bool{}
	for i, value := range list {
		entry, ok := value.(map[string]any)
		if !ok || entry["shared_secret"] != nil {
			return nil, errConfigurationSecret
		}
		encoded, err := json.Marshal(entry["shared_secret_version"])
		var version int64
		if err != nil || json.Unmarshal(encoded, &version) != nil || version < 1 {
			return nil, errConfigurationSecret
		}
		delete(entry, "shared_secret_version")
		delete(entry, "shared_secret")
		base := fmt.Sprintf("/authentication_and_policy_servers/aaa/%d", i)
		var ise *policyISEMetadata
		var pxgrid, cert *bool
		var password configurationCredentialIntent
		if i == aaaCount {
			base = "/authentication_and_policy_servers/ise"
			var err error
			ise, pxgrid, cert, password, err = parsePolicyISE(entry, base)
			if err != nil {
				return nil, err
			}
		}
		extra, err := parsePolicyServerKeyWrap(entry, base)
		if err != nil {
			return nil, err
		}
		server, err := parsePolicyServerMetadata(entry)
		if err != nil {
			return nil, err
		}
		// Pinned provider requires retries 1..3 and timeout 2..20. Input must
		// have had NaC defaults resolved upstream, as for other components.
		if server.Retries == nil || *server.Retries < 1 || *server.Retries > 3 || server.Timeout == nil || *server.Timeout < 2 || *server.Timeout > 20 ||
			(server.Protocol != "TACACS" && (server.AuthenticationPort == nil || server.AccountingPort == nil)) ||
			(server.Protocol != "RADIUS" && server.Port == nil) || seen[server.IP] {
			return nil, errors.New("AAA execution requires unique addresses and resolved ports/retries/timeout")
		}
		if len(extra) > 0 {
			server.EncryptionScheme = "KEYWRAP"
		}
		if ise != nil {
			enabled := true
			ise.IP = server.IP
			server.ISE = &enabled
			server.CiscoISE = []policyISEMetadata{*ise}
			server.Pxgrid, server.UseCatcCert = pxgrid, cert
			extra = append(extra, password)
		}
		seen[server.IP] = true
		out = append(out, policyServerIntent{server: server, version: version, path: base + "/shared_secret", extra: extra})
	}
	return out, nil
}

type policyServerExecutor struct {
	client   *client
	desired  []policyServerIntent
	secrets  *configurationSecretSnapshot
	owner    string
	receipts []ciskov1.NetworkControllerConfigurationCredentialApplication
}

func (e *policyServerExecutor) count() int {
	count := len(e.desired)
	for _, want := range e.desired {
		if want.server.ISE != nil && *want.server.ISE {
			count++
		}
	}
	return count
}
func (e *policyServerExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	return op != nil && !strings.HasPrefix(op.Resource, policyISEIntegrationResource)
}

func (e *policyServerExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.authenticationPolicyServers(ctx)
	if err != nil {
		return nil, err
	}
	// Catalyst Center's ISE integration is a singleton. An address change
	// cannot silently register a second ISE or replace an unowned integration.
	for _, want := range e.desired {
		if want.server.ISE == nil || !*want.server.ISE {
			continue
		}
		for ip, server := range actual {
			if server.ISE != nil && *server.ISE && ip != want.server.IP {
				return nil, errConfigurationOwned
			}
		}
	}
	var first *configurationWrite
	for _, want := range e.desired {
		secrets, versions, err := e.credentials(want)
		if err != nil {
			return nil, err
		}
		var prior *ciskov1.NetworkControllerConfigurationCredentialApplication
		for i := range e.receipts {
			receipt := &e.receipts[i]
			if receipt.Section == "system_settings" && receipt.Resource == want.server.IP {
				if prior != nil || receipt.ConfigUID != e.owner || !validAPIID(receipt.TargetID) || !configurationOperationID.MatchString(receipt.OperationID) || (len(receipt.Versions) < 1 || len(receipt.Versions) > 4) {
					return nil, errConfigurationOwned
				}
				prior = receipt
			}
		}
		got, exists := actual[want.server.IP]
		if exists && (prior == nil || prior.TargetID != got.ID || got.ISE == nil || *got.ISE != *want.server.ISE || got.Role != "primary" || (got.EncryptionScheme != "" && (got.EncryptionScheme != want.server.EncryptionScheme || len(prior.Versions) != len(versions)))) {
			return nil, errors.New("AAA server cannot be adopted or overwritten without its matching server-type application receipt")
		}
		rotate := true
		if prior != nil {
			rotate, err = policyCredentialRotation(prior.Versions, versions)
			if err != nil {
				return nil, err
			}
		}
		if exists && !rotate && policyServerMatches(want.server, got) {
			continue
		}
		if exists && got.Protocol != want.server.Protocol && want.server.Protocol != "RADIUS_TACACS" {
			return nil, errors.New("AAA protocol removal or replacement requires a separate lifecycle")
		}
		method, path, action := http.MethodPost, authenticationPolicyServersPath, "Create"
		identity := configurationResourceIdentity{credentials: versions}
		if exists {
			method, path, action, identity.target = http.MethodPut, authenticationPolicyServersPath+"/"+url.PathEscape(got.ID), "Update", got.ID
		}
		payload := policyServerMetadata(want.server)
		for field, secret := range secrets {
			if field == "isePassword" {
				dto := payload["ciscoIseDtos"].([]any)[0].(map[string]any)
				dto["password"] = secret
				dto["subscriberName"] = want.server.IP
			} else {
				payload[field] = secret
			}
		} // Full-object PUT includes all managed credentials.
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, errConfigurationSecret
		}
		write := &configurationWrite{resource: want.server.IP, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, method, path, body)
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
	if first != nil {
		return first, nil
	}
	return e.nextISEIntegration(ctx, actual)
}

func (e *policyServerExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && strings.HasPrefix(op.Resource, policyISEIntegrationResource) {
		return e.verifyISEIntegration(ctx, op)
	}
	_, verified, err := e.verifyCredential(ctx, op)
	return verified, err
}

func (e *policyServerExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	if op == nil || op.Section != "system_settings" || op.ConfigUID != e.owner || op.ParentID != "" || op.TargetSerial != "" ||
		(op.Action != "Create" && op.Action != "Update") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return "", false, errConfigurationTransition
	}
	for _, want := range e.desired {
		if want.server.IP != op.Resource {
			continue
		}
		_, versions, err := e.credentials(want)
		if err != nil || !slices.Equal(op.CredentialVersions, versions) {
			return "", false, errConfigurationSecret
		}
		actual, err := e.client.authenticationPolicyServers(ctx)
		if err != nil {
			return "", false, err
		}
		got, exists := actual[want.server.IP]
		if !exists {
			return "", false, nil
		}
		if op.TargetID != "" && op.TargetID != got.ID {
			return "", false, errConfigurationStale
		}
		return got.ID, got.EncryptionScheme == want.server.EncryptionScheme && policyServerMatches(want.server, got), nil
	}
	return "", false, errConfigurationTransition
}
