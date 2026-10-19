// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Observation is evidence for later identity/ownership checks, not proof of a
// CVK-owned replacement. Do not infer success from presence, or from an unknown
// native status. Preserve native serial spelling for subsequent API requests.
type deviceReplacementRecord struct {
	ID                     string `json:"id"`
	FaultyID               string `json:"faultyDeviceId"`
	FaultySerial           string `json:"faultyDeviceSerialNumber"`
	ReplacementSerial      string `json:"replacementDeviceSerialNumber"`
	Status                 string `json:"replacementStatus"`
	WorkflowID             string `json:"workflowId"`
	WorkflowFailedStep     string `json:"workflowFailedStep"`
	NetworkReadinessTaskID string `json:"networkReadinessTaskId"`
	ReadinessTaskID        string `json:"readinesscheckTaskId"`
	CreatedAt              int64  `json:"creationTime"`
	ReplacedAt             int64  `json:"replacementTime"`
}

func (c *client) deviceReplacements(ctx context.Context) ([]deviceReplacementRecord, error) {
	get := func(ctx context.Context, path string, query url.Values) ([]byte, error) {
		// A 204/206 is not a complete replacement inventory.
		return c.get(ctx, path, query, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, deviceReplacementPath, nil, func(r deviceReplacementRecord) string { return r.ID })
	if err != nil {
		return nil, err
	}
	faultyIDs, faultySerials := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if !validAPIID(row.ID) || !validAPIID(row.FaultyID) || faultyIDs[row.FaultyID] || row.Status == "" || len(row.Status) > 128 || strings.TrimSpace(row.Status) != row.Status || strings.ContainsAny(row.Status, "\x00\r\n") || row.CreatedAt < 0 || row.ReplacedAt < 0 || len(row.WorkflowFailedStep) > 256 || strings.TrimSpace(row.WorkflowFailedStep) != row.WorkflowFailedStep || strings.ContainsAny(row.WorkflowFailedStep, "\x00\r\n") {
			return nil, errInvalidResponse
		}
		faultyIDs[row.FaultyID] = true
		// Serial is optional in the documented inventory response. Missing serials
		// must be resolved through authenticated device inventory before mutation.
		faulty := ""
		if row.FaultySerial != "" {
			faulty, err = topologyidentity.CanonicalPhysicalIdentity(row.FaultySerial)
			if err != nil || faultySerials[faulty] {
				return nil, errInvalidResponse
			}
			faultySerials[faulty] = true
		}
		if row.ReplacementSerial != "" {
			replacement, err := topologyidentity.CanonicalPhysicalIdentity(row.ReplacementSerial)
			if err != nil || replacement == faulty {
				return nil, errInvalidResponse
			}
		}
		for _, id := range []string{row.WorkflowID, row.NetworkReadinessTaskID, row.ReadinessTaskID} {
			if id != "" && !validAPIID(id) {
				return nil, errInvalidResponse
			}
		}
	}
	return rows, nil
}
