// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
)

const lanSessionStartPath = "/dna/intent/api/v2/lan-automation"
const lanSessionStartPrefix = "lan-session-start:v1:"

type lanSessionStartWrite struct {
	resource    string
	observation string
	submit      func(context.Context, func(context.Context) error) (Task, error)
}

// Private primitive for the shared physical group/journal caller. Its admission
// callback must qualify authentication, discovery containment, seed eligibility,
// allocator semantics, all-member reservations and the durable Submitting state.
// This is not connected to public Apply until those contracts are implemented.
func (c *client) planLANSessionStart(ctx context.Context, want lanSessionIntent) (*lanSessionStartWrite, error) {
	if want.Password != "" && want.PasswordVersion == nil {
		return nil, errConfigurationSecret
	}
	body, err := lanSessionStartPayload(want)
	if err != nil {
		return nil, err
	}
	before, err := c.lanSessionPreflight(ctx, want)
	if err != nil {
		return nil, err
	}
	digest := before.observationDigest()
	if digest == "" {
		return nil, errConfigurationTransition
	}
	resource, err := lanSessionStartResource(want, before)
	if err != nil {
		return nil, err
	}
	// Copy intent through JSON so caller mutations cannot change revalidation.
	var retained lanSessionIntent
	full, err := json.Marshal(want)
	if err != nil || json.Unmarshal(full, &retained) != nil {
		return nil, errConfigurationTransition
	}
	var used atomic.Bool
	return &lanSessionStartWrite{resource: resource, observation: digest, submit: func(ctx context.Context, authorize func(context.Context) error) (Task, error) {
		if used.Swap(true) || authorize == nil {
			return Task{}, errConfigurationTransition
		}
		check := func() error {
			fresh, err := c.lanSessionPreflight(ctx, retained)
			if err != nil {
				return err
			}
			if fresh.observationDigest() != digest {
				return errConfigurationStale
			}
			return nil
		}
		if err := check(); err != nil {
			return Task{}, err
		}
		if err := authorize(ctx); err != nil {
			return Task{}, err
		}
		if err := check(); err != nil {
			return Task{}, err
		}
		response, err := c.mutateQueryStatus(ctx, http.MethodPost, lanSessionStartPath, nil, body, http.StatusOK)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(response, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: receipt.Response.ID}, nil
	}}, nil
}
