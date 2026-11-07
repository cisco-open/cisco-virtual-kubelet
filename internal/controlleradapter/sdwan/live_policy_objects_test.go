// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The lab has a singleton policy-object profile. Child parcels can be tested
// under it without creating, updating, or deleting the parent profile.
func TestLiveExistingPolicyObjectChildren(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	fixture := os.Getenv("CVK_SDWAN_POLICY_FIXTURE")
	if fixture == "" {
		t.Skip("set CVK_SDWAN_POLICY_FIXTURE")
	}
	allowed := map[string]bool{"policy-profile": true, "policy-prefixes": true, "policy-dependencies": true, "policy-security": true}
	if !allowed[fixture] {
		t.Fatal("unsupported policy-object fixture")
	}
	c := liveClient(t)
	ctx := context.Background()
	const collection = "/dataservice/v1/feature-profile/sdwan/policy-object"
	var profiles []struct {
		ID   string `json:"profileId"`
		Name string `json:"profileName"`
	}
	if err := c.Request(ctx, "GET", collection, nil, &profiles); err != nil || len(profiles) != 1 || !validID.MatchString(profiles[0].ID) {
		t.Fatal("expected exactly one existing policy-object profile", err)
	}
	parent := profiles[0]
	children := func() ([]string, error) {
		var profile struct {
			Parcels []struct {
				ID string `json:"parcelId"`
			} `json:"associatedProfileParcels"`
		}
		if err := c.Request(context.Background(), "GET", collection+"/"+parent.ID, nil, &profile); err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(profile.Parcels))
		for _, p := range profile.Parcels {
			ids = append(ids, p.ID)
		}
		sort.Strings(ids)
		return ids, nil
	}
	before, err := children()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := children()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Error("policy-object parent child IDs changed after cleanup", err)
		}
	})
	resources := parcelFixtures(t, fixture)
	var definitions struct{ Parcels map[string]scalarDefinition }
	if err := json.Unmarshal(scalarDefinitions, &definitions); err != nil {
		t.Fatal(err)
	}
	selected := os.Getenv("CVK_SDWAN_POLICY_ONLY")
	skipped := map[string]bool{}
	for _, kind := range strings.Split(os.Getenv("CVK_SDWAN_POLICY_SKIP"), ",") {
		if kind != "" {
			skipped[kind] = true
		}
	}
	count := 0
	for _, r := range resources {
		if r.Kind == "profile" && r.Type == "policy-object" {
			continue
		}
		if r.Collection != collection || r.Kind != "parcel" {
			t.Fatal("fixture has resources outside the existing policy-object parent")
		}
		if (selected != "" && r.Type != selected) || skipped[r.Type] {
			continue
		}
		r.ParentName = parent.Name
		if observed, err := c.Observe(ctx, r); err != nil || observed != nil {
			t.Fatal("fixture child must be absent", r.Type, r.Name, err)
		}
		id, err := c.Create(ctx, r)
		if err != nil {
			t.Fatal("create", r.Type, r.Name, err)
		}
		t.Cleanup(func() {
			if err := c.DeleteResourceFixture(context.Background(), r, id); err != nil {
				t.Error("cleanup", r.Type, r.Name, err)
			}
		})
		observed, err := c.Observe(ctx, r)
		if err != nil || observed == nil {
			t.Fatal("readback", r.Type, r.Name, err)
		}
		same, err := Equal(r, observed.Body)
		if err != nil || !same {
			if os.Getenv("CVK_SDWAN_POLICY_DIAG") == "1" {
				t.Logf("expected data: %#v", r.Body["data"])
				t.Logf("observed data: %#v", observed.Body["payload"])
			}
			t.Fatal("create verification", r.Type, r.Name, err)
		}
		if !definitions.Parcels[r.Type].IgnoreDescription {
			r.Body["description"] = "CVK updated isolated fixture"
		}
		if err := c.Update(ctx, r, observed); err != nil {
			t.Fatal("update", r.Type, r.Name, err)
		}
		observed, err = c.Observe(ctx, r)
		if err != nil || observed == nil {
			t.Fatal("updated readback", r.Type, r.Name, err)
		}
		same, err = Equal(r, observed.Body)
		if err != nil || !same {
			t.Fatal("update verification", r.Type, r.Name, err)
		}
		count++
		t.Logf("verified %s %s", r.Type, r.Name)
	}
	if count == 0 {
		t.Fatal("no policy-object children selected")
	}
	t.Logf("qualified %d child parcels under existing profile %s", count, parent.Name)
}

func TestPolicyObjectManagerDefaults(t *testing.T) {
	for _, sample := range []struct{ kind, discriminator string }{
		{"policy_object_security_url_allow_list", "urlallowed"},
		{"policy_object_security_url_block_list", "urlblocked"},
	} {
		found := false
		for _, r := range parcelFixtures(t, "policy-profile") {
			if r.Type != sample.kind {
				continue
			}
			found = true
			data := r.Body["data"].(map[string]any)
			if data["type"] != sample.discriminator {
				t.Fatal("URL list discriminator missing", sample.kind)
			}
		}
		if !found {
			t.Fatal("URL list fixture missing", sample.kind)
		}
	}
	r := Resource{Kind: "parcel", Type: "policy_object_application_list", Body: map[string]any{"name": "app", "description": "", "data": map[string]any{"entries": []any{}}}}
	observed := map[string]any{"payload": map[string]any{"name": "app", "description": "", "data": map[string]any{"entries": []any{}, "saasApp": false}}}
	same, err := Equal(r, observed)
	if err != nil || !same {
		t.Fatal("Manager-added disabled SaaS flag should compare equal", err)
	}
	observed["payload"].(map[string]any)["data"].(map[string]any)["saasApp"] = true
	same, err = Equal(r, observed)
	if err != nil || same {
		t.Fatal("enabled SaaS flag must remain drift", err)
	}
}
