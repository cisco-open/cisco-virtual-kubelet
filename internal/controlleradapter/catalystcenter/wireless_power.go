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
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
)

const wirelessPowerPath = "/dna/intent/api/v1/wirelessSettings/powerProfiles"

type wirelessPowerRule struct {
	InterfaceType  string `json:"interfaceType"`
	InterfaceID    string `json:"interfaceId"`
	ParameterType  string `json:"parameterType"`
	ParameterValue string `json:"parameterValue"`
}
type wirelessPowerProfile struct {
	ID          string              `json:"id,omitempty"`
	Name        string              `json:"profileName"`
	Description *string             `json:"description,omitempty"`
	Rules       []wirelessPowerRule `json:"rules"`
}

func validateWirelessPower(p wirelessPowerProfile) error {
	if !validProfileName(p.Name) || p.Rules == nil || len(p.Rules) > 1000 {
		return errInvalidResponse
	}
	if p.Description != nil && (len(*p.Description) > 4096 || strings.ContainsRune(*p.Description, 0)) {
		return errInvalidResponse
	}
	for _, r := range p.Rules {
		if !slices.Contains([]string{"ETHERNET", "RADIO", "USB"}, r.InterfaceType) || !slices.Contains([]string{"GIGABITETHERNET0", "GIGABITETHERNET1", "LAN1", "LAN2", "LAN3", "6GHZ", "5GHZ", "SECONDARY_5GHZ", "2_4GHZ", "USB0"}, r.InterfaceID) || !slices.Contains([]string{"SPEED", "SPATIALSTREAM", "STATE"}, r.ParameterType) || !slices.Contains([]string{"5000MBPS", "2500MBPS", "1000MBPS", "100MBPS", "EIGHT_BY_EIGHT", "FOUR_BY_FOUR", "THREE_BY_THREE", "TWO_BY_TWO", "ONE_BY_ONE", "DISABLE"}, r.ParameterValue) {
			return errInvalidResponse
		}
	}
	return nil
}

// Native NCND86002/NCND86004 reject blank descriptions and empty rules. Keep
// desired validation separate from observation: optional appliance metadata
// must not be mistaken for a writable request, or invalidate unrelated reads.
func validateWirelessPowerIntent(p wirelessPowerProfile) error {
	if err := validateWirelessPower(p); err != nil {
		return err
	}
	if len(p.Rules) == 0 || p.Description != nil && strings.TrimSpace(*p.Description) == "" {
		return errors.New("power profiles require at least one rule and a nonblank explicit description; omit description to retain its current value")
	}
	return nil
}
func parseWirelessPower(raw []byte) ([]wirelessPowerProfile, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("wireless requires a section-specific model")
	}
	section, ok := root["wireless"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errors.New("unsupported wireless collection")
	}
	list, ok := section["power_profiles"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errors.New("invalid power profile collection")
	}
	out := []wirelessPowerProfile{}
	seen := map[string]bool{}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		for key, value := range m {
			if (key != "name" && key != "description" && key != "rules") || value == nil {
				return nil, errors.New("unsupported or null power profile field")
			}
		}
		rules, ok := m["rules"].([]any)
		if !ok {
			return nil, errors.New("power profile rules must be an explicit list")
		}
		transformed := []any{}
		fields := map[string]string{"interface_type": "interfaceType", "interface_id": "interfaceId", "parameter_type": "parameterType", "parameter_value": "parameterValue"}
		for _, v := range rules {
			r, ok := v.(map[string]any)
			if !ok || len(r) != 4 {
				return nil, errInvalidResponse
			}
			rule := map[string]any{}
			for k, v := range r {
				dest, ok := fields[k]
				if !ok || v == nil {
					return nil, errors.New("unsupported or null power profile rule field")
				}
				rule[dest] = v
			}
			transformed = append(transformed, rule)
		}
		payload := map[string]any{"profileName": m["name"], "rules": transformed}
		if d, ok := m["description"]; ok {
			payload["description"] = d
		}
		encoded, _ := json.Marshal(payload)
		var p wirelessPowerProfile
		if json.Unmarshal(encoded, &p) != nil || seen[p.Name] {
			return nil, errors.New("invalid or repeated power profile")
		}
		if err := validateWirelessPowerIntent(p); err != nil {
			return nil, err
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) wirelessPowerProfiles(ctx context.Context) (map[string]wirelessPowerProfile, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessPowerPath, nil, func(p wirelessPowerProfile) string { return p.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessPowerProfile{}
	for _, p := range rows {
		if !validAPIID(p.ID) || validateWirelessPower(p) != nil {
			return nil, errInvalidResponse
		}
		if _, ok := out[p.Name]; ok {
			return nil, errInvalidResponse
		}
		out[p.Name] = p
	}
	return out, nil
}
func wirelessPowerMatches(want, got wirelessPowerProfile) bool {
	return want.Name == got.Name && slices.Equal(want.Rules, got.Rules) && (want.Description == nil || (got.Description != nil && *want.Description == *got.Description))
}

type wirelessPowerExecutor struct {
	client  *client
	desired []wirelessPowerProfile
}

func (e *wirelessPowerExecutor) count() int { return len(e.desired) }
func (e *wirelessPowerExecutor) next(ctx context.Context) (*configurationWrite, error) {
	for _, want := range e.desired {
		if err := validateWirelessPowerIntent(want); err != nil {
			return nil, err
		}
	}
	actual, err := e.client.wirelessPowerProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, want := range e.desired {
		got, exists := actual[want.Name]
		if exists && wirelessPowerMatches(want, got) {
			continue
		}
		method, path, action := http.MethodPost, wirelessPowerPath, "Create"
		identity := configurationResourceIdentity{}
		if exists {
			method, path, action = http.MethodPut, wirelessPowerPath+"/"+url.PathEscape(got.ID), "Update"
			identity.target = got.ID
			if want.Description == nil && got.Description != nil && strings.TrimSpace(*got.Description) != "" {
				want.Description = got.Description
			}
		}
		body, _ := json.Marshal(want)
		write := &configurationWrite{resource: "power-profile:" + want.Name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
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
		if err := guardWirelessDefinitionWrite(write, actual, []string{want.Name}, e.client.wirelessPowerProfiles, func() any { return e.desired }); err != nil {
			return nil, err
		}
		return write, nil
	}
	return nil, nil
}
func (e *wirelessPowerExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	actual, err := e.client.wirelessPowerProfiles(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource == "power-profile:"+want.Name {
			got, ok := actual[want.Name]
			return ok && wirelessPowerMatches(want, got) && (op.TargetID == "" || op.TargetID == got.ID), nil
		}
	}
	return false, errConfigurationTransition
}
func (e *wirelessPowerExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.wirelessPowerProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, ok := actual[want.Name]
		if ok && wirelessPowerMatches(want, got) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: "power-profile:" + want.Name, Path: "/power_profiles", Change: change})
	}
	return out, nil
}
