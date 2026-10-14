// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestProfileSchemaAndVariableMapping(t *testing.T) {
	rs, e := Compile([]byte(`sdwan:
  feature_profiles:
    system_profiles:
      - name: system
        banner:
          login: "hello\n"
          motd_variable: message
`))
	if e != nil {
		t.Fatal(e)
	}
	if len(rs) != 2 || rs[0].Kind != "profile" || rs[1].ParentName != "system" {
		t.Fatal("dependency order")
	}
	data := rs[1].Body["data"].(map[string]any)
	if data["login"].(map[string]any)["value"] != "hello\\n" || data["motd"].(map[string]any)["value"] != "{{message}}" {
		t.Fatal("upstream variable/newline mapping")
	}
	if _, e = Compile([]byte("sdwan:\n  feature_profiles:\n    system_profiles:\n      - name: system\n        unknown_feature: {}\n")); e == nil {
		t.Fatal("unsupported feature silently accepted")
	}
}
func TestProfileNativeDependencyLifecycle(t *testing.T) {
	var profile, parcel map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		isParcel := strings.Contains(r.URL.Path, "/banner")
		if isParcel && profile == nil {
			t.Error("parcel before profile")
		}
		switch {
		case r.Method == "POST":
			if isParcel {
				json.NewDecoder(r.Body).Decode(&parcel)
				fmt.Fprint(w, `{"parcelId":"parcel-id"}`)
			} else {
				json.NewDecoder(r.Body).Decode(&profile)
				fmt.Fprint(w, `{"id":"profile-id"}`)
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/system"):
			if profile == nil {
				fmt.Fprint(w, `[]`)
			} else {
				json.NewEncoder(w).Encode([]any{map[string]any{"profileId": "profile-id", "profileName": profile["name"]}})
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/banner"):
			if parcel == nil {
				fmt.Fprint(w, `{"data":[]}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"parcelId": "parcel-id", "payload": parcel}}})
			}
		case r.Method == "GET" && isParcel:
			json.NewEncoder(w).Encode(map[string]any{"payload": parcel})
		case r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"profileName": profile["name"], "description": profile["description"]})
		default:
			http.Error(w, "bad", 400)
		}
	})
	rs, e := Compile([]byte("sdwan:\n  feature_profiles:\n    system_profiles:\n      - name: fixture\n        banner: {}\n"))
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	o, e := c.Observe(ctx, rs[1])
	if e != nil || o != nil {
		t.Fatal("dependency absence", e)
	}
	for _, r := range rs {
		if _, e = c.Create(ctx, r); e != nil {
			t.Fatal(e)
		}
		o, e = c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal("observation", e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal("comparison", e)
		}
	}
}
