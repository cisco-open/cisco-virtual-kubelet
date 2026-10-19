// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"
)

func TestLiveCatalystCenterTemplateTagAssociationFilters(t *testing.T) {
	if os.Getenv("CATC_TEMPLATE_TAG_READ") != "association-filters" {
		t.Skip("read-only template association filter qualification not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tags, err := c.listTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("no existing native tag for read-only filter qualification")
	}
	for _, association := range []string{"", "DYNAMIC", "STATIC", "MIXED"} {
		members, err := c.tagMembersByAssociation(ctx, tags[names[0]].ID, "template", association)
		if err != nil {
			t.Fatal("native template association filter unavailable", association, err)
		}
		t.Log("read-only template association query", association, "count", len(members))
	}
}

func qualifyLiveTemplateTag(t *testing.T, c *client, ctx context.Context, projectName, memberID, memberName string, settled *bool, submit func(context.Context, string, string, any)) {
	t.Helper()
	name := "CVK-Q-Association-" + time.Now().UTC().Format("20060102-150405")
	id := ""
	tags, err := c.listTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := tags[name]; exists {
		t.Fatal("fixture tag exists")
	}
	t.Cleanup(func() {
		if !*settled || id == "" {
			t.Log("retained uncertain association", name, id)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tags, err := c.listTags(cleanup)
		if err != nil {
			t.Error(err)
			*settled = false
			return
		}
		tag, exists := tags[name]
		if !exists {
			return
		}
		if tag.ID != id || tag.SystemTag == nil || *tag.SystemTag {
			t.Error("tag identity/protection changed")
			*settled = false
			return
		}
		members, err := c.templateTagMembers(cleanup, id)
		if err != nil {
			t.Error(err)
			*settled = false
			return
		}
		if members[memberID] {
			submit(cleanup, http.MethodDelete, tagsPath+"/"+id+"/member/"+memberID, nil)
		}
		members, err = c.templateTagMembers(cleanup, id)
		if err != nil || len(members) != 0 {
			t.Error("tag has remaining memberships", err)
			*settled = false
			return
		}
		submit(cleanup, http.MethodDelete, tagsPath+"/"+id, nil)
		tags, err = c.listTags(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := tags[name]; exists {
			t.Error("tag remains")
			return
		}
		*settled = true
		t.Log("association and tag cleanup verified", id)
	})
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"tags": []any{map[string]any{"name": name, "description": "CVK isolated association", "system_tag": false}}, "projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{map[string]any{"name": memberName, "description": "CVK isolated commit qualification", "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": []any{}, "tags": []string{name}}}}}}}})
	files := map[string]string{memberName + ".j2": "! CVK isolated qualification updated\n"}
	for step := 0; step < 3; step++ {
		executor, err := c.compileConfigurationExecutor(raw, "templates", files)
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if step == 2 {
			if write != nil {
				t.Fatal("membership did not converge")
			}
			break
		}
		if write == nil {
			t.Fatal("missing tag or membership operation")
		}
		*settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("template tag operation", write.action, task.ID)
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if done {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		executor, err = c.compileConfigurationExecutor(raw, "templates", files)
		if err != nil {
			t.Fatal(err)
		}
		// Verification must use only serialized operation data and a fresh
		// compiler; the original in-memory project observation is unavailable.
		encoded, err := json.Marshal(&ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent})
		if err != nil {
			t.Fatal(err)
		}
		var recovered ciskov1.NetworkControllerConfigurationOperation
		if err := json.Unmarshal(encoded, &recovered); err != nil {
			t.Fatal(err)
		}
		if step == 1 {
			selected, valid := templateMembershipJournalBinding("templates", recovered.Resource)
			if !selected || !valid {
				t.Fatal("template association did not retain a project-bound resource")
			}
		}
		if ok, err := executor.verify(ctx, &recovered); err != nil || !ok {
			t.Fatal("association verification failed", err)
		}
		tags, err = c.listTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id = tags[name].ID
		if step == 1 {
			for _, association := range []string{"", "DYNAMIC", "STATIC", "MIXED"} {
				members, err := c.tagMembersByAssociation(ctx, id, "template", association)
				if err != nil {
					t.Fatal("native template provenance read failed", association, err)
				}
				expected := association == "" || association == "STATIC"
				if expected && (len(members) != 1 || !members[memberID]) || !expected && len(members) != 0 {
					t.Fatal("native template provenance disagrees with an isolated manual association", association, len(members))
				}
				t.Log("isolated template association class", association, "count", len(members))
			}
		}
		*settled = true
	}
	t.Log("native tag creation and template association verified", id, memberID)
}
