// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestCloudProviderSettingsModel(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/cloud-provider-settings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Kind != "cloud-setting" || rs[0].Body["umbrellaOrgId"] != "123456" || rs[0].Body["ciscoSSEContextSharing"] != false {
		t.Fatal("invalid settings mapping")
	}
	if _, err = Compile([]byte(strings.Replace(string(raw), "123456", "not-an-id", 1))); err == nil {
		t.Fatal("invalid organization ID accepted")
	}
	if _, err = Compile([]byte(strings.Replace(string(raw), "umbrella_org_id", "unknown_field", 1))); err == nil {
		t.Fatal("unknown cloud setting accepted")
	}
}

func TestCloudSettingsLocalLifecycle(t *testing.T) {
	var stored map[string]any
	posts, puts := 0, 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		if r.URL.Path != "/dataservice/settings/configuration/cloudProviderSetting" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "GET":
			rows := []any{}
			if stored != nil {
				rows = append(rows, stored)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
		case "POST", "PUT":
			if r.Method == "POST" {
				posts++
			} else {
				puts++
			}
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	})
	raw, err := os.ReadFile("../../../examples/configs/sdwan/cloud-provider-settings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	ctx := context.Background()
	if o, err := c.Observe(ctx, r); err != nil || o != nil {
		t.Fatal("expected absent", err)
	}
	id, err := c.Create(ctx, r)
	if err != nil || id != cloudSettingsID {
		t.Fatal("create", err)
	}
	o, err := c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("readback", err)
	}
	if same, err := Equal(r, o.Body); err != nil || !same {
		t.Fatal("create mismatch", err)
	}
	r.Body["ciscoSSEContextSharing"] = true
	if err := c.Update(ctx, r, o); err != nil {
		t.Fatal("update", err)
	}
	o, err = c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("updated readback", err)
	}
	if same, err := Equal(r, o.Body); err != nil || !same || posts != 1 || puts != 1 {
		t.Fatal("update mismatch", err, posts, puts)
	}
}
