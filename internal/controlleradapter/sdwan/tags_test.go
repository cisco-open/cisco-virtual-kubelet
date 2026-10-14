// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestTagFixture(t *testing.T) {
	raw, e := os.ReadFile("../../../examples/configs/sdwan/tags.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(rs) != 2 || rs[0].Name != "CVK-TAG-A" || !reflect.DeepEqual(rs[0].TagDevices, []string{"CVK-TEST-ROUTER-1", "CVK-TEST-ROUTER-2"}) {
		t.Fatal("tag grouping changed")
	}
	if _, e = Compile([]byte(strings.Replace(string(raw), "CVK-TEST-ROUTER-2", "CVK-TEST-ROUTER-1", 1))); e == nil {
		t.Fatal("duplicate chassis ID accepted")
	}
}

func TestTagAssociationPreservesOtherTags(t *testing.T) {
	var posted map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/dataservice/system/device/vedges"):
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ROUTER-1","tags":[{"id":"OTHER-TAG"}]}]}`))
		case r.Method == "POST" && r.URL.Path == "/dataservice/v1/tags/associate":
			if e := json.NewDecoder(r.Body).Decode(&posted); e != nil {
				t.Error(e)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	})
	if e := c.associateTag(context.Background(), "CVK-TAG", []string{"ROUTER-1"}, false); e != nil {
		t.Fatal(e)
	}
	rows, ok := posted["data"].([]any)
	if !ok || len(rows) != 2 || rows[0].(map[string]any)["tagId"] != "CVK-TAG" || rows[1].(map[string]any)["tagId"] != "OTHER-TAG" {
		t.Fatal("overlapping tag membership not preserved")
	}
}

func TestLiveTagUnassignedLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("explicit fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	r := Resource{Key: "sites/tags/CVK-TAG-UNASSIGNED", Name: "CVK-TAG-UNASSIGNED", Section: "sites", Type: "tag", Kind: "tag", Body: map[string]any{"data": []any{map[string]any{"name": "CVK-TAG-UNASSIGNED", "description": "CVK-TAG-UNASSIGNED"}}}}
	if o, e := c.Observe(ctx, r); e != nil || o != nil {
		t.Fatal("tag must be absent", e)
	}
	id, e := c.Create(ctx, r)
	if e != nil {
		t.Fatal("create", e)
	}
	t.Cleanup(func() {
		if e := c.DeleteResourceFixture(context.Background(), r, id); e != nil {
			t.Error("cleanup", e)
		}
	})
	o, e := c.Observe(ctx, r)
	if e != nil || o == nil {
		t.Fatal("readback", e)
	}
	if same, e := Equal(r, o.Body); e != nil || !same {
		t.Fatal("tag mismatch", e)
	}
}
