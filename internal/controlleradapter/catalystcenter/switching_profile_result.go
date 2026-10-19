// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/google/uuid"
)

// This service returns the UUID in a closed success grammar, not a bare UUID or
// generic JSON data field. Never search arbitrary progress for a UUID, and never
// retain its free-form text. Task success alone does not establish native state.
func decodeSwitchingProfileCreateResult(raw []byte, task string, submitted, now time.Time) (string, bool, error) {
	done, err := decodeConfigurationTaskComplete(raw, task, submitted, now)
	if err != nil || !done {
		return "", false, err
	}
	var body struct {
		Response struct {
			Service  string `json:"serviceType"`
			Progress string `json:"progress"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response.Service != "Site Profile Service" {
		return "", false, errInvalidResponse
	}
	const prefix, suffix = "Network Profile [", "] Successfully Created"
	if !strings.HasPrefix(body.Response.Progress, prefix) || !strings.HasSuffix(body.Response.Progress, suffix) {
		return "", false, errInvalidResponse
	}
	id := strings.TrimSuffix(strings.TrimPrefix(body.Response.Progress, prefix), suffix)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return "", false, errInvalidResponse
	}
	return id, true, nil
}

func (c *client) switchingProfileCreateResult(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (string, bool, error) {
	if op == nil || op.Action != "Create" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return "", false, errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+op.TaskID, nil, http.StatusOK)
	if err != nil {
		return "", false, err
	}
	return decodeSwitchingProfileCreateResult(raw, op.TaskID, op.SubmittedAt.Time, now)
}

// Read-only creation correlation, not receipt publication or adoption authority.
// Its caller must independently bind the original owner/source/operation before
// retaining custody. The declared native name, namespace, project/template
// lineage, task-returned UUID and stable complete selected observation must agree.
func (e *networkProfileConfiguration) verifySwitchingProfileCreatedResult(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (string, bool, error) {
	if op == nil || op.Action != "Create" || op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying" {
		return "", false, errConfigurationTransition
	}
	selection := op.DeepCopy()
	selection.Phase = "Unknown"
	kind, name, valid := networkProfileMetadataResource(op.Resource)
	selected, found := e.selectedSwitchingProfile(name)
	if !valid || kind != "switching-profile" || !found || !e.selectsProfileMetadata(selection) {
		return "", false, errConfigurationTransition
	}
	id, done, err := selected.client.switchingProfileCreateResult(ctx, op, now)
	if err != nil || !done {
		return "", false, err
	}
	if done, err = selected.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now); err != nil || !done {
		return "", false, err
	}
	read := func() ([]switchingProfileObservation, bool, error) {
		rows, err := selected.observe(ctx)
		if err != nil {
			return nil, false, err
		}
		if len(rows) != 1 {
			return nil, false, errInvalidResponse
		}
		o := rows[0]
		if o.binding != op.ParentID && !(legacyEmptySwitchingProfileBinding(op.ParentID) && profileDependencyIdentity(o.desired) == op.ParentID) {
			return nil, false, errSwitchingProfileBinding
		}
		if o.actual.ID == "" {
			return rows, false, nil
		}
		if o.actual.ID != id {
			return nil, false, errConfigurationOwned
		}
		return rows, switchingProfileMatches(o.desired, o.actual), nil
	}
	before, beforeDone, err := read()
	if err != nil {
		return "", false, err
	}
	again, done, err := selected.client.switchingProfileCreateResult(ctx, op, now)
	if err != nil || !done {
		return "", false, err
	}
	if again != id {
		return "", false, errInvalidResponse
	}
	after, afterDone, err := read()
	if err != nil {
		return "", false, err
	}
	if !sameSwitchingProfileObservation(before, after) {
		return "", false, errConfigurationStale
	}
	if !beforeDone || !afterDone {
		return "", false, nil
	}
	return id, true, nil
}
