// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

const projectModel = `{"catalyst_center":{"templates":{"projects":[{"name":"CVK-Test","description":"test project"}]}}}`

func TestTemplateProjects(t *testing.T) {
	for _, mode := range []string{"create", "update", "missing-receipt", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			actual := []templateProject{}
			writes := 0
			if mode == "update" {
				description := "old"
				actual = append(actual, templateProject{ID: "project", Name: "CVK-Test", Description: &description})
			}
			if mode == "duplicate" {
				actual = []templateProject{{ID: "one", Name: "same"}, {ID: "two", Name: "same"}}
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != templateProjectsPath {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(actual)
					return
				}
				if mode == "update" && (r.Method != "PUT" || r.URL.Query().Get("id") != "project") {
					t.Error("update identity lost")
				}
				if mode != "update" && r.Method != "POST" {
					t.Error("expected create")
				}
				var p templateProject
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				p.ID = "project"
				actual = []templateProject{p}
				writes++
				if mode == "missing-receipt" {
					fmt.Fprint(w, `{}`)
					return
				}
				fmt.Fprint(w, `{"response":{"taskId":"project-task"}}`)
			})
			executor, err := c.compileConfigurationExecutor([]byte(projectModel), "templates")
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(context.Background())
			if mode == "duplicate" {
				if err == nil || write != nil {
					t.Fatal("ambiguous inventory accepted")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("missing plan", err)
			}
			_, err = write.submit(context.Background())
			if mode == "missing-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("uncertain write accepted or replayed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			executor, err = c.compileConfigurationExecutor([]byte(projectModel), "templates")
			if err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target}
			if ok, err := executor.verify(context.Background(), op); err != nil || !ok {
				t.Fatal("verify failed", err)
			}
			if next, err := executor.next(context.Background()); err != nil || next != nil {
				t.Fatal("did not converge", err)
			}
			if writes != 1 {
				t.Fatal("replayed")
			}
		})
	}
}
func TestTemplateProjectRejectsUnimplementedContent(t *testing.T) {
	for _, raw := range []string{`{"catalyst_center":{"templates":{"projects":[{"name":"p","dayn_templates":[]}]}}}`, `{"catalyst_center":{"templates":{"projects":[{"name":"p"},{"name":"p"}]}}}`} {
		if _, err := parseTemplateProjects([]byte(raw)); err == nil {
			t.Fatal("unsupported model accepted")
		}
	}
}

func TestBuiltInTemplateProjectIsReferenceOnly(t *testing.T) {
	raw := []byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Onboarding Configuration","description":"ignored upstream"}]}}}`)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("built-in project mutation")
		}
		fmt.Fprint(w, `[{"id":"builtin","name":"Onboarding Configuration","description":"system"}]`)
	})
	executor, err := c.compileConfigurationExecutor(raw, "templates")
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(context.Background()); err != nil || write != nil {
		t.Fatal("built-in project not preserved", err)
	}
	plans, err := c.compileConfigReports(raw, []string{"templates"})
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := plans[0].observe(context.Background()); err != nil || len(drift) != 0 {
		t.Fatal("built-in drift incorrectly reported", err)
	}
}
