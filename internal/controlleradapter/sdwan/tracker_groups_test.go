// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestTrackerGroupReferenceValidation(t *testing.T) {
	resources := parcelFixtures(t, "service-features")
	seen := map[string]bool{}
	for _, r := range resources {
		for _, ref := range r.ParcelRefs {
			if !seen[r.ParentName+"/"+ref.ParcelPath+"/"+ref.Name] {
				t.Fatal("group precedes its tracker")
			}
		}
		seen[r.ParentName+"/"+r.ParcelPath+"/"+r.Name] = true
	}
	for _, names := range [][]any{{"a"}, {"a", "a"}, {"a", "external"}, {"a", float64(1)}} {
		profile := FeatureProfile{Name: "test", IPv4Trackers: []map[string]any{{"name": "a"}, {"name": "b"}}, IPv4TrackerGroups: []map[string]any{{"name": "group", "trackers": names}}}
		if _, err := compileTrackerGroups(profile, "service", "test"); err == nil {
			t.Fatal("invalid tracker references accepted")
		}
	}
}

func TestTrackerGroupMissingDependencyDoesNotWrite(t *testing.T) {
	resources := parcelFixtures(t, "service-features")
	var group Resource
	for _, r := range resources {
		if r.ParcelPath == "trackergroup" {
			group = r
		}
	}
	present := false
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		if r.Method != "GET" {
			writes++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			encoded, _ := json.Marshal(body)
			if !strings.Contains(string(encoded), "11111111-1111-1111-1111-111111111111") || !strings.Contains(string(encoded), "22222222-2222-2222-2222-222222222222") {
				t.Error("group write did not resolve both tracker IDs")
			}
			fmt.Fprint(w, `{"parcelId":"group-id"}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/service"):
			fmt.Fprint(w, `[{"profileId":"profile-id","profileName":"CVK-SERVICE-FEATURES"}]`)
		case strings.HasSuffix(r.URL.Path, "/tracker"):
			if !present {
				fmt.Fprint(w, `{"data":[]}`)
			} else {
				fmt.Fprint(w, `{"data":[{"parcelId":"11111111-1111-1111-1111-111111111111","name":"CVK-SERVICE-TRACKER"},{"parcelId":"22222222-2222-2222-2222-222222222222","name":"CVK-SERVICE-TRACKER-2"}]}`)
			}
		default:
			fmt.Fprint(w, `{"payload":{}}`)
		}
	})
	ctx := context.Background()
	if _, err := c.Create(ctx, group); !errors.Is(err, ErrDependencyPending) || writes != 0 {
		t.Fatal("missing dependency allowed a write", err, writes)
	}
	present = true
	if _, err := c.Create(ctx, group); err != nil || writes != 1 {
		t.Fatal("resolved group create failed", err, writes)
	}
}
