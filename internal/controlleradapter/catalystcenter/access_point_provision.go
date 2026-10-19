// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "context"

const accessPointProvisionPath = "/dna/intent/api/v1/wirelessAccessPoints/provision"

// One admitted physical target per request permits reuse of the existing device
// reservation. The modern native API has no reprovision field. Reprovision is
// a requested action in the journal, not an invented request-body attribute.
// The caller must establish assignment, RF dependencies and mutation admission.
func (c *client) provisionAccessPoint(ctx context.Context, target accessPointConfigurationTarget, siteID, rfProfile, state string) (Task, error) {
	if state != "PROVISION" && state != "REPROVISION" {
		return Task{}, errAccessPointModel
	}
	if !validAPIID(siteID) {
		return Task{}, errAccessPointModel
	}
	if _, err := plannedAPText(rfProfile); err != nil {
		return Task{}, errAccessPointModel
	}
	if _, err := target.member("", false); err != nil {
		return Task{}, err
	}
	body := map[string]any{
		"networkDevices": []any{map[string]any{"deviceId": target.device.ID}},
		"siteId":         siteID, "rfProfileName": rfProfile,
	}
	return c.submitTask(ctx, accessPointProvisionPath, body)
}
