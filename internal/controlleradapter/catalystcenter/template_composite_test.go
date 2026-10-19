// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strings"
	"testing"
)

func TestCompositeExecutionTracksMemberVersions(t *testing.T) {
	model := `{"catalyst_center":{"templates":{"projects":[{"name":"Lab","dayn_templates":[{"name":"combined","composite":true,"containing_templates":["base"],"language":"JINJA","software_type":"IOS-XE","device_types":[{"product_family":"Switches and Hubs"}]},{"name":"base","language":"JINJA","software_type":"IOS-XE","device_types":[{"product_family":"Switches and Hubs"}]}]}]}}}`
	drafts := map[string]map[string]any{}
	committed := map[string]map[string]any{}
	versions := map[string]int{}
	comments := map[string]string{}
	writes := []string{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if r.URL.Path == templateVersionsPath {
				id, _ := body["templateId"].(string)
				if drafts[id] == nil {
					t.Fatal("commit without draft")
				}
				versions[id]++
				raw, _ := json.Marshal(drafts[id])
				var snapshot map[string]any
				json.Unmarshal(raw, &snapshot)
				snapshot["id"] = fmt.Sprintf("%s-v%d", id, versions[id])
				snapshot["version"] = fmt.Sprint(versions[id])
				committed[id] = snapshot
				comments[id], _ = body["comments"].(string)
				writes = append(writes, "commit:"+id)
			} else {
				id := "base-" + body["name"].(string)
				body["id"] = id
				body["projectId"] = "project"
				drafts[id] = body
				writes = append(writes, "draft:"+id)
			}
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			return
		}
		switch {
		case r.URL.Path == templateProjectsPath:
			fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
		case r.URL.Path == templatesPath:
			list := []any{}
			for id, d := range drafts {
				list = append(list, map[string]any{"templateId": id, "projectId": "project", "name": d["name"]})
			}
			json.NewEncoder(w).Encode(list)
		case strings.HasPrefix(r.URL.Path, templateVersionsPath+"/"):
			id := strings.TrimPrefix(r.URL.Path, templateVersionsPath+"/")
			items := []any{map[string]any{"id": id}}
			if versions[id] > 0 {
				items = append(items, map[string]any{"id": committed[id]["id"], "version": versions[id], "versionComment": comments[id]})
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"templateId": id, "versionsInfo": items}})
		default:
			id := strings.TrimPrefix(r.URL.Path, templatesPath+"/")
			if d, ok := drafts[id]; ok {
				json.NewEncoder(w).Encode(d)
				return
			}
			for _, d := range committed {
				if d["id"] == id {
					json.NewEncoder(w).Encode(d)
					return
				}
			}
			w.WriteHeader(404)
		}
	})
	for _, content := range []string{"first", "second"} {
		files := map[string]string{"base.j2": content}
		converged := false
		for step := 0; step < 5; step++ {
			executor, err := c.compileConfigurationExecutor([]byte(model), "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			op, err := executor.next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if op == nil {
				converged = true
				break
			}
			if _, err = op.submit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if ok, err := executor.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: op.resource, Action: op.action, TargetID: op.identity.target, ParentID: op.identity.parent}); err != nil || !ok {
				t.Fatal("verification failed", err)
			}
		}
		if !converged {
			t.Fatal("composite did not converge")
		}
	}
	expected := "[draft:base-base commit:base-base draft:base-combined commit:base-combined draft:base-base commit:base-base commit:base-combined]"
	if fmt.Sprint(writes) != expected {
		t.Fatalf("dependency ordering or recommit wrong: %v", writes)
	}

	executor, err := c.compileConfigurationExecutor([]byte(model), "templates", map[string]string{"base.j2": "second"})
	if err != nil {
		t.Fatal(err)
	}
	committed["base-combined"]["language"] = "VELOCITY"
	operation := &ciskov1.NetworkControllerConfigurationOperation{Resource: "commit:Lab#combined", Action: "Update", TargetID: "base-combined", ParentID: "project"}
	if ok, err := executor.verify(context.Background(), operation); err != nil || ok {
		t.Fatal("commit marker alone established completion", err)
	}
	committed["base-combined"]["language"] = "JINJA"
	comments["base-combined"] = "stale member versions"
	if ok, err := executor.verify(context.Background(), operation); err != nil || ok {
		t.Fatal("stale dependency marker established completion", err)
	}
}
