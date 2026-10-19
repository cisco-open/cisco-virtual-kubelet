// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Native mark step only. The adapter must supply physical admission, a durable
// operation and owned completion/recovery before exposing this through Apply.
// An existing record cannot be adopted merely because it is already marked.
func (c *client) planDeviceReplacementMark(ctx context.Context, ref configurationDeviceReference, want deviceReplacementIntent) (*configurationWrite, error) {
	if ref.Serial == "" {
		return nil, errConfigurationDeviceAdmission
	}
	before, err := c.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return nil, err
	}
	if before.record != nil {
		return nil, errConfigurationOwned
	}
	body, err := deviceReplacementMarkPayload(before.target.ID)
	if err != nil {
		return nil, err
	}
	resource, err := deviceReplacementMarkResource(before)
	if err != nil {
		return nil, err
	}

	return &configurationWrite{
		action: "Create", resource: resource,
		identity: configurationResourceIdentity{target: before.target.ID, serial: before.serial, parent: before.siteID},
		submit: func(ctx context.Context) (Task, error) {
			if err := c.revalidateDeviceReplacement(ctx, ref, want, before); err != nil {
				return Task{}, err
			}
			return c.submitTask(ctx, deviceReplacementPath, json.RawMessage(body))
		},
	}, nil
}

// Exclude the newly created replacement record so completion can bind the same
// physical and provisioning identity present before the mark POST. The intended
// spare is not part of the mark effect; source binding and fresh pre-submit
// validation separately protect changes to that intent.
func deviceReplacementMarkResource(before *deviceReplacementSnapshot) (string, error) {
	if before == nil {
		return "", errConfigurationTransition
	}
	identity, err := json.Marshal(struct {
		DeviceID, Serial, SiteID, ProvisionID string
	}{before.target.ID, before.serial, before.siteID, before.provisionID})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("rma-mark:%x", sha256.Sum256(identity)), nil
}
