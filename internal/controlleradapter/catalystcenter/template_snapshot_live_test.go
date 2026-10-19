// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Focused final-code qualification of the exact-number snapshot decoder and
// full version reader. Existing definitions are observed only; no task or write.
func TestLiveCatalystCenterTemplateExactSnapshotReads(t *testing.T) {
	if os.Getenv("CATC_TEMPLATE_SNAPSHOT_READS") != "existing-read-only" {
		t.Skip("read-only native snapshot qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for name, p := range projects {
		names[p.ID] = name
	}
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k, d := range definitions {
		if !strings.HasPrefix(names[d.ProjectID], "CVK-") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Skip("no existing non-fixture template available for read-only qualification")
	}
	d := definitions[keys[0]]
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	comparison := &templateInputComparison{key: key, keyID: strings.Repeat("a", 64)}
	resource := templateCreatorResource("r", d.ProjectID, d.Name) + ":i:" + templateComparisonTag + ":" + comparison.keyID
	read := func() string {
		raw, err := c.get(ctx, templatesPath+"/"+d.ID, url.Values{"unCommitted": {"true"}}, 200)
		if err != nil {
			t.Fatal(err)
		}
		exact, err := decodeTemplateNativeJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		history, err := c.templateVersionFootprint(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		mac, err := templateNativeMAC(comparison, resource, map[string]any{"definition": exact, "history": history})
		if err != nil {
			t.Fatal(err)
		}
		return mac
	}
	first, second := read(), read()
	if first != second {
		t.Fatal("native snapshot changed between observations; no effect performed")
	}
	t.Log("existing native template detail and complete version history have stable exact-number keyed snapshots; no mutation performed")
}
