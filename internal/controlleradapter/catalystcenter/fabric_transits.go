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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const fabricTransitsPath = "/dna/intent/api/v1/sda/transitNetworks"

type ipTransitSettings struct {
	Protocol string `json:"routingProtocolName"`
	ASN      string `json:"autonomousSystemNumber"`
}
type sdaTransitSettings struct {
	Multicast     *bool    `json:"isMulticastOverTransitEnabled,omitempty"`
	ControlPlanes []string `json:"controlPlaneNetworkDeviceIds"`
}

type fabricTransit struct {
	ID   string              `json:"id,omitempty"`
	Name string              `json:"name"`
	Type string              `json:"type"`
	IP   *ipTransitSettings  `json:"ipTransitSettings,omitempty"`
	SDA  *sdaTransitSettings `json:"sdaTransitSettings,omitempty"`
}

func transitASN(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", errInvalidResponse
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return "", errInvalidResponse
	}
	n, err := strconv.ParseUint(string(number), 10, 32)
	if err != nil || n == 0 {
		return "", errors.New("transit ASN must be an integer from 1 through 4294967295")
	}
	return strconv.FormatUint(n, 10), nil
}
func parseFabricTransits(raw []byte) ([]fabricTransit, error) {
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
	list, ok := fabric["transits"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errors.New("invalid transit collection")
	}
	out := []fabricTransit{}
	seen := map[string]bool{}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("invalid transit")
		}
		for key, value := range m {
			switch key {
			case "name", "type", "routing_protocol_name", "autonomous_system_number":
			case "create_per_site":
				if flag, ok := value.(bool); !ok || flag {
					return nil, errors.New("per-site transit scope is not implemented")
				}
			default:
				return nil, errors.New("unsupported transit field")
			}
		}
		name, ok := m["name"].(string)
		if !ok || !validProfileName(name) || seen[name] || m["type"] != "IP_BASED_TRANSIT" || m["routing_protocol_name"] != "BGP" {
			return nil, errors.New("invalid or unsupported transit identity/type/protocol")
		}
		asn, err := transitASN(m["autonomous_system_number"])
		if err != nil {
			return nil, err
		}
		seen[name] = true
		out = append(out, fabricTransit{Name: name, Type: "IP_BASED_TRANSIT", IP: &ipTransitSettings{Protocol: "BGP", ASN: asn}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) fabricTransits(ctx context.Context) (map[string]fabricTransit, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, fabricTransitsPath, nil, func(t fabricTransit) string { return t.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]fabricTransit{}
	for _, t := range rows {
		if !validAPIID(t.ID) || !validProfileName(t.Name) {
			return nil, errInvalidResponse
		}
		if _, ok := out[t.Name]; ok {
			return nil, errInvalidResponse
		}
		switch t.Type {
		case "IP_BASED_TRANSIT":
			if t.IP == nil || t.IP.Protocol != "BGP" {
				return nil, errInvalidResponse
			}
			asn, err := transitASN(t.IP.ASN)
			if err != nil {
				return nil, errInvalidResponse
			}
			t.IP.ASN = asn
		case "SDA_LISP_PUB_SUB_TRANSIT", "SDA_LISP_BGP_TRANSIT":
		default:
			return nil, errInvalidResponse
		}
		out[t.Name] = t
	}
	return out, nil
}
func transitMatches(want, got fabricTransit) bool {
	return want.Type == got.Type && want.IP != nil && got.IP != nil && *want.IP == *got.IP
}

type fabricTransitExecutor struct {
	client  *client
	desired []fabricTransit
}

func (e *fabricTransitExecutor) count() int { return len(e.desired) }
func (e *fabricTransitExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.fabricTransits(ctx)
	if err != nil {
		return nil, err
	}
	var missing *fabricTransit
	for _, want := range e.desired {
		if got, ok := actual[want.Name]; ok {
			if !transitMatches(want, got) {
				return nil, errors.New("transit type, routing protocol or ASN change requires explicit replacement")
			}
		} else if missing == nil {
			copy := want
			missing = &copy
		}
	}
	if missing == nil {
		return nil, nil
	}
	body, _ := json.Marshal([]fabricTransit{*missing})
	return &configurationWrite{resource: "transit:" + missing.Name, action: "Create", submit: func(ctx context.Context) (Task, error) {
		raw, err := e.client.mutate(ctx, http.MethodPost, fabricTransitsPath, body)
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
func (e *fabricTransitExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Create" || op.TargetID != "" || op.ParentID != "" || op.TargetSerial != "" {
		return false, errConfigurationTransition
	}
	actual, err := e.client.fabricTransits(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if op.Resource == "transit:"+want.Name {
			got, ok := actual[want.Name]
			return ok && transitMatches(want, got), nil
		}
	}
	return false, errConfigurationTransition
}
func (e *fabricTransitExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.fabricTransits(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, ok := actual[want.Name]
		if ok && transitMatches(want, got) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if ok {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: "transit:" + want.Name, Path: "/transits", Change: change})
	}
	return out, nil
}
