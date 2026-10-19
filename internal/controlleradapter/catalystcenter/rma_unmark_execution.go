// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

type rmaUnmarkBinding struct {
	scope, recordID string
	createdAt       int64
}

func parseRMAUnmarkResource(resource string) (rmaUnmarkBinding, error) {
	var zero rmaUnmarkBinding
	parts := strings.Split(resource, ":")
	if len(parts) != 4 || parts[0] != "rma-unmark" || !validAPIID(parts[2]) {
		return zero, errConfigurationTransition
	}
	digest, err := hex.DecodeString(parts[1])
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != parts[1] {
		return zero, errConfigurationTransition
	}
	created, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || created <= 0 || strconv.FormatInt(created, 10) != parts[3] {
		return zero, errConfigurationTransition
	}
	return rmaUnmarkBinding{scope: "rma-mark:" + parts[1], recordID: parts[2], createdAt: created}, nil
}

// Private native primitive. The adapter must prove ownership of recordID,
// explicit pruning/deletion authorization and physical admission before dispatch.
// Missing/read-failed records never trigger a speculative cleanup request.
func (c *client) planDeviceReplacementUnmark(ctx context.Context, ref configurationDeviceReference, want deviceReplacementIntent, recordID string) (*configurationWrite, error) {
	if ref.Serial == "" || !validAPIID(recordID) {
		return nil, errConfigurationDeviceAdmission
	}
	before, err := c.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return nil, err
	}
	record := before.record
	if record == nil || record.ID != recordID || record.CreatedAt <= 0 {
		return nil, errConfigurationStale
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
	if err != nil || serial != before.serial {
		return nil, errConfigurationStale
	}
	if record.Status != "MARKED-FOR-REPLACEMENT" && record.Status != "READY-FOR-REPLACEMENT" || record.WorkflowID != "" || record.WorkflowFailedStep != "" || record.ReplacedAt != 0 || record.ReplacementSerial != "" {
		return nil, errConfigurationTransition
	}
	scope, err := deviceReplacementMarkResource(before)
	if err != nil {
		return nil, err
	}
	resource := fmt.Sprintf("rma-unmark:%s:%s:%d", strings.TrimPrefix(scope, "rma-mark:"), recordID, record.CreatedAt)
	body, err := json.Marshal([]struct {
		ID     string `json:"id"`
		Status string `json:"replacementStatus"`
	}{{recordID, "NON-FAULTY"}})
	if err != nil {
		return nil, err
	}
	return &configurationWrite{resource: resource, action: "Delete", identity: configurationResourceIdentity{target: before.target.ID, parent: before.siteID, serial: before.serial}, submit: func(ctx context.Context) (Task, error) {
		if err := c.revalidateDeviceReplacement(ctx, ref, want, before); err != nil {
			return Task{}, err
		}
		raw, err := c.mutate(ctx, http.MethodPut, deviceReplacementPath, body)
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

// A completed unmark task plus an unchanged physical scope is required. Native
// absence is accepted only here after the known task, never as dispatch authority
// or as sufficient evidence to retire the ownership receipt/reservation.
func (c *client) observeDeviceReplacementUnmark(ctx context.Context, ref configurationDeviceReference, want deviceReplacementIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if op == nil || (op.Phase != "Accepted" && op.Phase != "Verifying") || op.Section != "inventory" || op.Action != "Delete" || ref.Serial == "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.Before(op.SubmittedAt.Time) || !validAPIID(op.TaskID) || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	binding, err := parseRMAUnmarkResource(op.Resource)
	if err != nil {
		return false, err
	}
	snapshot, err := c.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return false, err
	}
	scope, err := deviceReplacementMarkResource(snapshot)
	if err != nil || scope != binding.scope || snapshot.target.ID != op.TargetID || snapshot.serial != op.TargetSerial || snapshot.siteID != op.ParentID {
		return false, errConfigurationStale
	}
	done, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	if record := snapshot.record; record != nil {
		serial, err := topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
		if err != nil || record.ID != binding.recordID || record.CreatedAt != binding.createdAt || serial != snapshot.serial {
			return false, errConfigurationStale
		}
		if record.WorkflowID != "" || record.WorkflowFailedStep != "" || record.ReplacedAt != 0 || record.ReplacementSerial != "" {
			return false, errConfigurationOwned
		}
		switch record.Status {
		case "NON-FAULTY":
		case "MARKED-FOR-REPLACEMENT", "READY-FOR-REPLACEMENT":
			return false, nil
		default:
			return false, errConfigurationTransition
		}
	}
	if err := c.revalidateDeviceReplacement(ctx, ref, want, snapshot); err != nil {
		return false, err
	}
	return true, nil
}
