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

const deviceRolePath = "/dna/intent/api/v1/network-device/brief"

func validDeviceRole(role string) bool {
	switch role {
	case "ACCESS", "CORE", "DISTRIBUTION", "BORDER ROUTER":
		return true
	}
	return false
}
func validDeviceRoleSource(source string) bool { return source == "MANUAL" || source == "AUTO" }

// Native resource semantics. Manifest device mutation admission remains shared
// with site assignment and provisioning; parsing a role grants no write authority.
type deviceRoleExecutor struct {
	client  *client
	desired []inventoryDeviceIntent
}

func (e *deviceRoleExecutor) count() int {
	n := 0
	for _, d := range e.desired {
		if d.role != "" {
			n++
		}
	}
	return n
}

type observedDeviceRole struct {
	desired inventoryDeviceIntent
	device  Device
}

func (e *deviceRoleExecutor) observe(ctx context.Context) ([]observedDeviceRole, error) {
	devices, err := e.client.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := []observedDeviceRole{}
	seen := map[string]bool{}
	for _, d := range e.desired {
		if d.role == "" && d.roleSource == "" {
			continue
		}
		if !validDeviceRole(d.role) || !validDeviceRoleSource(d.roleSource) || d.reference.Serial == "" {
			return nil, errors.New("role update requires explicit role/source and physical identity")
		}
		device, err := resolveConfigurationDevice(d.reference, devices)
		if err != nil {
			return nil, err
		}
		if seen[device.ID] || device.Serial == "" || !validDeviceRole(device.Role) || !validDeviceRoleSource(device.RoleSource) {
			return nil, errInvalidResponse
		}
		seen[device.ID] = true
		out = append(out, observedDeviceRole{desired: d, device: device})
	}
	return out, nil
}
func (e *deviceRoleExecutor) next(ctx context.Context) (*configurationWrite, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range observed {
		if d.device.Role == d.desired.role && d.device.RoleSource == d.desired.roleSource {
			continue
		}
		body, _ := json.Marshal(map[string]string{"id": d.device.ID, "role": d.desired.role, "roleSource": d.desired.roleSource})
		return &configurationWrite{resource: d.desired.reference.Name, action: "Update", identity: configurationResourceIdentity{target: d.device.ID, serial: d.device.Serial}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, http.MethodPut, deviceRolePath, body)
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
func (e *deviceRoleExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Update" || !validAPIID(op.TargetID) || op.TargetSerial == "" || op.ParentID != "" {
		return false, errConfigurationTransition
	}
	observed, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range observed {
		if d.desired.reference.Name != op.Resource {
			continue
		}
		if d.device.ID != op.TargetID || d.device.Serial != op.TargetSerial {
			return false, errors.New("role target identity changed")
		}
		return d.device.Role == d.desired.role && d.device.RoleSource == d.desired.roleSource, nil
	}
	return false, errConfigurationTransition
}
