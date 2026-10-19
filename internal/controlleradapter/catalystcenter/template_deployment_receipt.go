// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const templateDeployPath = "/dna/intent/api/v2/template-programmer/template/deploy"
const templateDeployStatusPath = templatesPath + "/deploy/status/"

// The pinned provider observes this misspelling in the v2 progress response.
// Support the corrected spelling too, without treating arbitrary progress text
// or a generic task ID as a deployment receipt.
var templateDeploymentProgress = regexp.MustCompile(`(?i)Template (?:Deployemnt|Deployment) Id:\s*(\S+)`)

func canonicalDeploymentID(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || !strings.EqualFold(parsed.String(), id) {
		return "", errInvalidResponse
	}
	return parsed.String(), nil
}
func templateDeploymentReceipt(raw []byte) (string, error) {
	var body struct {
		ID       string `json:"deploymentId"`
		Response struct {
			ID       string `json:"deploymentId"`
			Progress string `json:"progress"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", errInvalidResponse
	}
	ids := []string{}
	if body.ID != "" {
		ids = append(ids, body.ID)
	}
	if body.Response.ID != "" {
		ids = append(ids, body.Response.ID)
	}
	matches := templateDeploymentProgress.FindAllStringSubmatch(body.Response.Progress, -1)
	if len(matches) > 1 {
		return "", errInvalidResponse
	}
	if len(matches) == 1 {
		ids = append(ids, matches[0][1])
	}
	if len(ids) == 0 {
		return "", errInvalidResponse
	}
	result := ""
	for _, id := range ids {
		canonical, err := canonicalDeploymentID(id)
		if err != nil {
			return "", err
		}
		if result != "" && result != canonical {
			return "", errInvalidResponse
		}
		result = canonical
	}
	return result, nil
}

type templateDeploymentIdentity struct {
	deploymentID, deviceID, project, name string
	version                               int64
}
type templateDeploymentDeviceStatus struct {
	DeviceID   string `json:"deviceId"`
	Identifier string `json:"identifier"`
	Type       string `json:"targetType"`
	Status     string `json:"status"`
	Start      string `json:"startTime"`
	End        string `json:"endTime"`
}
type templateDeploymentStatus struct {
	ID      string                           `json:"deploymentId"`
	Project string                           `json:"projectName"`
	Name    string                           `json:"templateName"`
	Version string                           `json:"templateVersion"`
	Status  string                           `json:"status"`
	Start   string                           `json:"startTime"`
	End     string                           `json:"endTime"`
	Devices []templateDeploymentDeviceStatus `json:"devices"`
}

func templateDeploymentTime(raw string) (time.Time, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return time.Time{}, errInvalidResponse
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, errInvalidResponse
	}
	return parsed, nil
}
func templateDeploymentInterval(start, end string, submitted, now time.Time) error {
	from, err := templateDeploymentTime(start)
	if err != nil {
		return err
	}
	to, err := templateDeploymentTime(end)
	if err != nil {
		return err
	}
	if from.Before(submitted.Add(-configurationTaskClockSkew)) || from.After(now.Add(configurationTaskClockSkew)) || to.Before(from) || to.After(now.Add(configurationTaskClockSkew)) {
		return errInvalidResponse
	}
	return nil
}

// Bounded single-device completion evidence. No status message, CLI output or
// parameter value is returned in errors. Missing/expired receipts are errors,
// never successful no-ops or permission to repeat the deployment.
func (c *client) templateDeploymentComplete(ctx context.Context, want templateDeploymentIdentity, submitted, now time.Time) (bool, error) {
	id, err := canonicalDeploymentID(want.deploymentID)
	if err != nil || !validAPIID(want.deviceID) || !validProfileName(want.project) || !validProfileName(want.name) || want.version < 1 || submitted.IsZero() || now.Before(submitted) {
		return false, errInvalidResponse
	}
	raw, err := c.get(ctx, templateDeployStatusPath+id, nil, 200)
	if err != nil {
		return false, err
	}
	var got templateDeploymentStatus
	if json.Unmarshal(raw, &got) != nil {
		return false, errInvalidResponse
	}
	actual, err := canonicalDeploymentID(got.ID)
	if err != nil || actual != id {
		return false, errInvalidResponse
	}
	switch got.Status {
	case "INIT", "IN_PROGRESS":
		return false, nil
	case "FAILURE":
		return false, newConfigurationTaskFailure("TEMPLATE_DEPLOYMENT_FAILURE")
	case "SUCCESS":
	default:
		return false, errInvalidResponse
	}
	version, err := strconv.ParseInt(got.Version, 10, 64)
	if err != nil || version != want.version || got.Project != want.project || got.Name != want.name || len(got.Devices) != 1 {
		return false, errInvalidResponse
	}
	d := got.Devices[0]
	if d.DeviceID != want.deviceID || (d.Identifier != "" && d.Identifier != want.deviceID) || (d.Type != "" && d.Type != "MANAGED_DEVICE_UUID") {
		return false, errInvalidResponse
	}
	switch d.Status {
	case "INIT", "IN_PROGRESS":
		return false, nil
	case "FAILURE":
		return false, newConfigurationTaskFailure("TEMPLATE_DEVICE_DEPLOYMENT_FAILURE")
	case "SUCCESS":
	default:
		return false, errInvalidResponse
	}
	if err := templateDeploymentInterval(got.Start, got.End, submitted, now); err != nil {
		return false, err
	}
	if err := templateDeploymentInterval(d.Start, d.End, submitted, now); err != nil {
		return false, err
	}
	return true, nil
}
