// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const pnpSiteClaimPath = pnpDevicesPath + "/site-claim"

// This records only that the documented HTTP response was observed. It is not
// a task ID, workflow ID, durable ownership receipt or provisioning completion.
type pnpClaimResponse struct{ deviceID, siteID string }
type pnpClaimWrite struct {
	resource         string
	deviceID, siteID string
	submit           func(context.Context) (*pnpClaimResponse, error)
	preflight        func(context.Context) error
	submitValidated  func(context.Context) (*pnpClaimResponse, error)
}

// Private native primitive, intentionally not convertible to configurationWrite.
// Public execution still needs physical admission, readiness/version gates and
// durable recovery for an acknowledgement that supplies no native task UUID.
func (c *client) planPnPSiteClaim(ctx context.Context, want pnpDeviceIntent, deviceID string, index *nacmodel.TemplateIndex) (*pnpClaimWrite, error) {
	// Freeze template variables and stack options so caller mutations cannot
	// change either the request or its preflight after planning.
	raw, err := json.Marshal(want)
	if err != nil {
		return nil, err
	}
	var frozen pnpDeviceIntent
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, errInvalidResponse
	}
	switch frozen.Type {
	case "", "Default", "StackSwitch":
	case "AccessPoint":
		if frozen.RFProfile == "" {
			return nil, errConfigurationTransition
		}
	default:
		// The pinned model has no Sensor profile or WLC static-address inputs.
		// Do not send an incomplete native request for those platform types.
		return nil, errConfigurationTransition
	}
	target, err := c.pnpClaimTarget(ctx, frozen, deviceID)
	if err != nil {
		return nil, err
	}
	dependencies, err := c.pnpClaimDependencies(ctx, frozen, deviceID, index)
	if err != nil {
		return nil, err
	}
	body, err := pnpClaimPayload(frozen, dependencies.refs)
	if err != nil {
		return nil, err
	}
	resource, err := pnpClaimPreparedResource(frozen, dependencies, target)
	if err != nil {
		return nil, err
	}
	var used, validated atomic.Bool
	write := &pnpClaimWrite{resource: resource, deviceID: deviceID, siteID: dependencies.refs.siteID}
	write.preflight = func(ctx context.Context) error {
		if used.Swap(true) {
			return errConfigurationTransition
		}
		if err := c.revalidatePnPClaimDependencies(ctx, frozen, deviceID, index, dependencies); err != nil {
			return err
		}
		fresh, err := c.pnpClaimTarget(ctx, frozen, deviceID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(target, fresh) {
			return errConfigurationStale
		}
		validated.Store(true)
		return nil
	}
	write.submitValidated = func(ctx context.Context) (*pnpClaimResponse, error) {
		if !validated.CompareAndSwap(true, false) {
			return nil, errConfigurationTransition
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, pnpSiteClaimPath, nil, body, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Response *string `json:"response"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil || strings.TrimSpace(*envelope.Response) == "" {
			return nil, errInvalidResponse
		}
		// Never persist or expose free-form response text, which can contain
		// configuration values. In particular, do not parse it as a task ID.
		return &pnpClaimResponse{deviceID: deviceID, siteID: dependencies.refs.siteID}, nil
	}
	write.submit = func(ctx context.Context) (*pnpClaimResponse, error) {
		if err := write.preflight(ctx); err != nil {
			return nil, err
		}
		return write.submitValidated(ctx)
	}
	return write, nil
}
