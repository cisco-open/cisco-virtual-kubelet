// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestInventoryTagReport(t *testing.T) {
	reads := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != tagsPath {
			t.Error("unexpected API request")
			w.WriteHeader(400)
			return
		}
		reads++
		fmt.Fprint(w, `{"response":[{"id":"tag","name":"existing","description":"old","systemTag":false}]}`)
	})
	raw := []byte(`{"catalyst_center":{"inventory":{"tags":[{"name":"existing","description":"new"},{"name":"missing"}]}}}`)
	plans, err := c.compileConfigReports(raw, []string{"inventory"})
	if err != nil || len(plans) != 1 || plans[0].count != 2 || reads != 0 {
		t.Fatal("inventory report compilation", err)
	}
	drift, err := plans[0].observe(context.Background())
	if err != nil || len(drift) != 2 {
		t.Fatal("inventory tag drift", err)
	}
	for _, d := range drift {
		if d.Section != "inventory" || d.Path != "/tags" {
			t.Fatal("incorrect drift attribution")
		}
	}
	if _, err := c.compileConfigurationExecutor(raw, "inventory"); err != nil {
		t.Fatal("inventory apply compilation", err)
	}
}

func TestInventoryTagRejectsUnimplementedFields(t *testing.T) {
	for _, raw := range []string{
		`{"catalyst_center":{"inventory":{"devices":[{"name":"switch"}],"tags":[{"name":"tag"}]}}}`,
		`{"catalyst_center":{"inventory":{"tags":[{"name":"tag","unknown":true}]}}}`,
		`{"catalyst_center":{"inventory":{"tags":[{"name":"tag"},{"name":"tag"}]}}}`,
	} {
		if _, err := parseInventoryTags([]byte(raw)); err == nil {
			t.Fatal("accepted unsupported inventory intent")
		}
	}
}
