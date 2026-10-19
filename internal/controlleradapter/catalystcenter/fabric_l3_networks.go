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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const fabricL3NetworksPath = "/dna/intent/api/v1/sda/layer3VirtualNetworks"

type fabricL3Network struct {
	ID               string   `json:"id"`
	Name             string   `json:"virtualNetworkName"`
	Fabrics          []string `json:"fabricIds"`
	Anchor           string   `json:"anchoredSiteId"`
	membershipsKnown bool
}

// Definition reads can tolerate an absent membership list; replacement PUTs
// cannot. Keep that distinction without changing the existing definition flow.
func (n *fabricL3Network) UnmarshalJSON(raw []byte) error {
	type wire fabricL3Network
	var decoded wire
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(raw, &presence); err != nil {
		return err
	}
	field, present := presence["fabricIds"]
	decoded.membershipsKnown = present && string(field) != "null"
	*n = fabricL3Network(decoded)
	return nil
}

// Global definitions own names, not fabric membership. Association/anchoring
// must be explicitly implemented before admitting their model fields.
func parseFabricL3Networks(raw []byte) ([]string, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("fabric requires a section-specific model")
	}
	fabric, ok := root["fabric"].(map[string]any)
	if !ok || len(fabric) != 1 {
		return nil, errors.New("unsupported fabric collection")
	}
	list, ok := fabric["l3_virtual_networks"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errors.New("invalid L3 virtual network definitions")
	}
	names := []string{}
	seen := map[string]bool{}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok || len(m) != 1 {
			return nil, errors.New("unresolved or unsupported L3 virtual network fields")
		}
		name, ok := m["name"].(string)
		if !ok || !validProfileName(name) || seen[name] {
			return nil, errors.New("invalid or duplicate virtual network name")
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (c *client) fabricL3Networks(ctx context.Context) (map[string]fabricL3Network, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, fabricL3NetworksPath, nil, func(n fabricL3Network) string { return n.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]fabricL3Network{}
	for _, n := range rows {
		if !validAPIID(n.ID) || !validProfileName(n.Name) {
			return nil, errInvalidResponse
		}
		if _, ok := out[n.Name]; ok {
			return nil, errInvalidResponse
		}
		ids := map[string]bool{}
		for _, id := range n.Fabrics {
			if !validAPIID(id) || ids[id] {
				return nil, errInvalidResponse
			}
			ids[id] = true
		}
		if n.Anchor != "" && (!validAPIID(n.Anchor) || !ids[n.Anchor]) {
			return nil, errInvalidResponse
		}
		out[n.Name] = n
	}
	return out, nil
}

type fabricL3Executor struct {
	client  *client
	names   []string
	anchors map[string]string
}

func (e *fabricL3Executor) count() int { return len(e.names) }
func (e *fabricL3Executor) next(ctx context.Context) (*configurationWrite, error) {
	if len(e.anchors) > 0 {
		return nil, errConfigurationDeviceAdmission
	}
	actual, err := e.client.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for _, name := range e.names {
		if got, exists := actual[name]; exists {
			if got.Anchor != "" {
				return nil, errors.New("existing virtual network is anchored; explicit anchoring intent is required")
			}
			continue
		}
		if name == "INFRA_VN" || name == "DEFAULT_VN" {
			return nil, errors.New("system virtual network is missing; cannot create it")
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return nil, nil
	}
	name := missing[0]
	body, _ := json.Marshal([]map[string]string{{"virtualNetworkName": name}})
	return &configurationWrite{resource: "l3-virtual-network:" + name, action: "Create", submit: func(ctx context.Context) (Task, error) {
		raw, err := e.client.mutate(ctx, http.MethodPost, fabricL3NetworksPath, body)
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
func (e *fabricL3Executor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if len(e.anchors) > 0 {
		return false, errConfigurationDeviceAdmission
	}
	if op.Action != "Create" || op.TargetID != "" || op.ParentID != "" || op.TargetSerial != "" {
		return false, errConfigurationTransition
	}
	actual, err := e.client.fabricL3Networks(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range e.names {
		if op.Resource == "l3-virtual-network:"+name {
			n, ok := actual[name]
			return ok && n.Anchor == "" && len(n.Fabrics) == 0, nil
		}
	}
	return false, errConfigurationTransition
}
func (e *fabricL3Executor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	anchors, err := e.resolveAnchors(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, name := range e.names {
		got, ok := actual[name]
		if ok && got.Anchor == anchors[name] {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: "l3-virtual-network:" + name, Path: "/l3_virtual_networks", Change: change})
	}
	return out, nil
}
