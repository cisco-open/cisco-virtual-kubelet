// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveGlobalCredentialAssignmentsRead(t *testing.T) {
	if os.Getenv("CATC_GLOBAL_ASSIGNMENT_READ") != "existing-lab" {
		t.Skip("explicit read-only qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, s := range sites {
		if s.NameHierarchy == "Global" && s.Type == "global" {
			if id != "" {
				t.Fatal("duplicate root")
			}
			id = s.ID
		}
	}
	slots, err := c.siteCredentials(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	desired := []siteCredentialIntent{}
	for _, k := range globalCredentialKinds {
		s := slots[k.model]
		if s.id == "" {
			t.Log(k.model, "unassigned", "inherited", s.inherited, "disabled", s.unset)
			continue
		}
		name := ""
		for _, credential := range credentials[k.model] {
			if credential.ID == s.id {
				if name != "" {
					t.Fatal("ambiguous credential")
				}
				name = credential.Name
			}
		}
		if name == "" {
			t.Fatal("assigned credential unresolved")
		}
		desired = append(desired, siteCredentialIntent{site: "Global", siteKind: "global", kind: k.model, name: name})
	}
	if len(desired) > 0 {
		if write, err := (&siteCredentialExecutor{client: c, desired: desired}).next(ctx); err != nil || write != nil {
			t.Fatal("existing Global assignments not converged", err)
		}
	}
	t.Log("read six root slots; existing named assignments verified", len(desired))
}
