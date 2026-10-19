// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

// Only this target's settings may change as the result of the operation. Keep
// peer settings, physical/fabric identities, provisioning and AP locations bound.
func fabricEWLCResultDigest(deps *fabricEWLCFabricDependencies, payload fabricEWLCSetting) string {
	peers := []fabricEWLCSetting{}
	for _, row := range deps.scope.settings {
		if row.ID != payload.ID {
			peers = append(peers, row)
		}
	}
	raw, _ := json.Marshal([]any{deps.foundation.digest, deps.scope.fabricID, deps.scope.members, deps.foundation.physical, peers, deps.locations.digest, payload})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Preserve both the prepared baseline and expected-result binding. Recovery
// compares the result; dispatch revalidation must also match the baseline.
func fabricEWLCResource(deps *fabricEWLCFabricDependencies, payload fabricEWLCSetting) string {
	return "fabric-ewlc-update:" + deps.digest + ":" + fabricEWLCResultDigest(deps, payload)
}
func parseFabricEWLCResource(resource string) (string, string, error) {
	suffix, ok := strings.CutPrefix(resource, "fabric-ewlc-update:")
	parts := strings.Split(suffix, ":")
	if !ok || len(parts) != 2 {
		return "", "", errInvalidResponse
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return "", "", errInvalidResponse
		}
		if _, err := hex.DecodeString(part); err != nil {
			return "", "", errInvalidResponse
		}
	}
	return parts[0], parts[1], nil
}

// Native primitive: the group coordinator supplies the journal-bound permit
// and task recovery. A successful PUT is not an application receipt and never
// releases physical reservations. Public disable remains gated on reload verification.
func (c *client) planGuardedFabricEWLC(ctx context.Context, want fabricEWLCIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	deps, err := c.fabricEWLCFabricDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	payload, err := fabricEWLCPayload(deps.target.ID, want.settings)
	if err != nil {
		return nil, err
	}
	if deps.observed != nil && *deps.observed == payload {
		// Native false does not prove that the required cleanup reload completed.
		if !payload.Enabled {
			return nil, errConfigurationBusy
		}
		return nil, nil
	}
	endpoint, err := fabricEWLCPath(deps.scope.fabricID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resource := fabricEWLCResource(deps, payload)
	var used atomic.Bool
	return &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: deps.target.ID, parent: deps.scope.fabricID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.fabricEWLCFabricDependencies(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest || fabricEWLCResource(fresh, payload) != resource {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPut, endpoint, nil, body, http.StatusOK)
		if err != nil {
			return Task{}, err
		}
		var result struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &result) != nil || !validAPIID(result.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: result.Response.ID}, nil
	}}, nil
}
