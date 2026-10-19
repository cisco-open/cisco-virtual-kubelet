// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Native execution only: manifest compilation must first supply device mutation
// admission and ordering against device/site/profile prerequisites.
type wirelessManagedAPExecutor struct {
	client    *client
	reference configurationDeviceReference
	desired   wirelessManagedAPIntent
	anchor    bool
}
type wirelessManagedAPObservation struct {
	device          Device
	resource        string
	desired, actual map[string][]string
}

func (e *wirelessManagedAPExecutor) count() int { return 1 }
func (e *wirelessManagedAPExecutor) observe(ctx context.Context) (wirelessManagedAPObservation, error) {
	var out wirelessManagedAPObservation
	if e.reference.Serial == "" || e.reference.Name == "" {
		return out, errConfigurationTransition
	}
	devices, err := e.client.configurationDevices(ctx)
	if err != nil {
		return out, err
	}
	device, err := resolveConfigurationDevice(e.reference, devices)
	if err != nil {
		return out, err
	}
	if device.Family != "Wireless Controller" || device.Reachability != "Reachable" {
		return out, errConfigurationTransition
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return out, err
	}
	index := map[string]string{}
	for _, site := range sites {
		if !validAPIID(site.ID) || !validSitePath(site.NameHierarchy) {
			return out, errInvalidResponse
		}
		if _, exists := index[site.NameHierarchy]; exists {
			return out, errInvalidResponse
		}
		index[site.NameHierarchy] = site.ID
	}
	snapshot := wirelessManagedAPSnapshot{}
	kinds := []string{"primary", "secondary"}
	if e.anchor {
		kinds = []string{"anchor"}
	}
	actual := map[string][]string{}
	bindings := map[string]string{}
	for _, kind := range kinds {
		rows, err := e.client.wirelessManagedAPLocations(ctx, device.ID, kind)
		if err != nil {
			return out, err
		}
		switch kind {
		case "primary":
			snapshot.primary = rows
		case "secondary":
			snapshot.secondary = rows
		case "anchor":
			snapshot.anchor = rows
		}
		ids, err := wirelessManagedAPSet(nil, rows, index)
		if err != nil {
			return out, err
		}
		actual[kind+"ManagedAPLocationsSiteIds"] = ids
	}
	desired, err := e.desired.payload(snapshot, index, e.anchor)
	if err != nil {
		return out, err
	}
	// Bind the paths for every submitted site, including retained memberships.
	used := map[string]bool{}
	for _, ids := range desired {
		for _, id := range ids {
			used[id] = true
		}
	}
	for path, id := range index {
		if used[id] {
			bindings[path] = id
		}
	}
	digest, err := wirelessTagDigest(map[string]any{"name": e.reference.Name, "device": device.ID, "serial": device.Serial, "ip": device.ManagementIP, "anchor": e.anchor, "sites": bindings, "payload": desired})
	if err != nil {
		return out, err
	}
	return wirelessManagedAPObservation{device: device, resource: "managed-ap:" + digest, desired: desired, actual: actual}, nil
}
func (e *wirelessManagedAPExecutor) next(ctx context.Context) (*configurationWrite, error) {
	row, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	if reflect.DeepEqual(row.desired, row.actual) {
		return nil, nil
	}
	body, err := json.Marshal(row.desired)
	if err != nil {
		return nil, err
	}
	endpoint := "assignManagedApLocations"
	if e.anchor {
		endpoint = "assignAnchorManagedApLocations"
	}
	return &configurationWrite{resource: row.resource, action: "Update", identity: configurationResourceIdentity{target: row.device.ID, serial: row.device.Serial}, submit: func(ctx context.Context) (Task, error) {
		fresh, err := e.observe(ctx)
		if err != nil {
			return Task{}, err
		}
		if fresh.resource != row.resource || !reflect.DeepEqual(fresh.actual, row.actual) || !reflect.DeepEqual(fresh.desired, row.desired) {
			return Task{}, errConfigurationStale
		}
		raw, err := e.client.mutate(ctx, http.MethodPost, "/dna/intent/api/v1/wirelessControllers/"+url.PathEscape(row.device.ID)+"/"+endpoint, body)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return receipt.Response, nil
	}}, nil
}
func (e *wirelessManagedAPExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Update" || !validAPIID(op.TargetID) || op.TargetSerial == "" || op.ParentID != "" || len(op.CredentialVersions) > 0 {
		return false, errConfigurationTransition
	}
	row, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	if row.resource != op.Resource || row.device.ID != op.TargetID || row.device.Serial != op.TargetSerial {
		return false, errConfigurationStale
	}
	return reflect.DeepEqual(row.desired, row.actual), nil
}
