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

func TestTemplateMembershipPreservesOtherMembers(t *testing.T) {
	intent, err := parseTemplateIntent([]byte(strings.Replace(templateModel, "- name: base", "- name: base\n            tags: [lab]", 1)), map[string]string{"base.j2": "body"})
	if err != nil {
		t.Fatal(err)
	}
	members := []map[string]string{{"instanceUuid": "foreign"}}
	writes := 0
	tagID := "tag"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch r.URL.Path {
		case tagsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": tagID, "name": "lab", "systemTag": false}}})
		case templateProjectsPath:
			fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
		case templatesPath:
			fmt.Fprint(w, `[{"templateId":"template","projectId":"project","name":"base"}]`)
		case tagsPath + "/" + tagID + "/member":
			if r.Method == "GET" {
				if r.URL.Query().Get("memberType") != "template" {
					t.Error("member type filter missing")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": members})
				return
			}
			if r.Method != "POST" {
				t.Fatal("unexpected removal or replacement")
			}
			var body struct {
				Templates []string `json:"template"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Templates) != 1 || body.Templates[0] != "template" {
				t.Fatal("unrelated member in mutation")
			}
			members = append(members, map[string]string{"instanceUuid": "template"})
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"membership-task"}}`)
		default:
			t.Error("unexpected API")
			w.WriteHeader(404)
		}
	})
	executor := &templateMembershipExecutor{client: c, intent: intent}
	write, err := executor.next(context.Background())
	if err != nil || write == nil {
		t.Fatal("missing association", err)
	}
	if _, err = write.submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
	if ok, err := executor.verify(context.Background(), op); err != nil || !ok {
		t.Fatal("membership verification failed", err)
	}
	if next, err := executor.next(context.Background()); err != nil || next != nil {
		t.Fatal("association replayed", err)
	}
	if writes != 1 || len(members) != 2 || members[0]["instanceUuid"] != "foreign" {
		t.Fatal("foreign association changed")
	}
	tagID = "replacement"
	if ok, err := executor.verify(context.Background(), op); err == nil || ok {
		t.Fatal("replacement tag accepted")
	}
}
