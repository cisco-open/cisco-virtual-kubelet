// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const assignDevicesPath = "/dna/intent/api/v1/networkDevices/assignToSite/apply"

// The managed inventory compiler exposes this executor only behind the physical
// configuration admission integration. Each journal operation names one device.
type deviceAssignmentExecutor struct {
	client  *client
	desired []inventoryDeviceIntent
}
type observedDeviceAssignment struct {
	resource string
	device   Device
	siteID   string
	assigned bool
}

func (e *deviceAssignmentExecutor) count() int { return len(e.desired) }
func (e *deviceAssignmentExecutor) observe(ctx context.Context) ([]observedDeviceAssignment, error) {
	devices, err := e.client.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, s := range sites {
		byPath[s.NameHierarchy] = s
	}
	memberships := map[string]map[string]bool{}
	seen := map[string]bool{}
	out := []observedDeviceAssignment{}
	for _, desired := range e.desired {
		d, err := resolveConfigurationDevice(desired.reference, devices)
		if err != nil {
			return nil, err
		}
		if desired.reference.Serial == "" || d.Serial == "" || seen[d.ID] {
			return nil, errors.New("assignment requires unique physically identified devices")
		}
		seen[d.ID] = true
		s, ok := byPath[desired.site]
		if !ok || !validAPIID(s.ID) || s.Type == "global" {
			return nil, errors.New("assignment target site is unavailable")
		}
		if _, read := memberships[s.ID]; !read {
			memberships[s.ID], err = e.client.assignedDevices(ctx, s.ID)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, observedDeviceAssignment{resource: desired.reference.Name, device: d, siteID: s.ID, assigned: memberships[s.ID][d.ID]})
	}
	return out, nil
}
func (e *deviceAssignmentExecutor) next(ctx context.Context) (*configurationWrite, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range observed {
		if a.assigned {
			continue
		}
		body, _ := json.Marshal(map[string]any{"deviceIds": []string{a.device.ID}, "siteId": a.siteID})
		return &configurationWrite{resource: a.resource, action: "Update", identity: configurationResourceIdentity{target: a.device.ID, parent: a.siteID, serial: a.device.Serial}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, http.MethodPost, assignDevicesPath, body)
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
func (e *deviceAssignmentExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) {
		return false, errConfigurationTransition
	}
	observed, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, a := range observed {
		if a.resource != op.Resource {
			continue
		}
		if a.device.ID != op.TargetID || a.siteID != op.ParentID || op.TargetSerial == "" || a.device.Serial != op.TargetSerial {
			return false, errors.New("assignment device or site identity changed")
		}
		return a.assigned, nil
	}
	return false, errConfigurationTransition
}
