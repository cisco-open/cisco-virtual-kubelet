// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// This private native primitive does not grant manifest prune/delete authority.
// A caller must provide the original verified Create and an authority check.
// Public cleanup still needs a durable Delete journal and absence receipt; the
// process-local one-use guard alone cannot recover a lost acknowledgement.
type pnpRegistrationRemoval struct {
	resource         string
	snapshotDigest   string
	observeUnchanged func(context.Context) error
	submit           func(context.Context) error
	noDispatch       func(error) *configurationPreSubmitRefusal
	observeAbsent    func(context.Context) (bool, error)
}

type pnpContactMetadata struct {
	FirstContact      *float64 `json:"firstContact,omitempty"`
	LastContact       *float64 `json:"lastContact,omitempty"`
	ReloadRequested   *bool    `json:"reloadRequested,omitempty"`
	PopulateInventory *bool    `json:"populateInventory,omitempty"`
}

type pnpRunSummaryMetadata struct {
	ErrorFlag       *bool     `json:"errorFlag,omitempty"`
	Timestamp       *float64  `json:"timestamp,omitempty"`
	HistoryTaskInfo *struct{} `json:"historyTaskInfo,omitempty"`
}

type pnpRemovalDevice struct {
	Device         pnpDevice
	Contact        pnpContactMetadata
	History        []pnpRunSummaryMetadata
	ContentBlocked bool
}

