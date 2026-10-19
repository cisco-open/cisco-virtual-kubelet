// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const wirelessInterfacesPath = "/dna/intent/api/v1/wirelessSettings/interfaces"

type wirelessInterface struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"interfaceName"`
	VLAN int    `json:"vlanId"`
}

// Catalyst Center includes its built-in management interface with VLAN 0.
// Require the VLAN field so an omitted value cannot masquerade as that default.
func (v *wirelessInterface) UnmarshalJSON(raw []byte) error {
	type wire struct {
		ID   string `json:"id"`
		Name string `json:"interfaceName"`
		VLAN *int   `json:"vlanId"`
	}
	var decoded wire
	if json.Unmarshal(raw, &decoded) != nil || decoded.VLAN == nil {
		return errInvalidResponse
	}
	*v = wirelessInterface{ID: decoded.ID, Name: decoded.Name, VLAN: *decoded.VLAN}
	return nil
}

func parseWirelessInterfaces(raw []byte) ([]wirelessInterface, error) {
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
	list, ok := section["interfaces"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errors.New("invalid wireless interface collection")
	}
	out := []wirelessInterface{}
	seen := map[string]bool{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok || len(m) != 2 {
			return nil, errors.New("wireless interface requires only name and vlan_id")
		}
		name, ok := m["name"].(string)
		if !ok || !validProfileName(name) || strings.EqualFold(name, "management") || seen[name] {
			return nil, errors.New("invalid or repeated wireless interface name")
		}
		// A resolved model must contain an actual integer, not a quoted number.
		encoded, _ := json.Marshal(m["vlan_id"])
		vlan, err := strconv.Atoi(string(encoded))
		if err != nil || vlan < 1 || vlan > 4094 {
			return nil, errors.New("wireless interface vlan_id must be an integer from 1 through 4094")
		}
		seen[name] = true
		out = append(out, wirelessInterface{Name: name, VLAN: vlan})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) wirelessInterfaces(ctx context.Context) (map[string]wirelessInterface, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessInterfacesPath, nil, func(v wirelessInterface) string { return v.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessInterface{}
	for _, v := range rows {
		if !validAPIID(v.ID) || !validProfileName(v.Name) || (v.VLAN < 1 && !(v.Name == "management" && v.VLAN == 0)) || v.VLAN > 4094 {
			return nil, errInvalidResponse
		}
		if _, ok := out[v.Name]; ok {
			return nil, errInvalidResponse
		}
		out[v.Name] = v
	}
	return out, nil
}

type wirelessInterfaceExecutor struct {
	client  *client
	desired []wirelessInterface
}

func (e *wirelessInterfaceExecutor) count() int { return len(e.desired) }
func (e *wirelessInterfaceExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.wirelessInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	for _, want := range e.desired {
		got, exists := actual[want.Name]
		if exists && got.VLAN == want.VLAN {
			continue
		}
		method, path, action := http.MethodPost, wirelessInterfacesPath, "Create"
		identity := configurationResourceIdentity{}
		if exists {
			method, path, action = http.MethodPut, wirelessInterfacesPath+"/"+url.PathEscape(got.ID), "Update"
			identity.target = got.ID
		}
		body, _ := json.Marshal(want)
		write := &configurationWrite{resource: "interface:" + want.Name, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
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
		if err := guardWirelessDefinitionWrite(write, actual, []string{want.Name}, e.client.wirelessInterfaces, func() any { return e.desired }); err != nil {
			return nil, err
		}
		return write, nil
	}
	return nil, nil
}
func (e *wirelessInterfaceExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID != "" || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	actual, err := e.client.wirelessInterfaces(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource == "interface:"+want.Name {
			got, ok := actual[want.Name]
			return ok && got.VLAN == want.VLAN && (op.TargetID == "" || op.TargetID == got.ID), nil
		}
	}
	return false, errConfigurationTransition
}
func (e *wirelessInterfaceExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.wirelessInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, ok := actual[want.Name]
		if ok && got.VLAN == want.VLAN {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: want.Name, Path: "/interfaces", Change: change})
	}
	return out, nil
}
