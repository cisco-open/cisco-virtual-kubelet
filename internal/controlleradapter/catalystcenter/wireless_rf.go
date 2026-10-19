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
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type wirelessRFMetadata struct {
	id            string
	fields        map[string]any
	unknownFields bool
}

func wirelessRFUnknownFields(row map[string]any, prefix string) bool {
	for key, value := range row {
		path := prefix + key
		if prefix == "" && (key == "id" || key == "enableCustom") {
			continue
		}
		known, branch := false, false
		for _, field := range wirelessRFFields {
			if path == field.path {
				known = true
				break
			}
			if strings.HasPrefix(field.path, path+".") {
				branch = true
			}
		}
		if known {
			continue
		}
		child, ok := value.(map[string]any)
		if !branch || !ok || wirelessRFUnknownFields(child, path+".") {
			return true
		}
	}
	return false
}

func (c *client) wirelessRFProfiles(ctx context.Context) (map[string]wirelessRFMetadata, error) {
	get := func(ctx context.Context, path string, query url.Values) ([]byte, error) {
		return c.get(ctx, path, query, 200)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessRFPath, nil, func(raw json.RawMessage) string {
		var id struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &id)
		return id.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessRFMetadata{}
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			return nil, errInvalidResponse
		}
		id, _ := row["id"].(string)
		name, _ := row["rfProfileName"].(string)
		if !validAPIID(id) || !validProfileName(name) {
			return nil, errInvalidResponse
		}
		if _, exists := out[name]; exists {
			return nil, errInvalidResponse
		}
		fields := map[string]any{}
		for _, field := range wirelessRFFields {
			value, exists := getWirelessAPField(row, field.path)
			if !exists {
				continue
			}
			// Built-in profiles return their inherited 6 GHz parent even though
			// the pinned write schema permits only CUSTOM for this band.
			if field.path == "radioType6GHzProperties.parentProfile" {
				field.enums = "CUSTOM|LOW|TYPICAL|HIGH"
			}
			v, err := wirelessAPValue(field, value)
			if err != nil {
				return nil, fmt.Errorf("%w: RF field %s", errInvalidResponse, field.path)
			}
			v, err = normalizeWirelessRFList(field.path, v)
			if err != nil {
				return nil, fmt.Errorf("%w: RF list %s", errInvalidResponse, field.path)
			}
			setWirelessAPField(fields, field.path, v)
		}
		for _, key := range []string{"defaultRfProfile", "enableRadioTypeA", "enableRadioTypeB", "enableRadioType6GHz"} {
			if _, ok := fields[key].(bool); !ok {
				return nil, errInvalidResponse
			}
		}
		out[name] = wirelessRFMetadata{id: id, fields: fields, unknownFields: wirelessRFUnknownFields(row, "")}
	}
	return out, nil
}

type wirelessRFExecutor struct {
	client  *client
	desired []wirelessRFIntent
}

func (e *wirelessRFExecutor) count() int { return len(e.desired) }

func (e *wirelessRFExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.wirelessRFProfiles(ctx)
	if err != nil {
		return nil, err
	}
	// A default promotion changes other profiles as a side effect. Require the
	// currently selected default to be explicitly managed and deselected first.
	selected := ""
	for _, want := range e.desired {
		if want.fields["defaultRfProfile"] == true {
			selected = want.name
		}
	}
	for name, got := range actual {
		if selected == "" || name == selected || got.fields["defaultRfProfile"] != true {
			continue
		}
		deselect := false
		for _, want := range e.desired {
			if want.name == name && want.fields["defaultRfProfile"] == false {
				deselect = true
			}
		}
		if !deselect {
			return nil, errors.New("RF default promotion requires explicit deselection of the current default")
		}
	}
	for _, want := range e.desired {
		got, exists := actual[want.name]
		if exists && wirelessAPPublicMatches(want.fields, got.fields) {
			continue
		}
		if exists && got.unknownFields {
			return nil, errors.New("RF profile has unmapped API fields; refusing an update that could discard them")
		}
		if err := validateWirelessRFRanges(mergeWirelessAPFields(got.fields, want.fields)); err != nil {
			return nil, err
		}
		switch strings.ToUpper(want.name) {
		case "LOW", "TYPICAL", "HIGH":
			return nil, errors.New("built-in RF profile mutation is not supported")
		}
	}
	ordered := append([]wirelessRFIntent(nil), e.desired...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].fields["defaultRfProfile"] == false && ordered[j].fields["defaultRfProfile"] == true
	})
	for _, want := range ordered {
		got, exists := actual[want.name]
		if exists && wirelessAPPublicMatches(want.fields, got.fields) {
			continue
		}
		method, path, action := http.MethodPost, wirelessRFPath, "Create"
		identity := configurationResourceIdentity{}
		payload := want.fields
		if exists {
			method, path, action = http.MethodPut, wirelessRFPath+"/"+url.PathEscape(got.id), "Update"
			identity.target = got.id
			payload = mergeWirelessAPFields(got.fields, want.fields)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		write := &configurationWrite{resource: "rf-profile:" + want.name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
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
		if err := guardWirelessDefinitionWrite(write, actual, nil, e.client.wirelessRFProfiles, func() any { return wirelessRFDispatchIntent(e.desired) }); err != nil {
			return nil, err
		}
		return write, nil
	}
	return nil, nil
}

func (e *wirelessRFExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	actual, err := e.client.wirelessRFProfiles(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource != "rf-profile:"+want.name {
			continue
		}
		got, exists := actual[want.name]
		if !exists || !wirelessAPPublicMatches(want.fields, got.fields) || (op.TargetID != "" && got.id != op.TargetID) {
			return false, nil
		}
		if want.fields["defaultRfProfile"] == true {
			for name, other := range actual {
				if name != want.name && other.fields["defaultRfProfile"] == true {
					return false, nil
				}
			}
		}
		return true, nil
	}
	return false, errConfigurationTransition
}

func (e *wirelessRFExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.wirelessRFProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, exists := actual[want.name]
		if exists && wirelessAPPublicMatches(want.fields, got.fields) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: "rf-profile:" + want.name, Path: "/rf_profiles", Change: change})
	}
	return out, nil
}