// Cleanup-only facts must not change existing PnP claim/reference digests or
// serialized recovery snapshots. Decode only structural metadata, never details,
// commands or output, alongside the unchanged shared identity projection.
func (d *pnpRemovalDevice) UnmarshalJSON(raw []byte) error {
	var device pnpDevice
	if err := json.Unmarshal(raw, &device); err != nil {
		return err
	}
	var metadata struct {
		Contact    pnpContactMetadata      `json:"deviceInfo"`
		History    []pnpRunSummaryMetadata `json:"runSummaryList"`
		DayZero    json.RawMessage         `json:"dayZeroConfig"`
		Preview    json.RawMessage         `json:"dayZeroConfigPreview"`
		Blocked    bool                    `json:"_cvkRemovalContentBlocked"`
		Parameters json.RawMessage         `json:"workflowParameters"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return err
	}
	var private struct {
		Info struct {
			Credentials  json.RawMessage `json:"aaaCredentials"`
			Headers      json.RawMessage `json:"httpHeaders"`
			ProjectID    json.RawMessage `json:"projectId"`
			ProjectName  json.RawMessage `json:"projectName"`
			WorkflowName json.RawMessage `json:"workflowName"`
			ImageFile    json.RawMessage `json:"imageFile"`
			Profiles     json.RawMessage `json:"pnpProfileList"`
			Tags         json.RawMessage `json:"tags"`
		} `json:"deviceInfo"`
	}
	if err := json.Unmarshal(raw, &private); err != nil {
		return err
	}
	d.ContentBlocked = metadata.Blocked || !emptyPnPPrivateObject(metadata.DayZero, "config") || !emptyPnPPrivateObject(metadata.Preview) || !emptyPnPPrivateObject(private.Info.Credentials, "username", "password") || !emptyPnPPrivateList(private.Info.Headers)
	d.ContentBlocked = d.ContentBlocked || !emptyPnPPrivateString(private.Info.ProjectID) || !emptyPnPPrivateString(private.Info.ProjectName) || !emptyPnPPrivateString(private.Info.WorkflowName) || !emptyPnPPrivateString(private.Info.ImageFile) || !emptyPnPPrivateList(private.Info.Profiles) || !emptyPnPPrivateObject(private.Info.Tags) || !emptyPnPClaimParameters(metadata.Parameters)
	d.Device, d.Contact, d.History = device, metadata.Contact, metadata.History
	return nil
}

// The shared PnP pager serializes the safe projection into its ordinary
// envelope before decoding it again. Preserve native shape across that boundary
// so identity and cleanup-only metadata both survive without raw secret fields.
func (d pnpRemovalDevice) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(d.Device)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var info map[string]json.RawMessage
	if err := json.Unmarshal(fields["deviceInfo"], &info); err != nil {
		return nil, err
	}
	contact, err := json.Marshal(d.Contact)
	if err != nil {
		return nil, err
	}
	var facts map[string]json.RawMessage
	if err := json.Unmarshal(contact, &facts); err != nil {
		return nil, err
	}
	if len(facts) > 0 {
		if info == nil {
			info = map[string]json.RawMessage{}
		}
		for key, value := range facts {
			info[key] = value
		}
		fields["deviceInfo"], err = json.Marshal(info)
		if err != nil {
			return nil, err
		}
	}
	if d.History != nil {
		fields["runSummaryList"], err = json.Marshal(d.History)
		if err != nil {
			return nil, err
		}
	}
	if d.ContentBlocked {
		fields["_cvkRemovalContentBlocked"] = json.RawMessage("true")
	}
	return json.Marshal(fields)
}

func (c *client) pnpRemovalDevices(ctx context.Context) ([]pnpRemovalDevice, error) {
	rows, err := listPnPPages(ctx, c, pnpDevicesPath, func(d pnpRemovalDevice) string { return d.Device.ID }, 100)
	if err != nil {
		return nil, err
	}
	core := make([]pnpDevice, len(rows))
	for i := range rows {
		core[i] = rows[i].Device
	}
	// Info pointers are shared with the extended rows, preserving canonical
	// serial normalization and member sorting from the common inventory validator.
	if _, err := validatePnPInventory(core); err != nil {
		return nil, err
	}
	return rows, nil
}

func uncontactedPnPRegistration(receipt pnpRegistrationReceipt, row pnpRemovalDevice) bool {
	if row.ContentBlocked {
		return false
	}
	if !pnpRegistrationMatches(receipt, row.Device) || receipt.stack {
		return false
	}
	i, m := row.Device.Info, row.Contact
	if m.FirstContact != nil && *m.FirstContact != 0 || m.LastContact != nil && *m.LastContact != 0 || m.ReloadRequested != nil && *m.ReloadRequested || m.PopulateInventory != nil && *m.PopulateInventory {
		return false
	}
	// Native registration appends one metadata-only audit entry. Unknown, error,
	// task-bearing and repeated history is refused without interpreting details.
	if len(row.History) > 1 {
		return false
	}
	for _, audit := range row.History {
		if audit.ErrorFlag == nil || *audit.ErrorFlag || audit.Timestamp == nil || *audit.Timestamp <= 0 || audit.HistoryTaskInfo != nil {
			return false
		}
	}
	if len(i.StackInfo.Members) != 0 || row.Device.Workflow != nil || row.Device.SystemWorkflow != nil || row.Device.ResetWorkflow != nil || row.Device.Parameters != nil && len(row.Device.Parameters.ConfigList) != 0 {
		return false
	}
	return i.State == "Unclaimed" && i.OnboardingState == "Not Contacted" && i.SiteID == "" && i.Location.SiteID == "" && i.WorkflowID == ""
}

func (c *client) planPnPRegistrationRemoval(ctx context.Context, want pnpDeviceIntent, created *cvk.NetworkControllerConfigurationOperation, authority func(context.Context) error) (*pnpRegistrationRemoval, error) {
	if authority == nil || !validPnPRegistrationReceiptOperation(created) {
		return nil, errConfigurationTransition
	}
	// Only the supported single-device registration payload is relevant here.
	// Freeze it before calling any authority/native reader supplied by the caller.
	body, err := pnpRegistrationPayload(want)
	if err != nil {
		return nil, err
	}
	serials, err := validatePnPDeviceIntent(want)
	if err != nil || len(serials) != 1 {
		return nil, errConfigurationTransition
	}
	if created.Resource != fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)) {
		return nil, errConfigurationStale
	}
	expected := pnpRegistrationReceipt{resource: created.Resource, deviceID: created.CreatedResourceID, serial: serials[0], pid: want.PID, hostname: want.Name}
	return c.planPnPRemovalIdentity(ctx, expected, "", authority)
}

// The identity is supplied either by exact current source or by a separately
// verified canonical creator baseline. Neither path guesses an old raw payload.
func (c *client) planPnPRemovalIdentity(ctx context.Context, expected pnpRegistrationReceipt, pinned string, authority func(context.Context) error) (*pnpRegistrationRemoval, error) {
	if authority == nil || !validAPIID(expected.deviceID) {
		return nil, errConfigurationTransition
	}
	read := func(ctx context.Context, absent bool) (*pnpRemovalDevice, error) {
		return c.readPnPRemovalState(ctx, expected, absent)
	}

	if err := authority(ctx); err != nil {
		return nil, err
	}
	baseline, err := read(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := authority(ctx); err != nil {
		return nil, err
	}
	snapshotDigest := featureIdentityDigest(*baseline)
	if pinned != "" && snapshotDigest != pinned {
		return nil, errConfigurationOwned
	}
	resource := "pnp-remove:v1:" + expected.deviceID + ":" + snapshotDigest
	var acknowledged atomic.Bool
	write := &configurationWrite{resource: resource, action: "Delete", submitCompleted: func(ctx context.Context) error {
		raw, err := c.mutateQueryStatus(ctx, http.MethodDelete, pnpDevicesPath+"/"+expected.deviceID, nil, nil, http.StatusOK)
		if err != nil {
			return err
		}
		// The documented synchronous response is the deleted PnP object. Matching
		// UUID and any returned identity are acknowledgement only; independent
		// absence is still needed. As with registration, an ID-only object is valid;
		// if deviceInfo is returned, its immutable identity must also agree.
		// Deletion may itself change state/audit metadata, so eligibility is
		// established by the repeated pre-submit snapshots, not this response.
		var deleted pnpDevice
		if json.Unmarshal(raw, &deleted) != nil || deleted.ID != expected.deviceID || deleted.Info != nil && !pnpRegistrationMatches(expected, deleted) {
			return errInvalidResponse
		}
		acknowledged.Store(true)
		return nil
	}}
	unchanged := func(ctx context.Context) error {
		for range 2 {
			if err := authority(ctx); err != nil {
				return err
			}
			fresh, err := read(ctx, false)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(baseline, fresh) {
				return errConfigurationStale
			}
			if err := authority(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	guardConfigurationPreflight(write, unchanged)
	return &pnpRegistrationRemoval{resource: resource, snapshotDigest: snapshotDigest, observeUnchanged: unchanged, submit: write.submitCompleted, noDispatch: write.noDispatch, observeAbsent: func(ctx context.Context) (bool, error) {
		if !acknowledged.Load() {
			return false, errConfigurationTransition
		}
		for range 2 {
			if err := authority(ctx); err != nil {
				return false, err
			}
			if _, err := read(ctx, true); err != nil {
				return false, err
			}
			if err := authority(ctx); err != nil {
				return false, err
			}
		}
		return true, nil
	}}, nil
}

func (c *client) readPnPRemovalState(ctx context.Context, expected pnpRegistrationReceipt, absent bool) (*pnpRemovalDevice, error) {
	rows, err := c.pnpRemovalDevices(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	return validatePnPRemovalState(rows, inventory, expected, absent)
}

// Reuse the same eligibility and alias checks for a whole-cohort snapshot.
func validatePnPRemovalState(rows []pnpRemovalDevice, inventory []Device, expected pnpRegistrationReceipt, absent bool) (*pnpRemovalDevice, error) {
	// An incomplete managed serial inventory cannot establish physical
	// absence. Use the same canonical identity contract as registration.
	for _, device := range inventory {
		for _, serial := range strings.Split(device.Serial, ",") {
			if _, err := topologyidentity.CanonicalPhysicalIdentity(serial); err != nil {
				return nil, errInvalidResponse
			}
		}
	}
	if err := requirePnPRegistrationAbsentFromInventory(map[string]bool{expected.serial: true}, nil, inventory); err != nil {
		return nil, err
	}
	var selected *pnpRemovalDevice
	for _, row := range rows {
		if row.Device.ID == expected.deviceID {
			if absent || !uncontactedPnPRegistration(expected, row) {
				return nil, errConfigurationOwned
			}
			copy := row
			selected = &copy
		} else {
			if row.Device.Info.Serial == expected.serial {
				return nil, errConfigurationOwned
			}
			for _, member := range row.Device.Info.StackInfo.Members {
				if member.Serial == expected.serial {
					return nil, errConfigurationOwned
				}
			}
		}
	}
	if !absent && selected == nil {
		return nil, errConfigurationOwned
	}
	return selected, nil
}

// Keep only the refusal bit, never configuration/credential values or hashes of
// low-entropy secrets. Unknown shapes/keys are not interpreted as empty.
func emptyPnPPrivateObject(raw json.RawMessage, keys ...string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key, value := range fields {
		known := false
		for _, allowed := range keys {
			if key == allowed {
				known = true
			}
		}
		if !known {
			return false
		}
		if string(value) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil || text != "" {
			return false
		}
	}
	return true
}
func emptyPnPPrivateList(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var entries []json.RawMessage
	return json.Unmarshal(raw, &entries) == nil && entries != nil && len(entries) == 0
}

func emptyPnPPrivateString(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == ""
}
func emptyPnPClaimParameters(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key, value := range fields {
		switch key {
		case "configList":
			if !emptyPnPPrivateList(value) {
				return false
			}
		case "topOfStackSerialNumber", "licenseLevel", "licenseType":
			if !emptyPnPPrivateString(value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
