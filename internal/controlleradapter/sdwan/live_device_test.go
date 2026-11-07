// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"os"
	"testing"
)

func labTestChassis(t *testing.T) string {
	t.Helper()
	id := os.Getenv("CVK_SDWAN_TEST_CHASSIS")
	if id == "" {
		t.Skip("set CVK_SDWAN_TEST_CHASSIS to an unassociated lab router")
	}
	if !validID.MatchString(id) {
		t.Fatal("invalid test chassis ID")
	}
	return id
}

func TestLiveTagAssociationLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	chassis := labTestChassis(t)
	r := Resource{Key: "sites/tags/CVK-LIVE-ASSOCIATION", Name: "CVK-LIVE-ASSOCIATION", Section: "sites", Type: "tag", Kind: "tag", TagDevices: []string{chassis}, Body: map[string]any{"data": []any{map[string]any{"name": "CVK-LIVE-ASSOCIATION", "description": "CVK-LIVE-ASSOCIATION"}}}}
	if o, err := c.Observe(ctx, r); err != nil || o != nil {
		t.Fatal("fixture must be absent", err)
	}
	id, err := c.Create(ctx, r)
	if err != nil {
		t.Fatal("create and associate", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteResourceFixture(context.Background(), r, id); err != nil {
			t.Error("tag cleanup", err)
		}
	})
	o, err := c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("readback", err)
	}
	same, err := Equal(r, o.Body)
	if err != nil || !same {
		t.Fatal("association readback", err)
	}
	r.TagDevices = nil
	if err := c.Update(ctx, r, o); err != nil {
		t.Fatal("remove association", err)
	}
	o, err = c.Observe(ctx, r)
	if err != nil || o == nil {
		t.Fatal("removed association readback", err)
	}
	same, err = Equal(r, o.Body)
	if err != nil || !same {
		t.Fatal("removed association mismatch", err)
	}
}

func TestLiveConfigurationGroupAssociation(t *testing.T) {
	liveGroupAssociation(t, "configuration_group", "system")
}

func TestLivePolicyGroupAssociation(t *testing.T) {
	liveGroupAssociation(t, "policy_group", "application-priority")
}

func liveGroupAssociation(t *testing.T, groupType, profileType string) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	chassis := labTestChassis(t)
	b, err := os.ReadFile("../../../examples/configs/sdwan/group-device-deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(b)
	if err != nil {
		t.Fatal(err)
	}
	var group, deployment Resource
	for _, r := range resources {
		if r.Kind == "group" && r.Type == groupType {
			group = r
		}
		if r.Kind == "group-deployment" && r.Type == groupType {
			deployment = r
		}
	}
	if group.Name == "" || deployment.Name == "" {
		t.Fatal("missing group fixture")
	}
	// BR-2-WAN is the reachable router with no preexisting group associations.
	for _, collection := range []string{"/dataservice/v1/config-group", "/dataservice/v1/policy-group"} {
		var groups []struct {
			ID string `json:"id"`
		}
		if err := c.Request(ctx, "GET", collection, nil, &groups); err != nil {
			t.Fatal(err)
		}
		for _, item := range groups {
			associated, err := c.groupAssociatedDevices(ctx, Resource{Collection: collection}, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if associated[chassis] != nil {
				t.Fatal("test router has an existing group association")
			}
		}
	}
	for _, r := range resources {
		if r.Kind != "profile" || r.Type != profileType {
			continue
		}
		o, err := c.Observe(ctx, r)
		if err != nil || o != nil {
			t.Fatal("profile fixture must be absent", err)
		}
		id, err := c.Create(ctx, r)
		if err != nil {
			t.Fatal("create profile", err)
		}
		t.Cleanup(func() {
			if err := c.DeleteResourceFixture(context.Background(), r, id); err != nil {
				t.Error("profile cleanup", err)
			}
		})
	}
	if o, err := c.Observe(ctx, group); err != nil || o != nil {
		t.Fatal("group fixture must be absent", err)
	}
	groupID, err := c.Create(ctx, group)
	if err != nil {
		t.Fatal("create group", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteResourceFixture(context.Background(), group, groupID); err != nil {
			t.Error("group cleanup", err)
		}
	})
	deployment.GroupDevices = []GroupDeviceTarget{{ID: chassis, Deploy: false}}
	t.Cleanup(func() {
		path := group.Collection + "/" + groupID + "/device/associate/"
		body := map[string]any{"devices": []any{map[string]any{"id": chassis}}}
		if err := c.Request(context.Background(), "DELETE", path, body, nil); err != nil {
			t.Error("disassociate router", err)
		}
	})
	if _, err := c.Create(ctx, deployment); err != nil {
		t.Fatal("associate router", err)
	}
	o, err := c.Observe(ctx, deployment)
	if err != nil || o == nil {
		t.Fatal("association readback", err)
	}
	same, err := Equal(deployment, o.Body)
	if err != nil || !same {
		t.Fatal("association mismatch", err)
	}
}
