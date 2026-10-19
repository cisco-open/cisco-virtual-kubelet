// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const globalCredentialsPath = "/dna/intent/api/v2/global-credential"

type globalCredentialKind struct{ model, api string }

var globalCredentialKinds = []globalCredentialKind{
	{"cli_credentials", "cliCredential"}, {"https_read_credentials", "httpsRead"},
	{"https_write_credentials", "httpsWrite"}, {"snmpv2_read_credentials", "snmpV2cRead"},
	{"snmpv2_write_credentials", "snmpV2cWrite"}, {"snmpv3_credentials", "snmpV3"},
}

// Never decode returned credential material. Masked values are not evidence
// that the requested write-only version has been applied.
type globalCredentialMetadata struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"description"`
	Username       string `json:"username,omitempty"`
	Port           *int   `json:"port,omitempty"`
	AuthType       string `json:"authType,omitempty"`
	PrivacyType    string `json:"privacyType,omitempty"`
	Mode           string `json:"snmpMode,omitempty"`
	CredentialType string `json:"credentialType,omitempty"`
}
type globalCredentialIntent struct {
	kind        globalCredentialKind
	metadata    globalCredentialMetadata
	credentials []configurationCredentialIntent
}

func globalCredentialText(value any) (string, error) {
	s, ok := value.(string)
	if !ok || s == "" || len(s) > 255 || strings.TrimSpace(s) != s || strings.ContainsAny(s, "\x00\r\n") {
		return "", errors.New("invalid global credential metadata")
	}
	return s, nil
}
func parseGlobalCredentials(raw []byte, execution bool) ([]globalCredentialIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("global credentials require a section-specific model")
	}
	settings, ok := root["network_settings"].(map[string]any)
	if !ok || len(settings) != 1 {
		return nil, errors.New("global credentials require an explicit device_credentials section")
	}
	groups, ok := settings["device_credentials"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid global credential intent")
	}
	known := map[string]bool{}
	out := []globalCredentialIntent{}
	for _, kind := range globalCredentialKinds {
		known[kind.model] = true
		value, present := groups[kind.model]
		if !present {
			continue
		}
		list, ok := value.([]any)
		if !ok || len(list) > 128 {
			return nil, errors.New("invalid global credential collection")
		}
		names := map[string]bool{}
		for index, value := range list {
			entry, ok := value.(map[string]any)
			if !ok {
				return nil, errConfigurationSecret
			}
			want := globalCredentialIntent{kind: kind}
			allowed := map[string]bool{"name": true}
			want.metadata.Name, err = globalCredentialText(entry["name"])
			if err != nil || names[want.metadata.Name] {
				return nil, errors.New("invalid or duplicate global credential name")
			}
			names[want.metadata.Name] = true
			secrets := map[string]string{}
			switch kind.model {
			case "cli_credentials", "https_read_credentials", "https_write_credentials":
				allowed["username"] = true
				want.metadata.Username, err = globalCredentialText(entry["username"])
				if err != nil {
					return nil, err
				}
				secrets["password"] = "password"
				if kind.model == "cli_credentials" {
					if _, present := entry["enable_version"]; present {
						secrets["enable"] = "enablePassword"
					}
				} else {
					allowed["port"] = true
					want.metadata.Port, err = policyServerInteger(entry["port"], 1, 65535)
					if err != nil {
						return nil, err
					}
				}
			case "snmpv2_read_credentials":
				secrets["read_community"] = "readCommunity"
			case "snmpv2_write_credentials":
				secrets["write_community"] = "writeCommunity"
			case "snmpv3_credentials":
				allowed["username"], allowed["snmp_mode"] = true, true
				want.metadata.Username, err = globalCredentialText(entry["username"])
				if err != nil {
					return nil, err
				}
				want.metadata.Mode, err = globalCredentialText(entry["snmp_mode"])
				if err != nil {
					return nil, err
				}
				switch want.metadata.Mode {
				case "AUTHPRIV":
					allowed["privacy_type"] = true
					want.metadata.PrivacyType, err = globalCredentialText(entry["privacy_type"])
					if err != nil || !slices.Contains([]string{"AES128", "AES192", "AES256"}, want.metadata.PrivacyType) {
						return nil, errors.New("invalid SNMP privacy protocol")
					}
					secrets["privacy_password"] = "privacyPassword"
					fallthrough
				case "AUTHNOPRIV":
					allowed["auth_type"] = true
					want.metadata.AuthType, err = globalCredentialText(entry["auth_type"])
					if err != nil || !slices.Contains([]string{"SHA", "MD5"}, want.metadata.AuthType) {
						return nil, errors.New("invalid SNMP authentication protocol")
					}
					secrets["auth_password"] = "authPassword"
				case "NOAUTHNOPRIV":
				default:
					return nil, errors.New("invalid SNMP security mode")
				}
			}
			keys := make([]string, 0, len(secrets))
			for key := range secrets {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				if !execution {
					continue
				}
				allowed[key], allowed[key+"_version"] = true, true
				if entry[key] != nil {
					return nil, errConfigurationSecret
				}
				raw, err := json.Marshal(entry[key+"_version"])
				var version int64
				if err != nil || json.Unmarshal(raw, &version) != nil || version < 1 {
					return nil, errConfigurationSecret
				}
				want.credentials = append(want.credentials, configurationCredentialIntent{path: fmt.Sprintf("/device_credentials/%s/%d/%s", kind.model, index, key), field: secrets[key], version: version})
			}
			for key := range entry {
				if !allowed[key] {
					return nil, errors.New("unsupported global credential field or inline credential")
				}
			}
			out = append(out, want)
		}
	}
	for key := range groups {
		if !known[key] {
			return nil, errors.New("unsupported global credential type")
		}
	}
	return out, nil
}
func (c *client) globalCredentials(ctx context.Context) (map[string]map[string]globalCredentialMetadata, error) {
	raw, err := c.get(ctx, globalCredentialsPath, nil, 200)
	if err != nil {
		return nil, err
	}
	var body struct {
		Response map[string][]globalCredentialMetadata `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response == nil {
		return nil, errInvalidResponse
	}
	out := map[string]map[string]globalCredentialMetadata{}
	ids := map[string]bool{}
	for _, kind := range globalCredentialKinds {
		out[kind.model] = map[string]globalCredentialMetadata{}
		list := body.Response[kind.api]
		if len(list) > 10000 {
			return nil, errInvalidResponse
		}
		for _, value := range list {
			if value.CredentialType == "APP" {
				continue
			}
			if value.CredentialType != "" && value.CredentialType != "GLOBAL" {
				return nil, errInvalidResponse
			}
			if !validAPIID(value.ID) || ids[value.ID] {
				return nil, errInvalidResponse
			}
			if _, err := globalCredentialText(value.Name); err != nil {
				return nil, errInvalidResponse
			}
			if _, duplicate := out[kind.model][value.Name]; duplicate {
				return nil, errInvalidResponse
			}
			ids[value.ID] = true
			out[kind.model][value.Name] = value
		}
	}
	return out, nil
}
func globalCredentialMetadataPayload(metadata globalCredentialMetadata) map[string]any {
	metadata.ID = ""
	metadata.CredentialType = ""
	raw, _ := json.Marshal(metadata)
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	return payload
}
func globalCredentialMatches(want, actual globalCredentialMetadata) bool {
	return templateValueMatches(globalCredentialMetadataPayload(want), globalCredentialMetadataPayload(actual))
}
func (want globalCredentialIntent) payload(snapshot *configurationSecretSnapshot, owner, id string) ([]byte, []ciskov1.NetworkControllerConfigurationCredentialVersion, error) {
	values, versions, err := resolveVersionedConfigurationCredentials(snapshot, owner, "network_settings", want.credentials)
	if err != nil {
		return nil, nil, err
	}
	payload := globalCredentialMetadataPayload(want.metadata)
	for field, value := range values {
		payload[field] = value
	}
	var body any
	if id != "" {
		if !validAPIID(id) {
			return nil, nil, errConfigurationStale
		}
		payload["id"] = id
		body = map[string]any{want.kind.api: payload}
	} else {
		body = map[string]any{want.kind.api: []any{payload}}
	}
	raw, err := json.Marshal(body)
	return raw, versions, err
}

// Isolated qualification uses the same executor as manifest planning.
func (c *client) planGlobalCredentialCreate(ctx context.Context, want globalCredentialIntent, snapshot *configurationSecretSnapshot, owner string) (*configurationWrite, error) {
	return (&globalCredentialExecutor{client: c, desired: []globalCredentialIntent{want}, secrets: snapshot, owner: owner}).next(ctx)
}

func (c *client) reportGlobalCredentials(ctx context.Context, desired []globalCredentialIntent) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := c.globalCredentials(ctx)
	if err != nil {
		return nil, err
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, want := range desired {
		got, exists := actual[want.kind.model][want.metadata.Name]
		change := configapi.NetworkControllerDriftChangeUpdate
		if !exists {
			change = configapi.NetworkControllerDriftChangeAdd
		} else if globalCredentialMatches(want.metadata, got) {
			continue
		}
		drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "network_settings", Resource: want.resource(), Path: "/device_credentials/" + want.kind.model, Change: change})
	}
	return drift, nil
}
