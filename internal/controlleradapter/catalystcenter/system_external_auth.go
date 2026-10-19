// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const externalAuthPath = "/dna/system/api/v1/users/external-authentication"
const externalAAAAttributePath = "/dna/system/api/v1/users/external-servers/aaa-attribute"

func (c *client) hasAuthenticationPolicyServer(ctx context.Context) (bool, error) {
	servers, err := c.authenticationPolicyServers(ctx)
	return len(servers) > 0, err
}

type externalAuthIntent struct {
	enabled   bool
	attribute *string
}

func parseExternalAuth(raw []byte) (externalAuthIntent, error) {
	var out externalAuthIntent
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return out, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return out, errors.New("system settings require a section-specific model")
	}
	settings, ok := root["system_settings"].(map[string]any)
	if !ok || len(settings) != 1 {
		return out, errors.New("unsupported system settings; authentication policy servers require their own execution contract")
	}
	auth, ok := settings["external_authentication"].(map[string]any)
	if !ok {
		return out, errors.New("invalid external authentication intent")
	}
	for key, value := range auth {
		switch key {
		case "enabled":
			v, ok := value.(bool)
			if !ok {
				return out, errors.New("invalid external authentication enable flag")
			}
			out.enabled = v
		case "aaa_attribute":
			v, ok := value.(string)
			if !ok || len(v) > 256 || v == "" || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\x00\r\n") {
				return out, errors.New("invalid external authentication AAA attribute")
			}
			out.attribute = &v
		default:
			return out, errors.New("unsupported external authentication field")
		}
	}
	// The pinned module defaults enabled to false when this object is present.
	return out, nil
}
func (c *client) externalAuthentication(ctx context.Context) (bool, error) {
	raw, err := c.get(ctx, externalAuthPath, nil, 200)
	if err != nil {
		return false, err
	}
	var body struct {
		Response struct {
			Flags []struct {
				Enabled *bool `json:"enabled"`
			} `json:"external-authentication-flag"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Response.Flags) != 1 || body.Response.Flags[0].Enabled == nil {
		return false, errInvalidResponse
	}
	return *body.Response.Flags[0].Enabled, nil
}
func (c *client) externalAAAAttribute(ctx context.Context) (string, error) {
	raw, err := c.get(ctx, externalAAAAttributePath, nil, 200)
	if err != nil {
		return "", err
	}
	var body struct {
		Response struct {
			Attributes []struct {
				Name *string `json:"attributeName"`
			} `json:"aaa-attributes"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Response.Attributes) != 1 || body.Response.Attributes[0].Name == nil {
		return "", errInvalidResponse
	}
	return *body.Response.Attributes[0].Name, nil
}

// HTTP 202, missing/invalid JSON, and a task-shaped response cannot be mistaken
// for synchronous completion. Readback still follows every validated response.
func (c *client) submitExternalAuth(ctx context.Context, path string, payload any) error {
	if path != externalAuthPath && path != externalAAAAttributePath {
		return errConfigurationTransition
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errConfigurationTransition
	}
	raw, err := c.mutateQueryStatus(ctx, http.MethodPost, path, nil, body, 200)
	if err != nil {
		return err
	}
	var receipt struct {
		Response struct {
			Message string `json:"message"`
			TaskID  string `json:"taskId"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || strings.TrimSpace(receipt.Response.Message) == "" || receipt.Response.TaskID != "" {
		return errInvalidResponse
	}
	return nil
}

type externalAuthExecutor struct {
	client  *client
	desired externalAuthIntent
}

func (e *externalAuthExecutor) count() int {
	if e.desired.attribute != nil {
		return 2
	}
	return 1
}
func (e *externalAuthExecutor) observe(ctx context.Context) (bool, string, error) {
	enabled, err := e.client.externalAuthentication(ctx)
	if err != nil {
		return false, "", err
	}
	attribute := ""
	if e.desired.attribute != nil {
		attribute, err = e.client.externalAAAAttribute(ctx)
	}
	return enabled, attribute, err
}
func (e *externalAuthExecutor) next(ctx context.Context) (*configurationWrite, error) {
	enabled, attribute, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	resource, path := "external_authentication", externalAuthPath
	payload := map[string]any{"enable": e.desired.enabled}
	if enabled == e.desired.enabled {
		if e.desired.attribute == nil || attribute == *e.desired.attribute {
			return nil, nil
		}
		resource, path = "external_authentication_aaa_attribute", externalAAAAttributePath
		payload = map[string]any{"attributeName": *e.desired.attribute}
	}
	present, err := e.client.hasAuthenticationPolicyServer(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errors.New("external authentication mutation requires an authentication policy server")
	}
	return &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: resource}, submitCompleted: func(ctx context.Context) error { return e.client.submitExternalAuth(ctx, path, payload) }}, nil
}
func (e *externalAuthExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Update" || op.ParentID != "" || op.TargetSerial != "" || op.TargetID != op.Resource {
		return false, errConfigurationTransition
	}
	switch op.Resource {
	case "external_authentication":
		enabled, err := e.client.externalAuthentication(ctx)
		return enabled == e.desired.enabled, err
	case "external_authentication_aaa_attribute":
		if e.desired.attribute == nil {
			return false, errConfigurationTransition
		}
		attribute, err := e.client.externalAAAAttribute(ctx)
		return attribute == *e.desired.attribute, err
	}
	return false, errConfigurationTransition
}
func (e *externalAuthExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	enabled, attribute, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	if enabled != e.desired.enabled {
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "system_settings", Resource: "external_authentication", Path: "/external_authentication/enabled", Change: configapi.NetworkControllerDriftChangeUpdate})
	}
	if e.desired.attribute != nil && attribute != *e.desired.attribute {
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "system_settings", Resource: "external_authentication_aaa_attribute", Path: "/external_authentication/aaa_attribute", Change: configapi.NetworkControllerDriftChangeUpdate})
	}
	return out, nil
}
