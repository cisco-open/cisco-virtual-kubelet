// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

type pnpRegistrationReceipt struct {
	resource, deviceID, serial, pid, hostname string
	stack                                     bool
}
type pnpRegistrationWrite struct {
	resource   string
	submit     func(context.Context) (*pnpRegistrationReceipt, error)
	noDispatch func(error) *configurationPreSubmitRefusal
}

// Native database-registration primitive. Dispatch requires durable preparation
// and an atomic journal transition retaining the returned ID. Database ownership
// grants no physical claim/reset/provisioning admission.
// Do not adapt this to submitCompleted (which cannot retain a created identity).
func (c *client) planPnPRegistration(ctx context.Context, want pnpDeviceIntent) (*pnpRegistrationWrite, error) {
	body, err := pnpRegistrationPayload(want)
	if err != nil {
		return nil, err
	}
	serials, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	if err := c.requirePnPRegistrationAbsent(ctx, serials); err != nil {
		return nil, err
	}
	resource := fmt.Sprintf("pnp-register:%x", sha256.Sum256(body))
	expected := pnpRegistrationReceipt{resource: resource, serial: serials[0], pid: want.PID, hostname: want.Name, stack: len(serials) > 1}
	var receipt *pnpRegistrationReceipt
	write := &configurationWrite{resource: resource, action: "Create", submitCreated: func(ctx context.Context) (string, error) {
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, pnpDevicesPath, nil, body, http.StatusOK)
		if err != nil {
			return "", err
		}
		var created pnpDevice
		if json.Unmarshal(raw, &created) != nil || !validAPIID(created.ID) {
			return "", errInvalidResponse
		}
		accepted := expected
		accepted.deviceID = created.ID
		if created.Info != nil && !pnpRegistrationMatches(accepted, created) {
			return "", errConfigurationStale
		}
		receipt = &accepted
		return accepted.deviceID, nil
	}}
	// The existing guard owns both one-use dispatch and the process-local proof.
	// Once the POST closure is entered, even a matching API error cannot mint it.
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		return c.requirePnPRegistrationAbsent(ctx, serials)
	})
	return &pnpRegistrationWrite{resource: resource, noDispatch: write.noDispatch, submit: func(ctx context.Context) (*pnpRegistrationReceipt, error) {
		id, err := write.submitCreated(ctx)
		if err != nil {
			return nil, err
		}
		if receipt == nil || receipt.deviceID != id {
			return nil, errInvalidResponse
		}
		return receipt, nil
	}}, nil
}

func (c *client) requirePnPRegistrationAbsent(ctx context.Context, serials []string) error {
	wanted := map[string]bool{}
	for _, serial := range serials {
		wanted[serial] = true
	}
	pnp, err := c.pnpDevices(ctx)
	if err != nil {
		return err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return err
	}
	return requirePnPRegistrationAbsentFromInventory(wanted, pnp, inventory)
}

// Shared by single-record and whole-manifest registration preflight. Readers
// provide validated complete inventories; no network call is hidden in a loop.
func requirePnPRegistrationAbsentFromInventory(wanted map[string]bool, pnp []pnpDevice, inventory []Device) error {
	for _, device := range pnp {
		if wanted[device.Info.Serial] {
			return errConfigurationOwned
		}
		for _, member := range device.Info.StackInfo.Members {
			if wanted[member.Serial] {
				return errConfigurationOwned
			}
		}
	}
	for _, device := range inventory {
		// Native stack inventories can carry a comma-separated serial list.
		for _, part := range strings.Split(device.Serial, ",") {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(part)
			if err == nil && wanted[serial] {
				return errConfigurationOwned
			}
		}
	}
	return nil
}

func pnpRegistrationMatches(receipt pnpRegistrationReceipt, device pnpDevice) bool {
	if device.ID != receipt.deviceID || device.Info == nil {
		return false
	}
	info := device.Info
	serial, err := topologyidentity.CanonicalPhysicalIdentity(info.Serial)
	if err != nil || serial != receipt.serial || info.PID != receipt.pid || info.Hostname != receipt.hostname || receipt.stack && (info.Stack == nil || !*info.Stack) || !receipt.stack && info.Stack != nil && *info.Stack {
		return false
	}
	return true
}

// Independent readback requires the acknowledged native UUID. Matching a serial
// alone after a lost POST response cannot supply an ownership receipt.
func (c *client) observePnPRegistration(ctx context.Context, receipt *pnpRegistrationReceipt) (bool, error) {
	if receipt == nil || !validAPIID(receipt.deviceID) || !strings.HasPrefix(receipt.resource, "pnp-register:") || receipt.serial == "" {
		return false, errConfigurationTransition
	}
	rows, err := c.pnpDevices(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.ID == receipt.deviceID {
			if !pnpRegistrationMatches(*receipt, row) {
				return false, errConfigurationStale
			}
			return true, nil
		}
		if row.Info.Serial == receipt.serial {
			return false, errConfigurationOwned
		}
	}
	return false, nil
}
