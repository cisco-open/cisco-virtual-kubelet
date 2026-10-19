// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

const templateVersionsPath = templatesPath + "/version"

type templateVersion struct {
	ID      string
	Number  int64
	Comment string
}

// Version comments are not completion evidence: verify the committed body.
// Null/zero denotes an uncommitted draft and never qualifies as deployable.
func templateVersionNumber(raw json.RawMessage) (int64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "null" {
		return 0, nil
	}
	if strings.HasPrefix(value, "\"") {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0, errInvalidResponse
		}
		value = s
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidResponse
	}
	return n, nil
}
func (c *client) latestTemplateVersion(ctx context.Context, id string) (*templateVersion, error) {
	if !validAPIID(id) {
		return nil, errInvalidResponse
	}
	raw, err := c.get(ctx, templateVersionsPath+"/"+id, nil, 200)
	if err != nil {
		return nil, err
	}
	return decodeLatestTemplateVersion(raw, id)
}

func decodeLatestTemplateVersion(raw []byte, id string) (*templateVersion, error) {
	var groups []struct {
		ID       string `json:"templateId"`
		Versions []struct {
			ID      string          `json:"id"`
			Version json.RawMessage `json:"version"`
			Comment string          `json:"versionComment"`
		} `json:"versionsInfo"`
	}
	if json.Unmarshal(raw, &groups) != nil || groups == nil || len(groups) > 1 {
		return nil, errInvalidResponse
	}
	if len(groups) == 0 {
		return nil, nil
	}
	group := groups[0]
	if group.ID != id || group.Versions == nil || len(group.Versions) > 10000 {
		return nil, errInvalidResponse
	}
	var latest *templateVersion
	ids := map[string]bool{}
	numbers := map[int64]bool{}
	for _, v := range group.Versions {
		version := v.Version
		// The appliance omits version on the base-ID draft entry. Absence on
		// any other UUID remains an invalid response, never a committed version.
		if len(version) == 0 && v.ID == id {
			version = json.RawMessage("0")
		}
		number, err := templateVersionNumber(version)
		if err != nil || !validAPIID(v.ID) || ids[v.ID] {
			return nil, errInvalidResponse
		}
		ids[v.ID] = true
		if number == 0 {
			continue
		}
		if numbers[number] {
			return nil, errInvalidResponse
		}
		numbers[number] = true
		if latest == nil || number > latest.Number {
			latest = &templateVersion{ID: v.ID, Number: number, Comment: v.Comment}
		}
	}
	return latest, nil
}

func (c *client) readCommittedTemplate(ctx context.Context, base templateSummary, version templateVersion) (map[string]any, error) {
	if !validAPIID(base.ID) || !validAPIID(base.ProjectID) || !validAPIID(version.ID) || version.Number <= 0 || version.ID == base.ID {
		return nil, errInvalidResponse
	}
	raw, err := c.get(ctx, templatesPath+"/"+version.ID, nil, 200)
	if err != nil {
		return nil, err
	}
	return decodeCommittedTemplate(raw, base, version)
}

func decodeCommittedTemplate(raw []byte, base templateSummary, version templateVersion) (map[string]any, error) {
	if !validAPIID(base.ID) || !validAPIID(base.ProjectID) || !validAPIID(version.ID) || version.Number <= 0 || version.ID == base.ID {
		return nil, errInvalidResponse
	}
	decoded, err := decodeTemplateNativeJSON(raw)
	if err != nil {
		return nil, err
	}
	body, ok := decoded.(map[string]any)
	if !ok || body["id"] != version.ID || body["projectId"] != base.ProjectID || body["name"] != base.Name {
		return nil, errInvalidResponse
	}
	encoded, err := json.Marshal(body["version"])
	if err != nil {
		return nil, errInvalidResponse
	}
	number, err := templateVersionNumber(encoded)
	if err != nil || number != version.Number {
		return nil, errInvalidResponse
	}
	return body, nil
}
