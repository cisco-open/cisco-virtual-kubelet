// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
)

func probeLiveTemplateProfileReferences(t *testing.T, c *client, ctx context.Context, projectName, templateName, profileID string) {
	t.Helper()
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := definitions[projects[projectName].ID+"#"+templateName]
	if !validAPIID(target.ID) {
		t.Fatal("reference fixture target missing")
	}
	path := "/dna/intent/api/v1/templates/" + target.ID + "/networkProfilesForSites"
	raw, err := c.get(ctx, path, nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := decodeList[map[string]any](raw)
	if err != nil {
		t.Fatal(err)
	}
	fields, identities := []string{}, []string{}
	if len(rows) == 1 {
		for k, v := range rows[0] {
			fields = append(fields, k)
			if value, ok := v.(string); ok && value == profileID {
				identities = append(identities, k)
			}
		}
	}
	sort.Strings(fields)
	sort.Strings(identities)
	countRaw, err := c.get(ctx, path+"/count", nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	var count struct {
		Response struct {
			Count *int `json:"count"`
		} `json:"response"`
	}
	if json.Unmarshal(countRaw, &count) != nil || count.Response.Count == nil {
		t.Fatal("count envelope unavailable")
	}
	result := map[string]any{"scope": "Nonempty legacy-created profile association structural inspection only; no removal authority", "returnedItems": len(rows), "count": *count.Response.Count, "itemFields": fields, "knownProfileIdentityFields": identities}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile("/tmp/cvk-template-profile-reference-shape.json", append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("reverse native profile reference shape", len(rows), *count.Response.Count, fields, identities)
}
