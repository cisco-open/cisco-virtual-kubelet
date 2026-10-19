// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// planWirelessDeviceProvision supplies the native submission step only. The
// caller must hold device mutation admission and persist a planned operation
// before invoking submit. It is not yet enabled by manifest compilation.
// NetAsCode 0.5.0 supplies no optional provisioning body fields; both states
// invoke the same endpoint. A task receipt is acceptance, not convergence.
func (c *client) planWirelessDeviceProvision(ctx context.Context, ref configurationDeviceReference, state, siteID string, modelConfigRequired bool) (*configurationWrite, error) {
	if !validAPIID(siteID) || ref.Serial == "" || ref.Name == "" || len(ref.Name) > 256 || strings.TrimSpace(ref.Name) != ref.Name || strings.ContainsAny(ref.Name, "\x00\r\n") || state != "PROVISION" && state != "REPROVISION" {
		return nil, errWirelessProfileModel
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	device, err := resolveConfigurationDevice(ref, inventory)
	if err != nil {
		return nil, err
	}
	if device.Family != "Wireless Controller" || device.Reachability != "Reachable" {
		return nil, errConfigurationTransition
	}
	action := "Create"
	if state == "REPROVISION" {
		action = "Update"
	}
	baseline, err := c.wirelessDeviceProvisionStatus(ctx, device.ID)
	if err != nil {
		return nil, err
	}
	if baseline.siteID != siteID || baseline.name != device.Hostname {
		return nil, errConfigurationStale
	}
	for _, status := range []string{baseline.networkIntent, baseline.modelConfig} {
		if status == "CONFIGURING" || status == "PENDING" {
			return nil, errConfigurationBusy
		}
	}
	if baseline.lastProvisioned == "" && (baseline.networkIntent != "NOT_STARTED" || baseline.modelConfig != "NOT_STARTED") {
		return nil, errInvalidResponse
	}
	record, err := wirelessProvisionRecordFor(ref.Name, action, wirelessProvisionBaseline{device: device, siteID: siteID, lastProvisioned: baseline.lastProvisioned, modelConfigRequired: modelConfigRequired})
	if err != nil {
		return nil, err
	}
	resource, err := record.resource()
	if err != nil {
		return nil, err
	}
	return &configurationWrite{
		resource: resource, action: action,
		identity: configurationResourceIdentity{target: device.ID, serial: device.Serial},
		submit: func(ctx context.Context) (Task, error) {
			inventory, err := c.configurationDevices(ctx)
			if err != nil {
				return Task{}, err
			}
			fresh, err := resolveConfigurationDevice(ref, inventory)
			if err != nil {
				return Task{}, err
			}
			if fresh.Hostname != device.Hostname || fresh.ID != device.ID || fresh.Serial != device.Serial || fresh.Family != "Wireless Controller" || fresh.Reachability != "Reachable" || !sameManagementIP(fresh.ManagementIP, device.ManagementIP) {
				return Task{}, errConfigurationStale
			}
			current, err := c.wirelessDeviceProvisionStatus(ctx, device.ID)
			if err != nil {
				return Task{}, err
			}
			if current != baseline {
				return Task{}, errConfigurationStale
			}
			raw, err := c.mutate(ctx, http.MethodPost, "/dna/intent/api/v1/wirelessControllers/"+url.PathEscape(device.ID)+"/provision", []byte(`{}`))
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
		},
	}, nil
}
