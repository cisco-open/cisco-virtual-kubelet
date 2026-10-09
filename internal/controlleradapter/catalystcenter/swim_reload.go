// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type standardReloadAPI interface {
	CheckStandardReloadProfile(context.Context, string) error
	UpdateImagesStandardReload(context.Context, string, string) (Task, error)
}

// Keep this qualification bounded to the actual appliance build. An upgrade of
// Catalyst Center requires renewed contract qualification, not a warning bypass.
func (c *client) CheckStandardReloadProfile(ctx context.Context, deviceID string) error {
	if !validAPIID(deviceID) {
		return errors.New("invalid device ID")
	}
	raw, err := c.Get(ctx, "/dna/intent/api/v1/dnac-release", nil)
	if err != nil {
		return err
	}
	var release struct {
		Response struct {
			InstalledVersion string `json:"installedVersion"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &release) != nil || release.Response.InstalledVersion != "3.2.3-75346.100" {
		return errors.New("standard reload profile requires qualified Catalyst Center build")
	}
	return nil
}

// No optional upgrade features are selected. Do not guess feature names from
// UI-only APIs or silently switch to a legacy activation endpoint.
func (c *client) UpdateImagesStandardReload(ctx context.Context, deviceID, imageID string) (Task, error) {
	if !validAPIID(deviceID) || !validAPIID(imageID) {
		return Task{}, errors.New("invalid SWIM device or image ID")
	}
	if err := c.CheckStandardReloadProfile(ctx, deviceID); err != nil {
		return Task{}, err
	}
	return c.submitTask(ctx, networkDeviceImagesPath+deviceID+"/activate", map[string]any{
		"installedImages":    []map[string]string{{"id": imageID}},
		"compatibleFeatures": []map[string]string{},
	})
}

func validateStandardReloadReadiness(items []ReadinessResult, intent swimIntent, task string, submitted, now time.Time) error {
	if intent.StandardReloadProfile != "CatalystCenter323" || intent.APIContract != swimModernContract {
		return errors.New("standard reload readiness requires pinned profile")
	}
	copyItems := append([]ReadinessResult(nil), items...)
	for n, item := range copyItems {
		// The exact version-path warning is distinct from device eligibility,
		// generic image compatibility, and unknown xFSU failures. Preserve all
		// binding/freshness/completeness checks in the ordinary validator.
		if item.ParentID == task && item.Name == "XFSU Compatibility Check" && item.Status == "WARNING" &&
			item.XFSUVersionPathTarget != "" && swimVersionMatches(item.XFSUVersionPathTarget, intent.TargetVersion) {
			copyItems[n].Status = "SUCCESS"
		}
	}
	return validateXEReadiness(copyItems, intent.ControllerDeviceID, task, intent.TransferFallbackAddress, submitted, now)
}
