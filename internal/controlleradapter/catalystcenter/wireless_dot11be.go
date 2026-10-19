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
	"sort"
)

const wirelessDot11bePath = "/dna/intent/api/v1/wirelessSettings/dot11beProfiles"

type wirelessDot11beProfile struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"profileName"`
	Down     bool   `json:"ofdmaDownLink"`
	Up       bool   `json:"ofdmaUpLink"`
	MIMODown bool   `json:"muMimoDownLink"`
	MIMOUp   bool   `json:"muMimoUpLink"`
	MultiRU  bool   `json:"ofdmaMultiRu"`
	// Newer API fields are outside the pinned model. Preserve them on PUT.
	Default *bool           `json:"default,omitempty"`
	MLO     json.RawMessage `json:"mloGroup,omitempty"`
}

func parseWirelessDot11be(raw []byte) ([]wirelessDot11beProfile, error) {
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
	list, ok := section["dot11be_profiles"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errors.New("invalid 802.11be profile collection")
	}
	fields := map[string]string{"ofdma_down_link": "ofdmaDownLink", "ofdma_up_link": "ofdmaUpLink", "mu_mimo_down_link": "muMimoDownLink", "mu_mimo_up_link": "muMimoUpLink", "ofdma_multi_ru": "ofdmaMultiRu"}
	out := []wirelessDot11beProfile{}
	seen := map[string]bool{}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		name, ok := m["name"].(string)
		if !ok || !validProfileName(name) || seen[name] {
			return nil, errors.New("invalid or repeated 802.11be profile name")
		}
		payload := map[string]any{"profileName": name, "ofdmaDownLink": true, "ofdmaUpLink": true, "muMimoDownLink": false, "muMimoUpLink": false, "ofdmaMultiRu": false}
		for k, v := range m {
			if k == "name" {
				continue
			}
			dest, ok := fields[k]
			flag, valid := v.(bool)
			if !ok || !valid {
				return nil, errors.New("unsupported or non-boolean 802.11be field")
			}
			payload[dest] = flag
		}
		encoded, _ := json.Marshal(payload)
		var p wirelessDot11beProfile
		_ = json.Unmarshal(encoded, &p)
		seen[name] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) wirelessDot11beProfiles(ctx context.Context) (map[string]wirelessDot11beProfile, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessDot11bePath, nil, func(v json.RawMessage) string {
		var id struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(v, &id)
		return id.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessDot11beProfile{}
	for _, raw := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, errInvalidResponse
		}
		for _, key := range []string{"ofdmaDownLink", "ofdmaUpLink", "muMimoDownLink", "muMimoUpLink", "ofdmaMultiRu"} {
			var flag *bool
			if json.Unmarshal(fields[key], &flag) != nil || flag == nil {
				return nil, errInvalidResponse
			}
		}
		var p wirelessDot11beProfile
		if json.Unmarshal(raw, &p) != nil || !validAPIID(p.ID) || !validProfileName(p.Name) {
			return nil, errInvalidResponse
		}
		if len(p.MLO) > 0 && string(p.MLO) != "null" {
			var m map[string]bool
			if json.Unmarshal(p.MLO, &m) != nil {
				return nil, errInvalidResponse
			}
		}
		if _, ok := out[p.Name]; ok {
			return nil, errInvalidResponse
		}
		out[p.Name] = p
	}
	return out, nil
}
func wirelessDot11beMatches(want, got wirelessDot11beProfile) bool {
	return want.Name == got.Name && want.Down == got.Down && want.Up == got.Up && want.MIMODown == got.MIMODown && want.MIMOUp == got.MIMOUp && want.MultiRU == got.MultiRU
}

type wirelessDot11beExecutor struct {
	client  *client
	desired []wirelessDot11beProfile
}

func (e *wirelessDot11beExecutor) count() int { return len(e.desired) }
func (e *wirelessDot11beExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.wirelessDot11beProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, want := range e.desired {
		got, exists := actual[want.Name]
		if exists && wirelessDot11beMatches(want, got) {
			continue
		}
		method, path, action := http.MethodPost, wirelessDot11bePath, "Create"
		identity := configurationResourceIdentity{}
		if exists {
			method, path, action = http.MethodPut, wirelessDot11bePath+"/"+url.PathEscape(got.ID), "Update"
			identity.target = got.ID
			want.Default = got.Default
			want.MLO = got.MLO
		}
		body, _ := json.Marshal(want)
		write := &configurationWrite{resource: "dot11be-profile:" + want.Name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
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
		if err := guardWirelessDefinitionWrite(write, actual, []string{want.Name}, e.client.wirelessDot11beProfiles, func() any { return e.desired }); err != nil {
			return nil, err
		}
		return write, nil
	}
	return nil, nil
}
func (e *wirelessDot11beExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	actual, err := e.client.wirelessDot11beProfiles(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource == "dot11be-profile:"+want.Name {
			got, ok := actual[want.Name]
			return ok && wirelessDot11beMatches(want, got) && (op.TargetID == "" || op.TargetID == got.ID), nil
		}
	}
	return false, errConfigurationTransition
}
func (e *wirelessDot11beExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.wirelessDot11beProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, ok := actual[want.Name]
		if ok && wirelessDot11beMatches(want, got) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: "dot11be-profile:" + want.Name, Path: "/dot11be_profiles", Change: change})
	}
	return out, nil
}
