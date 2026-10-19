// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strconv"
	"testing"
)

func TestTemplateExecutionCommitsAndConverges(t *testing.T) {
	var draft, committed map[string]any
	version := 0
	writes := []string{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			switch r.URL.Path {
			case templateProjectsPath + "/project/template", templatesPath:
				if draft == nil && r.Method != "POST" {
					t.Error("first draft was not created")
				}
				if draft != nil && r.Method != "PUT" {
					t.Error("draft was recreated")
				}
				draft = body
				draft["id"] = "base"
				draft["projectId"] = "project"
				writes = append(writes, "draft")
			case templateVersionsPath:
				if body["templateId"] != "base" || r.Method != "POST" {
					t.Error("incorrect commit")
				}
				version++
				raw, _ := json.Marshal(draft)
				json.Unmarshal(raw, &committed)
				committed["id"] = "version-" + strconv.Itoa(version)
				committed["version"] = strconv.Itoa(version)
				writes = append(writes, "commit")
			default:
				t.Errorf("unexpected write %s", r.URL.Path)
			}
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			return
		}
		switch r.URL.Path {
		case templateProjectsPath:
			fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
		case templatesPath:
			if draft == nil {
				fmt.Fprint(w, `[]`)
			} else {
				fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"base"}]`)
			}
		case templatesPath + "/base":
			json.NewEncoder(w).Encode(draft)
		case templateVersionsPath + "/base":
			versions := []map[string]any{{"id": "base"}}
			if version > 0 {
				versions = append(versions, map[string]any{"id": "version-" + strconv.Itoa(version), "version": strconv.Itoa(version)})
			}
			json.NewEncoder(w).Encode([]any{map[string]any{"templateId": "base", "versionsInfo": versions}})
		default:
			if committed == nil || r.URL.Path != templatesPath+"/version-"+strconv.Itoa(version) {
				t.Errorf("unexpected read %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(committed)
		}
	})
	for _, body := range []string{"first\n", "second\n"} {
		files := map[string]string{"base.j2": body}
		for step := 0; step < 3; step++ {
			executor, err := c.compileConfigurationExecutor([]byte(templateModel), "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if step == 2 {
				if write != nil {
					t.Fatal("did not converge after commit")
				}
				break
			}
			if write == nil {
				t.Fatal("draft mistaken for committed completion")
			}
			if _, err = write.submit(context.Background()); err != nil {
				t.Fatal(err)
			}
			executor, err = c.compileConfigurationExecutor([]byte(templateModel), "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if ok, err := executor.verify(context.Background(), op); err != nil || !ok {
				t.Fatal("read-back failed", err)
			}
		}
	}
	if fmt.Sprint(writes) != "[draft commit draft commit]" {
		t.Fatalf("unexpected writes %v", writes)
	}
}
