// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "math"

// Safe projection of workflow tasks. Work-item commands/output, task names,
// descriptions and configuration parameters are intentionally discarded. Native
// timing units and terminal-state semantics require separate qualification.
type pnpWorkflowTaskMetadata struct {
	State      string                        `json:"state"`
	Type       string                        `json:"type"`
	Sequence   *float64                      `json:"taskSeqNo,omitempty"`
	StartTime  *float64                      `json:"startTime,omitempty"`
	EndTime    *float64                      `json:"endTime,omitempty"`
	WorkItems  []pnpWorkflowWorkItemMetadata `json:"workItemList,omitempty"`
	ConfigInfo *pnpWorkflowConfigReference   `json:"configInfo,omitempty"`
}

// Retain only the structural template UUID, never rendered CLI, URLs or values.
type pnpWorkflowConfigReference struct {
	ConfigID string `json:"configId,omitempty"`
}

type pnpWorkflowWorkItemMetadata struct {
	State     string   `json:"state"`
	StartTime *float64 `json:"startTime,omitempty"`
	EndTime   *float64 `json:"endTime,omitempty"`
}

func validPnPNativeCounter(value *float64) bool {
	return value == nil || !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 9007199254740991 && math.Trunc(*value) == *value
}

func validPnPNativeInterval(start, end *float64) bool {
	if !validPnPNativeCounter(start) || !validPnPNativeCounter(end) {
		return false
	}
	// Zero/absent is retained as unknown/not-started, never terminal evidence.
	return start == nil || end == nil || *start == 0 || *end == 0 || *end >= *start
}

func validatePnPWorkflowMetadata(workflow *pnpWorkflowIdentity) error {
	if workflow == nil {
		return nil
	}
	for _, id := range []string{workflow.ID, workflow.ImageID, workflow.ConfigID} {
		if id != "" && !validAPIID(id) {
			return errInvalidResponse
		}
	}
	if !validPnPNativeInterval(workflow.StartTime, workflow.EndTime) || !validPnPNativeCounter(workflow.UpdatedAt) || len(workflow.Tasks) > 1000 {
		return errInvalidResponse
	}
	sequences := map[float64]bool{}
	workItems := 0
	for _, task := range workflow.Tasks {
		if task.ConfigInfo != nil && task.ConfigInfo.ConfigID != "" && !validAPIID(task.ConfigInfo.ConfigID) {
			return errInvalidResponse
		}
		if !validPnPNativeCounter(task.Sequence) || !validPnPNativeInterval(task.StartTime, task.EndTime) {
			return errInvalidResponse
		}
		if task.Sequence != nil {
			if sequences[*task.Sequence] {
				return errInvalidResponse
			}
			sequences[*task.Sequence] = true
		}
		workItems += len(task.WorkItems)
		if len(task.WorkItems) > 1000 || workItems > 10000 {
			return errInvalidResponse
		}
		for _, item := range task.WorkItems {
			if !validPnPNativeInterval(item.StartTime, item.EndTime) {
				return errInvalidResponse
			}
		}
	}
	return nil
}
