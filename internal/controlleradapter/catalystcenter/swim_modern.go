// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"net/url"
	"time"
)

const imageUpdatesPath = "/dna/intent/api/v1/networkDeviceImageUpdates"

// Modern distribution and activation are deliberately separate from the
// legacy methods. Callers pin the contract before dispatch; a failure never
// causes a retry through a different endpoint. Activation may also distribute.
func (c *client) DistributeImages(ctx context.Context, deviceID, imageID string) (Task, error) {
	return c.submitImageUpdate(ctx, deviceID, imageID, "distribute", "distributedImages")
}

func (c *client) UpdateImages(ctx context.Context, deviceID, imageID string) (Task, error) {
	return c.submitImageUpdate(ctx, deviceID, imageID, "activate", "installedImages")
}

func (c *client) submitImageUpdate(ctx context.Context, deviceID, imageID, action, field string) (Task, error) {
	if !validAPIID(deviceID) || !validAPIID(imageID) {
		return Task{}, errors.New("invalid SWIM device or image ID")
	}
	return c.submitTask(ctx, networkDeviceImagesPath+deviceID+"/"+action, map[string]any{field: []map[string]string{{"id": imageID}}})
}

// ImageUpdate is the per-device workflow, not the parent submission task.
type ImageUpdate struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	DeviceID  string `json:"networkDeviceId"`
	Version   string `json:"updateImageVersion"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
}

func (c *client) ListImageUpdates(ctx context.Context, deviceID, taskID string) ([]ImageUpdate, error) {
	if !validAPIID(deviceID) || !validAPIID(taskID) {
		return nil, errors.New("invalid image update binding")
	}
	items, err := listInventoryQuery(ctx, c, imageUpdatesPath, url.Values{"parentId": {taskID}, "networkDeviceId": {deviceID}}, func(i ImageUpdate) string { return i.ID })
	if err != nil {
		return nil, err
	}
	for _, i := range items {
		if !validAPIID(i.ID) || i.ParentID != taskID || i.DeviceID != deviceID {
			return nil, errInvalidResponse
		}
	}
	return items, nil
}

// imageUpdateState requires exactly one correlated workflow and an exact
// controller image version. Missing workflows are pending, never success.
// Callers still need fresh device verification after successful activation.
func imageUpdateState(items []ImageUpdate, deviceID, taskID, version, kind string, submitted, now time.Time) (swimTaskState, error) {
	if !validAPIID(deviceID) || !validAPIID(taskID) || version == "" || (kind != "DISTRIBUTE" && kind != "ACTIVATE") || submitted.IsZero() || submitted.After(now) {
		return swimTaskState{}, errors.New("invalid image update observation binding")
	}
	if len(items) == 0 {
		return swimTaskState{}, nil
	}
	if len(items) != 1 {
		return swimTaskState{}, errors.New("ambiguous image update workflows")
	}
	i := items[0]
	if !validAPIID(i.ID) || i.DeviceID != deviceID || i.ParentID != taskID || i.Type != kind || i.Version != version || i.StartTime < submitted.UnixMilli() || i.StartTime > now.UnixMilli() {
		return swimTaskState{}, errInvalidResponse
	}
	switch i.Status {
	case "PENDING", "IN_PROGRESS", "IN-PROGRESS":
		if i.EndTime != 0 {
			return swimTaskState{}, errInvalidResponse
		}
		return swimTaskState{}, nil
	case "SUCCESS", "FAILURE":
		if i.EndTime < i.StartTime || i.EndTime == 0 || i.EndTime > now.UnixMilli() {
			return swimTaskState{}, errInvalidResponse
		}
		return swimTaskState{complete: i.Status == "SUCCESS", failed: i.Status == "FAILURE"}, nil
	default:
		return swimTaskState{}, errInvalidResponse
	}
}
