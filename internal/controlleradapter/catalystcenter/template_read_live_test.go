// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"
)

func TestLiveCatalystCenterTemplateRead(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for key := range definitions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	checked := 0
	for _, key := range keys {
		if checked == 3 {
			break
		}
		if _, err := c.readTemplateDefinition(ctx, definitions[key]); err != nil {
			t.Fatal(err)
		}
		version, err := c.latestTemplateVersion(ctx, definitions[key].ID)
		if err != nil {
			t.Fatal(err)
		}
		if version != nil {
			raw, err := c.get(ctx, templatesPath+"/"+version.ID, nil, 200)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if json.Unmarshal(raw, &body) != nil {
				t.Fatal("invalid committed definition")
			}
			t.Logf("committed detail: idMatchesVersion=%v idMatchesBase=%v projectMatches=%v versionFieldType=%T", body["id"] == version.ID, body["id"] == definitions[key].ID, body["projectId"] == definitions[key].ProjectID, body["version"])
		}
		checked++
	}
	t.Logf("template draft inventory=%d; identity-bound detail reads=%d; no values printed or writes performed", len(definitions), checked)
}
