// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const pnpDevicesPath = "/dna/intent/api/v1/onboarding/pnp-device"

// Keep only identity/ownership evidence. The native object also contains AAA
// credentials, day-zero configuration and CLI output; never retain those in a
// dependency snapshot or pass raw native objects to status/reporting code.
type pnpDeviceInfo struct {
	Serial          string `json:"serialNumber"`
	PID             string `json:"pid"`
	Hostname        string `json:"hostname"`
	Stack           *bool  `json:"stack"`
	State           string `json:"state"`
	OnboardingState string `json:"onbState"`
	AuthStatus      string `json:"authStatus"`
	WorkflowID      string `json:"workflowId"`
	SiteID          string `json:"siteId"`
	Location        struct {
		SiteID string `json:"siteId"`
	} `json:"location"`
	StackInfo struct {
		Members []struct {
			Serial string `json:"serialNumber"`
			PID    string `json:"pid"`
			State  string `json:"state"`
		} `json:"stackMemberList"`
	} `json:"stackInfo"`
}
type pnpWorkflowIdentity struct {
	ID        string                    `json:"_id"`
	State     string                    `json:"state"`
	ImageID   string                    `json:"imageId,omitempty"`
	ConfigID  string                    `json:"configId,omitempty"`
	StartTime *float64                  `json:"startTime,omitempty"`
	EndTime   *float64                  `json:"endTime,omitempty"`
	UpdatedAt *float64                  `json:"lastupdateOn,omitempty"`
	Tasks     []pnpWorkflowTaskMetadata `json:"tasks,omitempty"`
}

// Native libraries use id while older/embedded schemas use _id. Normalize at
// decoding so paging and every downstream consumer see one identity. Conflicting
// aliases cannot establish ownership or a complete reference inventory.
func (w *pnpWorkflowIdentity) UnmarshalJSON(raw []byte) error {
	type projection pnpWorkflowIdentity
	var decoded struct {
		projection
		NativeID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.NativeID != "" {
		if !validAPIID(decoded.NativeID) || decoded.ID != "" && decoded.ID != decoded.NativeID {
			return errInvalidResponse
		}
		decoded.ID = decoded.NativeID
	}
	*w = pnpWorkflowIdentity(decoded.projection)
	return nil
}

// Only structural configuration UUIDs survive this projection. Never add
// configParameters, licenses or top-of-stack identity to this safe subtree.
type pnpWorkflowParameterReferences struct {
	ConfigList []struct {
		ConfigID string `json:"configId"`
	} `json:"configList,omitempty"`
}

type pnpDevice struct {
	ID             string                          `json:"id"`
	Info           *pnpDeviceInfo                  `json:"deviceInfo"`
	Workflow       *pnpWorkflowIdentity            `json:"workflow"`
	SystemWorkflow *pnpWorkflowIdentity            `json:"systemWorkflow"`
	ResetWorkflow  *pnpWorkflowIdentity            `json:"systemResetWorkflow"`
	Parameters     *pnpWorkflowParameterReferences `json:"workflowParameters,omitempty"`
}

func (c *client) pnpDevices(ctx context.Context) ([]pnpDevice, error) {
	rows, err := listPnPPages(ctx, c, pnpDevicesPath, func(d pnpDevice) string { return d.ID }, 100)
	if err != nil {
		return nil, err
	}
	return validatePnPInventory(rows)
}

// Shared canonical identity/stack/workflow validation. Private readers may add
// safe metadata without changing the established dependency/journal projection.
func validatePnPInventory(rows []pnpDevice) ([]pnpDevice, error) {
	serialOwners := map[string]string{}
	for i := range rows {
		d := &rows[i]
		if !validAPIID(d.ID) || d.Info == nil || !validProfileName(d.Info.PID) {
			return nil, errInvalidResponse
		}
		info := d.Info
		serial, err := topologyidentity.CanonicalPhysicalIdentity(info.Serial)
		if err != nil {
			return nil, errInvalidResponse
		}
		info.Serial = serial
		members := map[string]bool{serial: true}
		if len(info.StackInfo.Members) > 100 {
			return nil, errInvalidResponse
		}
		seen := map[string]bool{}
		for j := range info.StackInfo.Members {
			member := &info.StackInfo.Members[j]
			serial, err := topologyidentity.CanonicalPhysicalIdentity(member.Serial)
			if err != nil || seen[serial] || !validProfileName(member.PID) {
				return nil, errInvalidResponse
			}
			seen[serial], members[serial] = true, true
			member.Serial = serial
		}
		if info.Stack != nil && !*info.Stack && (len(members) > 1 || len(info.StackInfo.Members) > 0) {
			return nil, errInvalidResponse
		}
		for serial := range members {
			if owner, exists := serialOwners[serial]; exists && owner != d.ID {
				return nil, errInvalidResponse
			}
			serialOwners[serial] = d.ID
		}
		for _, id := range []string{info.WorkflowID, info.SiteID, info.Location.SiteID} {
			if id != "" && !validAPIID(id) {
				return nil, errInvalidResponse
			}
		}
		if info.SiteID != "" && info.Location.SiteID != "" && info.SiteID != info.Location.SiteID {
			return nil, errInvalidResponse
		}
		for _, workflow := range []*pnpWorkflowIdentity{d.Workflow, d.SystemWorkflow, d.ResetWorkflow} {
			if err := validatePnPWorkflowMetadata(workflow); err != nil {
				return nil, err
			}
		}
		sort.Slice(info.StackInfo.Members, func(i, j int) bool { return info.StackInfo.Members[i].Serial < info.StackInfo.Members[j].Serial })
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
