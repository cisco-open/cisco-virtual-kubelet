// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func credentialSlot(kind string) string {
	switch kind {
	case "cli_credentials":
		return "cliCredentialsId"
	case "https_read_credentials":
		return "httpReadCredentialsId"
	case "https_write_credentials":
		return "httpWriteCredentialsId"
	case "snmpv2_read_credentials":
		return "snmpv2cReadCredentialsId"
	case "snmpv2_write_credentials":
		return "snmpv2cWriteCredentialsId"
	case "snmpv3_credentials":
		return "snmpv3CredentialsId"
	}
	return ""
}

type siteCredentialIntent struct{ site, siteKind, kind, name string }

func (d siteCredentialIntent) resource() string {
	return "credential-assignment:" + d.site + "#" + d.kind
}

type siteCredentialState struct {
	id               string
	inherited, unset bool
}

func siteCredentialsPath(id string) string { return sitesPath + "/" + id + "/deviceCredentials" }

// Read local overrides explicitly. An inherited match is not a local assignment,
// and {} (disabled) must never be confused with null (inherit).
func (c *client) siteCredentialResponse(ctx context.Context, id string) (map[string]json.RawMessage, error) {
	if !validAPIID(id) {
		return nil, errInvalidResponse
	}
	raw, err := c.get(ctx, siteCredentialsPath(id), url.Values{"_inherited": {"false"}}, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Response map[string]json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil {
		return nil, errInvalidResponse
	}
	return envelope.Response, nil
}

func (c *client) siteCredentials(ctx context.Context, id string) (map[string]siteCredentialState, error) {
	response, err := c.siteCredentialResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	out := map[string]siteCredentialState{}
	for _, k := range globalCredentialKinds {
		value, present := response[credentialSlot(k.model)]
		if !present {
			return nil, errInvalidResponse
		}
		state := siteCredentialState{}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			state.inherited = true
		} else {
			var slot map[string]json.RawMessage
			if json.Unmarshal(value, &slot) != nil || slot == nil {
				return nil, errInvalidResponse
			}
			if len(slot) == 0 {
				state.unset = true
			} else {
				// The local-only endpoint must not return an inherited effective value.
				var inheritedID string
				if v, ok := slot["inheritedSiteId"]; ok && string(v) != "null" {
					if json.Unmarshal(v, &inheritedID) != nil || inheritedID != "" {
						return nil, errInvalidResponse
					}
				}
				v, ok := slot["credentialsId"]
				if !ok {
					return nil, errInvalidResponse
				}
				if string(v) == "null" {
					state.inherited = true
				} else if json.Unmarshal(v, &state.id) != nil || !validAPIID(state.id) {
					return nil, errInvalidResponse
				}
			}
		}
		out[k.model] = state
	}
	return out, nil
}

type siteCredentialObservation struct {
	desired              siteCredentialIntent
	siteID, credentialID string
	matches              bool
	pending              bool
}
type siteCredentialExecutor struct {
	client  *client
	desired []siteCredentialIntent
}

func (e *siteCredentialExecutor) count() int {
	if e == nil {
		return 0
	}
	return len(e.desired)
}
func (e *siteCredentialExecutor) observe(ctx context.Context) ([]siteCredentialObservation, error) {
	return e.observeWithDefinitions(ctx, nil)
}
func (e *siteCredentialExecutor) observeWithDefinitions(ctx context.Context, definitions map[string]bool) ([]siteCredentialObservation, error) {
	if e.count() == 0 {
		return nil, nil
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		if _, found := byPath[site.NameHierarchy]; found {
			return nil, errInvalidResponse
		}
		byPath[site.NameHierarchy] = site
	}
	credentials, err := e.client.globalCredentials(ctx)
	if err != nil {
		return nil, err
	}
	observed := map[string]map[string]siteCredentialState{}
	out := []siteCredentialObservation{}
	for _, d := range e.desired {
		site, exists := byPath[d.site]
		if !exists || site.Type != d.siteKind || !validAPIID(site.ID) || credentialSlot(d.kind) == "" {
			return nil, errors.New("credential assignment site is missing or incompatible")
		}
		id := ""
		pending := false
		if d.name != "" {
			credential, exists := credentials[d.kind][d.name]
			if !exists && definitions[d.kind+"/"+d.name] {
				pending = true
			} else if !exists || !validAPIID(credential.ID) {
				return nil, errors.New("credential assignment name does not resolve uniquely")
			}
			id = credential.ID
		}
		if _, found := observed[site.ID]; !found {
			observed[site.ID], err = e.client.siteCredentials(ctx, site.ID)
			if err != nil {
				return nil, err
			}
		}
		state := observed[site.ID][d.kind]
		matches := state.id == id && !state.inherited && !state.unset
		if id == "" {
			matches = state.inherited
		}
		out = append(out, siteCredentialObservation{d, site.ID, id, matches && !pending, pending})
	}
	return out, nil
}
func (e *siteCredentialExecutor) next(ctx context.Context) (*configurationWrite, error) {
	return e.nextWithDefinitions(ctx, nil)
}
func (e *siteCredentialExecutor) nextWithDefinitions(ctx context.Context, definitions map[string]bool) (*configurationWrite, error) {
	// The lab turns nested null into disabled, while top-level null fails its
	// task. Do not expose either behavior as successful parent inheritance.
	for _, d := range e.desired {
		if d.name == "" {
			return nil, errors.New("credential inheritance removal is not qualified for execution")
		}
	}
	observations, err := e.observeWithDefinitions(ctx, definitions)
	if err != nil {
		return nil, err
	}
	for _, o := range observations {
		if o.matches || o.pending {
			continue
		}
		// Omission preserves every undeclared slot.
		body, _ := json.Marshal(map[string]any{credentialSlot(o.desired.kind): map[string]any{"credentialsId": o.credentialID}})
		return &configurationWrite{resource: o.desired.resource(), action: "Update", identity: configurationResourceIdentity{target: o.siteID, parent: o.credentialID}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, http.MethodPut, siteCredentialsPath(o.siteID), body)
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
		}}, nil
	}
	return nil, nil
}
func (e *siteCredentialExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	// Persisted operations must repeat complete task/dependency/effect proof at
	// verification. Legacy isolated executor probes carry no task identity.
	if op != nil && op.TaskID != "" {
		return e.observeKnownTask(ctx, op, time.Now())
	}
	if op.Action != "Update" || !validAPIID(op.TargetID) {
		return false, errConfigurationTransition
	}
	observations, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range observations {
		if o.desired.resource() != op.Resource {
			continue
		}
		if o.siteID != op.TargetID || o.credentialID != op.ParentID {
			return false, errors.New("credential assignment dependency was replaced")
		}
		return o.matches, nil
	}
	return false, errConfigurationTransition
}
func (e *siteCredentialExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	observations, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, o := range observations {
		if !o.matches {
			out = append(out, configapi.NetworkControllerDriftEntry{Section: "network_settings", Resource: o.desired.resource(), Path: "/" + o.desired.kind, Change: configapi.NetworkControllerDriftChangeUpdate})
		}
	}
	return out, nil
}
